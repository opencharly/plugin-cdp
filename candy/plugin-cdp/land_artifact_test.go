package cdp

// land_artifact_test.go — the G-9 screenshot-landing contract over a REAL devtools
// endpoint stub and a REAL on-disk artifact: dispatch(method=screenshot) writes the
// PNG to the host path and runs the declared artifact-reality validators through the
// ONE sdk.LandArtifact call. The failing-validator sub-test FAILS if the LandArtifact
// validation is removed (the write alone would succeed) — the B12 regression net.

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/plugin-cdp/candy/plugin-cdp/params"
	"github.com/opencharly/spec/spec"
)

// tinyPNG encodes a real 1x1 PNG (image.DecodeConfig-able, so the dimension
// validator reads the PNG header rather than erroring on a non-image).
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 200, G: 30, B: 60, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return b.Bytes()
}

// screenshotOp builds the desugared op for a screenshot step: the modifier map
// needs tab + artifact, and the artifact validators read artifact_min_bytes /
// artifact_min_dimensions from the same plugin input.
func screenshotOp(artifact, dims string, minBytes int) *spec.Op {
	return &spec.Op{
		PluginInput: map[string]any{
			"tab":                     "tab1",
			"artifact":                artifact,
			"artifact_min_dimensions": dims,
			"artifact_min_bytes":      minBytes,
		},
	}
}

func TestScreenshotLandsArtifactAndValidates(t *testing.T) {
	stub := newCdpEndpointStub(t, nil)
	stub.capturePNG = tinyPNG(t)
	ep := &cdpEndpoint{URL: stub.url}

	t.Run("validators pass against the landed PNG", func(t *testing.T) {
		artifact := filepath.Join(t.TempDir(), "shot.png")
		in := &params.CdpInput{Method: "screenshot", Tab: "tab1", Artifact: artifact}
		out, err := dispatch(context.Background(), ep, screenshotOp(artifact, "1x1", 1), in)
		if err != nil {
			t.Fatalf("dispatch(screenshot) error = %v", err)
		}
		if !strings.Contains(out, "Screenshot saved") {
			t.Errorf("out = %q, want it to report the saved screenshot", out)
		}
		b, err := os.ReadFile(artifact)
		if err != nil {
			t.Fatalf("artifact %q was not written: %v", artifact, err)
		}
		if len(b) == 0 {
			t.Fatal("artifact is empty")
		}
	})

	t.Run("a failing validator fails the screenshot verb", func(t *testing.T) {
		artifact := filepath.Join(t.TempDir(), "bad.png")
		in := &params.CdpInput{Method: "screenshot", Tab: "tab1", Artifact: artifact}
		// The stub returns a real 1x1 PNG; 64x64 is unattainable, so the
		// artifact_min_dimensions validator inside sdk.LandArtifact must fail the
		// verb. Without the LandArtifact call the write alone would succeed and
		// this test fails — the regression net.
		_, err := dispatch(context.Background(), ep, screenshotOp(artifact, "64x64", 1), in)
		if err == nil {
			t.Fatal("screenshot succeeded although artifact_min_dimensions requires 64x64 on a 1x1 PNG — the sdk.LandArtifact validation did not run")
		}
		if !strings.Contains(err.Error(), "required min 64x64") {
			t.Errorf("error = %q, want it to name the failing artifact validator (dimensions 1x1 < required 64x64)", err)
		}
	})
}
