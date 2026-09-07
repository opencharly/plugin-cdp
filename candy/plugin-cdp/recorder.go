package cdp

// recorder.go — the DETACHED host-side session recorder (plan Cutover E, E-3).
// cdp: session start hands THIS binary (in recorder mode, env
// CHARLY_CDP_RECORDER=1) to the runner's generic background-session service
// (plugin-check's compiled-in verb:session seam). The recorder owns the CDP
// wire for the whole session: it re-dials the host-pre-resolved DevTools
// endpoint (the plugin's existing browser venue — resolveTabWS resolves the
// session tab's WebSocket debugger URL), enables the Page domain, starts the
// screencast (Page.startScreencast), appends every Page.screencastFrame
// event's JPEG payload as-is into <state_dir>/frames.mjpeg (MJPEG by
// concatenation — the JPEG arrives base64 from the browser, no re-encode),
// paces the browser by acking at the session fps, and on SIGTERM/SIGINT
// finalizes: Page.stopScreencast + the deterministic FINAL marker + the
// evidence row.json ("instrument"/"origin"/"verb"/"artifact" — the shared
// #EvidenceRow shape, plan §4 A-task-1). While it runs, the PROVIDER spawns no
// process, knows no transport, and owns no pidfile.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Recorder-mode env contract between session_method.go (the spawn env) and
// cmd/serve's recorder mode (the reader). The provider builds these keys in
// buildSessionSpawn.
const (
	EnvRecorder  = "CHARLY_CDP_RECORDER"
	EnvEndpoint  = "CHARLY_CDP_ENDPOINT"
	EnvFps       = "CHARLY_CDP_FPS"
	EnvStateDir  = "CHARLY_CDP_STATE_DIR"
	EnvSessionID = "CHARLY_CDP_SESSION_ID"
	EnvVenue     = "CHARLY_CDP_VENUE"
	EnvPhase     = "CHARLY_CDP_PHASE"
	EnvTab       = "CHARLY_CDP_TAB"
)

// framesFile is the MJPEG artifact name inside the session state dir; finalMarker
// is the deterministic end-of-stream marker the stop path greps for.
const (
	framesFile   = "frames.mjpeg"
	finalMarker  = "FINAL"
	evidenceFile = "row.json"
)

// evidenceRow mirrors the shared #EvidenceRow shape (plan §4 A-task-1) — the
// minimal session subset the recorder writes and sessionStop reads back. No
// plugin-specific manifest code: the runner's evidence phase consumes the general
// shape.
type evidenceRow struct {
	Instrument string             `json:"instrument"`
	Origin     string             `json:"origin"`
	Verb       string             `json:"verb"`
	Venue      string             `json:"venue,omitempty"`
	Phase      string             `json:"phase,omitempty"`
	Artifact   []evidenceArtifact `json:"artifact,omitempty"`
}

type evidenceArtifact struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// ParseEndpoint decodes the endpoint JSON the provider threads via
// CHARLY_CDP_ENDPOINT (the cdpEndpoint wire shape).
func ParseEndpoint(raw []byte) (*cdpEndpoint, error) {
	var ep cdpEndpoint
	if err := json.Unmarshal(raw, &ep); err != nil {
		return nil, fmt.Errorf("decode endpoint JSON: %w", err)
	}
	return &ep, nil
}

// RecorderConfig is the detached-session recorder's full runtime contract.
type RecorderConfig struct {
	Endpoint  *cdpEndpoint
	Tab       string // the captured tab: a 1-based page index or a DevTools UUID ("" = "1")
	Fps       int    // ack-paced screencast frame rate (default 5)
	StateDir  string // the run's state dir: frames.mjpeg + FINAL + row.json land here
	SessionID string // the venue-scoped session id — stamped into the evidence row
	Venue     string // evidence-row provenance
	Phase     string // evidence-row provenance (build|live|update|teardown)
}

