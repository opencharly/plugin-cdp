package cdp

// catalog_invariant_test.go pins the invariant the jetkvm, cua, punktfunk, gh, omarchy,
// herdr, spice, desktop-kind, appium and vm siblings keep and this plugin needs for the same
// reason: every word the SCHEMA advertises must be reachable, and every word the plugin
// routes must be in the schema. Without it a word can sit in the catalog as a phantom
// (advertised but unimplemented): the schema validates the authored `cdp: …` step, and the
// step then fails only at dispatch with an unknown-method error, so the catalog and the
// skill that documents it mislead the agent that dispatches on it.
//
// THIS repo routes `method` in TWO places, and the invariant has to read both or it is wrong
// in the most dangerous direction:
//
//   - provider.go special-cases `session` BEFORE dispatch (the detached screencast recorder
//     holds the CDP WebSocket, so the provider never dials and dispatch is never reached);
//   - methods.go's `dispatch` carries a first switch (the four HTTP methods) and a second
//     switch after the per-tab WebSocket connects (every interactive + spa method).
//
// A test that read only the switches would report `session` as a phantom; one that read only
// the special case would report the other 21.
//
// It also pins the Go-side word set the census cannot see, `requiredModifiers`: its KEYS are
// method words and its VALUES are plugin-input field names. A typo in either half does not
// fail to compile — it silently disables the required-input check for the method it was meant
// to guard, or names a key no author can write.
//
// session_method.go's `switch in.Action` IS `#CdpInput.action`'s router, so the `_action`
// table and that switch must agree in both directions.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
)

var (
	dispatchSwitch = regexp.MustCompile(`(?s)func dispatch\(.*?\n\}`)
	runSessionFunc = regexp.MustCompile(`(?s)func runSession\(.*?\n\}`)
	caseArm        = regexp.MustCompile(`(?m)^[ \t]*case[ \t]+((?:"(?:[^"\\]|\\.)*"[ \t]*,?[ \t]*)+):`)
	quotedWord     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	// provider.go routes one method before dispatch, as a plain comparison.
	preDispatch = regexp.MustCompile(`method == "([a-z][a-z0-9-]*)"`)
	// requiredModifiers is a Go map literal of method -> required input keys.
	modifiersMap  = regexp.MustCompile(`(?s)var requiredModifiers = map\[string\]\[\]string\{(.*?)\n\}`)
	modifierEntry = regexp.MustCompile(`(?m)^\t"([a-z][a-z0-9-]*)":\s*\{(.*?)\},`)
)

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// schemaMethods returns the sorted method names the SERVED #CdpInput.method enum accepts. It
// reads the schema through the CUE API rather than its text: the enum is now §10.1 S3, and a
// text parse of the union would find ZERO methods and leave every invariant here passing
// vacuously.
func schemaMethods(t *testing.T) []string {
	t.Helper()
	return fieldWordList(t, servedSchema(t), "CdpInput", "method")
}

// schemaActions returns the sorted words the SERVED #CdpInput.action enum accepts.
func schemaActions(t *testing.T) []string {
	t.Helper()
	return fieldWordList(t, servedSchema(t), "CdpInput", "action")
}

// parsedRequiredModifiers returns the parsed map: method word -> required input field
// names. It is NOT named after the Go symbol it reads — the package already declares
// `requiredModifiers`, and a same-named helper would not compile (measured: gofmt passed and
// only `go vet` caught the redeclaration).
func parsedRequiredModifiers(t *testing.T) map[string][]string {
	t.Helper()
	block := modifiersMap.FindStringSubmatch(readSource(t, "methods.go"))
	if block == nil {
		t.Fatal("could not locate the requiredModifiers map in methods.go — if it moved, this test must move with it, not silently pass")
	}
	out := map[string][]string{}
	for _, m := range modifierEntry.FindAllStringSubmatch(block[1], -1) {
		var keys []string
		for _, q := range quotedWord.FindAllStringSubmatch(m[2], -1) {
			keys = append(keys, q[1])
		}
		out[m[1]] = keys
	}
	if len(out) == 0 {
		t.Fatal("requiredModifiers parsed to zero entries — this check would pass over nothing")
	}
	return out
}

