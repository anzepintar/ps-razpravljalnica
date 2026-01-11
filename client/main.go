package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/anzepintar/ps-razpravljalnica/client/razpravljalnica"

	_ "github.com/Jille/grpc-multi-resolver"
	_ "github.com/Jille/raft-grpc-leader-rpc/leaderhealth"
	_ "google.golang.org/grpc/health"
)

const (
	connectionIdleTimeout      = 5 * time.Minute
	connectionKeepaliveTime    = 30 * time.Second
	connectionKeepaliveTimeout = 10 * time.Second

	healthCheckInterval = 15 * time.Second
	healthCheckTimeout  = 3 * time.Second

	snitchMaxRetries    = 3
	snitchRetryInterval = 500 * time.Millisecond

	stateCacheTTL = 30 * time.Second
)

// tuiMode disables logging output when TUI is active
var tuiMode = false

var CLI struct {
	EntryPoint string        `help:"Entry point address (control plane node)" default:"127.0.0.1:6000" name:"entry" short:"e"`
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

func parseTUIFlags() (bool, string, error) {
	tuiFlags := flag.NewFlagSet("tui", flag.ContinueOnError)
	tuiFlags.SetOutput(io.Discard)

	tuiMode := tuiFlags.Bool("t", false, "Enable TUI mode")
	tuiModeLong := tuiFlags.Bool("tui", false, "Enable TUI mode")
	entryPoint := tuiFlags.String("e", "127.0.0.1:6000", "Entry point address")
	entryPointLong := tuiFlags.String("entry", "127.0.0.1:6000", "Entry point address")

	args := os.Args[1:]
	var remainingArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Handle combined short flags like -t or -e=value
		if arg == "-t" || arg == "--tui" {
			*tuiMode = true
			*tuiModeLong = true
		} else if arg == "-e" && i+1 < len(args) {
			*entryPoint = args[i+1]
			i++
		} else if arg == "--entry" && i+1 < len(args) {
			*entryPointLong = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "-e=") {
			*entryPoint = strings.TrimPrefix(arg, "-e=")
		} else if strings.HasPrefix(arg, "--entry=") {
			*entryPointLong = strings.TrimPrefix(arg, "--entry=")
		} else {
			remainingArgs = append(remainingArgs, arg)
		}
	}

	isTUI := *tuiMode || *tuiModeLong
	ep := *entryPoint
	if *entryPointLong != "127.0.0.1:6000" {
		ep = *entryPointLong
	}

	return isTUI, ep, nil
}

