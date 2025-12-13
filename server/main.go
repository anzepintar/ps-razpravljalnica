package main

import (
	"context"
	"fmt"
	"github.com/alecthomas/kong"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log"
	"net"
	rpb "server/razpravljalnica"
	"sync"
)

type RunNodeCmd struct {
	ControllAddress string `arg:""`
}

// TODO forward state
func (s *RunNodeCmd) Run() error {
	var msrv = MessageBoardServer{}

	lis, err := net.Listen("tcp", ":50051") // TODO rm this port
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	srv := grpc.NewServer()
	rpb.RegisterMessageBoardServer(srv, &msrv)

	log.Printf("server listening at %v", lis.Addr())
	if err := srv.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}
	return nil
}

type StartCmd struct{}

func (StartCmd) Run() error {
	return (&RunNodeCmd{}).Run()
}

var cli struct {
	RunNode RunNodeCmd `cmd:"run-node" help:"Starts a node in the chain"`
	Start   StartCmd   `cmd:"" default:"1"`
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
	Message_idx int64
	Messages    map[int64]Message
	Subscribers []struct {
		Token         string
		Target_server rpb.NodeInfo
		ch            chan<- MessageEv
	}
}

type Message struct {
	UserId    int64
	Text      string
	CreatedAt *timestamppb.Timestamp
	Likes     map[int64]struct{}
}

type User struct {
	Name string
}

type MessageEv struct {
	SequenceNumber int64
	Op             rpb.OpType
	Message        *Message
	EventAt        *timestamppb.Timestamp
}

func (s *MessageBoardServer) CreateTopic(_ context.Context, req *rpb.CreateTopicRequest) (*rpb.Topic, error) {
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	name := req.Name
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Missing Topic name")
	}

	s.Topics = append(s.Topics, Topic{Name: name, Messages: map[int64]Message{}})
	return &rpb.Topic{Id: int64(len(s.Topics) - 1), Name: name}, nil
}
func (s *MessageBoardServer) CreateUser(_ context.Context, req *rpb.CreateUserRequest) (*rpb.User, error) {
	s.users_mtx.Lock()
	defer s.users_mtx.Unlock()

	name := req.Name
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Missing User name")
	}

	s.Users = append(s.Users, User{Name: name})
	return &rpb.User{Id: int64(len(s.Users) - 1), Name: name}, nil
}
func (s *MessageBoardServer) DeleteMessage(_ context.Context, req *rpb.DeleteMessageRequest) (*emptypb.Empty, error) {
	// TODO subs
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	var topic_id int64
	if t, err := s.topicExist(req.TopicId); err != nil {
		return nil, err
	} else {
		topic_id = t
	}
	var user_id int64
	if u, err := s.userExist(req.UserId); err != nil {
		return nil, err
	} else {
		user_id = u
	}
	topic := &s.Topics[topic_id]
	var message_id int64
	if u, err := s.msgExist(topic_id, req.MessageId); err != nil {
		return nil, err
	} else {
		message_id = u
	}
	if topic.Messages[message_id].UserId != user_id {
		return nil, status.Error(codes.PermissionDenied, "Can't delete other user's message")
	}
	delete(topic.Messages, message_id)
	return &emptypb.Empty{}, nil
}
func (s *MessageBoardServer) GetMessages(_ context.Context, req *rpb.GetMessagesRequest) (*rpb.GetMessagesResponse, error) {
	s.topics_mtx.RLock()
	defer s.topics_mtx.RUnlock()

	out := make([]*rpb.Message, 0, req.Limit)
	var topic_id int64
	if t, err := s.topicExist(req.TopicId); err != nil {
		return nil, err
	} else {
		topic_id = t
	}
	topic := &s.Topics[topic_id]
	limit := req.Limit
	for k, v := range topic.Messages {
		if limit <= 0 {
			break
		}
		if k >= req.FromMessageId {
			out = append(out, &rpb.Message{
				Id:        k,
				TopicId:   topic_id,
				UserId:    v.UserId,
				Text:      v.Text,
				CreatedAt: v.CreatedAt,
				Likes:     int32(len(v.Likes)),
			})
			limit--
		}
	}
	return &rpb.GetMessagesResponse{Messages: out}, nil
}

