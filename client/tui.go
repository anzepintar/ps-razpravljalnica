package main

import (
	"context"
	"fmt"
	"io"
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
	app           *tview.Application
	client        pb.MessageBoardClient
	conn          *grpc.ClientConn
	serverAddress string

	currentUserID   int64
	currentUserName string

	statusBar    *tview.TextView
	topicsList   *tview.List
	messagesList *tview.TextView
	inputField   *tview.InputField

	topics         []*pb.Topic
	currentTopicID int64
	messages       []*pb.Message

	subCancel context.CancelFunc
}

func RunTUI(serverAddress string) error {
	tui := &TUI{
		app:           tview.NewApplication(),
		serverAddress: serverAddress,
	}

	conn, err := grpc.NewClient(serverAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connection to server %s failed: %v", serverAddress, err)
	}
	tui.conn = conn
	tui.client = pb.NewMessageBoardClient(conn)

	tui.currentUserID = 0
	tui.currentUserName = "TestUser"

	tui.setupUI()
	tui.loadTopics()

	return tui.app.Run()
}

func (t *TUI) setupUI() {
	t.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetText(fmt.Sprintf("[green]User: %s[white] | Server: %s", t.currentUserName, t.serverAddress))
	t.statusBar.SetBackgroundColor(tcell.ColorDarkBlue)

	t.topicsList = tview.NewList().
		ShowSecondaryText(false)
	t.topicsList.SetBorder(true).SetTitle(" Topics (r: refresh) ")

	t.topicsList.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		if index < len(t.topics) {
			t.currentTopicID = t.topics[index].Id
			t.loadMessages()
			t.startSubscription()
		}
	})

	t.messagesList = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true)
	t.messagesList.SetBorder(true).SetTitle(" Messages ")

	t.inputField = tview.NewInputField().
		SetLabel("Message: ").
		SetFieldWidth(0)
	t.inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			t.sendMessage()
		}
	})
	t.inputField.SetBorder(true).SetTitle(" Post Message (Enter to send) ")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewFlex().
			AddItem(t.topicsList, 30, 1, true).
			AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(t.messagesList, 0, 1, false).
				AddItem(t.inputField, 3, 0, false), 0, 3, false), 0, 1, true).
		AddItem(t.statusBar, 1, 0, false)

	t.app.SetRoot(layout, true)

	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			t.cleanup()
			t.app.Stop()
			return nil
		}
		return event
	})

	t.topicsList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			t.app.SetFocus(t.inputField)
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'r':
				t.loadTopics()
				return nil
			}
		}
		return event
	})

	t.inputField.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			t.app.SetFocus(t.topicsList)
			return nil
		}
		return event
	})
}

func (t *TUI) cleanup() {
	if t.subCancel != nil {
		t.subCancel()
	}
	if t.conn != nil {
		t.conn.Close()
	}
}

func (t *TUI) loadTopics() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := t.client.ListTopics(ctx, &emptypb.Empty{})
	if err != nil {
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

	resp, err := t.client.GetMessages(ctx, &pb.GetMessagesRequest{
		TopicId:       t.currentTopicID,
		FromMessageId: 0,
		Limit:         100,
	})
	if err != nil {
		t.showError(fmt.Sprintf("Failed to load messages: %v", err))
		return
	}

	t.messages = resp.Messages
	t.renderMessages()
}

func (t *TUI) renderMessages() {
	t.messagesList.Clear()
	var sb strings.Builder

	for _, msg := range t.messages {
		userName := fmt.Sprintf("User#%d", msg.UserId)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		user, err := t.client.GetUser(ctx, &pb.GetUserRequest{UserId: msg.UserId})
		cancel()
		if err == nil {
			userName = user.Name
		}

		timeStr := ""
		if msg.CreatedAt != nil {
			timeStr = msg.CreatedAt.AsTime().Format("15:04:05")
		}

		sb.WriteString(fmt.Sprintf("[%d] %s (%s)\n", msg.Id, userName, timeStr))
		sb.WriteString(fmt.Sprintf("  %s\n\n", msg.Text))
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

	_, err := t.client.PostMessage(ctx, &pb.PostMessageRequest{
		TopicId: t.currentTopicID,
		UserId:  t.currentUserID,
		Text:    text,
	})
	if err != nil {
		t.showError(fmt.Sprintf("Failed to send message: %v", err))
		return
	}

	t.inputField.SetText("")
	t.loadMessages()
}

func (t *TUI) showError(message string) {
	modal := tview.NewModal().
		SetText(message).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			t.app.Stop()
		})

	t.app.SetRoot(modal, true)
}

func (t *TUI) startSubscription() {
	if t.subCancel != nil {
		t.subCancel()
	}

	if t.currentTopicID < 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	subNodeResp, err := t.client.GetSubscriptionNode(ctx, &pb.SubscriptionNodeRequest{
		UserId:  t.currentUserID,
		TopicId: []int64{t.currentTopicID},
	})
	cancel()

	if err != nil {
		return
	}

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

	go func() {
		defer subConn.Close()
		for {
			_, err := stream.Recv()
			if err == io.EOF || err != nil {
				return
			}

			t.app.QueueUpdateDraw(func() {
				t.loadMessages()
			})
		}
	}()
}
