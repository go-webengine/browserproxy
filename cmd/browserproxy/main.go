// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

// Command browserproxy serves the go-webengine remote-browser WebSocket
// endpoint: it renders web pages server-side with the pure-Go engine and
// streams frames to a client (e.g. the wasmdesk clients/browser front-end),
// forwarding the client's input back as navigation.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-webengine/browserproxy"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

// run parses flags, starts the HTTP server and blocks until a termination
// signal, then shuts down gracefully. It returns a process exit code. Splitting
// it out of main (which only calls os.Exit) keeps the startup path testable.
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("browserproxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":8090", "listen address for the WebSocket endpoint")
	origins := fs.String("origins", "", "comma-separated allowed WS origins (empty or \"*\" = any)")
	maxConc := fs.Int("max-concurrent", 4, "max concurrent page renders across all sessions")
	minNav := fs.Duration("min-nav-interval", 250*time.Millisecond, "per-session minimum interval between navigations")
	vpW := fs.Int("width", 1024, "default viewport width in pixels")
	vpH := fs.Int("height", 768, "default viewport height in pixels")
	renderTimeout := fs.Duration("render-timeout", 35*time.Second, "per-navigation render timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := browserproxy.Config{
		AllowedOrigins:       splitOrigins(*origins),
		DefaultW:             *vpW,
		DefaultH:             *vpH,
		MaxConcurrentRenders: *maxConc,
		MinNavInterval:       *minNav,
		RenderTimeout:        *renderTimeout,
	}
	srv := browserproxy.NewServer(cfg)

	mux := http.NewServeMux()
	mux.Handle("/ws", srv)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	fmt.Fprintf(stderr, "browserproxy: listening on %s (ws path /ws)\n", *addr)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(stderr, "browserproxy: server error: %v\n", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		fmt.Fprintln(stderr, "browserproxy: shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutCtx); err != nil {
			fmt.Fprintf(stderr, "browserproxy: shutdown error: %v\n", err)
			return 1
		}
		return 0
	}
}

// splitOrigins parses a comma-separated origins flag into a trimmed,
// empty-free slice.
func splitOrigins(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
