// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"context"
	"errors"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webengine/engine"
	"github.com/gorilla/websocket"
)

// fakeConn is an in-memory wsConn: it replays queued reads then returns EOF, and
// records every write. writeErrAt (>=0) forces the Nth write to fail.
type fakeConn struct {
	reads      [][]byte
	ri         int
	writes     [][]byte
	writeErrAt int
}

func (c *fakeConn) ReadMessage() (int, []byte, error) {
	if c.ri >= len(c.reads) {
		return 0, nil, io.EOF
	}
	b := c.reads[c.ri]
	c.ri++
	return websocket.TextMessage, b, nil
}

func (c *fakeConn) WriteMessage(_ int, data []byte) error {
	if c.writeErrAt >= 0 && len(c.writes) == c.writeErrAt {
		return errors.New("write failed")
	}
	c.writes = append(c.writes, append([]byte(nil), data...))
	return nil
}

func kindsOf(out []any) []string {
	var ks []string
	for _, m := range out {
		switch v := m.(type) {
		case Frame:
			ks = append(ks, v.Kind)
		case State:
			ks = append(ks, v.Kind)
		case ErrorMsg:
			ks = append(ks, v.Kind)
		}
	}
	return ks
}

func hasKind(out []any, kind string) bool {
	for _, k := range kindsOf(out) {
		if k == kind {
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
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindNavigate, URL: "http://a.example/"})
		if !hasKind(out, KindFrame) || !hasKind(out, KindState) || hasKind(out, KindError) {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("navigate blocked", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindNavigate, URL: "http://127.0.0.1/"})
		if !hasKind(out, KindError) || !hasKind(out, KindFrame) || !hasKind(out, KindState) {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("click and back and forward", func(t *testing.T) {
		link := engine.Link{Rect: image.Rect(0, 0, 100, 40), Href: "http://dst.example/"}
		srv, sess := testServerAndSession([]engine.Link{link})
		_ = sess.Navigate(ctx, "http://src.example/")
		if out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindClick, X: 5, Y: 5}); !hasKind(out, KindFrame) {
			t.Errorf("click kinds = %v", kindsOf(out))
		}
		if out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindBack}); !hasKind(out, KindFrame) {
			t.Errorf("back kinds = %v", kindsOf(out))
		}
		if out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindForward}); !hasKind(out, KindFrame) {
			t.Errorf("forward kinds = %v", kindsOf(out))
		}
	})

	t.Run("back with no history errors", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindBack})
		if !hasKind(out, KindError) {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("forward with no history errors", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindForward})
		if !hasKind(out, KindError) {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("resize scroll key", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		_ = sess.Navigate(ctx, "http://a/")
		for _, m := range []ClientMsg{
			{Kind: KindResize, W: 800, H: 600},
			{Kind: KindScroll, DY: 100},
			{Kind: KindKey, Key: "ArrowDown"},
		} {
			if out := srv.dispatch(ctx, sess, m); !hasKind(out, KindFrame) || !hasKind(out, KindState) {
				t.Errorf("%s kinds = %v", m.Kind, kindsOf(out))
			}
		}
	})

	t.Run("unknown kind errors", func(t *testing.T) {
		srv, sess := testServerAndSession(nil)
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: "bogus"})
		if !hasKind(out, KindError) || !hasKind(out, KindState) {
			t.Errorf("kinds = %v", kindsOf(out))
		}
	})

	t.Run("frame encode failure", func(t *testing.T) {
		orig := encodePNG
		defer func() { encodePNG = orig }()
		encodePNG = func(image.Image) ([]byte, error) { return nil, errors.New("enc") }
		srv, sess := testServerAndSession(nil)
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindScroll, DY: 1})
		if !hasKind(out, KindError) { // the "frame encode failed" error
			t.Errorf("kinds = %v", kindsOf(out))
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

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		allowed []string
		origin  string
		want    bool
	}{
		{nil, "http://any", true},                     // empty allowlist
		{[]string{"http://x"}, "", true},              // empty origin (non-browser)
		{[]string{"*"}, "http://any", true},           // wildcard
		{[]string{"http://ok"}, "http://ok", true},    // exact
		{[]string{"http://OK"}, "http://ok", true},    // case-insensitive
		{[]string{"http://ok"}, "http://evil", false}, // mismatch
	}
	for _, c := range cases {
		if got := originAllowed(c.allowed, c.origin); got != c.want {
			t.Errorf("originAllowed(%v,%q) = %v, want %v", c.allowed, c.origin, got, c.want)
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
	if srv.upgrader.CheckOrigin == nil {
		t.Error("no CheckOrigin set")
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

// TestServeHTTP_RoundTrip exercises the real WebSocket upgrade and the
// serveConn success path over a localhost socket, with a stub-rendered session
// so no external network is touched.
func TestServeHTTP_RoundTrip(t *testing.T) {
	srv := NewServer(Config{RenderTimeout: 2 * time.Second})
	srv.newSession = func() *Session { return newSession(320, 240, stubRender(1000, nil, nil), Options{}) }
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	wsURL := "ws" + ts.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, _, err := conn.ReadMessage(); err != nil { // initial state
		t.Fatalf("read initial state: %v", err)
	}
	nav, _ := Encode(ClientMsg{Kind: KindNavigate, URL: "http://a.example/"})
	if err := conn.WriteMessage(websocket.TextMessage, nav); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	sawFrame := false
	for i := 0; i < 4 && !sawFrame; i++ {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if contains(string(data), `"kind":"frame"`) {
			sawFrame = true
		}
	}
	if !sawFrame {
		t.Error("no frame received over the real WebSocket")
	}
}

func TestNewServer_CheckOrigin(t *testing.T) {
	srv := NewServer(Config{AllowedOrigins: []string{"http://ok"}})
	mk := func(origin string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, "http://x/ws", nil)
		r.Header.Set("Origin", origin)
		return r
	}
	if !srv.upgrader.CheckOrigin(mk("http://ok")) {
		t.Error("allowed origin rejected")
	}
	if srv.upgrader.CheckOrigin(mk("http://evil")) {
		t.Error("disallowed origin accepted")
	}
}

func TestServeHTTP_UpgradeFailure(t *testing.T) {
	// A plain GET without WebSocket headers fails the upgrade; ServeHTTP must
	// return cleanly (the Upgrader has already written a 400).
	srv := NewServer(Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://x/ws", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusSwitchingProtocols {
		t.Errorf("expected upgrade failure, got %d", rec.Code)
	}
}

func TestDispatch_ClickAndResizeErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("click to blocked href errors", func(t *testing.T) {
		link := engine.Link{Rect: image.Rect(0, 0, 100, 40), Href: "http://10.0.0.1/"}
		srv, sess := testServerAndSession([]engine.Link{link})
		_ = sess.Navigate(ctx, "http://src.example/")
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindClick, X: 5, Y: 5})
		if !hasKind(out, KindError) {
			t.Errorf("blocked click kinds = %v", kindsOf(out))
		}
	})

	t.Run("resize render error", func(t *testing.T) {
		// A renderer that succeeds once (navigate) then fails (the resize
		// re-render) drives the resize error branch.
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
		out := srv.dispatch(ctx, sess, ClientMsg{Kind: KindResize, W: 800, H: 600})
		if !hasKind(out, KindError) {
			t.Errorf("resize-error kinds = %v", kindsOf(out))
		}
	})
}

