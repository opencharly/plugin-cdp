package cdp

// cdp_endpoint_test.go — a MINIMAL Chrome DevTools endpoint stub (devtools HTTP
// + CDP WebSocket), so the session recorder's WIRE path — /json tab resolution,
// the WebSocket dial, Page.enable/startScreencast/stopScreencast round-trips and
// the Page.screencastFrame event stream — is unit-tested over a REAL socket, not
// just a synthetic event feeder (the vnc analogue: rfb_server_test.go's RFC 6143
// stub). The stub speaks just enough of the protocol to be a real DevTools
// endpoint to this plugin's client: /json advertises one page tab whose
// webSocketDebuggerUrl points back at the stub, and the WS handler answers every
// Page.* call and streams the configured JPEG frames as screencastFrame events.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// cdpEndpointStub is the minimal DevTools endpoint used by the recorder tests.
type cdpEndpointStub struct {
	ln           net.Listener
	url          string // http://127.0.0.1:port (the DevTools base URL the provider threads)
	frames       [][]byte
	started      chan struct{} // closed when Page.startScreencast arrives
	stopReceived chan struct{} // closed when Page.stopScreencast arrives
	// capturePNG, when set, makes Page.captureScreenshot return a real PNG (the
	// screenshot-landing tests). Nil keeps the generic empty result — the recorder
	// tests never call captureScreenshot.
	capturePNG []byte
}

func newCdpEndpointStub(t *testing.T, frames [][]byte) *cdpEndpointStub {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cdp stub listen: %v", err)
	}
	s := &cdpEndpointStub{
		ln:           ln,
		url:          "http://" + ln.Addr().String(),
		frames:       frames,
		started:      make(chan struct{}),
		stopReceived: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.Handle("/json", http.HandlerFunc(s.handleJSON))
	mux.Handle("/devtools/page/tab1", websocket.Server{Handler: s.handleWS})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return s
}

func (s *cdpEndpointStub) handleJSON(w http.ResponseWriter, _ *http.Request) {
	wsURL := "ws://" + s.ln.Addr().String() + "/devtools/page/tab1"
	tabs := []devToolsTab{{
		ID:                   "tab1",
		Title:                "fixture",
		URL:                  "about:blank",
		Type:                 "page",
		WebSocketDebuggerURL: wsURL,
	}}
	b, _ := json.Marshal(tabs)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *cdpEndpointStub) handleWS(ws *websocket.Conn) {
	defer ws.Close() //nolint:errcheck
	go s.streamFrames(ws)
	for {
		var msg cdpMessage
		if err := websocket.JSON.Receive(ws, &msg); err != nil {
			return
		}
		switch msg.Method {
		case "Page.startScreencast":
			closeChan(s.started)
		case "Page.stopScreencast":
			closeChan(s.stopReceived)
		case "Page.captureScreenshot":
			if s.capturePNG != nil {
				params, _ := json.Marshal(map[string]any{"data": base64.StdEncoding.EncodeToString(s.capturePNG)})
				if err := websocket.JSON.Send(ws, cdpMessage{ID: msg.ID, Result: json.RawMessage(params)}); err != nil {
					return
				}
				continue
			}
		}
		if err := websocket.JSON.Send(ws, cdpMessage{ID: msg.ID, Result: json.RawMessage("{}")}); err != nil {
			return
		}
	}
}

// streamFrames pushes each configured frame as a Page.screencastFrame event,
// base64 JPEG data + a monotonic sessionId, paced so a fast recorder keeps up.
func (s *cdpEndpointStub) streamFrames(ws *websocket.Conn) {
	for i, frame := range s.frames {
		data := base64.StdEncoding.EncodeToString(frame)
		params, _ := json.Marshal(map[string]any{
			"data":      data,
			"sessionId": i + 1,
			"metadata": map[string]any{
				"timestamp": float64(time.Now().UnixNano()) / 1e9,
			},
		})
		if err := websocket.JSON.Send(ws, cdpMessage{Method: "Page.screencastFrame", Params: params}); err != nil {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
}

// closeChan closes a channel idempotently (multiple goroutines may race).
func closeChan[T any](ch chan T) {
	defer func() { _ = recover() }()
	close(ch)
}

func (s *cdpEndpointStub) Close() {
	_ = s.ln.Close()
}

// testFrames builds n distinct tiny JPEG frames (real jpeg.Encode output — the
// recorder passes bytes through, never re-encodes).
func testFrames(t *testing.T, n int) [][]byte {
	t.Helper()
	frames := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		img := solidRGBA(16, 16, color.RGBA{R: uint8(30 + i*20), G: 90, B: 160, A: 255})
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 70}); err != nil {
			t.Fatalf("jpeg encode: %v", err)
		}
		frames = append(frames, b.Bytes())
	}
	return frames
}

// solidRGBA returns an RGBA image filled with one color.
func solidRGBA(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0 // zero-fill via loop (RGBA channels set below)
	}
	return fillRGBA(img, c)
}

func fillRGBA(img *image.RGBA, c color.RGBA) *image.RGBA {
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}
