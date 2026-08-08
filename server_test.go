// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"context"
	"errors"
	"image"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/go-webengine/browserproxy/browserpb"
	"github.com/go-webengine/engine"
	"google.golang.org/grpc"
)

// ---- client-message builders -------------------------------------------------

func cmNavigate(url string) *browserpb.ClientMsg {
	return &browserpb.ClientMsg{Msg: &browserpb.ClientMsg_Navigate{Navigate: &browserpb.Navigate{Url: url}}}
}
func cmClick(x, y int) *browserpb.ClientMsg {
	return &browserpb.ClientMsg{Msg: &browserpb.ClientMsg_Click{Click: &browserpb.Click{X: int32(x), Y: int32(y)}}}
}
func cmScroll(dy int) *browserpb.ClientMsg {
	return &browserpb.ClientMsg{Msg: &browserpb.ClientMsg_Scroll{Scroll: &browserpb.Scroll{Dy: int32(dy)}}}
}
func cmKey(k string) *browserpb.ClientMsg {
	return &browserpb.ClientMsg{Msg: &browserpb.ClientMsg_Key{Key: &browserpb.Key{Key: k}}}
}
func cmResize(w, h int) *browserpb.ClientMsg {
	return &browserpb.ClientMsg{Msg: &browserpb.ClientMsg_Resize{Resize: &browserpb.Resize{W: int32(w), H: int32(h)}}}
}
func cmBack() *browserpb.ClientMsg {
	return &browserpb.ClientMsg{Msg: &browserpb.ClientMsg_Back{Back: &browserpb.Back{}}}
}
func cmForward() *browserpb.ClientMsg {
	return &browserpb.ClientMsg{Msg: &browserpb.ClientMsg_Forward{Forward: &browserpb.Forward{}}}
}

// kind reports the oneof case of a server message as a short string.
func kind(m *browserpb.ServerMsg) string {
	switch m.GetMsg().(type) {
	case *browserpb.ServerMsg_Frame:
		return "frame"
	case *browserpb.ServerMsg_State:
		return "state"
	case *browserpb.ServerMsg_Error:
		return "error"
	}
	return ""
}

func kindsOf(out []*browserpb.ServerMsg) []string {
	ks := make([]string, 0, len(out))
	for _, m := range out {
		ks = append(ks, kind(m))
	}
	return ks
}

func hasKind(out []*browserpb.ServerMsg, want string) bool {
	for _, k := range kindsOf(out) {
		if k == want {
			return true
		}
	}
	return false
}

// testServerAndSession builds a Server plus a stub-rendered session so dispatch
// runs without the network.
func testServerAndSession(links []engine.Link) (*Server, *Session) {
	srv := NewServer(Config{RenderTimeout: 2 * time.Second})
	sess := newSession(1024, 768, stubRender(2000, links, nil), Options{})
	return srv, sess
}

