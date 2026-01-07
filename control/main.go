package main

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"

	// "google.golang.org/protobuf/types/known/timestamppb"
	"context"
	rpb "control/razpravljalnica"
	"log"
	"net"
)

var cli RunControlCmd

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}

var grpc_security_opt = grpc.WithTransportCredentials(insecure.NewCredentials())

type RunControlCmd struct {
	BindAddr     string   `arg:"" help:"binds to this address"`
	OtherMembers []string `arg:"" optional:"" help:"other control server addresses"`
}

var cntsrv = ControlPlaneServer{}

func statePrinter() {
	for {
		// cntsrv.mtx.RLock()
		log.Printf(".{.idx = %v, .nodes = %+v}\n", cntsrv.Idx, cntsrv.Nodes)
		// cntsrv.mtx.RUnlock()
		time.Sleep(time.Second)
	}
}

func (s *RunControlCmd) Run() error {
	go statePrinter()
	lis, err := net.Listen("tcp", s.BindAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	cntsrv.self = lis.Addr().String()
	srv := grpc.NewServer()
	rpb.RegisterControlPlaneServer(srv, &cntsrv)
	log.Printf("server listening at %v", lis.Addr())
	if err := srv.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %v", err)
	}
	return nil
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

	self string // TODO
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

	if idx < len(s.Nodes)-1 {
		h := s.Nodes[idx+1]
		head = &rpb.NodeInfo{NodeId: h.id, Address: h.address}
	}
	if idx > 0 {
		t := s.Nodes[idx-1]
		tail = &rpb.NodeInfo{NodeId: t.id, Address: t.address}
	}
	return &rpb.GetClusterStateResponse{
		Head:    head,
		Tail:    tail,
		Servers: []*rpb.ControlInfo{{Address: s.self}}, // TODO
	}, nil
	// return nil, status.Error(codes.Unimplemented, "method GetClusterStateServer not implemented")
}
func (s *ControlPlaneServer) GetClusterStateClient(context.Context, *emptypb.Empty) (*rpb.GetClusterStateResponse, error) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	if len(s.Nodes) < 1 {
		return nil, status.Error(codes.Unavailable, "Missing nodes.")

	}
	head := s.Nodes[0]
	tail := s.Nodes[len(s.Nodes)-1]
	return &rpb.GetClusterStateResponse{
		Head:    &rpb.NodeInfo{NodeId: head.id, Address: head.address},
		Tail:    &rpb.NodeInfo{NodeId: tail.id, Address: tail.address},
		Servers: []*rpb.ControlInfo{{Address: s.self}}, // TODO
	}, nil
}
func (s *ControlPlaneServer) Register(ctx context.Context, req *rpb.RegisterRequest) (*rpb.NodeInfo, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	id := s.Idx
	s.Idx++
	s.Nodes = append(s.Nodes, Node{id: id, address: req.Address})
	if len(s.Nodes) > 1 {
		go s.sendUpdate(s.Nodes[len(s.Nodes)-2].id)
	}

	return &rpb.NodeInfo{NodeId: id, Address: req.Address}, nil
}
func (s *ControlPlaneServer) Snitch(ctx context.Context, req *rpb.SnitchRequest) (*emptypb.Empty, error) {
	go s.removeNode(req.NodeId)
	return &emptypb.Empty{}, nil
}

func (s *ControlPlaneServer) sendUpdate(id int64) {
	s.mtx.RLock()
	defer s.mtx.RUnlock()

	idx := slices.IndexFunc(s.Nodes, func(e Node) bool { return e.id == id })
	if idx == -1 {
		return
	}
	v := s.Nodes[idx]

	conn, err := grpc.NewClient(v.address, grpc_security_opt)
	if err != nil {
		log.Printf("removing node %v due to %v", idx, err)
		go s.removeNode(id)
		return
	}
	defer conn.Close()
	client := rpb.NewControlledPlaneClient(conn)
	_, err = client.ChainChange(context.Background(), &emptypb.Empty{})
	if err != nil {
		log.Printf("removing node %v due to %v", idx, err)
		go s.removeNode(id)
		return
	}
}

func (s *ControlPlaneServer) removeNode(id int64) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	idx := slices.IndexFunc(s.Nodes, func(e Node) bool { return e.id == id })
	if idx == -1 {
		return
	}
	if idx > 0 {
		go s.sendUpdate(s.Nodes[idx-1].id)
	}
	if idx < len(s.Nodes)-1 {
		go s.sendUpdate(s.Nodes[idx+1].id)
	}

	s.Nodes = slices.Delete(s.Nodes, idx, idx+1)
}
