# AGENTS.md — plugin-cdp

Standalone out-of-tree plugin repo serving the `cdp` Chrome DevTools Protocol
check verb (`verb:cdp`). The plugin is a Go module at `candy/plugin-cdp/`
(module path `github.com/opencharly/plugin-cdp/candy/plugin-cdp`); the root
`charly.yml` only declares `discover: candy` so the repo is a project and its
candy is scanned.

Canonical files:

- `candy/plugin-cdp/charly.yml` — the `plugin-cdp:` candy entity
  (`plugin:` block, `plan:` checks).
- `candy/plugin-cdp/plugin.go` / `provider.go` — the verb provider and
  `NewProvider()`/`NewMeta()`.
- `candy/plugin-cdp/methods.go` / `browser_cdp.go` / `cdp_spa.go` /
  `session_method.go` / `recorder.go` — the verb's method implementations.
- `candy/plugin-cdp/schema/cdp.cue` — the typed `#CdpInput` (single source for
  `params/cue_types_gen.go`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.
- `README.md` — user overview only; never agent guidance.

## Load these skills first (R0)

- `/charly-check:cdp` — the `cdp:` verb reference. Load before changing the verb
  surface.
- `/charly-check:check` — the declarative check-step surface the verb is
  authored through.
- `/charly-internals:plugin` — the plugin authoring reference: the `plugin:`
  block, the `verb` provider class, the per-plugin CUE-schema contract.
- `/charly-internals:git-workflow` — before any git/PR action.

## Build / validate / test

- `go build ./...` in `candy/plugin-cdp/` — compile the plugin module.
- `go test ./...` in `candy/plugin-cdp/` — the plugin's Go tests (the CDP
  timeout/endpoint/session seams).
- `charly box validate` at the repo root — the structural check (the candy +
  `plugin:` block, CUE schema).
- The merge gate is the **org-wide** `charly/pr-validator` (required check
  `validate / validate`, defined in `opencharly/.github`); this repo has **no**
  per-repo candy gate.
- The R10 consumer is a Chrome-bearing pod bed whose check composes this plugin
  (e.g. `sway-browser-vnc`).

## Modify this repo

- Edit the `plugin-cdp:` candy entity, the Go source, and `schema/cdp.cue`
  **together** — the schema is the single source for the verb's `params/` struct;
  regenerate `params/cue_types_gen.go` from it.
- The CDP endpoint is resolved by the generic reverse-leg (the host owns the
  port-mapping machinery); keep the plugin free of container inspection.

## Landing

- PR-only. Every change lands through a pull request; the org-required
  `charly/pr-validator` validates the diff and body and arms native auto-merge on
  PASS. Direct pushes to `main` are blocked.
- History lives in `CHANGELOG/` (written by `tag-on-merge` at merge time); the PR
  body IS the changelog.
- The authoritative rulebook is the umbrella `AGENTS.md` in
  `opencharly/opencharly` and `charly/AGENTS.md` in the charly repo. Do not
  restate its rules here.
