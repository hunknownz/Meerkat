# Request budget authorization

Implemented in the Go service; install the matching runtime before using these capabilities.

For scheduler-driven Pi runs, Go opens a private request authority bound to the frozen task, profile, model, run and session. A bundled Pi extension wraps provider HTTP requests. The task prompt is submitted only after the extension confirms that the supported bridge is installed.

## Task token policy

New tasks without an explicit `maxTokens` use `budget.mode: "monitor"` by default.
The default 500,000 tokens is a warning threshold, not a cumulative task stop limit.
Usage, reservations and unknown outcomes remain recorded. The panel shows a warning
when confirmed request totals reach the threshold; missing usage is still unknown.

Select a hard cumulative task cap with `"budget": {"mode":"enforce","maxTokens":150000}`.
An explicit `maxTokens` without `mode` preserves the previous hard-cap behavior.
To set a custom warning threshold use `"mode":"monitor"` with `maxTokens`.
Prepared historical tasks keep their original hard caps, digests and additions.

Monitor mode still respects each frozen Profile's per-Run token cap, task and Run
wall time, request count, output cap and fix-round limits. It does not repair an
uncertain request or enable automatic replay. Stage token reservations apply only
to hard task caps; wrap-up within a Run remains available in both modes. Monitor
mode can receive an authorized time addition, but has no task token cap to increase.
This policy does not impose a reliable monetary ceiling: costs remain unknown
without trustworthy billing evidence.

## Request lifecycle

1. Estimate the input and ask Go for a reservation. SQLite atomically checks ownership, the frozen contract, task and run limits, request count and deadline.
2. Reduce the actual outgoing maximum output to the granted amount.
3. Obtain a one-time send permit, then send the HTTP request.
4. Observe the provider's raw SSE usage and settle the reservation. A lost reserve or send-permit reply is not retried.

Hard task token caps, when selected, and request-count limits carry across role runs. Ledger-backed runs are accounted for once, through their request records. Other historical runs require known complete usage before a new reservation can be approved. The versioned default policy permits at most 256 requests per task and 8192 output tokens per request; the remaining task/run allowance can lower the output cap further.

## Unknown results and stopping

Missing fields remain null. SDK-generated zero values and historical session totals do not replace raw usage. Cache-write usage and fees remain unknown when the provider does not supply reliable evidence. The request ledger supplies current Run usage to the scheduler.

A request whose outcome is unknown retains at least its reservation. Service restart does not refund it or resend it. An uncertain budget policy blocks further requests. Observed overruns are recorded and block subsequent requests. A denied request or unknown settlement notifies the RPC owner immediately, which clears queued messages, aborts and verifies idle before shutdown; Pi's retry wait does not keep the task running.

Verified session history and verified budget outcome are separate. A known idle session with an unknown request outcome still leaves Task/Run unknown. It cannot produce a successful delivery.

## Supported scope

The current bridge is verified with Pi **0.99.1**, text requests using **`openai-completions` over HTTP SSE**. Unsupported versions, APIs, media inputs, alternate models and transports are refused. Scheduler-driven Pi runs require the bridge. Direct executor fixtures without a budget authority exercise only the RPC adapter.

Input reservation uses request UTF-8 byte length plus 1024 and the controlled maximum output. This is an explicit estimate, not a validated tokenizer or monetary ceiling. Request count and deadline are checked before sending. The bridge controls Pi provider requests; it does not sandbox shell commands or arbitrary network activity.

[Stage reserves and early wrap-up](stage-budgets.md), [verified checkpoints](checkpoints.md) and [authorized budget additions](budget-decisions.md) are implemented.

## Storage and verification

SQLite V5 adds private budget policies and request records. V1–V4 stores and supported backups upgrade without rewriting historical records. The ledger holds hashes, metadata, permissions and nullable usage, with no prompt bodies, headers or keys. Backups validate record state, binding and overrun evidence. Pending requests become unknown on restart.

Tests cover reservation concurrency, task/run quotas, send-permit uniqueness, settlement deduplication, unknown/overrun outcomes, fencing, frozen contracts, restart, migration and backup corruption. The installed-Pi probe uses an isolated HOME/config and a loopback fake model with a dummy key. It verifies authorization before actual HTTP traffic, output rewriting, raw/missing usage and first/second request denials without paid model access.
