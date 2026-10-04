# Metrics

Export JSON or CSV while the daemon is stopped:

```sh
node scripts/launch.mjs export --format json --output /private/metrics.json
node scripts/launch.mjs export --format csv --output /private/metrics.csv
```

Rows join Change, Project, Task, Agent, Session, Run, Role, Executor and Model.
They retain actual nullable token/cache fields, fee source, queue and wall time,
check time when measured, fix round, request counts, unknown and definite rejected
requests, checkpoint continuation and the first review result.

- First review pass rate: true / known values of `firstReviewPass`. It appears
  only on the first reviewer Run. Missing review evidence remains null.
- Checkpoint continuation success: successful Runs with `checkpointId` / ended
  Runs with `checkpointId`; report unknown outcomes separately.
- Unknown request proportion: sum `unknownRequests` / sum `requestCount` among
  ledger-backed rows. Historical unmeasured counts remain null.
- Delivery token use: sum confirmed actual Run totals for the selected Task,
  keeping partial/unknown counts separate. Review/fix/polish costs remain included.

Wall time and summed Agent execution time differ for parallel runs. Model/check
time, compression cost, repeated-read overhead, pure rate-limit delay and decision
waiting remain null unless actually measured. Scheduled retry delay is preserved
privately as a decision; it is not reported as actual waiting time. No transcript,
private session path, credential or authorization text appears in this export.

Appended columns preserve the prior CSV column order. Historical absent Agent,
Session, ledger or checkpoint evidence is not reconstructed by guesswork.
