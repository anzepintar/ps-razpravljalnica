package main

import (
	"context"
	"fmt"
	"github.com/alecthomas/kong"
	"log"
	"net"
	rpb "server/razpravljalnica"
	"sync"

	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var cli RunNodeCmd

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}

var grpc_security_opt = grpc.WithTransportCredentials(insecure.NewCredentials())

type RunNodeCmd struct {
	ControllAddress string `arg:""`
	BindAddr        string `short:"b" help:"binds to this address"`
}

var msrv = MessageBoardServer{}
var cntrlldp = ControlledPlaneServer{
	stop_chan: make(chan struct{}),
}

func (s *RunNodeCmd) Run() error {
	for {

		lis, err := net.Listen("tcp", s.BindAddr)
		if err != nil {
			return fmt.Errorf("failed to listen: %v", err)
		}
		err = cntrlldp.register(lis.Addr().String(), s)
		if err != nil {
			return fmt.Errorf("Failed to register at %v: %v", s.ControllAddress, err)
		}

		srv := grpc.NewServer()
		go stopper(srv, &cntrlldp)
		rpb.RegisterMessageBoardServer(srv, &msrv)
		rpb.RegisterControlledPlaneServer(srv, &cntrlldp)

		log.Printf("server listening at %v", lis.Addr())
		if err := srv.Serve(lis); err != nil {
			return fmt.Errorf("failed to serve: %v", err)
		}
	}
}

func stopper(srv *grpc.Server, cntrlldp *ControlledPlaneServer) {
	<-cntrlldp.stop_chan
	srv.GracefulStop()
}

type Node struct {
	id      int64
	address string
}

type ControlledPlaneServer struct {
	rpb.UnimplementedControlledPlaneServer

	mtx  sync.RWMutex
	self *Node

	chain_prev *Node
	chain_next *Node

	control []string

	stop_chan chan struct{}
}

type MessageBoardServer struct {
	rpb.UnimplementedMessageBoardServer

	topics_mtx  sync.RWMutex
	Message_idx int64
	Topics      []Topic
	users_mtx   sync.RWMutex
	Users       []User
}

type Topic struct {
	Name        string
	Messages    map[int64]Message
	Subscribers []struct {
		Token         string
		Target_server int64
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

// TODO forward state

func (s *MessageBoardServer) CreateTopic(ctx context.Context, req *rpb.CreateTopicRequest) (*rpb.Topic, error) {
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	name := req.Name
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Missing Topic name")
	}

	next_topic, err := fwCreateTopic(ctx, req)
	if err != nil {
		return nil, err
	}

	s.Topics = append(s.Topics, Topic{Name: name, Messages: map[int64]Message{}})
	id := int64(len(s.Topics) - 1)
	if next_topic != nil && next_topic.Id != id {
		cntrlldp.snitchNext()
		s.Topics = s.Topics[:id]
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}
	return &rpb.Topic{Id: id, Name: name}, nil
}
func fwCreateTopic(ctx context.Context, req *rpb.CreateTopicRequest) (*rpb.Topic, error) {
	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	if cntrlldp.chain_next != nil {
		conn, err := grpc.NewClient(cntrlldp.chain_next.address, grpc_security_opt)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		client := rpb.NewMessageBoardClient(conn)
		topic, err := client.CreateTopic(ctx, req)
		if err != nil {
			return nil, err
		}
		return topic, nil
	}
	return nil, nil
}

func (s *MessageBoardServer) CreateUser(ctx context.Context, req *rpb.CreateUserRequest) (*rpb.User, error) {
	s.users_mtx.Lock()
	defer s.users_mtx.Unlock()

	name := req.Name
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Missing User name")
	}
	next_user, err := fwCreateUser(ctx, req)
	if err != nil {
		return nil, err
	}

	s.Users = append(s.Users, User{Name: name})
	id := int64(len(s.Users) - 1)
	if next_user != nil && next_user.Id != id {
		cntrlldp.snitchNext()
		s.Users = s.Users[:id]
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}
	return &rpb.User{Id: id, Name: name}, nil
}
func fwCreateUser(ctx context.Context, req *rpb.CreateUserRequest) (*rpb.User, error) {
	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	if cntrlldp.chain_next != nil {
		conn, err := grpc.NewClient(cntrlldp.chain_next.address, grpc_security_opt)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		client := rpb.NewMessageBoardClient(conn)
		user, err := client.CreateUser(ctx, req)
		if err != nil {
			return nil, err
		}
		return user, nil
	}
	return nil, nil
}