func main() {
	isTUI, entryPoint, err := parseTUIFlags()
	if err != nil {
		fmt.Printf("Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if isTUI {
		if err := RunTUI(entryPoint); err != nil {
			fmt.Printf("TUI Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
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

type SnitchError struct {
	NodeID  int64
	Retries int
	Err     error
}

func (e *SnitchError) Error() string {
	return fmt.Sprintf("snitch failed for node %d after %d retries: %v", e.NodeID, e.Retries, e.Err)
}

func (e *SnitchError) Unwrap() error {
	return e.Err
}

type CachedClusterState struct {
	ControlAddrs []string
	HeadAddr     string
	HeadNodeID   int64
	TailAddr     string
	TailNodeID   int64
	CachedAt     time.Time
}

func (c *CachedClusterState) IsValid() bool {
	if c == nil {
		return false
	}
	return time.Since(c.CachedAt) < stateCacheTTL
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
	headNodeID int64
	headConn   *grpc.ClientConn
	headClient pb.MessageBoardClient

	tailAddr   string
	tailNodeID int64
	tailConn   *grpc.ClientConn
	tailClient pb.MessageBoardClient

	keepaliveParams keepalive.ClientParameters

	cachedState *CachedClusterState

	healthCheckCancel context.CancelFunc
	healthCheckDone   chan struct{}
}

func NewClusterClient(entryPoint string) (*ClusterClient, error) {
	c := &ClusterClient{
		entryPoint: entryPoint,
		keepaliveParams: keepalive.ClientParameters{
			Time:                connectionKeepaliveTime,
			Timeout:             connectionKeepaliveTimeout,
			PermitWithoutStream: true, // Send pings even without active streams
		},
		healthCheckDone: make(chan struct{}),
	}

	// Try to connect with retries
	const maxRetries = 3
	backoff := time.Second
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			logMessage("Retrying connection to entry point (attempt %d/%d)...", attempt+1, maxRetries)
			time.Sleep(backoff)
			backoff = min(backoff*2, 8*time.Second)
		}

		if err := c.refreshClusterState(); err != nil {
			lastErr = err
			continue
		}
		// Success - start health check and return
		c.startHealthCheck()
		return c, nil
	}

	return nil, fmt.Errorf("failed to connect to cluster after %d attempts: %v", maxRetries, lastErr)
}

func (c *ClusterClient) refreshClusterState() error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	return c.refreshClusterStateLocked()
}

// normalizacija za 127.0.0.1 ipd
func normalizeAddress(addr string) string {
	if strings.Contains(addr, "://") {
		return addr
	}
	return "passthrough:///" + addr
}

func (c *ClusterClient) refreshClusterStateLocked() error {
	// Collect addresses to try: entry point first, then cached control addresses
	addressesToTry := []string{c.entryPoint}
	if c.cachedState != nil && len(c.cachedState.ControlAddrs) > 0 {
		for _, addr := range c.cachedState.ControlAddrs {
			if addr != c.entryPoint {
				addressesToTry = append(addressesToTry, addr)
			}
		}
	}

	var state *pb.GetClusterStateResponse
	var lastErr error
	var successAddr string

	// Try each address until one works
	for _, addr := range addressesToTry {
		target := normalizeAddress(addr)
		conn, err := grpc.NewClient(target,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithKeepaliveParams(c.keepaliveParams))
		if err != nil {
			lastErr = err
			continue
		}

		client := pb.NewControlPlaneClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		state, err = client.GetClusterStateClient(ctx, &emptypb.Empty{})
		cancel()
		conn.Close()

		if err != nil {
			lastErr = err
			continue
		}

		successAddr = addr
		break
	}

	if state == nil {
		// All addresses failed, try to use cached state for graceful degradation
		if c.cachedState != nil && c.cachedState.IsValid() {
			logMessage("Warning: all control nodes unreachable, using cached state (age: %v)",
				time.Since(c.cachedState.CachedAt))
			return nil
		}
		return fmt.Errorf("failed to get cluster state from any control node: %v", lastErr)
	}

	// Log if we used an alternative address
	if successAddr != c.entryPoint {
		logMessage("Connected to alternative control node: %s", successAddr)
	}

	c.controlAddrs = make([]string, len(state.Servers))
	for i, s := range state.Servers {
		c.controlAddrs[i] = s.Address
	}

	//  multi:///  naslov
	if len(c.controlAddrs) > 0 {
		multiAddr := "multi:///" + strings.Join(c.controlAddrs, ",")
		// Only close and recreate if addresses changed
		newAddrs := strings.Join(c.controlAddrs, ",")
		oldAddrs := ""
		if c.cachedState != nil {
			oldAddrs = strings.Join(c.cachedState.ControlAddrs, ",")
		}
		if c.controlConn != nil && newAddrs != oldAddrs {
			c.controlConn.Close()
			c.controlConn = nil
		}
		// Retry options for control plane calls (leader might change)
		retryOpts := []grpc_retry.CallOption{
			grpc_retry.WithBackoff(grpc_retry.BackoffExponential(100 * time.Millisecond)),
			grpc_retry.WithMax(5),
			grpc_retry.WithCodes(codes.Unavailable, codes.ResourceExhausted),
		}
		if c.controlConn == nil {
			var err error
			c.controlConn, err = grpc.NewClient(multiAddr,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultServiceConfig(`{"healthCheckConfig": {"serviceName": ""}}`),
				grpc.WithUnaryInterceptor(grpc_retry.UnaryClientInterceptor(retryOpts...)),
				grpc.WithKeepaliveParams(c.keepaliveParams),
			)
			if err != nil {
				logMessage("Warning: could not create multi-resolver connection: %v", err)
				c.controlConn, err = grpc.NewClient(normalizeAddress(c.controlAddrs[0]),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithUnaryInterceptor(grpc_retry.UnaryClientInterceptor(retryOpts...)),
					grpc.WithKeepaliveParams(c.keepaliveParams))
				if err != nil {
					return fmt.Errorf("failed to connect to control plane: %v", err)
				}
			}
			c.controlClient = pb.NewControlPlaneClient(c.controlConn)
		}
	}

	// head za pisanje - ponovno uporabi povezavo, če se ne spremeni
	if state.Head != nil {
		if c.headConn != nil && c.headAddr != state.Head.Address {
			c.headConn.Close()
			c.headConn = nil
		}
		if c.headConn == nil || c.headAddr != state.Head.Address {
			c.headAddr = state.Head.Address
			c.headNodeID = state.Head.NodeId
			var headErr error
			c.headConn, headErr = grpc.NewClient(normalizeAddress(c.headAddr),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithKeepaliveParams(c.keepaliveParams))
			if headErr != nil {
				return fmt.Errorf("failed to connect to head %s: %v", c.headAddr, headErr)
			}
			c.headClient = pb.NewMessageBoardClient(c.headConn)
		}
	}

	// tail za branje
	if state.Tail != nil {
		if c.tailConn != nil && c.tailAddr != state.Tail.Address {
			c.tailConn.Close()
			c.tailConn = nil
		}
		if c.tailConn == nil || c.tailAddr != state.Tail.Address {
			c.tailAddr = state.Tail.Address
			c.tailNodeID = state.Tail.NodeId
			var tailErr error
			c.tailConn, tailErr = grpc.NewClient(normalizeAddress(c.tailAddr),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithKeepaliveParams(c.keepaliveParams))
			if tailErr != nil {
				return fmt.Errorf("failed to connect to tail %s: %v", c.tailAddr, tailErr)
			}
			c.tailClient = pb.NewMessageBoardClient(c.tailConn)
		}
	}

	// Update cached state for graceful degradation
	c.cachedState = &CachedClusterState{
		ControlAddrs: c.controlAddrs,
		HeadAddr:     c.headAddr,
		HeadNodeID:   c.headNodeID,
		TailAddr:     c.tailAddr,
		TailNodeID:   c.tailNodeID,
		CachedAt:     time.Now(),
	}

	return nil
}

// snitch reports a failed node to the control plane with retry logic
// Returns a SnitchError if all retries fail, nil on success
func (c *ClusterClient) snitch(nodeID int64) error {
	return c.snitchWithRetries(nodeID, snitchMaxRetries)
}

// snitchWithRetries reports a failed node with configurable retries
func (c *ClusterClient) snitchWithRetries(nodeID int64, maxRetries int) error {
	c.mtx.RLock()
	client := c.controlClient
	c.mtx.RUnlock()

	if client == nil {
		err := errors.New("no control plane client available")
		logMessage("Warning: snitch failed: %v", err)
		return &SnitchError{NodeID: nodeID, Retries: 0, Err: err}
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(snitchRetryInterval * time.Duration(attempt))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, lastErr = client.Snitch(ctx, &pb.SnitchRequest{NodeId: nodeID})
		cancel()

		if lastErr == nil {
			logMessage("Successfully snitched on node %d (attempt %d)", nodeID, attempt+1)
			return nil
		}

		logMessage("Snitch attempt %d/%d failed for node %d: %v", attempt+1, maxRetries, nodeID, lastErr)
	}

	return &SnitchError{NodeID: nodeID, Retries: maxRetries, Err: lastErr}
}

// snitchAsync performs snitch in background, useful for non-critical paths
func (c *ClusterClient) snitchAsync(nodeID int64) {
	go func() {
		if err := c.snitch(nodeID); err != nil {
			logMessage("Async snitch failed: %v", err)
		}
	}()
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

func (c *ClusterClient) HeadNodeID() int64 {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.headNodeID
}

func (c *ClusterClient) TailAddr() string {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.tailAddr
}

func (c *ClusterClient) TailNodeID() int64 {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.tailNodeID
}

// ControlAddrs returns the list of control plane addresses
func (c *ClusterClient) ControlAddrs() []string {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	addrs := make([]string, len(c.controlAddrs))
	copy(addrs, c.controlAddrs)
	return addrs
}

// tuiLogFunc is set by TUI to receive log messages
var tuiLogFunc func(string)

// logMessage logs to TUI if available, otherwise to standard log
func logMessage(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if tuiMode && tuiLogFunc != nil {
		tuiLogFunc(msg)
	} else {
		log.Print(msg)
	}
}

// startHealthCheck starts a background goroutine that periodically checks
// head and tail node health and proactively refreshes state if issues are detected
func (c *ClusterClient) startHealthCheck() {
	ctx, cancel := context.WithCancel(context.Background())
	c.healthCheckCancel = cancel

	go func() {
		ticker := time.NewTicker(healthCheckInterval)
		defer ticker.Stop()
		defer close(c.healthCheckDone)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.performHealthCheck()
			}
		}
	}()
}

// performHealthCheck checks health of head and tail nodes
func (c *ClusterClient) performHealthCheck() {
	c.mtx.RLock()
	headClient := c.headClient
	tailClient := c.tailClient
	headNodeID := c.headNodeID
	tailNodeID := c.tailNodeID
	c.mtx.RUnlock()

	needsRefresh := false

	// Check head node health
	if headClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
		_, err := headClient.ListTopics(ctx, &emptypb.Empty{})
		cancel()
		if err != nil {
			logMessage("Health check: head node %d unhealthy: %v", headNodeID, err)
			c.snitchAsync(headNodeID)
			needsRefresh = true
		}
	}

	// Check tail node health
	if tailClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
		_, err := tailClient.ListTopics(ctx, &emptypb.Empty{})
		cancel()
		if err != nil {
			logMessage("Health check: tail node %d unhealthy: %v", tailNodeID, err)
			c.snitchAsync(tailNodeID)
			needsRefresh = true
		}
	}

	if needsRefresh {
		logMessage("Health check triggered cluster state refresh")
		if err := c.refreshClusterState(); err != nil {
			logMessage("Health check: failed to refresh cluster state: %v", err)
		}
	}
}

