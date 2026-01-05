package main

import (
	"fmt"
	"github.com/alecthomas/kong"
	"google.golang.org/grpc"
	// codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	// status "google.golang.org/grpc/status"
	// emptypb "google.golang.org/protobuf/types/known/emptypb"
	// "google.golang.org/protobuf/types/known/timestamppb"
)

var cli RunControlCmd

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}

var grpc_security_opt = grpc.WithTransportCredentials(insecure.NewCredentials())

type RunControlCmd struct {
	BindAddr     string   `short:"b" help:"binds to this address"`
	OtherMembers []string `arg:""`
}

func (s *RunControlCmd) Run() error {
	fmt.Println(s)
	return nil
}
