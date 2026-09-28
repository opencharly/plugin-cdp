# plugin-cdp

The `cdp` Chrome DevTools Protocol check verb for OpenCharly — served
out-of-process by this plugin (`verb:cdp`).

The plugin speaks CDP over the DevTools HTTP (`/json`) and per-tab WebSocket
surface. It resolves the deployment's CDP port `9222` to a host-reachable
DevTools base URL via the generic reverse-leg (the host owns the podman / venue
/ port-mapping machinery), so the CDP client lives here, out of charly's core
check surface.

## What it provides

| Capability | Surface |
|---|---|
| `verb:cdp` | the declarative `cdp:` check step any candy or box can bake into its plan |

## The verb

An authored `cdp: <method>` step (scalar sugar) or `cdp: {method: …, tab: …}`
(map form) desugars to the plugin input envelope; every cdp-exclusive modifier
lives in the plugin's own `#CdpInput` (`schema/cdp.cue`).

- **query/action** — `status`, `list`, `url`, `text`, `html`, `eval`, `axtree`,
  `coords`, `raw`, `wait`, `screenshot`, `open`, `close`, `click`, `type`.
- **SPA remote-desktop input** — `spa-status`, `spa-click`, `spa-mouse`,
  `spa-type`, `spa-key`, `spa-key-combo`.
- **session** — the host-side detached CDP screencast recorder
  (`start`/`stop`/`status`): `Page.startScreencast` frames into an MJPEG stream
  through the runner's generic background-session service.

## How to use it

Compose the plugin candy in a box or check bed's `candy:` list:

```yaml
- '@github.com/opencharly/plugin-cdp/candy/plugin-cdp:<tag>'
```

Then author the verb in a plan:

```yaml
- check: the Chrome DevTools Protocol endpoint answers
  cdp: status
  stdout:
    - contains: ok
```

## Layout

- `candy/plugin-cdp/` — the plugin module: `plugin.go`, `provider.go`,
  `methods.go`, `browser_cdp.go`, `cdp_spa.go`, `session_method.go`,
  `recorder.go`, `params/cue_types_gen.go`, `schema/cdp.cue`,
  `cmd/serve/main.go`.
- `charly.yml` — the root project manifest (`discover: candy`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.

## Related

- Owning skill: `/charly-check:cdp` — the `cdp:` verb reference.
- `/charly-check:check` — the declarative check-step surface the verb is
  authored through.
- [`opencharly/charly`](https://github.com/opencharly/charly) — the charly CLI.
