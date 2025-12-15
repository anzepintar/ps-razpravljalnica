package main

import (
	"github.com/alecthomas/kong"
)

type RunMgrCmd struct{}

func registerServer() []string {
	return []string{}
}

// TODO forward state

var cli struct {
	RunNode RunNodeCmd `cmd:"" help:"Starts a node in the chain"`
	RunMgr  RunMgrCmd  `cmd:"" help:"Starts a control plane node"`
}

func main() {
	ctx := kong.Parse(&cli)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}
