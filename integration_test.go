// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestIntegration_ExampleCom is the end-to-end proof: a real WebSocket client
// drives a real Server, navigates to https://example.com, and asserts it
// receives a non-blank frame plus a state carrying the page title. It is
// skipped under -short (it needs live network).
func TestIntegration_ExampleCom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live-network integration test in -short mode")
	}
	srv := NewServer(Config{DefaultW: 1024, DefaultH: 768, RenderTimeout: 30 * time.Second})
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Drain the initial state.
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read initial: %v", err)
	}

	nav, _ := Encode(ClientMsg{Kind: KindNavigate, URL: "https://example.com"})
	if err := conn.WriteMessage(websocket.TextMessage, nav); err != nil {
		t.Fatalf("write navigate: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var gotFrame, gotTitle bool
	for i := 0; i < 8 && !(gotFrame && gotTitle); i++ {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if bytes.Contains(data, []byte(`"kind":"error"`)) {
			t.Fatalf("server error: %s", data)
		}
		if bytes.Contains(data, []byte(`"kind":"frame"`)) {
			var f Frame
			if err := json.Unmarshal(data, &f); err != nil {
				t.Fatalf("decode frame: %v", err)
			}
			raw, err := f.PNG()
			if err != nil {
				t.Fatalf("frame PNG: %v", err)
			}
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("PNG decode: %v", err)
			}
			if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 768 {
				t.Fatalf("frame size = %v, want 1024x768", b)
			}
			if !hasNonWhite(raw) {
				t.Fatal("frame is entirely blank/white")
			}
			gotFrame = true
		}
		if bytes.Contains(data, []byte(`"kind":"state"`)) {
			var s State
			if err := json.Unmarshal(data, &s); err != nil {
				t.Fatalf("decode state: %v", err)
			}
			if strings.Contains(strings.ToLower(s.Title), "example") {
				gotTitle = true
			}
		}
	}
	if !gotFrame {
		t.Error("never received a frame")
	}
	if !gotTitle {
		t.Error("never received a state with the example.com title")
	}
}

// hasNonWhite reports whether the decoded PNG has any non-white pixel — proof
// the page actually painted content, not a blank canvas.
func hasNonWhite(rawPNG []byte) bool {
	img, err := png.Decode(bytes.NewReader(rawPNG))
	if err != nil {
		return false
	}
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
