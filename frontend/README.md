# Meerkat frontend

React + TypeScript (Vite), Node 22. `../contracts/workflow.schema.json` is the authority for the
`GET /api/workflow` snapshot envelope; `src/generated/` (TS types + standalone Ajv validator) is generated from it.

```sh
npm ci            # exact versions from package-lock.json
npm run generate  # regenerate src/generated from the schema (alias: npm run types)
npm run check     # tsc --noEmit
npm test          # vitest (jsdom)
npm run build     # generate + check + browser bundle + IIFE mount bundle → ../internal/web/assets
```

Outputs (tracked, embedded by `internal/web/embed.go`, so Go builds need no npm):

- `app/` static browser bundle (BrowserTransport: GET /api/workflow, SSE /api/workflow/events).
- `mount/meerkat-ui.js` self-contained IIFE: `window.MeerkatUI.mount(container, {snapshot, legacyActive}, options)`
  → `{update(snapshot, legacyActive), setDisconnected(message), destroy()}`. Renders in a ShadowRoot; no fetch.
  `options`: `onAction({type:'reconnect'|'stop'|'settings', ...})`, `readonly`, `readonlyNote`, `transport`, `theme`.
- `mount/meerkat-ui.css` companion stylesheet (the same CSS is also injected into the ShadowRoot).
- `app/THIRD_PARTY_NOTICES.md`, `mount/THIRD_PARTY_NOTICES.md`: Magpie MIT attribution, copied from
  `../dashboard/public/THIRD_PARTY_NOTICES.md` (authoritative).
