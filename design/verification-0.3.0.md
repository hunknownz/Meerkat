# Meerkat 0.3.0 delivery evidence

Status: implementation and validation in progress. An unverified entry is not a passed gate.

## Verified so far

- Standalone generic repository extracted with history. Project profiles and names removed from public history.
- Go 1.26.0 and Node 22 used without changing the machine's default runtime.
- SQLite, Executor and workflow core passed independent Go race tests.
- History import and Issue receipt packages passed independent race tests using isolated data and mocked GitHub calls.
- React: 3 test files, 13 tests passed. Type check and both Vite builds passed with React 19.3.0, TypeScript 7.0.2 and Vite 8.3.2.

## Remaining verification

CLI/service integration, private history migration and recovery, actual Pi delivery through Go, installed plugin version, actual browser behavior, Codex sidebar evidence and remote repository SHA.

Private briefs and receipts remain outside this public repository. Tokens use provider-reported counts including cache use. Missing fees remain unknown.
