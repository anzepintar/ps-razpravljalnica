package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math/rand"
	"net"
	rpb "server/razpravljalnica"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alecthomas/kong"

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
	Tui             bool   `short:"t" help:"enable tui"`
}

var msrv = MessageBoardServer{}
var cntrlldp = ControlledPlaneServer{
	control: map[string]struct{}{},
}

func myLog(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	resp, err = handler(ctx, req)
	msg := fmt.Sprintf("%v(%+v) -> %+v %v", info.FullMethod, req, resp, err)
	if !cli.Tui {
		log.Print(msg)
	}
	TuiLog(msg)
	return resp, err
}

func (s *RunNodeCmd) Run() error {
	for !cntrlldp.should_stop.Load() {
		subs_ch := make(chan MessageEv)
		msrv.subs_ch = subs_ch
		msrv.subs = map[int64]Subscription{}

		cntrlldp.stop_chan = make(chan struct{})
		lis, err := net.Listen("tcp", s.BindAddr)
		if err != nil {
			return fmt.Errorf("failed to listen: %v", err)
		}
		cntrlldp.control[s.ControllAddress] = struct{}{}
		err = cntrlldp.register(lis.Addr().String(), s)
		if err != nil {
			return fmt.Errorf("Failed to register at %v: %v", s.ControllAddress, err)
		}
		err = syncState()
		if err != nil {
			return fmt.Errorf("Failed to get current state: %v", err)
		}
		srv := grpc.NewServer(grpc.ChainUnaryInterceptor(myLog))
		go msrv.subServer(subs_ch)
		go stopper(srv, &cntrlldp, &msrv)
		rpb.RegisterMessageBoardServer(srv, &msrv)
		rpb.RegisterControlledPlaneServer(srv, &cntrlldp)

		log.Printf("server listening at %v", lis.Addr())

		if s.Tui {
			go RunTUI()
		}
		if err := srv.Serve(lis); err != nil {
			return fmt.Errorf("failed to serve: %v", err)
		}
		time.Sleep(time.Second)
	}
	return nil
}

func (s *MessageBoardServer) subServer(ch <-chan MessageEv) {
	for {
		ev, ok := <-ch
		if !ok {
			return
		}
		func() {
			s.subs_mtx.RLock()
			defer s.subs_mtx.RUnlock()

			for _, v := range s.subs {
				if slices.Contains(v.topics, ev.topic) {
					if v.ch != nil {
						v.ch <- ev
					}
				}
			}
		}()
	}
}

func syncState() error {
	var res *rpb.GetClusterStateResponse
	success := false
	for k := range cntrlldp.control {
		conn, err := grpc.NewClient(k, grpc_security_opt)
		if err != nil {
			log.Printf("failed to contact controll server %v, due to %v", k, err)
			continue
		}
		defer conn.Close()
		client := rpb.NewControlPlaneClient(conn)
		res, err = client.GetClusterStateClient(context.Background(), &emptypb.Empty{})
		if err != nil {
			log.Printf("failed to contact controll server %v, due to %v", k, err)
			continue
		}
		success = true
		break
	}
	if !success {
		return fmt.Errorf("Couldn't connect to any controll plane servers: %v", cntrlldp.control)
	}

	head := res.Head
	if head == nil || head.Address == cntrlldp.self.address {
		return nil
	}
	conn, err := grpc.NewClient(head.Address, grpc_security_opt)
	if err != nil {
		cntrlldp.snitch(head.NodeId)
		return err
	}
	defer conn.Close()
	client := rpb.NewMessageBoardClient(conn)
	st, err := client.GetWholeState(context.Background(), &emptypb.Empty{})
	if err != nil {
		cntrlldp.snitch(head.NodeId)
		return err
	}
	msrv.message_idx = st.MessageId
	for _, v := range st.Users {
		msrv.users = append(msrv.users, User{v})
	}
	for _, v := range st.Topic {
		tp := Topic{name: v.Name, messages: map[int64]Message{}}
		for k, v := range v.Messages {
			msg := Message{user: v.UserId, text: v.Text, created: v.CreatedAt, likes: map[int64]struct{}{}}
			for _, v := range v.UserIdLiked {
				msg.likes[v] = struct{}{}
			}
			tp.messages[k] = msg
		}
		msrv.topics = append(msrv.topics, tp)
	}

	return nil
}

