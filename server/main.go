package main

import (
	"context"
	"fmt"
	"github.com/alecthomas/kong"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	"log"
	"net"
	rpb "server/razpravljalnica"
	"sync"
)

var cli struct {
}

type MessageBoard struct {
	rpb.UnimplementedMessageBoardServer
}

func (s *MessageBoard) CreateTopic(_ context.Context, _ *rpb.CreateTopicRequest) (*rpb.Topic, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateTopic not implemented")
}
func (s *MessageBoard) CreateUser(_ context.Context, _ *rpb.CreateUserRequest) (*rpb.User, error) {
	return nil, status.Error(codes.Unimplemented, "method CreateUser not implemented")
}
func (s *MessageBoard) DeleteMessage(_ context.Context, _ *rpb.DeleteMessageRequest) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "method DeleteMessage not implemented")
}
func (s *MessageBoard) GetMessages(_ context.Context, _ *rpb.GetMessagesRequest) (*rpb.GetMessagesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetMessages not implemented")
}
func (s *MessageBoard) GetSubcscriptionNode(_ context.Context, _ *rpb.SubscriptionNodeRequest) (*rpb.SubscriptionNodeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetSubcscriptionNode not implemented")
}
func (s *MessageBoard) LikeMessage(_ context.Context, _ *rpb.LikeMessageRequest) (*rpb.Message, error) {
	return nil, status.Error(codes.Unimplemented, "method LikeMessage not implemented")
}
func (s *MessageBoard) ListTopics(_ context.Context, _ *emptypb.Empty) (*rpb.ListTopicsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method ListTopics not implemented")
}
func (s *MessageBoard) PostMessage(_ context.Context, _ *rpb.PostMessageRequest) (*rpb.Message, error) {
	return nil, status.Error(codes.Unimplemented, "method PostMessage not implemented")
}
func (s *MessageBoard) SubscribeTopic(_ *rpb.SubscribeTopicRequest, _ grpc.ServerStreamingServer[rpb.MessageEvent]) error {
	return status.Error(codes.Unimplemented, "method SubscribeTopic not implemented")
}
func (s *MessageBoard) UpdateMessage(_ context.Context, _ *rpb.UpdateMessageRequest) (*rpb.Message, error) {
	return nil, status.Error(codes.Unimplemented, "method UpdateMessage not implemented")
}

func serve() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	srv := grpc.NewServer()
	rpb.RegisterMessageBoardServer(srv, &MessageBoard{})

	log.Printf("server listening at %v", lis.Addr())
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func main() {
	var wg sync.WaitGroup
	defer wg.Wait()

	ctx := kong.Parse(&cli)

	fmt.Println(ctx)
	wg.Go(serve)

}
