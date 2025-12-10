package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	rpb "server/razpravljalnica"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alecthomas/kong"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type RunNodeCmd struct {
	ControllAddress string `arg:""`
}

func (s *RunNodeCmd) Run() error {
	lis, err := net.Listen("tcp", ":50051") // TODO rm this port
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	srv := grpc.NewServer()
	var msrv = MessageBoardServer{}
	rpb.RegisterMessageBoardServer(srv, &msrv)

	log.Printf("server listening at %v", lis.Addr())
	if err := srv.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}
	return nil
}

var cli struct {
	RunNode RunNodeCmd `cmd:"" help:"Starts a node in the chain"`
	Start   struct{}   `cmd:"" default:"1"`
}

type MessageBoardServer struct {
	rpb.UnimplementedMessageBoardServer

	topics_mtx sync.RWMutex
	Topics     []Topic
	users_mtx  sync.RWMutex
	Users      []User
}

type Topic struct {
	Name        string
	Messages    []Message
	Subscribers []struct {
		Token string
		ch    chan<- MessageEv
	}
}

type User struct {
	Name string
}
type Message struct {
	User       int64
	Text       string
	Created_at struct{}
	Likes      atomic.Int32
}
type MessageEv struct{}

func (s *MessageBoardServer) CreateTopic(_ context.Context, req *rpb.CreateTopicRequest) (*rpb.Topic, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing Topic name")
	}
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	s.Topics = append(s.Topics, Topic{Name: name})
	return &rpb.Topic{Id: int64(len(s.Topics) - 1), Name: name}, nil
}
func (s *MessageBoardServer) CreateUser(_ context.Context, req *rpb.CreateUserRequest) (*rpb.User, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "Missing User name")
	}
	s.users_mtx.Lock()
	defer s.users_mtx.Unlock()

	s.Users = append(s.Users, User{Name: name})
	return &rpb.User{Id: int64(len(s.Users) - 1), Name: name}, nil
}
func (s *MessageBoardServer) DeleteMessage(_ context.Context, _ *rpb.DeleteMessageRequest) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteMessage not implemented")
}
func (s *MessageBoardServer) GetMessages(_ context.Context, _ *rpb.GetMessagesRequest) (*rpb.GetMessagesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetMessages not implemented")
}
func (s *MessageBoardServer) GetSubscriptionNode(_ context.Context, _ *rpb.SubscriptionNodeRequest) (*rpb.SubscriptionNodeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetSubcscriptionNode not implemented")
}
func (s *MessageBoardServer) LikeMessage(_ context.Context, _ *rpb.LikeMessageRequest) (*rpb.Message, error) {
	return nil, status.Error(codes.Unimplemented, "method LikeMessage not implemented")
}
func (s *MessageBoardServer) ListTopics(_ context.Context, _ *emptypb.Empty) (*rpb.ListTopicsResponse, error) {
	s.topics_mtx.RLock()
	defer s.topics_mtx.RUnlock()
	out := make([]*rpb.Topic, len(s.Topics))
	for i, v := range s.Topics {
		out[i] = &rpb.Topic{Id: int64(i), Name: v.Name}
	}
	return &rpb.ListTopicsResponse{Topics: out}, nil
}
func (s *MessageBoardServer) PostMessage(_ context.Context, _ *rpb.PostMessageRequest) (*rpb.Message, error) {
	return nil, status.Error(codes.Unimplemented, "method PostMessage not implemented")
}
func (s *MessageBoardServer) SubscribeTopic(req *rpb.SubscribeTopicRequest, _ grpc.ServerStreamingServer[rpb.MessageEvent]) error {
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	return status.Error(codes.Unimplemented, "method SubscribeTopic not implemented")
}
func (s *MessageBoardServer) UpdateMessage(_ context.Context, _ *rpb.UpdateMessageRequest) (*rpb.Message, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateMessage not implemented")
}

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}
