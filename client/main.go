package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/anzepintar/ps-razpravljalnica/client/razpravljalnica"

	_ "github.com/Jille/raft-grpc-leader-rpc/leaderhealth"
	_ "google.golang.org/grpc/health"
)

var CLI struct {
	EntryPoint string        `help:"Entry point address (control plane node)" default:"localhost:6000" name:"entry" short:"e"`
	Timeout    time.Duration `help:"Request timeout" default:"5s" name:"timeout"`
	Tui        bool          `short:"t" help:"Enable TUI mode"`

	CreateUser  CreateUserCmd  `cmd:"create-user" help:"Create a new user"`
	CreateTopic CreateTopicCmd `cmd:"create-topic" help:"Create a new topic"`
	PostMessage PostMessageCmd `cmd:"post" help:"Post a message to a topic"`
	Update      UpdateMsgCmd   `cmd:"update" help:"Update a message"`
	Delete      DeleteMsgCmd   `cmd:"delete" help:"Delete a message"`
	Like        LikeMsgCmd     `cmd:"like" help:"Like a message"`
	ListTopics  ListTopicsCmd  `cmd:"list-topics" help:"List topics"`
	GetMessages GetMessagesCmd `cmd:"get-messages" help:"Get messages for a topic"`
	Subscribe   SubscribeCmd   `cmd:"subscribe" help:"Subscribe to topics (streaming)"`
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
	// Check for -t flag before kong parsing to allow TUI without command
	for _, arg := range os.Args[1:] {
		if arg == "-t" || arg == "--tui" {
			entryPoint := "localhost:6000"
			// Check for -e flag
			for i, a := range os.Args[1:] {
				if (a == "-e" || a == "--entry") && i+2 < len(os.Args) {
					entryPoint = os.Args[i+2]
				} else if len(a) > 3 && a[:3] == "-e=" {
					entryPoint = a[3:]
				} else if len(a) > 8 && a[:8] == "--entry=" {
					entryPoint = a[8:]
				}
			}
			if err := RunTUI(entryPoint); err != nil {
				fmt.Printf("TUI Error: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	ctx := kong.Parse(&CLI,
		kong.Name("client"),
		kong.Description("Razpravljalnica CLI - use -t for TUI mode"),
		kong.UsageOnError(),
	)

	if err := ctx.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

// Ureja povezave na cluster
// piše v head, bere iz tail
// uporablja control plane za stanje clustra in snitching
type ClusterClient struct {
	mtx sync.RWMutex

	entryPoint string

	// Control plane nodes - raft cluster
	controlAddrs  []string
	controlConn   *grpc.ClientConn
	controlClient pb.ControlPlaneClient

	// Data plane nodes
	headAddr   string
	headConn   *grpc.ClientConn
	headClient pb.MessageBoardClient

	tailAddr   string
	tailConn   *grpc.ClientConn
	tailClient pb.MessageBoardClient
}

func NewClusterClient(entryPoint string) (*ClusterClient, error) {
	c := &ClusterClient{
		entryPoint: entryPoint,
	}

	if err := c.refreshClusterState(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *ClusterClient) refreshClusterState() error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	// najprej povezava na vhodni node
	conn, err := grpc.NewClient(c.entryPoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to entry point %s: %v", c.entryPoint, err)
	}

	client := pb.NewControlPlaneClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state, err := client.GetClusterStateClient(ctx, &emptypb.Empty{})
	conn.Close()
	if err != nil {
		return fmt.Errorf("failed to get cluster state: %v", err)
	}

	c.controlAddrs = make([]string, len(state.Servers))
	for i, s := range state.Servers {
		c.controlAddrs[i] = s.Address
	}

	//  multi:///  naslov
	if len(c.controlAddrs) > 0 {
		multiAddr := "multi:///" + strings.Join(c.controlAddrs, ",")
		if c.controlConn != nil {
			c.controlConn.Close()
		}
		c.controlConn, err = grpc.NewClient(multiAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultServiceConfig(`{"healthCheckConfig": {"serviceName": ""}}`),
		)
		if err != nil {
			log.Printf("Warning: could not create multi-resolver connection: %v", err)
			c.controlConn, err = grpc.NewClient(c.controlAddrs[0],
				grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("failed to connect to control plane: %v", err)
			}
		}
		c.controlClient = pb.NewControlPlaneClient(c.controlConn)
	}

	// head za pisanje
	if state.Head != nil {
		if c.headConn != nil && c.headAddr != state.Head.Address {
			c.headConn.Close()
		}
		if c.headAddr != state.Head.Address {
			c.headAddr = state.Head.Address
			c.headConn, err = grpc.NewClient(c.headAddr,
				grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("failed to connect to head %s: %v", c.headAddr, err)
			}
			c.headClient = pb.NewMessageBoardClient(c.headConn)
		}
	}

	// tail za branje
	if state.Tail != nil {
		if c.tailConn != nil && c.tailAddr != state.Tail.Address {
			c.tailConn.Close()
		}
		if c.tailAddr != state.Tail.Address {
			c.tailAddr = state.Tail.Address
			c.tailConn, err = grpc.NewClient(c.tailAddr,
				grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("failed to connect to tail %s: %v", c.tailAddr, err)
			}
			c.tailClient = pb.NewMessageBoardClient(c.tailConn)
		}
	}

	return nil
}

func (c *ClusterClient) snitch(nodeAddr string) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	// Najde node id, in snitcha na control pane
	for _, addr := range c.controlAddrs {
		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			continue
		}
		client := pb.NewControlPlaneClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		// osvežitev
		_, _ = client.GetClusterStateClient(ctx, &emptypb.Empty{})
		cancel()
		conn.Close()
		break
	}
}

func (c *ClusterClient) WriteClient() pb.MessageBoardClient {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.headClient
}

func (c *ClusterClient) ReadClient() pb.MessageBoardClient {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.tailClient
}

func (c *ClusterClient) HeadAddr() string {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.headAddr
}

func (c *ClusterClient) TailAddr() string {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.tailAddr
}

func (c *ClusterClient) Close() {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if c.headConn != nil {
		c.headConn.Close()
	}
	if c.tailConn != nil {
		c.tailConn.Close()
	}
	if c.controlConn != nil {
		c.controlConn.Close()
	}
}

var clusterClient *ClusterClient

func getClusterClient() (*ClusterClient, error) {
	if clusterClient != nil {
		return clusterClient, nil
	}
	var err error
	clusterClient, err = NewClusterClient(CLI.EntryPoint)
	return clusterClient, err
}

func (c *CreateUserCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.WriteClient()
	if client == nil {
		return fmt.Errorf("no head node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.CreateUser(reqCtx, &pb.CreateUserRequest{Name: c.Name})
	if err != nil {
		cluster.snitch(cluster.HeadAddr())
		cluster.refreshClusterState()
		return fmt.Errorf("error creating user: %v", err)
	}
	fmt.Printf("User created: ID=%d, Name=%s\n", resp.Id, resp.Name)
	return nil
}

func (c *CreateTopicCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.WriteClient()
	if client == nil {
		return fmt.Errorf("no head node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.CreateTopic(reqCtx, &pb.CreateTopicRequest{Name: c.Name})
	if err != nil {
		cluster.snitch(cluster.HeadAddr())
		cluster.refreshClusterState()
		return fmt.Errorf("error creating topic: %v", err)
	}
	fmt.Printf("Topic created: ID=%d, Name=%s\n", resp.Id, resp.Name)
	return nil
}

func (c *PostMessageCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.WriteClient()
	if client == nil {
		return fmt.Errorf("no head node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.PostMessage(reqCtx, &pb.PostMessageRequest{
		TopicId: c.TopicID,
		UserId:  c.UserID,
		Text:    c.Text,
	})
	if err != nil {
		cluster.snitch(cluster.HeadAddr())
		cluster.refreshClusterState()
		return fmt.Errorf("error posting message: %v", err)
	}
	fmt.Printf("Message posted: ID=%d, Text=%s, Likes=%d\n", resp.Id, resp.Text, resp.Likes)
	return nil
}

func (c *UpdateMsgCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.WriteClient()
	if client == nil {
		return fmt.Errorf("no head node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.UpdateMessage(reqCtx, &pb.UpdateMessageRequest{
		TopicId:   c.TopicID,
		MessageId: c.MessageID,
		UserId:    c.UserID,
		Text:      c.Text,
	})
	if err != nil {
		cluster.snitch(cluster.HeadAddr())
		cluster.refreshClusterState()
		return fmt.Errorf("error updating message: %v", err)
	}
	fmt.Printf("Message updated: ID=%d, New text=%s\n", resp.Id, resp.Text)
	return nil
}

func (c *DeleteMsgCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.WriteClient()
	if client == nil {
		return fmt.Errorf("no head node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	_, err = client.DeleteMessage(reqCtx, &pb.DeleteMessageRequest{
		TopicId:   c.TopicID,
		MessageId: c.MessageID,
		UserId:    c.UserID,
	})
	if err != nil {
		cluster.snitch(cluster.HeadAddr())
		cluster.refreshClusterState()
		return fmt.Errorf("error deleting message: %v", err)
	}
	fmt.Println("Message successfully deleted.")
	return nil
}

func (c *LikeMsgCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.WriteClient()
	if client == nil {
		return fmt.Errorf("no head node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.LikeMessage(reqCtx, &pb.LikeMessageRequest{
		TopicId:   c.TopicID,
		MessageId: c.MessageID,
		UserId:    c.UserID,
	})
	if err != nil {
		cluster.snitch(cluster.HeadAddr())
		cluster.refreshClusterState()
		return fmt.Errorf("error liking message: %v", err)
	}
	fmt.Printf("Message liked: ID=%d, Likes=%d\n", resp.Id, resp.Likes)
	return nil
}

func (c *ListTopicsCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.ReadClient()
	if client == nil {
		return fmt.Errorf("no tail node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.ListTopics(reqCtx, &emptypb.Empty{})
	if err != nil {
		cluster.snitch(cluster.TailAddr())
		cluster.refreshClusterState()
		return fmt.Errorf("error retrieving topics: %v", err)
	}
	fmt.Println("Topics list:")
	for _, topic := range resp.Topics {
		fmt.Printf("  ID=%d, Name=%s\n", topic.Id, topic.Name)
	}
	return nil
}

func (c *GetMessagesCmd) Run(ctx *kong.Context) error {
	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	client := cluster.ReadClient()
	if client == nil {
		return fmt.Errorf("no tail node available")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	resp, err := client.GetMessages(reqCtx, &pb.GetMessagesRequest{
		TopicId:       c.TopicID,
		FromMessageId: c.FromMessageID,
		Limit:         int32(c.Limit),
	})
	if err != nil {
		cluster.snitch(cluster.TailAddr())
		cluster.refreshClusterState()
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

	cluster, err := getClusterClient()
	if err != nil {
		return err
	}

	// GetSubscriptionNode - dejansko je write operacija, gre čez nodes
	client := cluster.WriteClient()
	if client == nil {
		return fmt.Errorf("no head node available")
	}

	// Naročnina in token?
	reqCtx, cancel := context.WithTimeout(context.Background(), CLI.Timeout)
	defer cancel()

	subNodeResp, err := client.GetSubscriptionNode(reqCtx, &pb.SubscriptionNodeRequest{
		UserId:  c.UserID,
		TopicId: topicIDs,
	})
	if err != nil {
		cluster.snitch(cluster.HeadAddr())
		cluster.refreshClusterState()
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