// RunSessionRecorder is the detached-mode engine (cmd/serve, recorder mode):
// resolves the tab's WebSocket URL off the host-resolved DevTools endpoint,
// starts the CDP screencast, appends the incoming JPEG frames into
// frames.mjpeg until done closes, stops the screencast, and finalizes the FINAL
// marker + row.json. Returns the captured frame count.
func RunSessionRecorder(cfg RecorderConfig, done <-chan struct{}) (int, error) {
	if cfg.Endpoint == nil || cfg.Endpoint.URL == "" {
		return 0, fmt.Errorf("recorder: nil endpoint")
	}
	if cfg.StateDir == "" {
		return 0, fmt.Errorf("recorder: empty state dir")
	}
	tab := cfg.Tab
	if tab == "" {
		tab = "1"
	}
	wsURL, err := resolveTabWS(cfg.Endpoint.URL, tab)
	if err != nil {
		return 0, fmt.Errorf("recorder: resolve tab %q: %w", tab, err)
	}
	client, err := NewCDPClient(context.Background(), wsURL)
	if err != nil {
		return 0, fmt.Errorf("recorder: dial %s: %w", wsURL, err)
	}
	defer client.Close()
	if _, err := client.Call("Page.enable", map[string]any{}); err != nil {
		return 0, fmt.Errorf("recorder: Page.enable: %w", err)
	}
	if _, err := client.Call("Page.startScreencast", screencastParams()); err != nil {
		return 0, fmt.Errorf("recorder: Page.startScreencast: %w", err)
	}
	count := writeScreencastFrames(cfg, client, done)
	// Best-effort stop — the stream may already be dead.
	_, _ = client.Call("Page.stopScreencast", map[string]any{})
	if err := finalizeSession(cfg, count); err != nil {
		return 0, err
	}
	return count, nil
}

// screencastParams are the Page.startScreencast arguments: JPEG frames at the
// tab's viewport resolution (the browser scales to the caps). The client paces
// the deliver rate by acking at the session fps.
func screencastParams() map[string]any {
	return map[string]any{
		"format":        "jpeg",
		"quality":       80,
		"everyNthFrame": 1,
		"maxWidth":      1920,
		"maxHeight":     1080,
	}
}

// captureInterval maps a fps int to the ack-pacing interval (default 5 fps;
// 100 fps cap — identical semantics to the vnc recorder's poll interval).
func captureInterval(fps int) time.Duration {
	if fps <= 0 {
		fps = 5
	}
	d := time.Second / time.Duration(fps)
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}

// writeScreencastFrames consumes the client's event stream: every
// Page.screencastFrame event's JPEG payload is base64-decoded and appended to
// <state_dir>/frames.mjpeg; the browser is paced by acking each frame at the
// session fps (a fire-and-forget Page.screencastFrameAck — never a blocked
// call). Returns when done closes (the SIGTERM trap) or the wire dies (events
// channel closed). Returns the frame count.
func writeScreencastFrames(cfg RecorderConfig, client *CDPClient, done <-chan struct{}) int {
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return 0
	}
	out, err := os.Create(filepath.Join(cfg.StateDir, framesFile))
	if err != nil {
		return 0
	}
	defer out.Close() //nolint:errcheck

	minAckInterval := captureInterval(cfg.Fps)
	nextAck := time.Time{}
	count := 0
	for {
		select {
		case <-done:
			return count
		case ev, ok := <-client.Events():
			if !ok {
				return count // wire died — finalize with what we got
			}
			if ev.Method != "Page.screencastFrame" {
				continue
			}
			var sf struct {
				SessionID int    `json:"sessionId"`
				Data      string `json:"data"`
			}
			if err := json.Unmarshal(ev.Params, &sf); err != nil || sf.Data == "" {
				continue
			}
			jpg, err := base64.StdEncoding.DecodeString(sf.Data)
			if err != nil || len(jpg) == 0 {
				continue
			}
			if _, err := out.Write(jpg); err != nil {
				return count
			}
			count++
			if time.Since(nextAck) >= minAckInterval {
				nextAck = time.Now()
				_ = client.Send("Page.screencastFrameAck", map[string]any{"sessionId": sf.SessionID})
			}
		}
	}
}

// finalizeSession writes the deterministic end-of-stream marker + the evidence row
// into the state dir. Called once, on the SIGTERM path — the runner's stop is
// complete only when row.json is on disk.
func finalizeSession(cfg RecorderConfig, count int) error {
	marker := fmt.Sprintf("final frames=%d\n", count)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, finalMarker), []byte(marker), 0o644); err != nil {
		return fmt.Errorf("recorder: write %s: %w", finalMarker, err)
	}
	row := evidenceRow{
		Instrument: cfg.SessionID,
		Origin:     "session",
		Verb:       "cdp",
		Venue:      cfg.Venue,
		Phase:      cfg.Phase,
		Artifact: []evidenceArtifact{{
			Path: filepath.Join(cfg.StateDir, framesFile),
			Kind: "mjpeg",
		}},
	}
	b, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		return fmt.Errorf("recorder: marshal evidence row: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(cfg.StateDir, evidenceFile), b, 0o644); err != nil {
		return fmt.Errorf("recorder: write %s: %w", evidenceFile, err)
	}
	return nil
}
