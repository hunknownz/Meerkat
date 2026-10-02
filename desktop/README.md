# Codex desktop adapter (experimental, NON-OFFICIAL)

`desktop/injector.mjs` attaches over the Chrome DevTools Protocol (CDP) to a Codex desktop window you
started yourself with remote debugging, and shows a read-only "Meerkat" sidebar entry. It is **not** a
Codex plugin or extension API (Codex currently has no sidebar plugin API); it depends on renderer
selectors that any Codex update may rename. Live use inside the native Codex app has **not** been
verified yet (CDP access was denied earlier); installation will be user-assisted. Until then only the
local tests below have run.

## Files

| Path | Role |
| --- | --- |
| `desktop/injector.mjs` | Node 22 CLI: target selection, loopback-only CDP websocket, polling, startup retry, reconnect, teardown. |
| `desktop/shell.js` | Single function expression evaluated in the renderer: sidebar entry (logo `assets/meerkat-sidebar.svg`), overlay, observer, cleanup. |
| `internal/web/assets/mount/meerkat-ui.js` | Trusted React mount bundle (`window.MeerkatUI` IIFE) built by `frontend/` (`npm run build`). Read from disk, never fetched. |
| `internal/web/assets/mount/meerkat-ui.css` | Companion CSS (also inlined by the bundle into its ShadowRoot); hashed into the asset version. |
| `frontend/src/generated/validate.js` | Contract validator for `contracts/workflow.schema.json`, used by the injector on every snapshot. |
| `tests/desktop.test.mjs` | CLI/URL/target/fetch/session tests and shell interaction tests with a fake mount in `tests/fixtures/mini-dom.mjs`. |
| `tests/desktop-mount.test.mjs` | Shell + real React bundle in jsdom (`frontend/node_modules/jsdom`). |

## Data flow

1. Node polls `GET <status-url>/api/workflow` every 4 s (3 s timeout, `redirect: 'error'`, loopback only),
   validates the envelope against `contracts/workflow.schema.json`, and copies only `data` and
   `legacyActive`. The `sessionToken` never leaves Node. An explicit 404 falls back to `GET /api/active`
   (shown as "run count unknown"); every other failure keeps the last snapshot visibly stale and polling
   pauses until the user clicks "重新连接".
2. Node calls CDP `Runtime.evaluate` with `(shell.js)(state, iconDataUrl, {version, load?})`. `load` is a
   closure around the local bundle; it is sent only when the renderer has no monitor or a different
   `version` (sha256 over shell, mount loader/bundle and CSS), and the old monitor is torn down first.
3. The shell mounts `MeerkatUI.mount(container, {snapshot: null}, {readonly: true, readonlyNote, theme,
   onAction})` inside the overlay's ShadowRoot (the mount nests its own ShadowRoot). Theme follows a
   `dark`/`light` class on Codex's `<html>`, else `prefers-color-scheme`.
4. `onAction` accepts only `reconnect`, which calls the CDP binding `__meerkatReconnect` to poll once.
   Stop and settings are rejected; use the coordinator CLI.

Nothing modifies `app.asar`, Codex files or Codex user data; Ctrl+C evaluates `shell('remove')`.

## Run (user-assisted, later)

```sh
# 1. Start Codex yourself with: --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222
# 2. With a local service serving GET /api/workflow:
node desktop/injector.mjs --cdp-port 9222 --status-url http://127.0.0.1:47824/
```

Exit codes: `64` usage/no CDP target, `2` Codex selectors not found (update `shell.js`), `1` renderer gone.

## Test (Node 22; `npm ci` in `frontend/` first, no Codex needed)

```sh
node --test tests/desktop*.test.mjs
```

After rebuilding the frontend, rerun the tests: the asset version changes and the shell reinstalls.
