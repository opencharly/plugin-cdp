package cdp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/plugin-cdp/candy/plugin-cdp/params"
)

// session_test.go covers the cdp: session method (plan Cutover E, E-3): the
// recorder's wire path against a REAL DevTools endpoint stub (tab resolution,
// Page.startScreencast, the screencastFrame event stream → frames.mjpeg,
// finalize → FINAL + row.json), the spawn request the provider hands to the
// runner's generic background-session service, and the runSession validation
// gates. The reverse-leg submission itself (InvokeProvider ClassVerb session)
// is exercised by the R10 bed — the venue-driving path.

// TestRunSessionRecorderAgainstFakeEndpoint is the screencast wire path over a
// REAL socket: the recorder resolves the stub tab, starts the screencast,
// appends every event's JPEG payload as-is into <state_dir>/frames.mjpeg, and
// on the done close (the SIGTERM analog) stops the screencast and finalizes
// with the FINAL marker + evidence row.json.
func TestRunSessionRecorderAgainstFakeEndpoint(t *testing.T) {
	frames := testFrames(t, 5)
	srv := newCdpEndpointStub(t, frames)
	defer srv.Close()
	stateDir := t.TempDir()
	done := make(chan struct{})
	cfg := RecorderConfig{
		Endpoint:  &cdpEndpoint{URL: srv.url},
		Fps:       100,
		StateDir:  stateDir,
		SessionID: "bed.recorder.wire",
		Venue:     "check-chrome-headless",
		Phase:     "live",
	}
	type res struct {
		count int
		err   error
	}
	rc := make(chan res, 1)
	go func() {
		c, err := RunSessionRecorder(cfg, done)
		rc <- res{c, err}
	}()
	// Let the frames flow, then close done (the SIGTERM analog).
	time.Sleep(120 * time.Millisecond)
	close(done)
	got := <-rc
	if got.err != nil {
		t.Fatalf("RunSessionRecorder: %v", got.err)
	}
	if got.count != len(frames) {
		t.Fatalf("captured %d frames, want %d", got.count, len(frames))
	}

	mjpeg, err := os.ReadFile(filepath.Join(stateDir, framesFile))
	if err != nil {
		t.Fatalf("frames.mjpeg: %v", err)
	}
	if parts := splitMJpeg(mjpeg); len(parts) != len(frames) {
		t.Fatalf("splitMJpeg frames = %d, want %d", len(parts), len(frames))
	}
	// Passthrough: each frame's bytes land byte-identical (JPEG arrives base64
	// from the browser — the recorder never re-encodes).
	if !bytes.Equal(mjpeg, bytes.Join(frames, nil)) {
		t.Error("frames.mjpeg is not the byte-identical JPEG concatenation")
	}

	marker, err := os.ReadFile(filepath.Join(stateDir, finalMarker))
	if err != nil {
		t.Fatalf("FINAL marker missing after stop: %v", err)
	}
	if want := "final frames=" + itoa(len(frames)); string(marker) != want+"\n" {
		t.Errorf("FINAL content = %q, want %q", marker, want)
	}

	raw, err := os.ReadFile(filepath.Join(stateDir, evidenceFile))
	if err != nil {
		t.Fatalf("row.json missing after stop: %v", err)
	}
	var row evidenceRow
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatalf("decode row.json: %v", err)
	}
	tw := evidenceRow{
		Instrument: "bed.recorder.wire",
		Origin:     "session",
		Verb:       "cdp",
		Venue:      "check-chrome-headless",
		Phase:      "live",
		Artifact:   []evidenceArtifact{{Path: filepath.Join(stateDir, framesFile), Kind: "mjpeg"}},
	}
	if row.Instrument != tw.Instrument || row.Origin != tw.Origin || row.Verb != tw.Verb ||
		row.Venue != tw.Venue || row.Phase != tw.Phase || len(row.Artifact) != 1 ||
		row.Artifact[0].Path != tw.Artifact[0].Path || row.Artifact[0].Kind != tw.Artifact[0].Kind {
		t.Errorf("row.json = %+v, want %+v", row, tw)
	}

	select {
	case <-srv.stopReceived:
	default:
		t.Error("stub never received Page.stopScreencast")
	}
}

