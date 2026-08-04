// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"net"
	"net/http"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestSplitOrigins(t *testing.T) {
	if got := splitOrigins("  a , b ,,c "); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("splitOrigins = %v", got)
	}
	if got := splitOrigins(""); got != nil {
		t.Errorf("empty = %v, want nil", got)
	}
}

func TestRun_BadFlag(t *testing.T) {
	var buf bytes.Buffer
	if code := run([]string{"-nope"}, &buf); code != 2 {
		t.Errorf("bad flag exit = %d, want 2", code)
	}
}

func TestRun_ListenError(t *testing.T) {
	// Occupy a port, then ask the server to bind the same address → it fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	var buf bytes.Buffer
	code := run([]string{"-addr", ln.Addr().String()}, &buf)
	if code != 1 {
		t.Errorf("listen-error exit = %d, want 1", code)
	}
}

func TestRun_GracefulShutdown(t *testing.T) {
	// Reserve a free port, then run the server on it and poll /healthz.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	addr := probe.Addr().String()
	probe.Close()

	done := make(chan int, 1)
	var buf bytes.Buffer
	go func() { done <- run([]string{"-addr", addr}, &buf) }()

	// Wait for the server to come up.
	up := false
	for i := 0; i < 100; i++ {
		if resp, err := http.Get("http://" + addr + "/healthz"); err == nil {
			resp.Body.Close()
			up = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !up {
		t.Fatal("server never became healthy")
	}

	// SIGTERM is caught by run's signal context (registered while it runs), so
	// it triggers graceful shutdown rather than killing the test process.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("graceful shutdown exit = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after SIGTERM")
	}
}
