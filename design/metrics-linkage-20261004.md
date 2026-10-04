# Metrics completion contract

Goal: exported evidence can join Agent/Session/Run, request uncertainty and
checkpoint continuation without reading private transcripts. Scope: store metrics
projection, focused tests and documentation. Existing JSON/CSV columns remain;
new columns are appended and nullable for historical unmeasured runs.

Only persisted ledger/session/review/checkpoint evidence supplies values. Export
first-review pass once on the first reviewed Run, and continuation provenance on
the exact resumed Run. Do not infer tokenization, fees, model/test duration,
compression cost or pure rate-limit waiting from wall time. No UI or remote change.
