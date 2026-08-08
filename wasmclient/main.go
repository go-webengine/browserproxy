// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build js && wasm

// Command wasmclient is the browser-side half of the wasm end-to-end test and a
// worked example of a GOOS=js/wasm browserproxy client. It dials the browserpb
// Browser service through grpc-transports/websocket's js/wasm DialOption, runs
// the bidirectional Session stream (navigate → frame + state), and reports the
// outcome to the JS host via globalThis.__bpResult. It is driven by run.mjs.
//
// This is the payoff of the gRPC-over-WebSocket migration: gorilla/websocket
// does not build for js/wasm, so before this transport a pure-Go browser client
// was impossible; here the very same client code runs in the browser.
package main

import (
	"context"
	"fmt"
	"syscall/js"
	"time"

	"github.com/go-webengine/browserproxy/browserpb"
	wstransport "github.com/grpc-transports/websocket"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	report := func(ok bool, msg string) {
		js.Global().Set("__bpResult", map[string]any{"ok": ok, "msg": msg})
	}
	url := js.Global().Get("__bpURL").String()

	opt, err := wstransport.DialOption(url, wstransport.ClientConfig{})
	if err != nil {
		report(false, "DialOption: "+err.Error())
		return
	}
	cc, err := grpc.NewClient("passthrough:///browserproxy",
		grpc.WithTransportCredentials(insecure.NewCredentials()), opt)
	if err != nil {
		report(false, "NewClient: "+err.Error())
		return
	}
	defer cc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, err := browserpb.NewBrowserClient(cc).Session(ctx)
	if err != nil {
		report(false, "Session: "+err.Error())
		return
	}

	// Drain the initial chrome state.
	if _, err := stream.Recv(); err != nil {
		report(false, "recv initial: "+err.Error())
		return
	}
	// Navigate, then read until we see a frame plus the matching state.
	if err := stream.Send(&browserpb.ClientMsg{
		Msg: &browserpb.ClientMsg_Navigate{Navigate: &browserpb.Navigate{Url: "http://a.example/"}},
	}); err != nil {
		report(false, "send navigate: "+err.Error())
		return
	}
	var gotFrame, gotState bool
	for i := 0; i < 6 && !(gotFrame && gotState); i++ {
		msg, err := stream.Recv()
		if err != nil {
			report(false, "recv: "+err.Error())
			return
		}
		if e := msg.GetError(); e != nil {
			report(false, "server error: "+e.GetMessage())
			return
		}
		if f := msg.GetFrame(); f != nil {
			if len(f.GetPng()) == 0 {
				report(false, "empty frame png")
				return
			}
			gotFrame = true
		}
		if s := msg.GetState(); s != nil && s.GetUrl() == "http://a.example/" {
			gotState = true
		}
	}
	if !gotFrame || !gotState {
		report(false, fmt.Sprintf("incomplete exchange frame=%v state=%v", gotFrame, gotState))
		return
	}
	_ = stream.CloseSend()
	report(true, "navigate → frame+state ok")
}
