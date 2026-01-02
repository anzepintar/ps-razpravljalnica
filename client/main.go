package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/anzepintar/ps-razpravljalnica/client/razpravljalnica"
)

var CLI struct {
	Server  string        `help:"Server address (host:port)" default:"localhost:5000" name:"server" short:"s"`
	Timeout time.Duration `help:"Request timeout" default:"5s" name:"timeout"`
	Cli     CliCmds       `cmd:"" help:"Command-line client operations"`
}

// CLI skupine ukazov
type CliCmds struct {
	CreateUser  CreateUserCmd  `cmd:"create-user" help:"Create a new user"`
	CreateTopic CreateTopicCmd `cmd:"create-topic" help:"Create a new topic"`
	PostMessage PostMessageCmd `cmd:"post" help:"Post a message to a topic"`
	Update      UpdateMsgCmd   `cmd:"update" help:"Update a message"`
	Delete      DeleteMsgCmd   `cmd:"delete" help:"Delete a message"`
	Like        LikeMsgCmd     `cmd:"like" help:"Like a message"`
	ListTopics  ListTopicsCmd  `cmd:"list-topics" help:"List topics"`
	GetMessages GetMessagesCmd `cmd:"get-messages" help:"Get messages for a topic"`
	Subscribe   SubscribeCmd   `cmd:"subscribe" help:"Subscribe to topics (streaming - todo)"`
}

// Definicije ukazov

type CreateUserCmd struct {
	Name string `arg:"" required:"" help:"Name of the user"`
}

type CreateTopicCmd struct {
	Name string `arg:"" required:"" help:"Name of the topic"`
}

type PostMessageCmd struct {
	TopicID int64  `name:"topic-id" help:"ID of the topic" required:"true"`
	UserID  int64  `name:"user-id" help:"ID of the user" required:"true"`
	Text    string `arg:"" help:"Message text" required:"true"`
}

type UpdateMsgCmd struct {
	TopicID   int64  `name:"topic-id" help:"ID of the topic" required:"true"`
	MessageID int64  `name:"message-id" help:"ID of the message" required:"true"`
	UserID    int64  `name:"user-id" help:"ID of the user" required:"true"`
	Text      string `arg:"" help:"New message text" required:"true"`
}

type DeleteMsgCmd struct {
	TopicID   int64 `name:"topic-id" help:"ID of the topic" required:"true"`
	MessageID int64 `name:"message-id" help:"ID of the message" required:"true"`
	UserID    int64 `name:"user-id" help:"ID of the user" required:"true"`
}

type LikeMsgCmd struct {
	TopicID   int64 `name:"topic-id" help:"ID of the topic" required:"true"`
	MessageID int64 `name:"message-id" help:"ID of the message" required:"true"`
	UserID    int64 `name:"user-id" help:"ID of the user" required:"true"`
}

type ListTopicsCmd struct{}

type GetMessagesCmd struct {
	TopicID       int64 `name:"topic-id" help:"ID of the topic" required:"true"`
	FromMessageID int64 `name:"from" help:"Starting message ID (0 = from beginning)" default:"0"`
	Limit         int   `name:"limit" help:"Max number of messages" default:"100"`
}

type SubscribeCmd struct {
	TopicIDs string `name:"topic-ids" help:"Comma separated topic ids to subscribe to" required:"true"`
	UserID   int64  `name:"user-id" help:"ID of the user" required:"true"`
	From     int64  `name:"from" help:"From message id" default:"0"`
}