func stopper(srv *grpc.Server, cnt *ControlledPlaneServer, msb *MessageBoardServer) {
	<-cnt.stop_chan

	srv.GracefulStop()
	close(msb.subs_ch)
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

	control map[string]struct{}

	should_stop atomic.Bool
	stop_chan   chan struct{}
}

type MessageBoardServer struct {
	rpb.UnimplementedMessageBoardServer

	topics_mtx  sync.RWMutex
	message_idx int64
	topics      []Topic

	users_mtx sync.RWMutex
	users     []User

	subs_ch  chan<- MessageEv
	subs_mtx sync.RWMutex
	subs     map[int64]Subscription // map[user_id]
}
type Subscription struct {
	token  string
	topics []int64
	ch     chan MessageEv
}

type Topic struct {
	name     string
	messages map[int64]Message
}

type Message struct {
	user    int64
	text    string
	created *timestamppb.Timestamp
	likes   map[int64]struct{}
}

type User struct {
	name string
}

type MessageEv struct {
	sequence_number int64
	topic           int64
	op              rpb.OpType
	message_id      int64
	message         Message
	at              *timestamppb.Timestamp
}

func (s *MessageBoardServer) CreateTopic(ctx context.Context, req *rpb.CreateTopicRequest) (*rpb.Topic, error) {
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	name := req.Name
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Missing Topic name")
	}

	next_topic, err := fwCreateTopic(ctx, req)
	if err != nil {
		if status.Code(err) != codes.Internal {
			go cntrlldp.snitchNext()
		}
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	s.topics = append(s.topics, Topic{name: name, messages: map[int64]Message{}})
	id := int64(len(s.topics) - 1)
	if next_topic != nil && next_topic.Id != id {
		go cntrlldp.snitchNext()
		s.topics = s.topics[:id]
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
		return client.CreateTopic(ctx, req)
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
		if status.Code(err) != codes.Internal {
			go cntrlldp.snitchNext()
		}
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	s.users = append(s.users, User{name: name})
	id := int64(len(s.users) - 1)
	if next_user != nil && next_user.Id != id {
		go cntrlldp.snitchNext()
		s.users = s.users[:id]
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
		return client.CreateUser(ctx, req)
	}
	return nil, nil
}

func (s *MessageBoardServer) DeleteMessage(ctx context.Context, req *rpb.DeleteMessageRequest) (*emptypb.Empty, error) {
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
	topic := &s.topics[topic_id]
	var message_id int64
	if u, err := s.msgExist(topic_id, req.MessageId); err != nil {
		return nil, err
	} else {
		message_id = u
	}
	msg := topic.messages[message_id]
	if msg.user != user_id {
		return nil, status.Error(codes.PermissionDenied, "Can't delete other user's message")
	}
	err := fwDeleteMessage(ctx, req)
	if err != nil {
		if status.Code(err) != codes.Internal {
			go cntrlldp.snitchNext()
		}
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	s.subs_ch <- MessageEv{op: rpb.OpType_OP_DELETE, message: msg, at: timestamppb.Now(), topic: topic_id, message_id: message_id}

	delete(topic.messages, message_id)
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
		ret, err := client.DeleteMessage(ctx, req)
		if err != nil {
			return err
		}
		if ret == nil {
			return status.Error(codes.Canceled, "Failed to delete")
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
	topic := &s.topics[topic_id]
	limit := req.Limit
	for k, v := range topic.messages {
		if limit <= 0 {
			break
		}
		if k >= req.FromMessageId {
			out = append(out, &rpb.Message{
				Id:        k,
				TopicId:   topic_id,
				UserId:    v.user,
				Text:      v.text,
				CreatedAt: v.created,
				Likes:     int32(len(v.likes)),
			})
			limit--
		}
	}
	return &rpb.GetMessagesResponse{Messages: out}, nil
}

func (s *MessageBoardServer) GetSubscriptionNode(ctx context.Context, req *rpb.SubscriptionNodeRequest) (*rpb.SubscriptionNodeResponse, error) {
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()

	if rand.Intn(2) == 0 {
		return s.addSubscription(ctx, req)
	}
	ret, err := fwGetSubscriptionNode(ctx, req)
	if err != nil {
		return s.addSubscription(ctx, req)
	}
	return ret, nil
}

func fwGetSubscriptionNode(ctx context.Context, req *rpb.SubscriptionNodeRequest) (*rpb.SubscriptionNodeResponse, error) {
	cntrlldp.mtx.RLock()
	defer cntrlldp.mtx.RUnlock()

	if cntrlldp.chain_next != nil {
		conn, err := grpc.NewClient(cntrlldp.chain_next.address, grpc_security_opt)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		client := rpb.NewMessageBoardClient(conn)
		return client.GetSubscriptionNode(ctx, req)
	}
	return nil, status.Error(codes.NotFound, "No next node in chain.")
}

func (s *MessageBoardServer) addSubscription(_ context.Context, req *rpb.SubscriptionNodeRequest) (*rpb.SubscriptionNodeResponse, error) {
	// LOCK expected
	user_id, err := s.userExist(req.UserId)
	if err != nil {
		return nil, err
	}
	var topics = make([]int64, 0, len(req.TopicId))
	for _, v := range req.TopicId {
		topic_id, err := s.topicExist(v)
		if err != nil {
			return nil, err
		}
		topics = append(topics, topic_id)
	}
	token := randString(1) // TODO
	s.subs_mtx.Lock()
	defer s.subs_mtx.Unlock()

	s.subs[user_id] = Subscription{token: token, topics: topics}
	return &rpb.SubscriptionNodeResponse{
		SubscribeToken: token,
		Node:           &rpb.NodeInfo{NodeId: cntrlldp.self.id, Address: cntrlldp.self.address},
	}, nil
}
func (s *MessageBoardServer) rmSubscription(user_id int64) {
	s.subs_mtx.Lock()
	defer s.subs_mtx.Unlock()
	sub, ok := s.subs[user_id]
	if !ok {
		return
	}
	if sub.ch != nil {
		close(sub.ch)
	}
	delete(s.subs, user_id)
}

func randString(n int) string {
	b := make([]byte, n)
	for i := range n {
		b[i] = byte(rand.Int())
	}
	return base64.StdEncoding.EncodeToString(b)
}

func (s *MessageBoardServer) LikeMessage(ctx context.Context, req *rpb.LikeMessageRequest) (*rpb.Message, error) {
	s.topics_mtx.Lock()
	defer s.topics_mtx.Unlock()
	var topic_id int64
	if t, err := s.topicExist(req.TopicId); err != nil {
		return nil, err
	} else {
		topic_id = t
	}
	topic := &s.topics[topic_id]
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
	msg := topic.messages[message_id]
	if _, ok := msg.likes[user_id]; ok {
		return nil, status.Error(codes.AlreadyExists, "User already liked message")
	}
	msg_next, err := fwLikeMessage(ctx, req)
	if err != nil {
		if status.Code(err) != codes.Internal {
			go cntrlldp.snitchNext()
		}
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	msg.likes[user_id] = struct{}{}
	likes := int32(len(msg.likes))
	if msg_next != nil && (msg_next.Id != message_id || msg_next.Likes != likes) {
		go cntrlldp.snitchNext()
		delete(msg.likes, user_id)
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	s.subs_ch <- MessageEv{op: rpb.OpType_OP_LIKE, message: msg, at: timestamppb.Now(), topic: topic_id, message_id: message_id}

	return &rpb.Message{
		Id:        message_id,
		TopicId:   topic_id,
		UserId:    user_id,
		Text:      msg.text,
		CreatedAt: msg.created,
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
		return client.LikeMessage(ctx, req)
	}
	return nil, nil
}

func (s *MessageBoardServer) ListTopics(_ context.Context, _ *emptypb.Empty) (*rpb.ListTopicsResponse, error) {
	s.topics_mtx.RLock()
	defer s.topics_mtx.RUnlock()
	out := make([]*rpb.Topic, len(s.topics))
	for i, v := range s.topics {
		out[i] = &rpb.Topic{Id: int64(i), Name: v.name}
	}
	return &rpb.ListTopicsResponse{Topics: out}, nil
}

func (s *MessageBoardServer) PostMessage(ctx context.Context, req *rpb.PostMessageRequest) (*rpb.Message, error) {
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
		if status.Code(err) != codes.Internal {
			go cntrlldp.snitchNext()
		}
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	topic := &s.topics[topic_id]
	id := s.message_idx
	msg := Message{user: user_id, text: req.Text, created: timestamppb.Now(), likes: map[int64]struct{}{}}
	if next_msg != nil && (next_msg.UserId != msg.user ||
		next_msg.Text != msg.text || next_msg.Likes != 0) {
		go cntrlldp.snitchNext()
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	s.subs_ch <- MessageEv{op: rpb.OpType_OP_POST, message: msg, at: timestamppb.Now(), topic: topic_id, message_id: id}

	s.message_idx++
	topic.messages[id] = msg
	return &rpb.Message{Id: id, TopicId: topic_id, UserId: msg.user, Text: msg.text, CreatedAt: msg.created, Likes: int32(len(msg.likes))}, nil
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
		return client.PostMessage(ctx, req)
	}
	return nil, nil
}

func (s *MessageBoardServer) SubscribeTopic(req *rpb.SubscribeTopicRequest, stream grpc.ServerStreamingServer[rpb.MessageEvent]) error {

	var seq_cnt int64 = 0

	user_id, err := func() (int64, error) {
		s.topics_mtx.RLock()
		defer s.topics_mtx.RUnlock()
		return s.userExist(req.UserId)
	}()
	if err != nil {
		return err
	}

	sub, ok := func() (sub Subscription, ok bool) {
		s.subs_mtx.RLock()
		defer s.subs_mtx.RUnlock()
		sub, ok = s.subs[user_id]
		return
	}()
	if !ok || sub.token != req.SubscribeToken {
		return status.Error(codes.PermissionDenied, "Invalid subscription.")
	}
	defer s.rmSubscription(user_id)

	if !slices.Equal(sub.topics, req.TopicId) {
		return status.Error(codes.PermissionDenied, "Topic sets differ.")
	}

	sub.ch = make(chan MessageEv)
	func() {
		s.subs_mtx.Lock()
		defer s.subs_mtx.Unlock()
		s.subs[user_id] = sub
	}()
	for _, topic_id := range req.TopicId {
		for msg_id, msg := range s.topics[topic_id].messages {
			stream.Send(&rpb.MessageEvent{
				SequenceNumber: seq_cnt,
				Op:             rpb.OpType_OP_POST,
				Message: &rpb.Message{
					Id:        msg_id,
					TopicId:   topic_id,
					UserId:    msg.user,
					Text:      msg.text,
					CreatedAt: msg.created,
					Likes:     int32(len(msg.likes)),
				},
				EventAt: nil,
			})
			seq_cnt++
		}
	}
	for {
		ev, ok := <-sub.ch
		if !ok {
			return nil
		}
		stream.Send(&rpb.MessageEvent{
			SequenceNumber: seq_cnt,
			Op:             ev.op,
			Message: &rpb.Message{
				Id:        ev.message_id,
				TopicId:   ev.topic,
				UserId:    ev.message.user,
				Text:      ev.message.text,
				CreatedAt: ev.message.created,
				Likes:     int32(len(ev.message.likes)),
			},
		})
		seq_cnt++
	}
}

func (s *MessageBoardServer) UpdateMessage(ctx context.Context, req *rpb.UpdateMessageRequest) (*rpb.Message, error) {
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
	topic := &s.topics[topic_id]
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
		if status.Code(err) != codes.Internal {
			go cntrlldp.snitchNext()
		}
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	msg := topic.messages[message_id]
	msg.text = req.Text
	if next_msg != nil && (next_msg.UserId != msg.user || next_msg.Text != msg.text || next_msg.CreatedAt != msg.created || next_msg.Likes != 0) {
		go cntrlldp.snitchNext()
		return nil, status.Error(codes.Internal, "Chain broke, retry.")
	}

	s.subs_ch <- MessageEv{op: rpb.OpType_OP_UPDATE, message: msg, at: timestamppb.Now(), topic: topic_id, message_id: message_id}

	topic.messages[message_id] = msg
	return &rpb.Message{
		Id:        message_id,
		TopicId:   topic_id,
		UserId:    user_id,
		Text:      msg.text,
		CreatedAt: msg.created,
		Likes:     int32(len(msg.likes)),
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
		return client.UpdateMessage(ctx, req)
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
	user := s.users[user_id]
	return &rpb.User{Id: user_id, Name: user.name}, nil
}

func (s *MessageBoardServer) GetWholeState(context.Context, *emptypb.Empty) (*rpb.WholeState, error) {
	s.topics_mtx.RLock()
	defer s.topics_mtx.RUnlock()
	s.users_mtx.RLock()
	defer s.users_mtx.RUnlock()

	topics := make([]*rpb.TopicState, len(s.topics))
	for i, v := range s.topics {
		messages := map[int64]*rpb.MessageState{}
		for i, v := range v.messages {
			likes := make([]int64, len(v.likes))
			for i := range v.likes {
				likes = append(likes, i)
			}
			messages[i] = &rpb.MessageState{
				UserId:      v.user,
				Text:        v.text,
				CreatedAt:   v.created,
				UserIdLiked: likes,
			}
		}
		topics[i] = &rpb.TopicState{
			Name:     v.name,
			Messages: messages,
		}
	}
	users := make([]string, len(s.users))
	for i, v := range s.users {
		users[i] = v.name
	}

	out := rpb.WholeState{
		MessageId: s.message_idx,
		Topic:     topics,
		Users:     users,
	}
	return &out, nil
}

func (s *MessageBoardServer) topicExist(id int64) (int64, error) {
	// expects lock
	if int64(len(s.topics)) > id {
		return id, nil
	}
	return 0, fmt.Errorf("Topic %d doesn't exist", id)
}

func (s *MessageBoardServer) userExist(id int64) (int64, error) {
	// expects lock
	if int64(len(s.users)) > id {
		return id, nil
	}
	return 0, fmt.Errorf("User %d doesn't exist", id)
}
func (s *MessageBoardServer) msgExist(topic_id, id int64) (int64, error) {
	// expects lock
	if _, ok := s.topics[topic_id].messages[id]; ok {
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

	success := false
	for k := range s.control {
		conn, err := grpc.NewClient(k, grpc_security_opt)
		if err != nil {
			log.Printf("failed to contact controll server %v, due to %v", k, err)
			continue
		}
		defer conn.Close()
		client := rpb.NewControlPlaneClient(conn)
		res, err := client.GetClusterStateServer(context.Background(), &rpb.GetClusterStateRequest{NodeId: s.self.id})
		if err != nil {
			log.Printf("failed to contact controll server %v, due to %v", k, err)
			continue
		}
		success = true
		if res.Head != nil {
			s.chain_prev = &Node{id: res.Head.NodeId, address: res.Head.Address}
		} else {
			s.chain_prev = nil
		}
		if res.Tail != nil {
			s.chain_next = &Node{id: res.Tail.NodeId, address: res.Tail.Address}
		} else {
			s.chain_next = nil
		}
		s.control = map[string]struct{}{}
		for _, v := range res.Servers {
			s.control[v.Address] = struct{}{}
		}
		break
	}
	if !success {
		return nil, fmt.Errorf("Couldn't connect to any controll plane servers from: %v", s.control)
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
	s.ChainChange(context.Background(), &emptypb.Empty{})
	return nil
}
func (s *ControlledPlaneServer) snitchNext() error {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	next_id := s.chain_next.id

	return s.snitch(next_id)
}

func (s *ControlledPlaneServer) snitch(id int64) error {
	success := false
	for k := range s.control {
		conn, err := grpc.NewClient(k, grpc_security_opt)
		if err != nil {
			log.Printf("failed to contact controll server %v, due to %v", k, err)
			continue
		}
		defer conn.Close()
		client := rpb.NewControlPlaneClient(conn)
		_, err = client.Snitch(context.Background(), &rpb.SnitchRequest{NodeId: id})
		if err != nil {
			log.Printf("failed to contact controll server %v, due to %v", k, err)
			continue
		}
		success = true
		break
	}
	if !success {
		return fmt.Errorf("Couldn't connect to any controll plane servers: %v", s.control)
	}
	return nil
}
