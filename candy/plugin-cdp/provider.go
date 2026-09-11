package cdp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/plugin-cdp/candy/plugin-cdp/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// provider.go is the out-of-process cdp verb provider — charly's host dispatches a
// `cdp:` check step to it through the registry (ResolveVerb("cdp") → this grpcProvider →
// Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv snapshot as
// env. Because the out-of-process path does NOT run a host-side matcher
// pipeline, this Invoke OWNS the whole verdict: read the host-pre-resolved DevTools URL
// (the host owns the podman / venue / port-mapping resolution), dispatch the method (the
// /json HTTP surface for status/open/list/close; the per-tab CDP WebSocket for the rest),
// then evaluate the stdout/stderr/exit_status matchers (the screenshot artifact
// validators run through sdk.LandArtifact inside the screenshot method itself — R3),
// and return the wire {status,message} the host decodes.

// cdpEndpoint is the dialable Chrome DevTools endpoint the plugin builds from the addr the
// generic host-endpoint reverse-leg (cc.ResolveEndpoint) returns for the in-venue CDP port
// 9222. URL is the host-reachable DevTools base URL the plugin dials.
type cdpEndpoint struct {
	URL string `json:"url"`
}

// cdpEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for a
// `cdp:` check step (provider_checkenv.go). Box/Mode mirror the shared CheckEnv; the endpoint
// is no longer pre-shipped here — the plugin resolves it itself via cc.ResolveEndpoint;
// Venue is the shared snapshot's venue id (session evidence-row provenance).
type cdpEnv struct {
	Box   string `json:"box"`
	Mode  string `json:"mode"` // "live" | "box"
	Venue string `json:"venue"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `cdp:` operation. It decodes the full #Op, the typed plugin
// input (params.CdpInput — the per-verb fields live in the desugared
// plugin_input since the schema-compaction cutover), and the env, skips in box
// mode (no live Chrome DevTools endpoint on a disposable `charly check box`),
// skips a nil endpoint, dispatches the method (the screenshot method lands its PNG
// through sdk.LandArtifact), and self-evaluates the matchers.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "cdp: decode op: "+err.Error())
		}
	}
	var in params.CdpInput
	kit.DecodeInput(op.PluginInput, &in)
	var env cdpEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := in.Method

	// Live-deployment verb: skip under `charly check box` (no running Chrome DevTools
	// endpoint on a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("cdp: %s requires a running deployment (skip under charly check box)", method))
	}
	// Resolve the dialable DevTools base URL via the GENERIC host-endpoint reverse-leg
	// (cc.ResolveEndpoint) — the host owns the venue/port-mapping/ssh -L machinery this
	// out-of-process plugin cannot reach. Replaces the former host-side cdp preresolver.
	cc, err := sdk.NewCheckContext(req.GetExecutorBrokerId(), req.GetEnvJson())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("cdp: %s: %v", method, err))
	}
	addr, err := cc.ResolveEndpoint(ctx, 9222)
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("cdp: %s: %v", method, err))
	}
	// No live venue (no-box context) → skip, the analogue of the host's empty-box skip.
	if addr == "" {
		return sdk.ResultJSON("skip", fmt.Sprintf("cdp: %s has no resolved DevTools endpoint (box=%q)", method, env.Box))
	}
	ep := &cdpEndpoint{URL: "http://" + addr}

	// session (Cutover E, E-3): the DETACHED recorder holds the CDP wire — the provider
	// never dials. The endpoint resolution above gates on the live deployment (mirroring
	// the record-session contract); start hands the spawn to the runner's generic
	// background-session service (verb:session) over the InvokeProvider reverse leg;
	// stop/status talk to that same service. No artifact is produced inside this Invoke
	// (the recorder writes frames.mjpeg detached), so artifactMethod stays false.
	if method == "session" {
		out, runErr := runSession(ctx, cc, ep, &in, env.Venue)
		return sdk.VerbVerdict("cdp", method, out, runErr, &op, false)
	}

	out, runErr := dispatch(ctx, ep, &op, &in)

	// The shared exit/stdout/stderr verdict pipeline (R3). The screenshot artifact
	// validators no longer ride VerbVerdict's artifact flag — runScreenshot lands the
	// PNG through sdk.LandArtifact (the ONE caller-side entry point, host leg), which
	// runs the validators itself; a mismatch surfaces as runErr → exit 1 here.
	return sdk.VerbVerdict("cdp", method, out, runErr, &op, false)
}
