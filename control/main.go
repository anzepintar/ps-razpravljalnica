package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"context"
	rpb "control/razpravljalnica"
	"github.com/Jille/raft-grpc-leader-rpc/leaderhealth"
	"github.com/Jille/raft-grpc-leader-rpc/rafterrors"
	transport "github.com/Jille/raft-grpc-transport"
	"github.com/Jille/raftadmin"
	"github.com/alecthomas/kong"
	"github.com/hashicorp/raft"
	boltdb "github.com/hashicorp/raft-boltdb"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

var cli RunControlCmd

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}

var grpc_security_opt = grpc.WithTransportCredentials(insecure.NewCredentials())

type RunControlCmd struct {
	BindAddr      string `arg:"" help:"binds to this address"`
	RaftID        string `arg:"" help:"ID used by raft protocol"`
	BootstrapRaft bool   `help:"Whether to bootstrap the Raft cluster"`
	Tui           bool   `short:"t" help:"enable tui"`
}

var cntsrv = ControlPlaneServer{}

func statePrinter() {
	if cli.Tui {
		return
	}
	for {
		time.Sleep(time.Second * 5)
		cntsrv.mtx.RLock()
		log.Printf(".{ .idx = %v, .nodes = %v, .config = %v }\n",
			cntsrv.Idx,
			cntsrv.Nodes,
			cntsrv.raft.GetConfiguration().Configuration().Servers,
		)
		cntsrv.mtx.RUnlock()
	}
}

func myLog(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	resp, err = handler(ctx, req)
	if !cli.Tui {
		log.Printf("%v(%+v) -> %+v %v", info.FullMethod, req, resp, err)
	}
	return resp, err
}

func (s *RunControlCmd) Run() error {
	go statePrinter()
	lis, err := net.Listen("tcp", s.BindAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	ctx := context.Background()
	r, tm, err := NewRaft(ctx, s.RaftID, lis.Addr().String(), &cntsrv)
	if err != nil {
		return fmt.Errorf("failed to raft: %f", err)
	}
	cntsrv.raft = r

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(myLog))
	rpb.RegisterControlPlaneServer(srv, &cntsrv)
	raftadmin.Register(srv, r)
	leaderhealth.Setup(r, srv, []string{""})
	tm.Register(srv)
	log.Printf("server listening at %v", lis.Addr())
	if s.Tui {
		go func() {
			if err := srv.Serve(lis); err != nil {
				log.Printf("failed to serve: %v", err)
			}
		}()
		RunTUI()
		return nil
	}
	if err := srv.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}
	return nil
}

func NewRaft(ctx context.Context, myID, myAddress string, fsm raft.FSM) (*raft.Raft, *transport.Manager, error) {
	c := raft.DefaultConfig()
	if cli.Tui {
		c.LogLevel = "Off"
	}
	c.LocalID = raft.ServerID(myID)

	raftDir := "runtime"
	err := os.Mkdir(raftDir, 0750)

	baseDir := filepath.Join(raftDir, myID)
	err = os.Mkdir(baseDir, 0750)

	ldb, err := boltdb.NewBoltStore(filepath.Join(baseDir, "logs.dat"))
	if err != nil {
		return nil, nil, fmt.Errorf(`boltdb.NewBoltStore(%q): %v`, filepath.Join(baseDir, "logs.dat"), err)
	}

	sdb, err := boltdb.NewBoltStore(filepath.Join(baseDir, "stable.dat"))
	if err != nil {
		return nil, nil, fmt.Errorf(`boltdb.NewBoltStore(%q): %v`, filepath.Join(baseDir, "stable.dat"), err)
	}

	fss, err := raft.NewFileSnapshotStore(baseDir, 3, os.Stderr)
	if err != nil {
		return nil, nil, fmt.Errorf(`raft.NewFileSnapshotStore(%q, ...): %v`, baseDir, err)
	}

	tm := transport.New(raft.ServerAddress(myAddress), []grpc.DialOption{grpc_security_opt})

	r, err := raft.NewRaft(c, fsm, ldb, sdb, fss, tm.Transport())
	if err != nil {
		return nil, nil, fmt.Errorf("raft.NewRaft: %v", err)
	}

	if cli.BootstrapRaft {
		cfg := raft.Configuration{

			Servers: []raft.Server{
				{
					Suffrage: raft.Voter,
					ID:       raft.ServerID(myID),
					Address:  raft.ServerAddress(myAddress),
				},
			},
		}
		f := r.BootstrapCluster(cfg)
		if err := f.Error(); err != nil {
			return nil, nil, fmt.Errorf("raft.Raft.BootstrapCluster: %v", err)
		}
	}

	return r, tm, nil
}

