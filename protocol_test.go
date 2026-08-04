// Copyright (c) the go-webengine/browserproxy authors.
// SPDX-License-Identifier: BSD-3-Clause

package browserproxy

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	f := NewFrame(png, 640, 480, 120)
	if f.Kind != KindFrame || f.W != 640 || f.H != 480 || f.OffsetY != 120 {
		t.Fatalf("frame header wrong: %+v", f)
	}
	got, err := f.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, png) {
		t.Errorf("PNG round-trip = %v, want %v", got, png)
	}
	// It must marshal to JSON with a base64 "frame" field.
	b, err := Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"kind":"frame"`)) || !bytes.Contains(b, []byte(`"frame":`)) {
		t.Errorf("frame JSON missing fields: %s", b)
	}
}

func TestFramePNG_BadBase64(t *testing.T) {
	if _, err := (Frame{Data: "not+valid+base64!!!"}).PNG(); err == nil {
		t.Error("want error decoding bad base64")
	}
}

func TestDecodeClient(t *testing.T) {
	m, err := DecodeClient([]byte(`{"kind":"click","x":12,"y":34}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindClick || m.X != 12 || m.Y != 34 {
		t.Errorf("decoded = %+v", m)
	}
	if _, err := DecodeClient([]byte(`{`)); err == nil {
		t.Error("want error on malformed JSON")
	}
	if _, err := DecodeClient([]byte(`{"x":1}`)); err == nil {
		t.Error("want error on missing kind")
	}
}

func TestEncodeState(t *testing.T) {
	b, err := Encode(State{Kind: KindState, URL: "http://x/", Title: "T", CanBack: true})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back["kind"] != "state" || back["url"] != "http://x/" || back["canBack"] != true {
		t.Errorf("state JSON = %v", back)
	}
}