func main() {
	if len(os.Args) == 1 {
		// TODO: TUI
		fmt.Println("TODO: TUI not yet implemented. Use 'client cli <command>'.")
		os.Exit(0)
	}

	ctx := kong.Parse(&CLI,
		kong.Name("client"),
		kong.Description("Razpravljalnica CLI - use 'client cli <command>' for commands"),
		kong.UsageOnError(),
	)

	if err := ctx.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func getGRPCClient() (pb.MessageBoardClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(CLI.Server,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("connection to server %s failed: %v", CLI.Server, err)
	}

	client := pb.NewMessageBoardClient(conn)
	return client, conn, nil
}

func (c *CreateUserCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.CreateUser(reqCtx, &pb.CreateUserRequest{Name: c.Name})
	if err != nil {
		return fmt.Errorf("error creating user: %v", err)
	}
	fmt.Printf("User created: ID=%d, Name=%s\n", resp.Id, resp.Name)
	return nil
}

func (c *CreateTopicCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.CreateTopic(reqCtx, &pb.CreateTopicRequest{Name: c.Name})
	if err != nil {
		return fmt.Errorf("error creating topic: %v", err)
	}
	fmt.Printf("Topic created: ID=%d, Name=%s\n", resp.Id, resp.Name)
	return nil
}

func (c *PostMessageCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.PostMessage(reqCtx, &pb.PostMessageRequest{
		TopicId: c.TopicID,
		UserId:  c.UserID,
		Text:    c.Text,
	})
	if err != nil {
		return fmt.Errorf("error posting message: %v", err)
	}
	fmt.Printf("Message posted: ID=%d, Text=%s, Likes=%d\n", resp.Id, resp.Text, resp.Likes)
	return nil
}

func (c *UpdateMsgCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.UpdateMessage(reqCtx, &pb.UpdateMessageRequest{
		TopicId:   c.TopicID,
		MessageId: c.MessageID,
		UserId:    c.UserID,
		Text:      c.Text,
	})
	if err != nil {
		return fmt.Errorf("error updating message: %v", err)
	}
	fmt.Printf("Message updated: ID=%d, New text=%s\n", resp.Id, resp.Text)
	return nil
}

func (c *DeleteMsgCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	_, err = client.DeleteMessage(reqCtx, &pb.DeleteMessageRequest{
		TopicId:   c.TopicID,
		MessageId: c.MessageID,
		UserId:    c.UserID,
	})
	if err != nil {
		return fmt.Errorf("error deleting message: %v", err)
	}
	fmt.Println("Message successfully deleted.")
	return nil
}

func (c *LikeMsgCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.LikeMessage(reqCtx, &pb.LikeMessageRequest{
		TopicId:   c.TopicID,
		MessageId: c.MessageID,
		UserId:    c.UserID,
	})
	if err != nil {
		return fmt.Errorf("error liking message: %v", err)
	}
	fmt.Printf("Message liked: ID=%d, Likes=%d\n", resp.Id, resp.Likes)
	return nil
}

func (c *ListTopicsCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.ListTopics(reqCtx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("error retrieving topics: %v", err)
	}
	fmt.Println("Topics list:")
	for _, topic := range resp.Topics {
		fmt.Printf("  ID=%d, Name=%s\n", topic.Id, topic.Name)
	}
	return nil
}

func (c *GetMessagesCmd) Run(ctx *kong.Context) error {
	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.GetMessages(reqCtx, &pb.GetMessagesRequest{
		TopicId:       c.TopicID,
		FromMessageId: c.FromMessageID,
		Limit:         int32(c.Limit),
	})
	if err != nil {
		return fmt.Errorf("error retrieving messages: %v", err)
	}
	fmt.Printf("Messages in topic %d:\n", c.TopicID)
	for _, msg := range resp.Messages {
		fmt.Printf("  [ID=%d, User=%d, Likes=%d] %s\n", msg.Id, msg.UserId, msg.Likes, msg.Text)
	}
	return nil
}

func (c *SubscribeCmd) Run(ctx *kong.Context) error {
	topicIDsStr := strings.Split(c.TopicIDs, ",")
	topicIDs := make([]int64, 0, len(topicIDsStr))
	for _, idStr := range topicIDsStr {
		id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid topic ID '%s': %v", idStr, err)
		}
		topicIDs = append(topicIDs, id)
	}

	client, conn, err := getGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Naročnina in token?
	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	subNodeResp, err := client.GetSubscriptionNode(reqCtx, &pb.SubscriptionNodeRequest{
		UserId:  c.UserID,
		TopicId: topicIDs,
	})
	if err != nil {
		return fmt.Errorf("error getting subscription node: %v", err)
	}

	fmt.Printf("Subscribing to topics %v as user %d...\n", topicIDs, c.UserID)
	fmt.Printf("Connecting to subscription node: %s\n", subNodeResp.Node.Address)

	subConn, err := grpc.NewClient(subNodeResp.Node.Address,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connection to subscription node failed: %v", err)
	}
	defer subConn.Close()

	subClient := pb.NewMessageBoardClient(subConn)

	// Naročnina na teme z žetonom?
	stream, err := subClient.SubscribeTopic(context.Background(), &pb.SubscribeTopicRequest{
		TopicId:        topicIDs,
		UserId:         c.UserID,
		FromMessageId:  c.From,
		SubscribeToken: subNodeResp.SubscribeToken,
	})
	if err != nil {
		return fmt.Errorf("error subscribing: %v", err)
	}

	fmt.Println("Listening for messages... (Ctrl+C to stop)")

	// Stream
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			fmt.Println("Stream closed.")
			break
		}
		if err != nil {
			return fmt.Errorf("error receiving: %v", err)
		}

		// Izpis
		opType := "UNKNOWN"
		switch event.Op {
		case pb.OpType_OP_POST:
			opType = "POST"
		case pb.OpType_OP_LIKE:
			opType = "LIKE"
		case pb.OpType_OP_DELETE:
			opType = "DELETE"
		case pb.OpType_OP_UPDATE:
			opType = "UPDATE"
		}

		fmt.Printf("[%s #%d] Topic=%d, Message=%d, User=%d, Likes=%d\n",
			opType, event.SequenceNumber, event.Message.TopicId,
			event.Message.Id, event.Message.UserId, event.Message.Likes)
		fmt.Printf("  Text: %s\n", event.Message.Text)
	}

	return nil
}
