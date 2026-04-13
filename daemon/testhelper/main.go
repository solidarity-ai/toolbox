package main

import (
	"context"
	"fmt"
	"os"

	connect "connectrpc.com/connect"
	daemonv1 "github.com/solidarity-ai/toolbox/daemon/apiv1"
	"github.com/solidarity-ai/toolbox/daemon/apiv1/daemonv1connect"
	"github.com/solidarity-ai/toolbox/daemon/internal/transport"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: peer-helper <socket>")
		os.Exit(2)
	}

	service := daemonv1connect.NewSessionServiceClient(transport.NewUnixHTTPClient(os.Args[1]), "http://toolbox-daemon")
	resp, err := service.Ping(context.Background(), connect.NewRequest(&daemonv1.PingRequest{}))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if payload := resp.Msg.GetPayload(); payload != "pong" {
		fmt.Fprintf(os.Stderr, "unexpected response payload: %q\n", payload)
		os.Exit(1)
	}
	fmt.Println(resp.Msg.GetPayload())
}