// TestRunSessionRecorderValidation guards the recorder's honest failures: a nil
// endpoint and an empty state dir error before anything is created.
func TestRunSessionRecorderValidation(t *testing.T) {
	if _, err := RunSessionRecorder(RecorderConfig{}, make(chan struct{})); err == nil {
		t.Fatal("RunSessionRecorder with nil endpoint: want error")
	}
	if _, err := RunSessionRecorder(RecorderConfig{Endpoint: &cdpEndpoint{URL: "http://127.0.0.1:1"}}, make(chan struct{})); err == nil {
		t.Fatal("RunSessionRecorder with empty state dir: want error")
	}
}

// TestCaptureInterval pins the fps → ack-pacing interval mapping (default 5 fps;
// 100 fps cap — same semantics as the vnc recorder).
func TestCaptureInterval(t *testing.T) {
	if d := captureInterval(0); d.String() != (200 * time.Millisecond).String() {
		t.Errorf("captureInterval(0) = %s, want 200ms (5 fps default)", d)
	}
	if d := captureInterval(5); d.String() != (200 * time.Millisecond).String() {
		t.Errorf("captureInterval(5) = %s, want 200ms", d)
	}
	if d := captureInterval(100); d.String() != (10 * time.Millisecond).String() {
		t.Errorf("captureInterval(100) = %s, want 10ms", d)
	}
	if d := captureInterval(500); d.String() != (10 * time.Millisecond).String() {
		t.Errorf("captureInterval(500) = %s, want 10ms (100 fps cap)", d)
	}
}

// TestBuildSessionSpawn asserts the exact spawn request the provider submits to the
// runner's generic session service: this plugin's binary in recorder mode + the
// endpoint/identity/tab env, with the venue default from the CheckEnv snapshot applied.
func TestBuildSessionSpawn(t *testing.T) {
	ep := &cdpEndpoint{URL: "http://127.0.0.1:9222"}
	in := &params.CdpInput{SessionId: "bed.member.cap", StateDir: "/var/run/checks/bed/x", Fps: 5, Tab: "2", Phase: "live"}
	req := buildSessionSpawn(in, ep, "/usr/lib/charly/plugin-cdp", "check-some-pod", "")
	if req.Op != "spawn" {
		t.Errorf("op = %q, want spawn", req.Op)
	}
	if req.SessionID != "bed.member.cap" {
		t.Errorf("session_id = %q", req.SessionID)
	}
	if len(req.Command) != 2 || req.Command[0] != "/usr/lib/charly/plugin-cdp" || req.Command[1] != "__dummy-arg" {
		t.Errorf("command = %v, want [<self> __dummy-arg]", req.Command)
	}
	if req.Env[EnvRecorder] != "1" {
		t.Errorf("CHARLY_CDP_RECORDER = %q, want 1", req.Env[EnvRecorder])
	}
	if req.Env[EnvFps] != "5" {
		t.Errorf("CHARLY_CDP_FPS = %q, want 5", req.Env[EnvFps])
	}
	if req.Env[EnvStateDir] != "/var/run/checks/bed/x" {
		t.Errorf("CHARLY_CDP_STATE_DIR = %q", req.Env[EnvStateDir])
	}
	if req.Env[EnvSessionID] != "bed.member.cap" {
		t.Errorf("CHARLY_CDP_SESSION_ID = %q", req.Env[EnvSessionID])
	}
	if req.Env[EnvTab] != "2" {
		t.Errorf("CHARLY_CDP_TAB = %q, want 2 (authored tab threaded)", req.Env[EnvTab])
	}
	if req.Env[EnvVenue] != "check-some-pod" {
		t.Errorf("CHARLY_CDP_VENUE = %q, want check-some-pod (CheckEnv default)", req.Env[EnvVenue])
	}
	if req.Env[EnvPhase] != "live" {
		t.Errorf("CHARLY_CDP_PHASE = %q, want live", req.Env[EnvPhase])
	}
	// the endpoint rides the env as the cdpEndpoint JSON (the DevTools base URL).
	var gotEP cdpEndpoint
	if err := json.Unmarshal([]byte(req.Env[EnvEndpoint]), &gotEP); err != nil {
		t.Fatalf("CHARLY_CDP_ENDPOINT not JSON: %v", err)
	}
	if gotEP.URL != "http://127.0.0.1:9222" {
		t.Errorf("endpoint JSON = %+v", gotEP)
	}
	// fps defaulting: zero fps in the input spawns the 5 fps default.
	def := buildSessionSpawn(&params.CdpInput{SessionId: "s", StateDir: "/x"}, ep, "/e", "", "")
	if def.Env[EnvFps] != "5" {
		t.Errorf("default fps = %q, want 5", def.Env[EnvFps])
	}
}

