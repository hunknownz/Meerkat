# Native Codex controls verification — 2026-10-04

Installed plugin and service: `0.4.0-beta.11`.

## Global entry

The user supplied a Codex screenshot with the Meerkat sidebar icon selected,
the full-page Meerkat Agents view open and the connection marked connected.
This accepts visibility and rendering of the global entry on this installation.
It does not prove persistence across host upgrades or every Codex version.
The screenshot is kept outside Git; SHA-256:
`58677f79249a358cd9960ba84a9caf3924335b0ccfdca2ceeadee0f8a49f2b28`.

## Test boundary

Two clean linked worktrees and frozen developer-only tasks exercised the native
MCP App, app-only tools, private Unix socket, Go controller, SQLite receipts and
Pi protocol adapter. The child was a deterministic local protocol fixture, not
Pi or an AI model. Its only HTTP connection was to its own Unix budget socket.
Both tasks used the clearly named `native-fixture/protocol-only` profile, a
5,000-token hard cap, 300-second time limit and no fix rounds. Test profiles,
contracts, protocol logs and screenshots stay in private evidence storage.

This is native host transport acceptance. The prior real Pi/browser instruction
test remains separate; see [that record](ui-intervention-20261004.md).
A real Pi/model task controlled from the native panel still needs a complete
end-to-end check. No new provider request was made here. The existing uncertain
paid Run was neither replayed nor granted new allowance.

## Instruction and receipt

In the actual Codex MCP App, the coordinator selected the fixture project,
expanded its running Agent, entered `Native host direction verified.` and
clicked **发送指令** once. The panel displayed the durable queued receipt.
Clicking **查询回执** read that same receipt without a second `steer` command.

The same child and Session received the direction and wrote its exact text into
the declared proof document. One scoped fixture commit was created:
`f8857cdd25eb2af52c2808e572aa5ad7064ac7d3`. The coordinator reviewed the actual
three-line diff, checked its contents and clean Git state, then released the
fixture to finish. Go verified the candidate SHA, Context and scope; the Run
became succeeded and the Task remained an unreviewed first delivery.
The scratch candidate was retained locally and was not integrated into `main`.

| Evidence | ID |
| --- | --- |
| Task | `0d55f90a-3b21-4f21-9901-8ffc473871c0` |
| Run | `28fe0283-a2f9-479b-b3cd-02c0246a3543` |
| Session | `7dceec06-a62b-42d5-9199-a9acd3892b08` |
| Instruction receipt | `60e4ea30-0cdd-4f21-b16c-ff26025706de` |

The native Task detail retained the acknowledged instruction, its queued protocol
disposition, actual completed Run, Context digest and exact candidate after exit.
Receipt acknowledgement alone was not used as proof of the file change.

## Stop and unknown-state protection

A second fixture Run was stopped by clicking **停止运行** once in the native
panel. It displayed the submitted stop request. The child received exactly one
`clear_queue` and one `abort`, reported an idle Session and exited. The controller
settled the Run as stopped; its Task had reason `stop_requested`, no candidate
and no new commit. The native Task detail showed **停止 · 已处理** together with
the actual stopped outcome. The coordinator separately confirmed the owned
child PID was absent.

| Evidence | ID |
| --- | --- |
| Task | `1d2bdeec-c2e3-4752-bb72-d040097e1d67` |
| Run | `84d2c4ad-4e39-4986-bdd8-4593fca2fc70` |
| Session | `5446e08d-4829-4040-a36a-8f0ac30ecf76` |
| Stop receipt | `1f64c4e1-3372-45e1-ade5-9c96dce4d4c7` |

The prior unknown Run's input, send and stop controls were also inspected in the
native panel and were disabled. Its state and missing usage remained unchanged.
The earlier automation timeout no longer prevents DOM inspection. Two later
screenshot attempts still timed out; the three successful screenshots below
are the retained visual evidence. No fallback operated the Codex main UI.

## Measurements and retained evidence

| Fixture | Provider requests | Queue seconds | Execution wall seconds | Stored tokens / fees |
| --- | ---: | ---: | ---: | --- |
| Instruction | 0 | 0.041508 | 162.963851 | Unknown / unknown |
| Stop | 0 | 0.037007 | 34.226894 | Unknown / unknown |

The wall times include deliberate waits for host interaction and are not a
development speed benchmark. SQLite request ledgers contain no provider requests;
the executor's missing token and fee values stay null. No simulated billing
counts or monetary estimates were inserted. The two test tasks remain labelled
as fixtures in history. The panel was restored to the Meerkat Agents view and
left open; there are no active fixture children.

| Private native screenshot | SHA-256 |
| --- | --- |
| Instruction and queued receipt | `a2c70bfdf8df0b8e3275fa443cc3369f26e5e3b4b4dabd5934906291b5d2a6b7` |
| Instruction Task result | `401ea300c8635b299d6a03e0c987a546243bb96c365edfefcf5dcea20925a23e` |
| Stop Task and processed receipt | `dfdf4e854fbffd0d00fcd0712d9f06024528885e3941e8e57943e8de53f6f55f` |

## Remaining acceptance

The global entry, native control submission, receipt lookup, retained Task
receipts and verified stopping passed for the observed host and protocol fixture.
Real Pi/model execution with these native controls remains pending. Neither this
record nor the earlier real Pi/browser test alone proves that complete combined
path. Public version release and directory submission are separate actions.
