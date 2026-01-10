package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/anzepintar/ps-razpravljalnica/client/razpravljalnica"
)

type TUI struct {
	app        *tview.Application
	pages      *tview.Pages
	cluster    *ClusterClient
	entryPoint string

	currentUserID   int64
	currentUserName string

	// Elementi prikaza
	statusBar    *tview.TextView
	topicsList   *tview.List
	messagesList *tview.TextView
	inputField   *tview.InputField

	topics         []*pb.Topic
	currentTopicID int64
	messages       []*pb.Message

	subCancel context.CancelFunc
}

func RunTUI(entryPoint string) error {
	tui := &TUI{
		app:        tview.NewApplication(),
		entryPoint: entryPoint,
	}

	tui.app.EnableMouse(true)

	// Poveče se na cluster prek entry
	cluster, err := NewClusterClient(entryPoint)
	if err != nil {
		return fmt.Errorf("connection to cluster via %s failed: %v", entryPoint, err)
	}
	tui.cluster = cluster

	tui.setupUI()

	tui.showLoginScreen()

	return tui.app.Run()
}

func (t *TUI) setupUI() {
	t.pages = tview.NewPages()
	t.pages.SetBackgroundColor(tcell.ColorBlack)

	t.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Not logged in[white] | Entry: " + t.entryPoint)
	t.statusBar.SetBackgroundColor(tcell.ColorDarkBlue)

	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			t.cleanup()
			t.app.Stop()
			return nil
		}
		return event
	})
}

func (t *TUI) cleanup() {
	if t.subCancel != nil {
		t.subCancel()
	}
	if t.cluster != nil {
		t.cluster.Close()
	}
}

func (t *TUI) writeClient() pb.MessageBoardClient {
	return t.cluster.WriteClient()
}

func (t *TUI) readClient() pb.MessageBoardClient {
	return t.cluster.ReadClient()
}

func (t *TUI) showLoginScreen() {
	form := tview.NewForm()

	var username string
	var userIDStr string

	form.AddInputField("Username (for new user)", "", 30, nil, func(text string) {
		username = text
	})
	form.AddInputField("Or enter existing User ID", "", 10, func(textToCheck string, lastChar rune) bool {
		return lastChar >= '0' && lastChar <= '9'
	}, func(text string) {
		userIDStr = text
	})
	form.AddButton("Create New User", func() {
		if username == "" {
			t.showError("Please enter a username")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := t.writeClient()
		if client == nil {
			t.showError("No head node available")
			return
		}
		user, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: username})
		if err != nil {
			t.cluster.snitch(t.cluster.HeadAddr())
			t.cluster.refreshClusterState()
			t.showError(fmt.Sprintf("Failed to create user: %v", err))
			return
		}
		t.currentUserID = user.Id
		t.currentUserName = user.Name
		t.updateStatusBar()
		t.showMainScreen()
	})
	form.AddButton("Login with ID", func() {
		if userIDStr == "" {
			t.showError("Please enter a User ID")
			return
		}
		id, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			t.showError("Invalid User ID")
			return
		}
		// Če user obstaja
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// bere iz repa
		client := t.readClient()
		if client == nil {
			t.showError("No tail node available")
			return
		}
		user, err := client.GetUser(ctx, &pb.GetUserRequest{UserId: id})
		if err != nil {
			t.cluster.snitch(t.cluster.TailAddr())
			t.cluster.refreshClusterState()
			t.showError(fmt.Sprintf("User not found: %v", err))
			return
		}
		t.currentUserID = user.Id
		t.currentUserName = user.Name
		t.updateStatusBar()
		t.showMainScreen()
	})
	form.AddButton("Quit", func() {
		t.cleanup()
		t.app.Stop()
	})

	form.SetBorder(true).SetTitle(" Razpravljalnica - Login ").SetTitleAlign(tview.AlignCenter)

	// center al neki
	flex := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 15, 1, true).
			AddItem(nil, 0, 1, false), 60, 1, true).
		AddItem(nil, 0, 1, false)

	t.pages.AddPage("login", flex, true, true)
	t.app.SetRoot(t.pages, true)
}