// TestDefaultTabSessionSpawn covers the default-tab threading: an authored session
// without tab spawns the recorder with no CHARLY_CDP_TAB (the recorder's "1" default).
func TestDefaultTabSessionSpawn(t *testing.T) {
	req := buildSessionSpawn(&params.CdpInput{SessionId: "s", StateDir: "/x"}, &cdpEndpoint{URL: "http://h:9222"}, "/e", "", "")
	if _, ok := req.Env[EnvTab]; ok && req.Env[EnvTab] != "" {
		t.Errorf("CHARLY_CDP_TAB = %q, want unset (recorder defaults to tab 1)", req.Env[EnvTab])
	}
}

// TestRunSessionValidation guards the required-modifier semantics of the session
// method WITHOUT the reverse leg: each gate fails before any submission.
func TestRunSessionValidation(t *testing.T) {
	ctx := t.Context()
	// session_id/state_dir have provider-side fallbacks (plan-step sessions); the
	// gate that remains is action validation: no action fails up front, and a
	// start without a state dir (after the fallback) hits the explicit gate
	// BEFORE any submission to the runner seam.
	if _, err := runSession(ctx, nil, nil, &params.CdpInput{SessionId: "s", StateDir: "/x"}, ""); err == nil {
		t.Fatal("runSession without action: want error")
	}
	// sessionStart's own gate fires BEFORE any submission: a start with an empty
	// state dir (the runSession fallback is bypassed — call the start path directly)
	// errors out without touching the nil CheckContext.
	if _, err := sessionStart(ctx, nil, nil, &params.CdpInput{Action: "start", SessionId: "s"}, ""); err == nil {
		t.Fatal("session start without state_dir: want the state_dir gate")
	}
}

// splitMJpeg splits an MJPEG stream into its JPEG frames on the SOI/EOI markers.
func splitMJpeg(b []byte) [][]byte {
	var out [][]byte
	for {
		i := bytes.Index(b, []byte{0xFF, 0xD8})
		if i < 0 {
			break
		}
		j := bytes.Index(b[i+2:], []byte{0xFF, 0xD9})
		if j < 0 {
			break
		}
		end := i + 2 + j + 2
		out = append(out, b[i:end])
		b = b[end:]
	}
	return out
}

// itoa is a tiny int formatter (avoids a strconv import in test assertions).
func itoa(n int) string { return fmt.Sprintf("%d", n) }

// TestWaitForEvidenceRow guards the stop path's bounded row wait: the row appears
// when the recorder's SIGTERM trap finalizes, and a missing row at the deadline
// fails the stop fast.
func TestWaitForEvidenceRow(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	rowPath := filepath.Join(stateDir, evidenceFile)

	// Appears after a short delay (the recorder's finalize lag).
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = os.WriteFile(rowPath, []byte("{}"), 0o644)
	}()
	if _, err := waitForEvidenceRow(ctx, rowPath, 5*time.Second); err != nil {
		t.Fatalf("waitForEvidenceRow: %v", err)
	}

	// Never appears → deadline error.
	if _, err := waitForEvidenceRow(ctx, filepath.Join(stateDir, "missing.json"), 250*time.Millisecond); err == nil {
		t.Fatal("waitForEvidenceRow on never-written row: want error")
	} else if !strings.Contains(err.Error(), "never finalized") {
		t.Fatalf("error = %v, want the never-finalized message", err)
	}
}
