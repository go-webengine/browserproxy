// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/go-webengine/browserproxy/browserpb"
	wstransport "github.com/grpc-transports/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// dialSession stands up srv behind a real gRPC-over-WebSocket transport on
// localhost and returns a connected Session stream plus a cleanup func. This is
// the actual production wire: grpc-transports/websocket carrying the browserpb
// service, the same transport the js/wasm client uses in the browser.
func dialSession(t *testing.T, srv *Server) (browserpb.Browser_SessionClient, func()) {
	t.Helper()
	lis, err := wstransport.ListenWebSocket("127.0.0.1:0", wstransport.ServerConfig{OriginPatterns: []string{"*"}})
	if err != nil {
		t.Fatalf("ListenWebSocket: %v", err)
	}
	gs := grpc.NewServer()
	srv.Register(gs)
	go func() { _ = gs.Serve(lis) }()

	opt, err := wstransport.DialOption("ws://"+lis.Addr().String(), wstransport.ClientConfig{})
	if err != nil {
		t.Fatalf("DialOption: %v", err)
	}
	cc, err := grpc.NewClient("passthrough:///browserproxy",
		grpc.WithTransportCredentials(insecure.NewCredentials()), opt)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	stream, err := browserpb.NewBrowserClient(cc).Session(context.Background())
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	return stream, func() { _ = cc.Close(); gs.Stop(); _ = lis.Close() }
}

// TestIntegration_StubbedTransport is the hermetic end-to-end proof: a real gRPC
// client, over the real WebSocket transport, drives a stub-rendered server and
// asserts a full bidirectional exchange — initial state, a navigate, and the
// resulting frame + state — without touching the network.
func TestIntegration_StubbedTransport(t *testing.T) {
	srv := NewServer(Config{DefaultW: 320, DefaultH: 240, RenderTimeout: 5 * time.Second})
	srv.newSession = func() *Session { return newSession(320, 240, stubRender(800, nil, nil), Options{}) }
	stream, cleanup := dialSession(t, srv)
	defer cleanup()

	first, err := stream.Recv() // initial (empty) chrome state
	if err != nil {
		t.Fatalf("recv initial: %v", err)
	}
	if first.GetState() == nil {
		t.Fatalf("first message is not state: %T", first.GetMsg())
	}

	if err := stream.Send(cmNavigate("http://a.example/")); err != nil {
		t.Fatalf("send navigate: %v", err)
	}

	var gotFrame, gotState bool
	for i := 0; i < 4 && !(gotFrame && gotState); i++ {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if e := msg.GetError(); e != nil {
			t.Fatalf("server error: %s", e.GetMessage())
		}
		if f := msg.GetFrame(); f != nil {
			if f.GetW() != 320 || f.GetH() != 240 {
				t.Errorf("frame size = %dx%d, want 320x240", f.GetW(), f.GetH())
			}
			img, derr := png.Decode(bytes.NewReader(f.GetPng()))
			if derr != nil {
				t.Fatalf("frame PNG decode: %v", derr)
			}
			if img.Bounds().Dx() != 320 {
				t.Errorf("decoded frame width = %d, want 320", img.Bounds().Dx())
			}
			gotFrame = true
		}
		if s := msg.GetState(); s != nil && s.GetUrl() == "http://a.example/" {
			gotState = true
		}
	}
	if !gotFrame {
		t.Error("never received a frame over the real transport")
	}
	if !gotState {
		t.Error("never received the post-navigate state")
	}
}

// TestIntegration_ExampleCom is the live-network proof: the same real gRPC
// client and transport drive a real engine-backed server against
// https://example.com and assert a non-blank frame plus the page title. Skipped
// under -short.
func TestIntegration_ExampleCom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live-network integration test in -short mode")
	}
	srv := NewServer(Config{DefaultW: 1024, DefaultH: 768, RenderTimeout: 30 * time.Second})
	stream, cleanup := dialSession(t, srv)
	defer cleanup()

	if _, err := stream.Recv(); err != nil { // initial state
		t.Fatalf("recv initial: %v", err)
	}
	if err := stream.Send(cmNavigate("https://example.com")); err != nil {
		t.Fatalf("send navigate: %v", err)
	}

	var gotFrame, gotTitle bool
	for i := 0; i < 8 && !(gotFrame && gotTitle); i++ {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if e := msg.GetError(); e != nil {
			t.Fatalf("server error: %s", e.GetMessage())
		}
		if f := msg.GetFrame(); f != nil {
			img, derr := png.Decode(bytes.NewReader(f.GetPng()))
			if derr != nil {
				t.Fatalf("PNG decode: %v", derr)
			}
			if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 768 {
				t.Fatalf("frame size = %v, want 1024x768", b)
			}
			if hasNonWhite(img) {
				gotFrame = true
			}
		}
		if s := msg.GetState(); s != nil && strings.Contains(strings.ToLower(s.GetTitle()), "example") {
			gotTitle = true
		}
	}
	if !gotFrame {
		t.Error("never received a non-blank frame")
	}
	if !gotTitle {
		t.Error("never received a state with the example.com title")
	}
}

// hasNonWhite reports whether img has any non-white pixel — proof the page
// actually painted content, not a blank canvas.
func hasNonWhite(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 3 {
		for x := b.Min.X; x < b.Max.X; x += 3 {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < 250 || g>>8 < 250 || bl>>8 < 250 {
				return true
			}
		}
	}
	return false
}
