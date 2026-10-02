# Meerkat UI prototype

Static, interactive design prototype for the generic Meerkat plugin. It uses
**sample data only**: it is not the native installed Codex UI, and it has no
connection to real Pi processes, agents, or any API. Nothing is sent over the
network.

## Run locally

From this directory (`plugins/meerkat/design/ui/`):

```sh
python3 -m http.server 8000 --bind 127.0.0.1
```

Then open <http://localhost:8000/>.

## What it covers

- **Agents** — local Pi instance list, status, and a disconnected/stale snapshot state.
- **Tasks** — task sheet with context, delivery (initial / final candidate), and per-task run history.
- **Usage** — run counts, input + output tokens (reported only), cache read/write
  listed separately (not included in the input + output subtotal), sample fees,
  and per role / model breakdown. Unknown values stay “未知” / “未返回” and are never estimated.
- **Theme / settings / demo states** — theme switching, settings (e.g. max fix rounds),
  and simulated states such as disconnect and reconnect.

## Checks

```sh
node --check app.js
```

Visual review is owned by Codex; this README makes no claim that visual checks were performed.

## Attribution

Some styling in `app.css` is adapted from Magpie
([yetone/magpie @ 47741ee31f2f66c365661e37ffd2cb7af53773e5](https://github.com/yetone/magpie/tree/47741ee31f2f66c365661e37ffd2cb7af53773e5)),
MIT licensed. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
