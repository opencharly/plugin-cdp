package cdp

// evaluate_params_test.go — opencharly/plugin-cdp#11. A page's async result must reach the
// check: the expression `(async () => { … await … return '…' })()` answered `{}` because
// Runtime.evaluate was not asked to await the Promise it returns.

import "testing"

func TestEvaluateParamsAwaitThePromise(t *testing.T) {
	const expr = "(async () => { await new Promise(r => setTimeout(r, 10)); return 'FRAMES-RISING 1->2'; })()"
	p := evaluateParams(expr)
	if p["expression"] != expr {
		t.Errorf("expression = %v, want %q", p["expression"], expr)
	}
	if p["returnByValue"] != true {
		t.Error("returnByValue must be set, or a value type is returned instead of the value")
	}
	if p["awaitPromise"] != true {
		t.Fatal("awaitPromise must be set, or an async expression answers with the Promise object — rendered `{}`, indistinguishable from an empty answer (#11)")
	}
}
