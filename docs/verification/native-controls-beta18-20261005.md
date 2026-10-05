# Native controls and reviewed delivery — 2026-10-05

Paid exercises used the installed beta.17 Go/Pi adapter, Pi 0.99.1 and
`zenmux/deepseek/deepseek-v4-flash` through the local gateway. Codex MCP Apps
controls were clicked directly in the expanded native panel. No loopback browser
or CLI instruction/pause write substitutes for these clicks.

## Direction and follow-up delivery

Task `78e0e1cf-bc6c-4101-b0ae-61b57d7f6ccb` received both controls in developer
Run `ccd8b5e2-2ecf-46f6-b19c-8b504907fa59`, Session
`3e32f210-6c78-4a2b-bb99-a2d0b358006b`:

| Native selector | Kind | Request UUID |
| --- | --- | --- |
| 当前工具后 | instruction | `0185c67d-37ca-479b-a8e9-d1102576e6de` |
| 当前轮结束后 | follow_up | `1fa0f89f-f26e-4bd7-9801-793f926f2c8e` |

Before either click, provisional HEAD was
`04cca73b7380a566652adde0df3265b3cfd62f46`; neither requested example sentence
was in its file or the frozen Context. The amended developer candidate
`1e95d01c47177a8cc7a39d80808248853acca90b` contains both sentences. Development,
independent review, polish and independent re-review completed. Polish changed
one example's case at `10e112de151c436fc19ae33f592084dbf63708b4`; the coordinator
identified that mismatch rather than claiming exact wording had survived.
The subsequent pause task corrected it and removed redundant exercise wording.

## Native pause and explicit continuation

Task `224b1220-f69c-4c83-be04-07d0ce2a0999` was paused through the native button.
Request `4f35b810-c564-4147-8d35-6e4b3f25b68c` refers to Run
`723c6a9e-3b24-401a-b7f4-fa966bf08c34`, Session
`b3690ee7-d1ee-46cf-86a5-611afe953d2f`.

Before the click, the guide had staged and unstaged changes (`MM`), with HEAD
unchanged. After acknowledgement, the Task became paused and checkpoint
`c5c4054e-0b7d-495f-a9b3-cc85b25ac70d` was saved. HEAD, status and both diffs
matched the pre-click evidence. An explicit new dispatch consumed that
checkpoint once and created Run `1434272e-e92b-45b7-9db8-a6652f0590bc` in the
original Session. Frozen Context, Profiles and allowance were preserved.

Development, independent review, no-change polish and final review completed on
`557d5f865e8f576bcf25b03cdc64c23bd8d1e708`. The coordinator reviewed the actual
guide, scope, exact example wording and checks. The three Pi commits
were integrated into main with their author history preserved; integration HEAD
is `64cf3b7915f5a05e18cdfaabc23a8bc2acad69bc`. This is local code delivery and
repository integration, not independent QA, deployment or customer acceptance.

The final model review listed plain-text digest reproduction as a gap. The
coordinator recomputed the documented canonical `{sources,text}` SHA-256:
`sha256:d7e7d1a3bb7035110b03c7e244eb36ced5b3ee2244dd616efd1e2447d2391cbb`
matched the stored frozen Context exactly. A plain-text hash is a different
representation. Native receipt/checkpoint evidence also closes the reviewer's
repository-only visibility limits; the original review record is retained.

## Failure and repaired display behavior

The earlier Task `428463fa-5154-4061-beeb-c17ed6ed5871` ended without
`meerkat_report` and failed with `report_missing`. Its unreviewed commit remains
in its isolated worktree; it was not integrated or changed into a success.
The next task used a longer finite input window and explicit final-report
instructions. Its failure cost is included below.

Beta.17 removed an expanded Run as soon as it ended, hiding an in-flight query
or reply. It also displayed `follow_up` and `pause` Task receipts as stop
requests. Beta.18 retains only the selected ended Run until collapse or another
selection. New controls remain disabled, receipt reads remain available and
running counts still come from the service. Task receipt labels cover all five
kinds. Regression checks reproduce a reply arriving after Run completion and
read the same UUID without another write.

## Installed beta.18

- Source: `64cf3b7915f5a05e18cdfaabc23a8bc2acad69bc`. Six target binaries were
  built from the clean committed tree. The local macOS arm64 binary has SHA-256
  `cfc6e0cd85308c8c4b8d0f4c90b12ccb74a9b95b2e11aa04f1f3ea5b1ad31b08`.
- Frontend type checking and three builds passed; 40 frontend tests, 71 Node
  distribution checks and Go web/MCP/CLI suites passed. These checks made no
  paid model requests. The full Go 1.26.8 race result remains the prior audit.
- The confirmed idle beta.17 owner exited; its runtime produced a consistent
  backup. Beta.18 started at the same address. All 27 non-lease authority tables
  matched exactly across the cutover: 43 Tasks, 74 Runs and four historical
  unknown outcomes. Backup integrity is `ok`.
- [Native installation CI](https://github.com/hunknownz/Meerkat/actions/runs/37334338061)
  passed on Windows amd64 and macos-14 at `64cf3b7`: native platform checks,
  PowerShell setup on Windows, clean installation and stdio panel resources.
  The expensive missing-toolchain download was not repeated; its earlier
  passing evidence remains in the installation audit.
- The installed stdio MCP server reports beta.18 and 23 tools. Its embedded HTML
  matches main, SHA-256
  `4e1a872c7545c2a93e94eaf2d0a5c8e08c93c1da446e92b308739a7933ed4d92`.
  Native reload and the changed receipt surface still require host evidence.

## Measured usage

| Exercise | Confirmed tokens | Wall span seconds | Summed Agent seconds |
| --- | ---: | ---: | ---: |
| Missing-report attempt | 29,089 | 57.337 | 57.337 |
| Native directions and four-role delivery | 392,103 | 304.599 | 304.214 |
| Native pause, resume and reviewed delivery | 423,816 | 452.771 | 367.567 |
| Subtotal before final stop | 845,008 | — | 729.117 |

Wall span includes the explicit pause interval. Agent time includes tools and
finite 90-second manual-control windows; it is not pure model time. Some token
categories are unknown; confirmed totals are preserved. Fees and Codex usage
are unavailable, not zero. Earlier beta.14 costs remain in their own record.
The four historical unknown Runs, requests and held reservations are unchanged.

## Native screenshots

Screenshots are retained privately; no raw Session or profile is published.

| View | SHA-256 |
| --- | --- |
| Native direction receipts (beta.17 label bug visible) | `10e0a13efbe2e271b859ffc1af12e9d493c9aa92096f67e0c18146daf344418d` |
| Native pause queue acknowledgement | `6c886a0d4a680c078d13b2c7e899cd9194fc9a691502dd56e59cd390e1885a7c` |
| Final native Task delivery | `a03fafbd87742bf38dc24a457aff6478d3d6032c59bd0eabf5294fbec1ad9f95` |
| Original developer Session and consumed checkpoint | `a3b051f9c99666fe78f84a58a32c59fda61d5d3dad577ebc402a7603634d4120` |

## Remaining

Fresh beta.18 native stop, post-end receipt lookup and corrected labels remain
pending after host reload. Real Windows Codex display is deferred until the
user's device test. Public prerelease/tag still requires explicit maintainer
authorization; directory submission is not required for GitHub installation.