func TestDispatch_AllKinds(t *testing.T) {
	ctx := context.Background()

	t.Run("navigate ok", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		out := srv.dispatch(ctx, sess, cmNavigate("http://a.example/"))
		if !hasKind(out, "frame") || !hasKind(out, "state") || hasKind(out, "error") {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("navigate blocked", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		out := srv.dispatch(ctx, sess, cmNavigate("http://127.0.0.1/"))
		if !hasKind(out, "error") || !hasKind(out, "frame") || !hasKind(out, "state") {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("click and back and forward", func(t *testing.T) {
		link := engine.Link{Rect: image.Rect(0, 0, 100, 40), Href: "http://dst.example/"}
		srv, sess := testServerAndSession([]engine.Link{link})
		_ = sess.Navigate(ctx, "http://src.example/")
		if out := srv.dispatch(ctx, sess, cmClick(5, 5)); !hasKind(out, "frame") {
			t.Errorf("click kinds = %v", kindsOf(out))
		}
		if out := srv.dispatch(ctx, sess, cmBack()); !hasKind(out, "frame") {
			t.Errorf("back kinds = %v", kindsOf(out))
		}
		if out := srv.dispatch(ctx, sess, cmForward()); !hasKind(out, "frame") {
			t.Errorf("forward kinds = %v", kindsOf(out))
		}
	})

	t.Run("back with no history errors", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		if out := srv.dispatch(ctx, sess, cmBack()); !hasKind(out, "error") {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("forward with no history errors", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		if out := srv.dispatch(ctx, sess, cmForward()); !hasKind(out, "error") {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("resize scroll key", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		_ = sess.Navigate(ctx, "http://a/")
		for _, m := range []*browserpb.ClientMsg{cmResize(800, 600), cmScroll(100), cmKey("ArrowDown")} {
			if out := srv.dispatch(ctx, sess, m); !hasKind(out, "frame") || !hasKind(out, "state") {
				t.Errorf("kinds = %v", kindsOf(out))
			}
		}
	})

	t.Run("empty message errors", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		if out := srv.dispatch(ctx, sess, &browserpb.ClientMsg{}); !hasKind(out, "error") || !hasKind(out, "state") {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("frame encode failure", func(t *testing.T) {
		orig := encodePNG
		defer func() { encodePNG = orig }()
		encodePNG = func(image.Image) ([]byte, error) { return nil, errors.New("enc") }
		srv, sess := testServerAndSession(nil)
		if out := srv.dispatch(ctx, sess, cmScroll(1)); !hasKind(out, "error") {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})
}

func TestDispatch_ClickAndResizeErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("click to blocked href errors", func(t *testing.T) {
		link := engine.Link{Rect: image.Rect(0, 0, 100, 40), Href: "http://10.0.0.1/"}
		srv, sess := testServerAndSession([]engine.Link{link})
		_ = sess.Navigate(ctx, "http://src.example/")
		if out := srv.dispatch(ctx, sess, cmClick(5, 5)); !hasKind(out, "error") {
			t.Errorf("blocked click kinds = %v", kindsOf(out))
		}
	})

	t.Run("resize render error", func(t *testing.T) {
		var n int
		r := func(_ context.Context, url string, w, h int) (*image.RGBA, *engine.RenderInfo, []engine.Link, error) {
			n++
			if n >= 2 {
				return nil, nil, nil, errors.New("render boom")
			}
			return image.NewRGBA(image.Rect(0, 0, w, h)), &engine.RenderInfo{URL: url}, nil, nil
		}
		srv := NewServer(Config{RenderTimeout: time.Second})
		sess := newSession(1024, 768, r, Options{})
		_ = sess.Navigate(ctx, "http://a/")
		if out := srv.dispatch(ctx, sess, cmResize(800, 600)); !hasKind(out, "error") {
			t.Errorf("resize-error kinds = %v", kindsOf(out))
		}
	})
}

func TestKeyScrollDelta(t *testing.T) {
	sess := newSession(1000, 800, stubRender(10, nil, nil), Options{})
	page := 800 * 9 / 10
	cases := map[string]int{
		"ArrowDown": keyScrollStep, "ArrowUp": -keyScrollStep,
		"PageDown": page, " ": page, "Spacebar": page, "PageUp": -page,
		"Home": -(1 << 30), "End": 1 << 30, "x": 0,
	}
	for key, want := range cases {
		if got := keyScrollDelta(sess, key); got != want {
			t.Errorf("keyScrollDelta(%q) = %d, want %d", key, got, want)
		}
	}
}

func TestNewServer_Defaults(t *testing.T) {
	srv := NewServer(Config{})
	if srv.cfg.DefaultW != defaultViewportW || srv.cfg.DefaultH != defaultViewportH {
		t.Errorf("defaults not applied: %+v", srv.cfg)
	}
	if srv.limiter != nil {
		t.Error("zero MaxConcurrentRenders should leave limiter nil")
	}
	if srv.timeout != defaultRenderTimeout {
		t.Errorf("timeout = %v", srv.timeout)
	}
	// A positive cap creates a buffered limiter.
	srv2 := NewServer(Config{MaxConcurrentRenders: 3, RenderTimeout: time.Second})
	if cap(srv2.limiter) != 3 {
		t.Errorf("limiter cap = %d, want 3", cap(srv2.limiter))
	}
	// The default newSession factory builds a real (guarded) session.
	if srv.newSession() == nil {
		t.Error("default newSession returned nil")
	}
}

func TestOriginPatterns(t *testing.T) {
	if got := NewServer(Config{}).originPatterns(); !reflect.DeepEqual(got, []string{"*"}) {
		t.Errorf("empty allowlist → %v, want [*]", got)
	}
	want := []string{"http://ok", "https://ok"}
	if got := NewServer(Config{AllowedOrigins: want}).originPatterns(); !reflect.DeepEqual(got, want) {
		t.Errorf("passthrough = %v, want %v", got, want)
	}
}

// fakeStream is an in-memory browserpb.Browser_SessionServer: it replays queued
// Recv results then blocks on io.EOF, and records/forces Send outcomes.
type fakeStream struct {
	grpc.ServerStream
	ctx      context.Context
	recvs    []recvResult
	ri       int
	sent     []*browserpb.ServerMsg
	sendErrs []error // per-call Send error (nil = ok); short slice ⇒ trailing calls ok
	si       int
}

type recvResult struct {
	msg *browserpb.ClientMsg
	err error
}

func (s *fakeStream) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *fakeStream) Recv() (*browserpb.ClientMsg, error) {
	if s.ri >= len(s.recvs) {
		return nil, io.EOF
	}
	r := s.recvs[s.ri]
	s.ri++
	return r.msg, r.err
}

func (s *fakeStream) Send(m *browserpb.ServerMsg) error {
	var err error
	if s.si < len(s.sendErrs) {
		err = s.sendErrs[s.si]
	}
	s.si++
	if err != nil {
		return err
	}
	s.sent = append(s.sent, m)
	return nil
}

func newLoopServer() *Server {
	srv := NewServer(Config{RenderTimeout: 2 * time.Second})
	srv.newSession = func() *Session { return newSession(320, 240, stubRender(1000, nil, nil), Options{}) }
	return srv
}

func TestSession_Loop(t *testing.T) {
	t.Run("navigate then clean EOF", func(t *testing.T) {
		srv := newLoopServer()
		st := &fakeStream{recvs: []recvResult{{msg: cmNavigate("http://a.example/")}}}
		if err := srv.Session(st); err != nil {
			t.Fatalf("Session = %v, want nil on clean EOF", err)
		}
		// initial state + (frame,state) for the navigate.
		if len(st.sent) != 3 || kind(st.sent[0]) != "state" {
			t.Fatalf("sent kinds = %v", kindsOf(st.sent))
		}
	})

	t.Run("recv error propagates", func(t *testing.T) {
		srv := newLoopServer()
		boom := errors.New("recv boom")
		st := &fakeStream{recvs: []recvResult{{err: boom}}}
		if err := srv.Session(st); !errors.Is(err, boom) {
			t.Fatalf("Session = %v, want %v", err, boom)
		}
	})

	t.Run("initial send failure stops loop", func(t *testing.T) {
		srv := newLoopServer()
		boom := errors.New("send boom")
		st := &fakeStream{sendErrs: []error{boom}, recvs: []recvResult{{msg: cmScroll(1)}}}
		if err := srv.Session(st); !errors.Is(err, boom) {
			t.Fatalf("Session = %v, want %v", err, boom)
		}
		if len(st.sent) != 0 {
			t.Errorf("nothing should have been sent, got %v", kindsOf(st.sent))
		}
	})

	t.Run("send failure mid-dispatch stops loop", func(t *testing.T) {
		srv := newLoopServer()
		boom := errors.New("send boom")
		// initial state ok, first dispatch send fails.
		st := &fakeStream{sendErrs: []error{nil, boom}, recvs: []recvResult{{msg: cmScroll(1)}}}
		if err := srv.Session(st); !errors.Is(err, boom) {
			t.Fatalf("Session = %v, want %v", err, boom)
		}
		if len(st.sent) != 1 { // only the initial state landed
			t.Errorf("sent = %v, want just the initial state", kindsOf(st.sent))
		}
	})
}

func TestServer_ContextTimeout(t *testing.T) {
	// dispatch derives a per-message timeout from the stream context; a
	// cancelled context makes a limiter-blocked render give up promptly.
	lim := make(chan struct{}, 1)
	lim <- struct{}{} // saturate
	srv := NewServer(Config{RenderTimeout: time.Second})
	sess := newSession(320, 240, stubRender(500, nil, nil), Options{GlobalLimiter: lim})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := srv.dispatch(ctx, sess, cmNavigate("http://a/"))
	if !hasKind(out, "error") {
		t.Errorf("cancelled render should surface an error, kinds = %v", kindsOf(out))
	}
}