func (s *MessageBoardServer) DeleteMessage(ctx context.Context, req *rpb.DeleteMessageRequest) (*emptypb.Empty, error) {
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
	err := fwDeleteMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	delete(topic.Messages, message_id)
	return &emptypb.Empty{}, nil
}
func fwDeleteMessage(ctx context.Context, req *rpb.DeleteMessageRequest) error {
	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	if cntrlldp.chain_next != nil {
		conn, err := grpc.NewClient(cntrlldp.chain_next.address, grpc_security_opt)
		if err != nil {
			return err
		}
		defer conn.Close()
		client := rpb.NewMessageBoardClient(conn)
		_, err = client.DeleteMessage(ctx, req)
		if err != nil {
			return err
		}
		return nil
	}
	return nil
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

func (s *MessageBoardServer) LikeMessage(ctx context.Context, req *rpb.LikeMessageRequest) (*rpb.Message, error) {
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
	msg, err := fwLikeMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	message.Likes[user_id] = struct{}{}
	likes := int32(len(message.Likes))
	if msg != nil && (msg.Id != message_id || msg.Likes != likes) {
		cntrlldp.snitchNext()
		delete(message.Likes, user_id)
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	return &rpb.Message{
		Id:        message_id,
		TopicId:   topic_id,
		UserId:    user_id,
		Text:      message.Text,
		CreatedAt: message.CreatedAt,
		Likes:     likes,
	}, nil
}
func fwLikeMessage(ctx context.Context, req *rpb.LikeMessageRequest) (*rpb.Message, error) {
	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	if cntrlldp.chain_next != nil {
		conn, err := grpc.NewClient(cntrlldp.chain_next.address, grpc_security_opt)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		client := rpb.NewMessageBoardClient(conn)
		msg, err := client.LikeMessage(ctx, req)
		if err != nil {
			return nil, err
		}
		return msg, nil
	}
	return nil, nil
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

func (s *MessageBoardServer) PostMessage(ctx context.Context, req *rpb.PostMessageRequest) (*rpb.Message, error) {
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
	next_msg, err := fwPostMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	topic := &s.Topics[topic_id]
	id := s.Message_idx
	msg := Message{UserId: user_id, Text: req.Text, CreatedAt: timestamppb.Now(), Likes: map[int64]struct{}{}}
	if next_msg != nil && (next_msg.UserId != msg.UserId || next_msg.Text != msg.Text || next_msg.CreatedAt != msg.CreatedAt || next_msg.Likes != 0) {
		cntrlldp.snitchNext()
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}
	s.Message_idx++
	topic.Messages[id] = msg
	return &rpb.Message{Id: id, UserId: msg.UserId, Text: msg.Text, CreatedAt: msg.CreatedAt, Likes: int32(len(msg.Likes))}, nil
}
func fwPostMessage(ctx context.Context, req *rpb.PostMessageRequest) (*rpb.Message, error) {
	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	if cntrlldp.chain_next != nil {
		conn, err := grpc.NewClient(cntrlldp.chain_next.address, grpc_security_opt)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		client := rpb.NewMessageBoardClient(conn)
		msg, err := client.PostMessage(ctx, req)
		if err != nil {
			return nil, err
		}
		return msg, nil
	}
	return nil, nil
}

func (s *MessageBoardServer) SubscribeTopic(req *rpb.SubscribeTopicRequest, _ grpc.ServerStreamingServer[rpb.MessageEvent]) error {
	return status.Error(codes.Unimplemented, "method SubscribeTopic not implemented")
}

func (s *MessageBoardServer) UpdateMessage(ctx context.Context, req *rpb.UpdateMessageRequest) (*rpb.Message, error) {
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
	next_msg, err := fwUpdateMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	msg := topic.Messages[message_id]
	msg.Text = req.Text
	if next_msg != nil && (next_msg.UserId != msg.UserId || next_msg.Text != msg.Text || next_msg.CreatedAt != msg.CreatedAt || next_msg.Likes != 0) {
		cntrlldp.snitchNext()
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	topic.Messages[message_id] = msg
	return &rpb.Message{
		Id:        message_id,
		TopicId:   topic_id,
		UserId:    user_id,
		Text:      msg.Text,
		CreatedAt: msg.CreatedAt,
		Likes:     int32(len(msg.Likes)),
	}, nil
}
func fwUpdateMessage(ctx context.Context, req *rpb.UpdateMessageRequest) (*rpb.Message, error) {
	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	if cntrlldp.chain_next != nil {
		conn, err := grpc.NewClient(cntrlldp.chain_next.address, grpc_security_opt)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		client := rpb.NewMessageBoardClient(conn)
		msg, err := client.UpdateMessage(ctx, req)
		if err != nil {
			return nil, err
		}
		return msg, nil
	}
	return nil, nil
}

func (s *MessageBoardServer) GetUser(_ context.Context, req *rpb.GetUserRequest) (*rpb.User, error) {
	s.users_mtx.RLock()
	defer s.users_mtx.RUnlock()

	var user_id int64
	if u, err := s.userExist(req.UserId); err != nil {
		return nil, err
	} else {
		user_id = u
	}
	user := s.Users[user_id]
	return &rpb.User{Id: user_id, Name: user.Name}, nil
}

func (s *MessageBoardServer) GetWholeState(context.Context, *emptypb.Empty) (*rpb.WholeState, error) {
	s.topics_mtx.RLock()
	defer s.topics_mtx.RUnlock()
	s.users_mtx.RLock()
	defer s.users_mtx.RUnlock()

	topics := make([]*rpb.TopicState, len(s.Topics))
	for i, v := range s.Topics {
		messages := map[int64]*rpb.MessageState{}
		for i, v := range v.Messages {
			likes := make([]int64, len(v.Likes))
			for i := range v.Likes {
				likes = append(likes, i)
			}
			messages[i] = &rpb.MessageState{
				UserId:      v.UserId,
				Text:        v.Text,
				CreatedAt:   v.CreatedAt,
				UserIdLiked: likes,
			}
		}
		topics[i] = &rpb.TopicState{
			Name:     v.Name,
			Messages: messages,
		}
	}

	out := rpb.WholeState{
		MessageId: s.Message_idx,
		Topic:     topics,
	}
	return &out, nil
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

func (s *ControlledPlaneServer) Stop(_ context.Context, emp *emptypb.Empty) (*emptypb.Empty, error) {
	close(s.stop_chan)
	return emp, nil
}

func (s *ControlledPlaneServer) ChainChange(ctx context.Context, emp *emptypb.Empty) (*emptypb.Empty, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	for _, k := range s.control {
		conn, err := grpc.NewClient(k, grpc_security_opt)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		client := rpb.NewControlPlaneClient(conn)
		res, err := client.GetClusterState(context.Background(), &emptypb.Empty{})
		if err != nil {
			return nil, err
		}
		s.self = &Node{address: res.Address, id: res.NodeId}
	}
	return &emptypb.Empty{}, nil
}

func (s *ControlledPlaneServer) register(self_address string, state *RunNodeCmd) error {
	conn, err := grpc.NewClient(state.ControllAddress, grpc_security_opt)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := rpb.NewControlPlaneClient(conn)
	res, err := client.Register(context.Background(), &rpb.RegisterRequest{Address: self_address})
	if err != nil {
		return err
	}
	s.self = &Node{address: res.Address, id: res.NodeId}
	return nil
}
func (s *ControlledPlaneServer) snitchNext() {
	log.Panic("implement snitchNext")
}