func (s *MessageBoardServer) GetSubscriptionNode(_ context.Context, _ *rpb.SubscriptionNodeRequest) (*rpb.SubscriptionNodeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetSubcscriptionNode not implemented")
}

func (s *MessageBoardServer) LikeMessage(_ context.Context, req *rpb.LikeMessageRequest) (*rpb.Message, error) {
	// TODO subs
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()
	var topic_id int64
	if t, err := s.topicExist(req.TopicId); err != nil {
		return nil, err
	} else {
		topic_id = t
	}
	topic := &s.Topics[topic_id]
	var user_id int64
	if u, err := s.userExist(req.UserId); err != nil {
		return nil, err
	} else {
		user_id = u
	}
	var message_id int64
	if u, err := s.msgExist(topic_id, req.MessageId); err != nil {
		return nil, err
	} else {
		message_id = u
	}
	message := topic.Messages[message_id]
	if _, ok := message.Likes[user_id]; ok {
		return nil, status.Error(codes.AlreadyExists, "User already liked message")
	}
	message.Likes[user_id] = struct{}{}
	return &rpb.Message{
		Id:        message_id,
		TopicId:   topic_id,
		UserId:    user_id,
		Text:      message.Text,
		CreatedAt: message.CreatedAt,
		Likes:     int32(len(message.Likes)),
	}, nil
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

func (s *MessageBoardServer) PostMessage(_ context.Context, req *rpb.PostMessageRequest) (*rpb.Message, error) {
	// TODO subs
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	var topic_id int64
	if t, err := s.topicExist(req.TopicId); err != nil {
		return nil, err
	} else {
		topic_id = t
	}
	var user_id int64
	if u, err := s.userExist(req.UserId); err != nil {
		return nil, err
	} else {
		user_id = u
	}
	if len(req.Text) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Empty message not permited")
	}

	topic := &s.Topics[topic_id]
	id := topic.Message_idx
	topic.Message_idx++
	msg := Message{UserId: user_id, Text: req.Text, CreatedAt: timestamppb.Now(), Likes: map[int64]struct{}{}}
	topic.Messages[id] = msg
	return &rpb.Message{Id: id, UserId: msg.UserId, Text: msg.Text, CreatedAt: msg.CreatedAt, Likes: int32(len(msg.Likes))}, nil
}

func (s *MessageBoardServer) SubscribeTopic(req *rpb.SubscribeTopicRequest, _ grpc.ServerStreamingServer[rpb.MessageEvent]) error {
	return status.Error(codes.Unimplemented, "method SubscribeTopic not implemented")
}

func (s *MessageBoardServer) UpdateMessage(_ context.Context, req *rpb.UpdateMessageRequest) (*rpb.Message, error) {
	// TODO subs
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()
	if len(req.Text) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Empty message not permited")
	}
	var topic_id int64
	if t, err := s.topicExist(req.TopicId); err != nil {
		return nil, err
	} else {
		topic_id = t
	}
	topic := &s.Topics[topic_id]
	var user_id int64
	if u, err := s.userExist(req.UserId); err != nil {
		return nil, err
	} else {
		user_id = u
	}
	var message_id int64
	if u, err := s.msgExist(topic_id, req.MessageId); err != nil {
		return nil, err
	} else {
		message_id = u
	}
	message := topic.Messages[message_id]
	message.Text = req.Text
	topic.Messages[message_id] = message
	return &rpb.Message{
		Id:        message_id,
		TopicId:   topic_id,
		UserId:    user_id,
		Text:      message.Text,
		CreatedAt: message.CreatedAt,
		Likes:     int32(len(message.Likes)),
	}, nil
}

func (s *MessageBoardServer) topicExist(id int64) (int64, error) {
	// expects lock
	if int64(len(s.Topics)) > id {
		return id, nil
	}
	return 0, fmt.Errorf("Topic %d doesn't exist", id)
}

func (s *MessageBoardServer) userExist(id int64) (int64, error) {
	// expects lock
	if int64(len(s.Users)) > id {
		return id, nil
	}
	return 0, fmt.Errorf("User %d doesn't exist", id)
}
func (s *MessageBoardServer) msgExist(topic_id, id int64) (int64, error) {
	// expects lock
	if _, ok := s.Topics[topic_id].Messages[id]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("Message %d doesn't exist", id)
}

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}
