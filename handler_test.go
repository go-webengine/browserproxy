// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-webengine/browserproxy/browserpb"
	wstransport "github.com/grpc-transports/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestHandlerListener drives the full HTTP-mounted path: HandlerListener mounts
// the gRPC-over-WebSocket service on an httptest server, a real gRPC client
// dials it over the WebSocket transport, exchanges one message, then the
// returned shutdown func stops the backing grpc.Server. Passing "" exercises the
// DefaultPath fallback.
func TestHandlerListener(t *testing.T) {
	srv := NewServer(Config{DefaultW: 320, DefaultH: 240, RenderTimeout: 5 * time.Second})
	srv.newSession = func() *Session { return newSession(320, 240, stubRender(800, nil, nil), Options{}) }

	handler, shutdown := srv.HandlerListener("") // "" → DefaultPath ("/ws")
	ts := httptest.NewServer(handler)
	defer ts.Close()
	defer shutdown()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + DefaultPath
	opt, err := wstransport.DialOption(wsURL, wstransport.ClientConfig{})
	if err != nil {
		t.Fatalf("DialOption: %v", err)
	}
	cc, err := grpc.NewClient("passthrough:///browserproxy",
		grpc.WithTransportCredentials(insecure.NewCredentials()), opt)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := browserpb.NewBrowserClient(cc).Session(ctx)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	first, err := stream.Recv() // initial chrome state
	if err != nil {
		t.Fatalf("recv initial: %v", err)
	}
	if first.GetState() == nil {
		t.Fatalf("first message is not state: %T", first.GetMsg())
	}
}
