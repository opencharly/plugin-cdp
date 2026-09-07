// Command serve is the OUT-OF-PROCESS entrypoint for the cdp verb plugin: a thin
// shim serving the importable provider over go-plugin gRPC via sdk.Serve. The SAME
// NewProvider()/NewMeta() compile INTO charly in-process when listed in
// compiled_plugins; this binary is host-built + connected only when they are NOT —
// placement is invisible above the registry.
//
// HIDDEN RECORDER MODE (plan Cutover E, E-3): with CHARLY_CDP_RECORDER=1 the SAME
// binary skips serving and becomes the DETACHED host-side session recorder — the
// runner's generic background-session service spawns it for a cdp: session start. It
// re-dials the host-resolved DevTools endpoint, starts the CDP screencast
// (Page.startScreencast), appends the frames into $CHARLY_CDP_STATE_DIR/frames.mjpeg,
// and on SIGTERM/SIGINT finalizes (FINAL marker + evidence row.json) before exiting 0:
// the runner's stop is complete only when row.json is on disk.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	cdp "github.com/opencharly/plugin-cdp/candy/plugin-cdp"
	"github.com/opencharly/sdk"
)

func main() {
	if os.Getenv(cdp.EnvRecorder) == "1" {
		os.Exit(recorderMain())
	}
	sdk.Serve(cdp.NewProvider(), cdp.NewMeta())
}

// recorderMain is the detached recorder process entrypoint (see the package doc). It
// returns the process exit code.
func recorderMain() int {
	endpointJSON := os.Getenv(cdp.EnvEndpoint)
	stateDir := os.Getenv(cdp.EnvStateDir)
	sessionID := os.Getenv(cdp.EnvSessionID)
	if endpointJSON == "" || stateDir == "" || sessionID == "" {
		fmt.Fprintf(os.Stderr, "charly-cdp recorder: missing env (endpoint=%q state_dir=%q session_id=%q)\n", endpointJSON, stateDir, sessionID)
		return 2
	}
	ep, err := cdp.ParseEndpoint([]byte(endpointJSON))
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly-cdp recorder: %v\n", err)
		return 2
	}
	fps, _ := strconv.Atoi(os.Getenv(cdp.EnvFps))
	cfg := cdp.RecorderConfig{
		Endpoint:  ep,
		Tab:       os.Getenv(cdp.EnvTab),
		Fps:       fps,
		StateDir:  stateDir,
		SessionID: sessionID,
		Venue:     os.Getenv(cdp.EnvVenue),
		Phase:     os.Getenv(cdp.EnvPhase),
	}

	done := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		close(done) // deterministic finalize: FINAL marker + row.json
	}()

	count, err := cdp.RunSessionRecorder(cfg, done)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly-cdp recorder: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "charly-cdp recorder: finalized session %s frames=%d state_dir=%s\n", sessionID, count, stateDir)
	return 0
}