func (t *TUI) showMainScreen() {
	// Teme na levi
	t.topicsList = tview.NewList().
		ShowSecondaryText(false)
	t.topicsList.SetBorder(true).SetTitle(" Topics (t: new, r: refresh) ")
	t.topicsList.SetBackgroundColor(tcell.ColorBlack)

	t.topicsList.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		if index < len(t.topics) {
			t.currentTopicID = t.topics[index].Id
			t.loadMessages()
			t.startSubscription()
		}
	})

	// Sporočila na sredi
	t.messagesList = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true)
	t.messagesList.SetBorder(true).SetTitle(" Messages ")
	t.messagesList.SetBackgroundColor(tcell.ColorBlack)
	t.messagesList.ScrollToEnd()

	// Vnos na dnu
	t.inputField = tview.NewInputField().
		SetLabel("Message: ").
		SetFieldWidth(0)
	t.inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			t.sendMessage()
		}
	})
	t.inputField.SetBorder(true).SetTitle(" Post Message (Enter to send) ")
	t.inputField.SetBackgroundColor(tcell.ColorBlack)

	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Keys:[white] Tab=switch panels | t=new topic | r=refresh | l=like msg | d=delete msg | e=edit msg | q=quit")
	helpText.SetBackgroundColor(tcell.ColorDarkGreen)

	// razporeditev
	mainFlex := tview.NewFlex().
		AddItem(t.topicsList, 30, 1, true).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(t.messagesList, 0, 1, false).
			AddItem(t.inputField, 3, 0, false), 0, 3, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(mainFlex, 0, 1, true).
		AddItem(helpText, 1, 0, false).
		AddItem(t.statusBar, 1, 0, false)

	t.topicsList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			t.setFocusPanel(t.inputField)
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 't':
				t.showNewTopicDialog()
				return nil
			case 'r':
				t.loadTopics()
				return nil
			case 'q':
				t.cleanup()
				t.app.Stop()
				return nil
			}
		}
		return event
	})

	t.inputField.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			t.setFocusPanel(t.messagesList)
			return nil
		case tcell.KeyEscape:
			t.setFocusPanel(t.topicsList)
			return nil
		}
		return event
	})

	t.messagesList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			t.setFocusPanel(t.topicsList)
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'l':
				t.showLikeDialog()
				return nil
			case 'd':
				t.showDeleteDialog()
				return nil
			case 'e':
				t.showEditDialog()
				return nil
			case 'r':
				t.loadMessages()
				return nil
			case 'q':
				t.cleanup()
				t.app.Stop()
				return nil
			}
		}
		return event
	})

	t.pages.AddPage("main", layout, true, true)
	t.pages.SwitchToPage("main")

	t.loadTopics()

	// naloženo na začetku, če kaj že obstaja
	if len(t.topics) > 0 {
		t.currentTopicID = t.topics[0].Id
		t.loadMessages()
		t.startSubscription()
	}

	t.setFocusPanel(t.topicsList)
}

func (t *TUI) setFocusPanel(p tview.Primitive) {
	t.topicsList.SetBorderColor(tcell.ColorWhite)
	t.messagesList.SetBorderColor(tcell.ColorWhite)
	t.inputField.SetBorderColor(tcell.ColorWhite)

	if box, ok := p.(interface{ SetBorderColor(tcell.Color) *tview.Box }); ok {
		box.SetBorderColor(tcell.ColorOrange)
	}
	t.app.SetFocus(p)
}

func (t *TUI) updateStatusBar() {
	headAddr := t.cluster.HeadAddr()
	tailAddr := t.cluster.TailAddr()
	t.statusBar.SetText(fmt.Sprintf("[green]User: %s (ID: %d)[white] | Head: %s | Tail: %s | Topic: %d",
		t.currentUserName, t.currentUserID, headAddr, tailAddr, t.currentTopicID))
}

func (t *TUI) loadTopics() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := t.readClient()
	if client == nil {
		t.showError("No tail node available")
		return
	}

	resp, err := client.ListTopics(ctx, &emptypb.Empty{})
	if err != nil {
		t.cluster.snitch(t.cluster.TailAddr())
		t.cluster.refreshClusterState()
		t.showError(fmt.Sprintf("Failed to load topics: %v", err))
		return
	}

	t.topics = resp.Topics
	t.topicsList.Clear()
	for _, topic := range t.topics {
		t.topicsList.AddItem(fmt.Sprintf("[%d] %s", topic.Id, topic.Name), "", 0, nil)
	}
}

func (t *TUI) loadMessages() {
	if t.currentTopicID < 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := t.readClient()
	if client == nil {
		t.showError("No tail node available")
		return
	}

	resp, err := client.GetMessages(ctx, &pb.GetMessagesRequest{
		TopicId:       t.currentTopicID,
		FromMessageId: 0,
		Limit:         100,
	})
	if err != nil {
		t.cluster.snitch(t.cluster.TailAddr())
		t.cluster.refreshClusterState()
		t.showError(fmt.Sprintf("Failed to load messages: %v", err))
		return
	}

	t.messages = resp.Messages
	// sporočila urejena po id
	slices.SortFunc(t.messages, func(a, b *pb.Message) int {
		return int(a.Id - b.Id)
	})
	t.renderMessages()
	t.updateStatusBar()
}

