# Deterministic role report writer

A real resumed Pi run committed its scoped file but transcribed a 39-character
SHA and used `observed` instead of `result` in checks. Strict validation correctly
refused delivery. Keep that failed run and all usage.

Goal: let the executor submit a structured report without transcribing bindings.
Scope: Pi private report tool, Go report writer, bridge and focused tests/docs.
Go derives exact HEAD and frozen Context; it never invents checks or verdicts.
The existing SHA/context/scope verifier remains authoritative. Reject wrong
role fields, credentials, changed duplicate bodies and report path substitution.
Identical reports may read the same private result; no automatic retry or model
call. The task state machine and frozen budget do not change.
