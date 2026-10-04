# Monitor UI simplification

Date: 2026-10-04, Asia/Shanghai. Version: 0.4.0-beta.12.

The maintainer approved all four groups after the kill-ai-slop source scan and
inspection of the real Codex panel. The existing logo, palette, system font and
Agents / Tasks / Usage views remain.

## Changes

- One project-scoped count summary replaces repeated counts and heartbeat boxes.
  Agent rows prioritize task, role, state, concise model name and latest activity.
  Expand a row for controls; expand its diagnostic section for full run metadata.
- Task details put the current candidate, check summary, matching review and gaps
  before budget and history. Full versions, check commands, ledger, sessions and
  receipts remain available through disclosures. Unknown results, queue/block
  reasons and budget warnings remain visible without opening those sections.
- Usage prioritizes reported total tokens, reported cost and summed Agent time.
  Category counts and wall time remain in a disclosure. Per-run rows retain total
  and cost, with full model/ID and category counts on expansion. Unknown values,
  partial coverage and lower-bound markers are preserved.
- Passive pills and stacked card surfaces are reduced. Instruction entry/send
  remain prominent; stop, receipt query and current restrictions remain available.
  Static help is collapsed. No control protocol or backend scheduling was changed.

## Checks

- TypeScript check and all 32 frontend tests passed. Coverage includes project
  counts, unknown/partial usage, current-versus-old review versions, exact evidence,
  receipt deduplication, stop acceptance and input surviving snapshot refreshes.
- Embedded browser, mount and MCP App bundles built. Go web/MCP/server tests:
  46 passed. Distribution metadata/launcher checks: 8 passed.
- Browser inspection at 360, 516 and 1200 pixels found no horizontal overflow;
  light/dark views and diagnostic expansion were checked using real service data.
  Screenshots are private. Browser checks do not prove fresh Codex-host rendering.
- The scanner still reports four intentional hits: link hover underline, a flat
  status dot and two functional theme/settings SVGs. They were inspected and kept.

## Execution and host boundary

The bounded Pi developer attempt returned `request_budget_unverifiable` before
writing any code. Its session was confirmed idle and the worktree clean. The
coordinator completed the approved local edits; the uncertain request was not
replayed. Usage/fee remain unknown and its reservation remains in SQLite.

Fresh beta.12 rendering in the real Codex panel is pending. Installation/resource
checks and browser screenshots must not be used to mark that gate complete.