type Node struct {
	id      int64
	address string
}

type ControlPlaneServer struct {
	rpb.UnimplementedControlPlaneServer

	mtx   sync.RWMutex
	Idx   int64
	Nodes []Node

	raft *raft.Raft
}

func (s *ControlPlaneServer) GetClusterStateServer(ctx context.Context, req *rpb.GetClusterStateRequest) (*rpb.GetClusterStateResponse, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	id := req.NodeId
	idx := slices.IndexFunc(s.Nodes, func(e Node) bool { return e.id == id })
	if idx == -1 {
		return nil, status.Error(codes.NotFound, "Node not found.")
	}
	var head *rpb.NodeInfo
	var tail *rpb.NodeInfo

	cf := s.raft.GetConfiguration()
	conf := cf.Configuration()

	out := make([]*rpb.ControlInfo, len(conf.Servers))
	for i, v := range conf.Servers {
		out[i] = &rpb.ControlInfo{Address: string(v.Address)}
	}

	if idx > 0 {
		h := s.Nodes[idx-1]
		head = &rpb.NodeInfo{NodeId: h.id, Address: h.address}
	}
	if idx < len(s.Nodes)-1 {
		t := s.Nodes[idx+1]
		tail = &rpb.NodeInfo{NodeId: t.id, Address: t.address}
	}
	return &rpb.GetClusterStateResponse{
		Head:    head,
		Tail:    tail,
		Servers: out, // TODO
	}, nil
}
func (s *ControlPlaneServer) GetClusterStateClient(context.Context, *emptypb.Empty) (*rpb.GetClusterStateResponse, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if len(s.Nodes) < 1 {
		return nil, status.Error(codes.Unavailable, "Missing nodes.")

	}
	head := s.Nodes[0]
	tail := s.Nodes[len(s.Nodes)-1]
	cf := s.raft.GetConfiguration()
	conf := cf.Configuration()

	out := make([]*rpb.ControlInfo, len(conf.Servers))
	for i, v := range conf.Servers {
		out[i] = &rpb.ControlInfo{Address: string(v.Address)}
	}

	return &rpb.GetClusterStateResponse{
		Head:    &rpb.NodeInfo{NodeId: head.id, Address: head.address},
		Tail:    &rpb.NodeInfo{NodeId: tail.id, Address: tail.address},
		Servers: out, // TODO
	}, nil
}
func (s *ControlPlaneServer) Register(ctx context.Context, req *rpb.RegisterRequest) (*rpb.NodeInfo, error) {
	f := s.raft.Apply(slices.Concat([]byte("+"), []byte(req.Address)), time.Second)
	if err := f.Error(); err != nil {
		return nil, rafterrors.MarkRetriable(err)
	}
	node, ok := f.Response().(*Node)
	if !ok {
		return nil, rafterrors.MarkRetriable(fmt.Errorf("Failed, to get a node commited"))
	}

	return &rpb.NodeInfo{NodeId: node.id, Address: node.address}, nil
}
func (s *ControlPlaneServer) Snitch(ctx context.Context, req *rpb.SnitchRequest) (*emptypb.Empty, error) {
	go s.removeNode(req.NodeId, true)
	return &emptypb.Empty{}, nil
}