// stopHealthCheck stops the background health check goroutine
func (c *ClusterClient) stopHealthCheck() {
	if c.healthCheckCancel != nil {
		c.healthCheckCancel()
		<-c.healthCheckDone // Wait for goroutine to finish
	}
}

func (c *ClusterClient) Close() {
	// Stop health check first
	c.stopHealthCheck()

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
		cluster.snitch(cluster.HeadNodeID())
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
		cluster.snitch(cluster.HeadNodeID())
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
		cluster.snitch(cluster.HeadNodeID())
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
		cluster.snitch(cluster.HeadNodeID())
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
		cluster.snitch(cluster.HeadNodeID())
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
		cluster.snitch(cluster.HeadNodeID())
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
		cluster.snitch(cluster.TailNodeID())
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
		cluster.snitch(cluster.TailNodeID())
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
		cluster.snitch(cluster.HeadNodeID())
		cluster.refreshClusterState()
		return fmt.Errorf("error getting subscription node: %v", err)
	}

	fmt.Printf("Subscribing to topics %v as user %d...\n", topicIDs, c.UserID)
	fmt.Printf("Connecting to subscription node: %s\n", subNodeResp.Node.Address)

	subConn, err := grpc.NewClient(normalizeAddress(subNodeResp.Node.Address),
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