func (t *TUI) renderMessages() {
	t.messagesList.Clear()
	var sb strings.Builder

	for _, msg := range t.messages {
		userName := fmt.Sprintf("User#%d", msg.UserId)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		client := t.readClient()
		if client != nil {
			user, err := client.GetUser(ctx, &pb.GetUserRequest{UserId: msg.UserId})
			if err == nil {
				userName = user.Name
			}
		}
		cancel()

		timeStr := ""
		if msg.CreatedAt != nil {
			timeStr = msg.CreatedAt.AsTime().Format("15:04:05")
		}

		sb.WriteString(fmt.Sprintf("[yellow][%d][white] [blue]%s[white] [dim](%s)[white]\n",
			msg.Id, userName, timeStr))
		sb.WriteString(fmt.Sprintf("  %s\n", msg.Text))
		sb.WriteString(fmt.Sprintf("  [red]♥ %d[white]\n\n", msg.Likes))
	}

	t.messagesList.SetText(sb.String())
	t.messagesList.ScrollToEnd()
}

func (t *TUI) sendMessage() {
	text := t.inputField.GetText()
	if text == "" || t.currentTopicID < 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := t.writeClient()
	if client == nil {
		t.showError("No head node available")
		return
	}

	_, err := client.PostMessage(ctx, &pb.PostMessageRequest{
		TopicId: t.currentTopicID,
		UserId:  t.currentUserID,
		Text:    text,
	})
	if err != nil {
		t.cluster.snitch(t.cluster.HeadAddr())
		t.cluster.refreshClusterState()
		t.showError(fmt.Sprintf("Failed to send message: %v", err))
		return
	}

	t.inputField.SetText("")
	t.loadMessages()
}

func (t *TUI) showNewTopicDialog() {
	form := tview.NewForm()
	form.SetBackgroundColor(tcell.ColorBlack)
	var topicName string

	form.AddInputField("Topic Name", "", 40, nil, func(text string) {
		topicName = text
	})
	form.AddButton("Create", func() {
		if topicName == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := t.writeClient()
		if client == nil {
			t.showError("No head node available")
			return
		}

		_, err := client.CreateTopic(ctx, &pb.CreateTopicRequest{Name: topicName})
		if err != nil {
			t.cluster.snitch(t.cluster.HeadAddr())
			t.cluster.refreshClusterState()
			t.showError(fmt.Sprintf("Failed to create topic: %v", err))
			return
		}
		t.pages.RemovePage("newtopic")
		t.loadTopics()
	})
	form.AddButton("Cancel", func() {
		t.pages.RemovePage("newtopic")
	})

	form.SetBorder(true).SetTitle(" New Topic ").SetTitleAlign(tview.AlignCenter)

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 9, 1, true).
			AddItem(nil, 0, 1, false), 50, 1, true).
		AddItem(nil, 0, 1, false)

	t.pages.AddPage("newtopic", modal, true, true)
}

func (t *TUI) showLikeDialog() {
	form := tview.NewForm()
	var msgIDStr string

	form.AddInputField("Message ID to like", "", 10, func(textToCheck string, lastChar rune) bool {
		return lastChar >= '0' && lastChar <= '9'
	}, func(text string) {
		msgIDStr = text
	})
	form.AddButton("Like", func() {
		if msgIDStr == "" {
			return
		}
		msgID, _ := strconv.ParseInt(msgIDStr, 10, 64)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := t.writeClient()
		if client == nil {
			t.showError("No head node available")
			return
		}

		_, err := client.LikeMessage(ctx, &pb.LikeMessageRequest{
			TopicId:   t.currentTopicID,
			MessageId: msgID,
			UserId:    t.currentUserID,
		})
		if err != nil {
			t.cluster.snitch(t.cluster.HeadAddr())
			t.cluster.refreshClusterState()
			t.showError(fmt.Sprintf("Failed to like message: %v", err))
			return
		}
		t.pages.RemovePage("like")
		t.loadMessages()
	})
	form.AddButton("Cancel", func() {
		t.pages.RemovePage("like")
	})

	form.SetBorder(true).SetTitle(" Like Message ").SetTitleAlign(tview.AlignCenter)

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 9, 1, true).
			AddItem(nil, 0, 1, false), 40, 1, true).
		AddItem(nil, 0, 1, false)

	t.pages.AddPage("like", modal, true, true)
}