func TestServeConn(t *testing.T) {
	srv := NewServer(Config{RenderTimeout: 2 * time.Second})
	// Inject a stub-rendered session so no network is touched.
	srv.newSession = func() *Session { return newSession(1024, 768, stubRender(2000, nil, nil), Options{}) }

	t.Run("navigate then EOF", func(t *testing.T) {
		conn := &fakeConn{writeErrAt: -1, reads: [][]byte{
			[]byte(`{"kind":"navigate","url":"http://a.example/"}`),
		}}
		srv.serveConn(context.Background(), conn)
		// Initial state + (frame,state) for the navigate.
		if len(conn.writes) < 3 {
			t.Errorf("expected >=3 writes, got %d", len(conn.writes))
		}
	})

	t.Run("malformed json → error message", func(t *testing.T) {
		conn := &fakeConn{writeErrAt: -1, reads: [][]byte{[]byte(`{`)}}
		srv.serveConn(context.Background(), conn)
		joined := ""
		for _, w := range conn.writes {
			joined += string(w)
		}
		if !contains(joined, `"kind":"error"`) {
			t.Errorf("no error message written: %s", joined)
		}
	})

	t.Run("write failure on initial state stops loop", func(t *testing.T) {
		conn := &fakeConn{writeErrAt: 0, reads: [][]byte{[]byte(`{"kind":"scroll","dy":1}`)}}
		srv.serveConn(context.Background(), conn)
		if len(conn.writes) != 0 {
			t.Errorf("expected no successful writes, got %d", len(conn.writes))
		}
	})

	t.Run("write failure mid-dispatch stops loop", func(t *testing.T) {
		conn := &fakeConn{writeErrAt: 1, reads: [][]byte{[]byte(`{"kind":"scroll","dy":1}`)}}
		srv.serveConn(context.Background(), conn)
		// Only the initial state (index 0) succeeded.
		if len(conn.writes) != 1 {
			t.Errorf("expected 1 write before failure, got %d", len(conn.writes))
		}
	})

	t.Run("write failure after decode error stops loop", func(t *testing.T) {
		conn := &fakeConn{writeErrAt: 1, reads: [][]byte{[]byte(`{`)}}
		srv.serveConn(context.Background(), conn)
		if len(conn.writes) != 1 {
			t.Errorf("expected 1 write before failure, got %d", len(conn.writes))
		}
	})
}

func TestWriteMsg_EncodeError(t *testing.T) {
	conn := &fakeConn{writeErrAt: -1}
	// A channel is not JSON-encodable → Encode fails and nothing is written.
	if err := writeMsg(conn, make(chan int)); err == nil {
		t.Error("want encode error")
	}
	if len(conn.writes) != 0 {
		t.Error("nothing should be written on encode failure")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