func (s *ControlPlaneServer) sendUpdate(id int64) {
	v_ptr := func() *Node {
		s.mtx.RLock()
		defer s.mtx.RUnlock()

		idx := slices.IndexFunc(s.Nodes, func(e Node) bool { return e.id == id })
		if idx == -1 {
			return nil
		}
		v := s.Nodes[idx]
		return &v
	}()
	if v_ptr == nil {
		return
	}
	v := *v_ptr

	conn, err := grpc.NewClient(v.address, grpc_security_opt)
	if err != nil {
		go s.removeNode(id, true)
		return
	}
	defer conn.Close()
	client := rpb.NewControlledPlaneClient(conn)
	_, err = client.ChainChange(context.Background(), &emptypb.Empty{})
	if err != nil {
		go s.removeNode(id, true)
		return
	}
}

func (s *ControlPlaneServer) idToNode(id int64) *Node {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	idx := slices.IndexFunc(s.Nodes, func(e Node) bool { return e.id == id })
	if idx == -1 {
		return nil
	}
	node := s.Nodes[idx]
	return &node
}

func (s *ControlPlaneServer) removeNode(id int64, shutdown bool) {
	if shutdown {
		go sendShutdown(s.idToNode(id).address)
	}
	req := []byte("-")
	req = strconv.AppendInt(req, id, 10)
	f := s.raft.Apply(req, time.Second)
	if err := f.Error(); err != nil {
		if status.Code(err) == codes.Unavailable {
			go s.removeNode(id, shutdown)
		}
		return
	}
	_ = f.Response()
}

func sendShutdown(addr string) {
	conn, err := grpc.NewClient(addr, grpc_security_opt)
	if err != nil {
		return
	}
	defer conn.Close()
	client := rpb.NewControlledPlaneClient(conn)
	_, err = client.Stop(context.Background(), &emptypb.Empty{})
	if err != nil {
		return
	}
}

func (s *ControlPlaneServer) Apply(l *raft.Log) any {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	req := l.Data
	op := req[0]

	if op == '+' {
		addr := string(req[1:])
		if idx := slices.IndexFunc(s.Nodes, func(e Node) bool { return e.address == addr }); idx != -1 {
			return &s.Nodes[idx]
		}

		id := s.Idx
		s.Idx++
		new_node := Node{id: id, address: addr}
		s.Nodes = append(s.Nodes, new_node)
		if len(s.Nodes) > 1 {
			go s.sendUpdate(s.Nodes[len(s.Nodes)-2].id)
		}
		return &new_node
	} else if op == '-' {
		id_str := req[1:]
		id, err := strconv.ParseInt(string(id_str), 10, 10)
		if err != nil {
			panic("invalid arg to '-' op")
		}
		idx := slices.IndexFunc(s.Nodes, func(e Node) bool { return e.id == id })
		if idx == -1 {
			return nil
		}
		if idx > 0 {
			go s.sendUpdate(s.Nodes[idx-1].id)
		}
		if idx < len(s.Nodes)-1 {
			go s.sendUpdate(s.Nodes[idx+1].id)
		}

		s.Nodes = slices.Delete(s.Nodes, idx, idx+1)
		return nil
	}
	log.Panicf("unkown apply op %v '%s'", op, req)
	return nil
}

func (s *ControlPlaneServer) Restore(r io.ReadCloser) error {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	var snapshot Snapshot
	err = json.Unmarshal(b, &snapshot)
	if err != nil {
		return err
	}
	s.Idx = snapshot.idx
	copy(s.Nodes, snapshot.nodes)
	return nil
}

type Snapshot struct {
	idx   int64
	nodes []Node
}

func (s *Snapshot) Persist(sink raft.SnapshotSink) error {
	bytes, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("sink.Write(): %v", err)
	}
	_, err = sink.Write(bytes)
	if err != nil {
		sink.Cancel()
		return fmt.Errorf("sink.Write(): %v", err)
	}
	return sink.Close()
}

func (s *Snapshot) Release() {}

func (s *ControlPlaneServer) Snapshot() (raft.FSMSnapshot, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	var new_nodes []Node
	copy(new_nodes, s.Nodes)
	return &Snapshot{idx: s.Idx, nodes: new_nodes}, nil
}