// routedMethods returns every method word the plugin routes: both dispatch switches' arms
// plus provider.go's pre-dispatch comparison.
func routedMethods(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	fn := dispatchSwitch.FindString(readSource(t, "methods.go"))
	if fn == "" {
		t.Fatal("could not locate dispatch in methods.go")
	}
	for _, arm := range caseArm.FindAllStringSubmatch(fn, -1) {
		for _, q := range quotedWord.FindAllStringSubmatch(arm[1], -1) {
			seen[q[1]] = true
		}
	}
	prov := readSource(t, "provider.go")
	pre := preDispatch.FindAllStringSubmatch(prov, -1)
	if len(pre) == 0 {
		t.Fatal(`could not locate the pre-dispatch ` + "`method == \"…\"`" + ` comparison in provider.go — if it moved, this test must move with it, not silently pass`)
	}
	for _, m := range pre {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// TestCatalogHasNoPhantomMethods asserts every schema method is routed — no word is
// advertised that the plugin cannot act on.
func TestCatalogHasNoPhantomMethods(t *testing.T) {
	reachable := map[string]bool{}
	for _, m := range routedMethods(t) {
		reachable[m] = true
	}
	if len(reachable) == 0 {
		t.Fatal("the dispatch surface is empty — the invariant would pass over nothing")
	}
	for _, m := range schemaMethods(t) {
		if !reachable[m] {
			t.Errorf("schema method %q is a PHANTOM: neither provider.go's pre-dispatch comparison nor dispatch's two switches routes it — add an implementation or remove it from the table", m)
		}
	}
}

// TestDispatchHasNoPhantomArms is the other direction: an arm for a word the schema no longer
// accepts is dead code, and it hides a rename that only half landed.
func TestDispatchHasNoPhantomArms(t *testing.T) {
	advertised := map[string]bool{}
	for _, m := range schemaMethods(t) {
		advertised[m] = true
	}
	for _, m := range routedMethods(t) {
		if !advertised[m] {
			t.Errorf("the plugin routes %q, which the served #CdpInput.method enum does NOT accept — the arm is unreachable", m)
		}
	}
}

// TestEverySchemaMethodIsRouted is the two-way set equality in one assertion.
func TestEverySchemaMethodIsRouted(t *testing.T) {
	schema := schemaMethods(t)
	routed := routedMethods(t)
	if len(schema) != len(routed) {
		t.Fatalf("served enum has %d methods but the plugin routes %d — the catalog and the dispatch have drifted\n schema=%v\n routed=%v",
			len(schema), len(routed), schema, routed)
	}
	for i := range schema {
		if schema[i] != routed[i] {
			t.Errorf("served method %q has no matching routed arm (routed=%v)", schema[i], routed)
			break
		}
	}
}

// TestRequiredModifierKeysAreSchemaMethods pins the map's KEY half. A key that names no
// method is not a harmless typo: the check it carries never runs, and the method it was meant
// to guard dispatches without its required input.
func TestRequiredModifierKeysAreSchemaMethods(t *testing.T) {
	advertised := map[string]bool{}
	for _, m := range schemaMethods(t) {
		advertised[m] = true
	}
	keys := parsedRequiredModifiers(t)
	for k := range keys {
		if !advertised[k] {
			t.Errorf("requiredModifiers is keyed by %q, which the served #CdpInput.method enum does NOT accept — the required-input check for that method never runs", k)
		}
	}
}

// TestRequiredModifierValuesAreInputFields pins the map's VALUE half: each required name is an
// input field the provider reads, and a name no field carries can never be satisfied — the
// method would be permanently rejected with a message naming a key the author cannot write.
func TestRequiredModifierValuesAreInputFields(t *testing.T) {
	input := servedSchema(t).LookupPath(cue.ParsePath("#CdpInput"))
	if err := input.Err(); err != nil {
		t.Fatalf("#CdpInput does not resolve in the served schema: %v", err)
	}
	fields := map[string]bool{}
	it, err := input.Fields(cue.Optional(true))
	if err != nil {
		t.Fatalf("#CdpInput has no fields: %v", err)
	}
	for it.Next() {
		// An OPTIONAL field's selector string carries the constraint marker (`tab?`) —
		// `cue.Optional(true)` is what makes those fields visible at all, and the author
		// writes the bare name.
		fields[strings.TrimSuffix(it.Selector().String(), "?")] = true
	}
	// CALIBRATION: the reader must see fields the def certainly carries, so a formatting
	// regression fails HERE instead of reporting every required name as unknown.
	if !fields["method"] || !fields["tab"] {
		t.Fatalf("#CdpInput's field reader does not see its own `method`/`tab` fields (read %d) — the reader is broken, not the map", len(fields))
	}
	checked := 0
	for method, names := range parsedRequiredModifiers(t) {
		for _, name := range names {
			checked++
			if !fields[name] {
				t.Errorf("requiredModifiers[%q] demands the input field %q, which #CdpInput does not carry — the method can never satisfy the check", method, name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("requiredModifiers carries no values — this check would pass over nothing")
	}
}

// TestSessionActionsAreSchemaActions pins the `_action` table against its ROUTER:
// session_method.go's `switch in.Action`. A merged or mistyped set falls through to the
// method's own error at dispatch instead of failing the schema.
func TestSessionActionsAreSchemaActions(t *testing.T) {
	fn := runSessionFunc.FindString(readSource(t, "session_method.go"))
	if fn == "" {
		t.Fatal("could not locate runSession in session_method.go")
	}
	routed := map[string]bool{}
	for _, arm := range caseArm.FindAllStringSubmatch(fn, -1) {
		for _, q := range quotedWord.FindAllStringSubmatch(arm[1], -1) {
			routed[q[1]] = true
		}
	}
	if len(routed) == 0 {
		t.Fatal("runSession routes no action — this check would pass over nothing")
	}
	advertised := map[string]bool{}
	for _, a := range schemaActions(t) {
		advertised[a] = true
	}
	for a := range routed {
		if !advertised[a] {
			t.Errorf("runSession routes the action %q, which the served #CdpInput.action enum does NOT accept — the arm is unreachable", a)
		}
	}
	for a := range advertised {
		if !routed[a] {
			t.Errorf("#CdpInput.action accepts %q but runSession has no arm for it — the step validates and then fails at dispatch", a)
		}
	}
}
