// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// The wire protocol is line-oriented JSON in both directions (one JSON object
// per WebSocket text message). Frames are PNG bytes base64-encoded into the
// same JSON object, so a client needs only a single JSON parse per message and
// the codec is fully testable without a socket. See docs/protocol.md.

// Client→server message kinds.
const (
	KindNavigate = "navigate" // {kind:navigate, url}
	KindClick    = "click"    // {kind:click, x, y}    content-area pixel coords
	KindScroll   = "scroll"   // {kind:scroll, dy}     wheel delta in pixels
	KindKey      = "key"      // {kind:key, key}       a key name (e.g. "Enter")
	KindResize   = "resize"   // {kind:resize, w, h}   content-area size in pixels
	KindBack     = "back"     // {kind:back}
	KindForward  = "forward"  // {kind:forward}
)

// Server→client message kinds.
const (
	KindFrame = "frame" // {kind:frame, frame(base64 PNG), w, h, offsetY}
	KindState = "state" // {kind:state, url, title, loading, canBack, canForward}
	KindError = "error" // {kind:error, message}
)

// ClientMsg is any message from the client. Only the fields relevant to Kind
// are populated; the rest stay zero.
type ClientMsg struct {
	Kind string `json:"kind"`
	URL  string `json:"url,omitempty"`
	X    int    `json:"x,omitempty"`
	Y    int    `json:"y,omitempty"`
	DY   int    `json:"dy,omitempty"`
	Key  string `json:"key,omitempty"`
	W    int    `json:"w,omitempty"`
	H    int    `json:"h,omitempty"`
}

// Frame is a rendered viewport slice: a PNG whose pixels are W×H, taken from
// the full page at vertical scroll OffsetY.
type Frame struct {
	Kind    string `json:"kind"`    // always KindFrame
	Data    string `json:"frame"`   // base64-encoded PNG bytes
	W       int    `json:"w"`       // slice width (px)
	H       int    `json:"h"`       // slice height (px)
	OffsetY int    `json:"offsetY"` // scroll offset of the slice within the full page
}

// State is the browser chrome model: what the address bar and the
// back/forward buttons should show.
type State struct {
	Kind       string `json:"kind"` // always KindState
	URL        string `json:"url"`
	Title      string `json:"title"`
	Loading    bool   `json:"loading"`
	CanBack    bool   `json:"canBack"`
	CanForward bool   `json:"canForward"`
}

// ErrorMsg reports a navigation or protocol failure the client should surface
// (e.g. an SSRF-blocked URL or an unreachable host).
type ErrorMsg struct {
	Kind    string `json:"kind"` // always KindError
	Message string `json:"message"`
}

// NewFrame builds a Frame from raw PNG bytes, base64-encoding them.
func NewFrame(png []byte, w, h, offsetY int) Frame {
	return Frame{
		Kind:    KindFrame,
		Data:    base64.StdEncoding.EncodeToString(png),
		W:       w,
		H:       h,
		OffsetY: offsetY,
	}
}

// PNG decodes a Frame's base64 payload back to raw PNG bytes.
func (f Frame) PNG() ([]byte, error) {
	return base64.StdEncoding.DecodeString(f.Data)
}

// DecodeClient parses one client→server message. It rejects an empty kind so a
// malformed message is a clear error rather than a silent no-op.
func DecodeClient(data []byte) (ClientMsg, error) {
	var m ClientMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return ClientMsg{}, fmt.Errorf("browserproxy: bad client message: %w", err)
	}
	if m.Kind == "" {
		return ClientMsg{}, fmt.Errorf("browserproxy: client message has no kind")
	}
	return m, nil
}

// Encode marshals any server→client message (Frame, State or ErrorMsg) to JSON.
func Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}
