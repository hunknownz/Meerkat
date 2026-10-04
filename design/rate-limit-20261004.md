# Definite rate-limit rejection and waiting

Only an HTTP 429 with the configured gateway's explicit
`X-Meerkat-Request-Status: rejected-before-generation` attestation permits another
attempt. Ordinary 429, 5xx, disconnects, missing usage and missing local receipts
remain unknown; they never replay. Existing Magpie responses are not assumed to
have this attestation. This protocol does not infer a zero invoice.

Go records the rejected attempt, computes a bounded delay (Retry-After or
exponential delay), and refuses more than three consecutive retries or waiting
past the frozen deadline. The thin bridge performs that delay and obtains a new
reservation and one-time send permit for a new UUID. Original attempts remain in
SQLite. Rejection token fields and fees remain null; confirmed model usage is
reported separately. Unknown processes and accounting remain blocked.

Scope: requestbudget contract, store ledger, Go request authority, Pi private
bridge and focused tests. SQLite V12 prevents older authorities reading a new
ledger state. No gateway changes, hidden SDK retry, pricing assumptions or remote
actions. Acceptance includes unproven 429 refusal, lost receipt, retry bounds,
deadline, new permit, cancellation and backup validation.