func (t *TUI) showDeleteDialog() {
	form := tview.NewForm()
	var msgIDStr string

	form.AddInputField("Message ID to delete", "", 10, func(textToCheck string, lastChar rune) bool {
		return lastChar >= '0' && lastChar <= '9'
	}, func(text string) {
		msgIDStr = text
	})
	form.AddButton("Delete", func() {
		if msgIDStr == "" {
			return
		}
		msgID, _ := strconv.ParseInt(msgIDStr, 10, 64)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := t.writeClient()
		if client == nil {
			t.showError("No head node available")
			return
		}

		_, err := client.DeleteMessage(ctx, &pb.DeleteMessageRequest{
			TopicId:   t.currentTopicID,
			MessageId: msgID,
			UserId:    t.currentUserID,
		})
		if err != nil {
			t.cluster.snitch(t.cluster.HeadAddr())
			t.cluster.refreshClusterState()
			t.showError(fmt.Sprintf("Failed to delete message: %v", err))
			return
		}
		t.pages.RemovePage("delete")
		t.loadMessages()
	})
	form.AddButton("Cancel", func() {
		t.pages.RemovePage("delete")
	})

	form.SetBorder(true).SetTitle(" Delete Message ").SetTitleAlign(tview.AlignCenter)

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 9, 1, true).
			AddItem(nil, 0, 1, false), 40, 1, true).
		AddItem(nil, 0, 1, false)

	t.pages.AddPage("delete", modal, true, true)
}

func (t *TUI) showEditDialog() {
	form := tview.NewForm()
	var msgIDStr string
	var newText string

	form.AddInputField("Message ID to edit", "", 10, func(textToCheck string, lastChar rune) bool {
		return lastChar >= '0' && lastChar <= '9'
	}, func(text string) {
		msgIDStr = text
	})
	form.AddInputField("New text", "", 45, nil, func(text string) {
		newText = text
	})
	form.AddButton("Update", func() {
		if msgIDStr == "" || newText == "" {
			return
		}
		msgID, _ := strconv.ParseInt(msgIDStr, 10, 64)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := t.writeClient()
		if client == nil {
			t.showError("No head node available")
			return
		}

		_, err := client.UpdateMessage(ctx, &pb.UpdateMessageRequest{
			TopicId:   t.currentTopicID,
			MessageId: msgID,
			UserId:    t.currentUserID,
			Text:      newText,
		})
		if err != nil {
			t.cluster.snitch(t.cluster.HeadAddr())
			t.cluster.refreshClusterState()
			t.showError(fmt.Sprintf("Failed to update message: %v", err))
			return
		}
		t.pages.RemovePage("edit")
		t.loadMessages()
	})
	form.AddButton("Cancel", func() {
		t.pages.RemovePage("edit")
	})

	form.SetBorder(true).SetTitle(" Edit Message ").SetTitleAlign(tview.AlignCenter)

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 12, 1, true).
			AddItem(nil, 0, 1, false), 60, 1, true).
		AddItem(nil, 0, 1, false)

	t.pages.AddPage("edit", modal, true, true)
}

func (t *TUI) showError(message string) {
	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			t.pages.RemovePage("error")
		})

	t.pages.AddPage("error", modal, true, true)
}

func (t *TUI) startSubscription() {
	// prekliči prejšnjo
	if t.subCancel != nil {
		t.subCancel()
	}

	if t.currentTopicID < 0 {
		return
	}

	client := t.writeClient() // GetSubscriptionNode čez nodes
	if client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	subNodeResp, err := client.GetSubscriptionNode(ctx, &pb.SubscriptionNodeRequest{
		UserId:  t.currentUserID,
		TopicId: []int64{t.currentTopicID},
	})
	cancel()

	if err != nil {
		t.cluster.snitch(t.cluster.HeadAddr())
		t.cluster.refreshClusterState()
		return
	}

	// poveži se s subscription node
	subConn, err := grpc.NewClient(subNodeResp.Node.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return
	}

	subClient := pb.NewMessageBoardClient(subConn)

	subCtx, subCancel := context.WithCancel(context.Background())
	t.subCancel = subCancel

	stream, err := subClient.SubscribeTopic(subCtx, &pb.SubscribeTopicRequest{
		TopicId:        []int64{t.currentTopicID},
		UserId:         t.currentUserID,
		FromMessageId:  0,
		SubscribeToken: subNodeResp.SubscribeToken,
	})
	if err != nil {
		subCancel()
		subConn.Close()
		return
	}

	// Listening v ozadju
	go func() {
		defer subConn.Close()
		for {
			event, err := stream.Recv()
			if err == io.EOF || err != nil {
				return
			}

			t.app.QueueUpdateDraw(func() {
				t.loadMessages()

				opName := "UPDATE"
				switch event.Op {
				case pb.OpType_OP_POST:
					opName = "NEW"
				case pb.OpType_OP_LIKE:
					opName = "LIKE"
				case pb.OpType_OP_DELETE:
					opName = "DELETE"
				}
				t.statusBar.SetText(fmt.Sprintf("[green]User: %s[white] | [cyan]Event: %s on msg #%d[white]",
					t.currentUserName, opName, event.Message.Id))
			})
		}
	}()
}
