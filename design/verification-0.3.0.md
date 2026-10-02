# Meerkat 0.3.0 delivery evidence

Status: Go, SQLite, React and private history migration verified locally. Actual Codex sidebar display is pending user-assisted verification; the full delivery plan remains incomplete until that evidence exists.

## Source and architecture

- Standalone generic repository extracted with history. Project profiles and names removed from public history.
- Go 1.26.0 and Node 22 used without changing the machine's default runtime.
- Go owns scheduling, processes, CLI, HTTP/SSE and the private Unix socket; SQLite owns state, receipts and history. Old Node entry points are forwarding wrappers.
- Executor is generic; Pi is the only implemented adapter. Profiles select provider/model per role. Worktrees isolate Git state and post-run scope checks; they are not an OS execution sandbox.
- React 19.3.0, TypeScript 7.0.2 and Vite 8.3.2 build the embedded app and shared read-only host mount.

## Automated checks

- Full `go test -race ./...` passed after the frozen-baseline and Git-inspection fixes. Final store/CLI changes also passed focused race tests. `go vet ./...` passed.
- Core fixtures cover normal delivery, review rejection and fix cap, polish changes and recheck, different/same worktree concurrency, dependencies, stop receipts, budgets, unknown recovery, lease fencing, pure dry-run and one-role delegation.
- Executor tests use real local test processes for owned stopping, malformed reports, SHA/digest binding, partial usage and limits. They do not substitute for the actual provider runs below.
- Import/Issue fixtures cover corrupt and conflicting input, duplicate import, explicit project relocation, receipt deduplication and unknown posting outcomes. GitHub calls were mocked; no Issue comment was sent.
- React: 14 tests in 3 files passed, along with type checking and both builds. Node wrapper/package/desktop checks: 36 passed, including real React mounting in jsdom, read-only actions and teardown. These do not prove native Codex display.

## Actual Pi runs through Go

| Practice | Result | Actual tokens | Agent time |
| --- | --- | --- | --- |
| First document workflow | Developer → review → polish with no change → fresh review; candidate `dc9d80d1fb5d6da862e3e655e0b53f65025414dc` | 54,403 | 83.26 s |
| Corrected document workflow | Developer → review → polish with a new commit → fresh review; candidate `87adcbf2e43e8c4729bd46fadc4ab25001683766` | 73,277 | 121.57 s |
| Project metrics export | One developer delegation; candidate `4306fac9416c434247243bec6141cc62ca86f037`, then coordinator diff review and store race tests | 211,040 | 159.76 s |

The second workflow wall clock was 122.16 s. All counts include cache reads/writes; provider fees were not returned. Single delegation stays `first_delivery` and does not claim automated review. The corrected workflow retained a reported gap noting that development and polish produced two scoped commits.

The full workflow pilot used source `5925ff7f61240ddeb26400704be9b5ecd72ca475`. Subsequent changes were the UI check display, explicit historical project move, and additive Project metrics linkage; the scheduler/executor workflow did not change.

## Migration and browser evidence

- Original private files and receipts preserved. Import `e921a28892a50a025543d8a2bcc7f97f` used an explicit project move, kept target metadata and recorded the original project. Exact repeat was a no-op.
- Before the final metrics delegation, 38 original receipts plus 8 native workflow runs produced 46 unique exported rows. Every token category matched the source receipts; costs remained null. Missing or partial usage was preserved.
- Consistent backup/restoration preserved project/context/profile/task/run/delivery/review/import/history/Issue row counts. Prepared Issue bodies had matching hashes and private permissions in both directories. Recovery never overwrote newer records.
- Real browser checks observed an active Pi role changing through review/polish, completed deliveries, SHA/digest and gaps, desktop/narrow layouts, dark/light themes, stale snapshots with unknown running count, and full snapshot recovery after service restart.
- Named deterministic checks were corrected and verified against actual Go data; Agent-reported commands/results are identified separately.

## Installation and remaining evidence

The clean staging package includes the Go binary and source/binary hashes; installation uses the standalone `meerkat` marketplace. Exact installed/cache version and remote SHA are recorded in private cutover receipts, outside the public source.

Actual Codex sidebar display remains unverified. Earlier CDP tool access was denied, so the native step requires user assistance. Browser captures and jsdom tests are not substitutes. No production operation or remote Issue write occurred.

Private briefs, profiles, keys, transcripts, full migration reports and runtime receipts remain outside this public repository. Numerical evidence above is measured; unknown fees/model/test time are not replaced with zero.
