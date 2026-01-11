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
	logView      *tview.TextView
	topicsList   *tview.List
	messagesList *tview.TextView
	inputField   *tview.InputField

	topics         []*pb.Topic
	currentTopicID int64
	messages       []*pb.Message

	subCancel          context.CancelFunc
	stateRefreshCancel context.CancelFunc
}

// tuiInstance is used for logging to TUI
var tuiInstance *TUI

func RunTUI(entryPoint string) error {
	// Enable TUI mode to redirect logs to TUI
	tuiMode = true

	tui := &TUI{
		app:        tview.NewApplication(),
		entryPoint: entryPoint,
	}
	tuiInstance = tui

	// Set up log function to write to TUI
	tuiLogFunc = func(msg string) {
		if tui.logView != nil && tui.app != nil {
			tui.app.QueueUpdateDraw(func() {
				timestamp := time.Now().Format("15:04:05")
				fmt.Fprintf(tui.logView, "[dim]%s[white] %s\n", timestamp, msg)
				tui.logView.ScrollToEnd()
			})
		}
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

	// Log view for displaying system logs
	t.logView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetMaxLines(100)
	t.logView.SetBackgroundColor(tcell.ColorBlack)
	t.logView.SetBorder(true).SetTitle(" Logs ").SetBorderColor(tcell.ColorDarkGray)

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
	if t.stateRefreshCancel != nil {
		t.stateRefreshCancel()
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

// refreshAndGetWriteClient refreshes cluster state and returns write client
func (t *TUI) refreshAndGetWriteClient() pb.MessageBoardClient {
	t.tuiLog("[yellow]Refreshing cluster state...")
	if err := t.cluster.refreshClusterState(); err != nil {
		t.tuiLog("[red]Failed to refresh cluster state: %v", err)
		return nil
	}
	t.app.QueueUpdateDraw(func() {
		t.updateStatusBar()
	})
	return t.cluster.WriteClient()
}

// refreshAndGetReadClient refreshes cluster state and returns read client
func (t *TUI) refreshAndGetReadClient() pb.MessageBoardClient {
	t.tuiLog("[yellow]Refreshing cluster state...")
	if err := t.cluster.refreshClusterState(); err != nil {
		t.tuiLog("[red]Failed to refresh cluster state: %v", err)
		return nil
	}
	t.app.QueueUpdateDraw(func() {
		t.updateStatusBar()
	})
	return t.cluster.ReadClient()
}

// safeWriteOp executes a write operation with automatic retry on failure
func (t *TUI) safeWriteOp(opName string, op func(client pb.MessageBoardClient) error) error {
	client := t.writeClient()
	if client == nil {
		client = t.refreshAndGetWriteClient()
		if client == nil {
			return fmt.Errorf("no head node available")
		}
	}

	err := op(client)
	if err != nil {
		t.tuiLog("[yellow]%s failed, trying to recover: %v", opName, err)
		t.cluster.snitchAsync(t.cluster.HeadNodeID())

		// Try to refresh and retry once
		client = t.refreshAndGetWriteClient()
		if client == nil {
			return fmt.Errorf("no head node available after refresh")
		}

		err = op(client)
		if err != nil {
			return err
		}
		t.tuiLog("[green]%s succeeded after recovery", opName)
	}
	return nil
}

// safeReadOp executes a read operation with automatic retry on failure
func (t *TUI) safeReadOp(opName string, op func(client pb.MessageBoardClient) error) error {
	client := t.readClient()
	if client == nil {
		client = t.refreshAndGetReadClient()
		if client == nil {
			return fmt.Errorf("no tail node available")
		}
	}

	err := op(client)
	if err != nil {
		t.tuiLog("[yellow]%s failed, trying to recover: %v", opName, err)
		t.cluster.snitchAsync(t.cluster.TailNodeID())

		// Try to refresh and retry once
		client = t.refreshAndGetReadClient()
		if client == nil {
			return fmt.Errorf("no tail node available after refresh")
		}

		err = op(client)
		if err != nil {
			return err
		}
		t.tuiLog("[green]%s succeeded after recovery", opName)
	}
	return nil
}

// tuiLog writes a message to the log view
func (t *TUI) tuiLog(format string, args ...any) {
	if t.logView != nil && t.app != nil {
		msg := fmt.Sprintf(format, args...)
		t.app.QueueUpdateDraw(func() {
			timestamp := time.Now().Format("15:04:05")
			fmt.Fprintf(t.logView, "[dim]%s[white] %s\n", timestamp, msg)
			t.logView.ScrollToEnd()
		})
	}
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

		var user *pb.User
		err := t.safeWriteOp("Create user", func(client pb.MessageBoardClient) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var err error
			user, err = client.CreateUser(ctx, &pb.CreateUserRequest{Name: username})
			return err
		})

		if err != nil {
			t.tuiLog("[red]Failed to create user: %v", err)
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

		var user *pb.User
		err = t.safeReadOp("Get user", func(client pb.MessageBoardClient) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var err error
			user, err = client.GetUser(ctx, &pb.GetUserRequest{UserId: id})
			return err
		})

		if err != nil {
			t.tuiLog("[red]User not found: %v", err)
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
		SetText("[yellow]Keys:[white] Tab=switch | t=new topic | r=refresh | l=like | d=delete | e=edit | s=servers | q=quit")
	helpText.SetBackgroundColor(tcell.ColorDarkGreen)

	// razporeditev
	mainFlex := tview.NewFlex().
		AddItem(t.topicsList, 30, 1, true).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(t.messagesList, 0, 1, false).
			AddItem(t.inputField, 3, 0, false), 0, 3, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(mainFlex, 0, 1, true).
		AddItem(t.logView, 5, 0, false).
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
			case 's':
				t.showServersDialog()
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
			case 's':
				t.showServersDialog()
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

	// Start periodic cluster state refresh
	t.startStateRefresh()

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

// startStateRefresh starts a background goroutine that periodically refreshes
// the cluster state and updates the status bar
func (t *TUI) startStateRefresh() {
	ctx, cancel := context.WithCancel(context.Background())
	t.stateRefreshCancel = cancel

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := t.cluster.refreshClusterState(); err == nil {
					t.app.QueueUpdateDraw(func() {
						t.updateStatusBar()
					})
				}
			}
		}
	}()
}

func (t *TUI) updateStatusBar() {
	headAddr := t.cluster.HeadAddr()
	tailAddr := t.cluster.TailAddr()
	t.statusBar.SetText(fmt.Sprintf("[green]User: %s (ID: %d)[white] | Head: %s | Tail: %s | Topic: %d",
		t.currentUserName, t.currentUserID, headAddr, tailAddr, t.currentTopicID))
}

func (t *TUI) loadTopics() {
	var resp *pb.ListTopicsResponse

	err := t.safeReadOp("Load topics", func(client pb.MessageBoardClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var err error
		resp, err = client.ListTopics(ctx, &emptypb.Empty{})
		return err
	})

	if err != nil {
		t.tuiLog("[red]Failed to load topics: %v", err)
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

	var resp *pb.GetMessagesResponse

	err := t.safeReadOp("Load messages", func(client pb.MessageBoardClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var err error
		resp, err = client.GetMessages(ctx, &pb.GetMessagesRequest{
			TopicId:       t.currentTopicID,
			FromMessageId: 0,
			Limit:         100,
		})
		return err
	})

	if err != nil {
		t.tuiLog("[red]Failed to load messages: %v", err)
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

	topicID := t.currentTopicID
	userID := t.currentUserID

	err := t.safeWriteOp("Send message", func(client pb.MessageBoardClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.PostMessage(ctx, &pb.PostMessageRequest{
			TopicId: topicID,
			UserId:  userID,
			Text:    text,
		})
		return err
	})

	if err != nil {
		t.tuiLog("[red]Failed to send message: %v", err)
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

		err := t.safeWriteOp("Create topic", func(client pb.MessageBoardClient) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := client.CreateTopic(ctx, &pb.CreateTopicRequest{Name: topicName})
			return err
		})

		if err != nil {
			t.tuiLog("[red]Failed to create topic: %v", err)
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
		topicID := t.currentTopicID
		userID := t.currentUserID

		err := t.safeWriteOp("Like message", func(client pb.MessageBoardClient) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := client.LikeMessage(ctx, &pb.LikeMessageRequest{
				TopicId:   topicID,
				MessageId: msgID,
				UserId:    userID,
			})
			return err
		})

		if err != nil {
			t.tuiLog("[red]Failed to like message: %v", err)
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
		topicID := t.currentTopicID
		userID := t.currentUserID

		err := t.safeWriteOp("Delete message", func(client pb.MessageBoardClient) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := client.DeleteMessage(ctx, &pb.DeleteMessageRequest{
				TopicId:   topicID,
				MessageId: msgID,
				UserId:    userID,
			})
			return err
		})

		if err != nil {
			t.tuiLog("[red]Failed to delete message: %v", err)
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
		topicID := t.currentTopicID
		userID := t.currentUserID
		text := newText

		err := t.safeWriteOp("Update message", func(client pb.MessageBoardClient) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := client.UpdateMessage(ctx, &pb.UpdateMessageRequest{
				TopicId:   topicID,
				MessageId: msgID,
				UserId:    userID,
				Text:      text,
			})
			return err
		})

		if err != nil {
			t.tuiLog("[red]Failed to update message: %v", err)
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

func (t *TUI) showServersDialog() {
	// Refresh cluster state first
	t.cluster.refreshClusterState()

	headAddr := t.cluster.HeadAddr()
	headNodeID := t.cluster.HeadNodeID()
	tailAddr := t.cluster.TailAddr()
	tailNodeID := t.cluster.TailNodeID()
	controlAddrs := t.cluster.ControlAddrs()

	var sb strings.Builder
	sb.WriteString("[yellow]Control Plane Servers:[white]\n")
	for i, addr := range controlAddrs {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, addr))
	}
	sb.WriteString("\n[yellow]Data Plane Servers:[white]\n")
	sb.WriteString(fmt.Sprintf("  [green]Head:[white] %s (ID: %d)\n", headAddr, headNodeID))
	sb.WriteString(fmt.Sprintf("  [blue]Tail:[white] %s (ID: %d)\n", tailAddr, tailNodeID))

	textView := tview.NewTextView().
		SetDynamicColors(true).
		SetText(sb.String())
	textView.SetBorder(true).SetTitle(" Cluster Servers ").SetTitleAlign(tview.AlignCenter)
	textView.SetBackgroundColor(tcell.ColorBlack)

	textView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyEnter ||
			(event.Key() == tcell.KeyRune && (event.Rune() == 'q' || event.Rune() == 's')) {
			t.pages.RemovePage("servers")
			return nil
		}
		return event
	})

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(textView, 15, 1, true).
			AddItem(nil, 0, 1, false), 60, 1, true).
		AddItem(nil, 0, 1, false)

	t.pages.AddPage("servers", modal, true, true)
	t.app.SetFocus(textView)
	t.updateStatusBar()
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

	var subNodeResp *pb.SubscriptionNodeResponse
	topicID := t.currentTopicID
	userID := t.currentUserID

	err := t.safeWriteOp("Get subscription node", func(client pb.MessageBoardClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var err error
		subNodeResp, err = client.GetSubscriptionNode(ctx, &pb.SubscriptionNodeRequest{
			UserId:  userID,
			TopicId: []int64{topicID},
		})
		return err
	})

	if err != nil {
		t.tuiLog("[red]Subscription failed: %v", err)
		return
	}

	// Check if node info is valid
	if subNodeResp == nil || subNodeResp.Node == nil || subNodeResp.Node.Address == "" {
		t.tuiLog("[red]Subscription failed: no valid subscription node")
		return
	}

	// poveži se s subscription node
	subConn, err := grpc.NewClient(normalizeAddress(subNodeResp.Node.Address),
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
				// Log event to log view instead of status bar
				timestamp := time.Now().Format("15:04:05")
				fmt.Fprintf(t.logView, "[dim]%s[white] [cyan]Event:[white] %s on msg #%d\n", timestamp, opName, event.Message.Id)
				t.logView.ScrollToEnd()
			})
		}
	}()
}
