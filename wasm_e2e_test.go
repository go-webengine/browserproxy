// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !js

package browserproxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	wstransport "github.com/grpc-transports/websocket"
	"google.golang.org/grpc"
)

// TestWasmClientE2E compiles the js/wasm browserproxy client, runs it under Node
// against a real (stub-rendered) server reached through grpc-transports/websocket,
// and asserts a full bidirectional Session completes. It proves the migration's
// payoff: a pure-Go browserproxy client that actually runs in the browser — not
// merely that it compiles. It skips cleanly when Node or the toolchain's wasm
// glue is unavailable (e.g. under qemu-emulated CI).
func TestWasmClientE2E(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found; skipping wasm e2e")
	}
	wasmExec := filepath.Join(runtime.GOROOT(), "lib", "wasm", "wasm_exec.js")
	if _, err := os.Stat(wasmExec); err != nil {
		t.Skipf("wasm_exec.js not found at %s; skipping", wasmExec)
	}

	tmp := t.TempDir()
	wasmPath := filepath.Join(tmp, "client.wasm")
	build := exec.Command("go", "build", "-o", wasmPath, "./wasmclient")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("wasm build failed: %v\n%s", err, out)
	}

	srv := NewServer(Config{DefaultW: 320, DefaultH: 240, RenderTimeout: 5 * time.Second})
	srv.newSession = func() *Session { return newSession(320, 240, stubRender(800, nil, nil), Options{}) }
	lis, err := wstransport.ListenWebSocket("127.0.0.1:0", wstransport.ServerConfig{OriginPatterns: []string{"*"}})
	if err != nil {
		t.Fatalf("ListenWebSocket: %v", err)
	}
	gs := grpc.NewServer()
	srv.Register(gs)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()
	defer lis.Close()

	cmd := exec.Command(node, filepath.Join("wasmclient", "run.mjs"))
	cmd.Env = append(os.Environ(),
		"URL=ws://"+lis.Addr().String(),
		"WASM="+wasmPath,
		"WASM_EXEC="+wasmExec,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node host failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "WASM_OK") {
		t.Fatalf("wasm e2e did not report success:\n%s", out)
	}
	t.Logf("wasm client e2e: %s", strings.TrimSpace(string(out)))
}
