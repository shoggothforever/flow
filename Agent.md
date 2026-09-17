# Flow Agent 开发指南

归档日期：2026-09-14。本文对应本机 `~/.codex/skills/flow-cli/SKILL.md` 的开发与使用指导，按用户指定文件名留档。更新 skill 时同步本文正文；具体接口和行为以当前源码为准。

## Project and working state

`flow` lives in `/dsm/oev/flow`. It uses Go/Cobra, an embedded HTML/CSS/JavaScript UI, and a shared JSON configuration. It manages projects, scripts, parametrised commands, grep/find presets, schedules, and TAPD/Codex session associations.

- Read `git status --short` and relevant diffs before editing. Preserve existing working-copy changes, including removals and untracked reference files. Do not delete `.codebuddy/`.
- Verify the executable with `command -v flow` and `flow --version`. The usual installation is `~/.local/bin/flow`; a fresh source build is `./bin/flow` and may differ from the installed version.
- Prefer CLI operations for supported configuration changes. For new behavior, implement a shared backend used by both CLI and UI.
- The project archive is `Agent.md` at the repository root. Keep its guidance aligned when updating this skill. Detailed user commands and HTTP routes are in the repository's `README.md`.
- The current checkout uses schema v2, which removed tmux/layout resources. `flow layout`, `flow up`, layout APIs, and layout picker entries are no longer supported.

## Configuration and dispatch

Configuration path precedence is `--config` > `FLOW_CONFIG` > `~/.config/flow/config.json`. CLI and UI use the same `config.Store`.

- All changes to the active config go through `Store.Mutate`: lock `<config-path>.lock`, load, mutate, validate, write a temporary file, fsync, then rename. Avoid whole-config read/modify/write outside this lock.
- `schema.go` defines the root config; `agent_session.go` defines session/requirement records and their validation. Non-additive changes require a schema-version bump and migration.
- Resource cwd/path fields use the existing `@project`, home-directory and environment-variable resolvers. Codex session cwd is metadata from Codex, not a Flow project alias.
- `main.go` calls `cmd.RewriteArgs()` before Cobra. No arguments select `flow run`. Lazy aliases resolve across script/cmd/grep/find; collisions require an explicit resource kind. New explicit Cobra subcommands are already excluded from alias rewriting.
- Picker and missing-parameter prompts use `/dev/tty`. In noninteractive environments, pass names and required parameters explicitly.

```bash
flow                            # unified picker
flow <alias> [args...]           # lazy script/cmd/grep/find dispatch
flow config path
flow config validate
flow config export              # defaults to ./flow.config.json
flow config import              # validates/migrates and replaces active config
flow ui                         # defaults to 127.0.0.1:7777
```

Config snapshots include TAPD requirements and tracked session metadata. Scheduler history, locks and fork operation records are runtime state and are not exported.

## Common resource operations

```bash
flow script add <alias> --cwd @project --command 'bash something.sh'
flow script run <alias> [--grep preset[,preset]] [-- extra args]
flow cmd run <alias> [positional...] [key=value ...]
flow grep save phase --pattern 'EnterPhase|RebuildCurStageConfig' --regex
flow grep run phase
flow find run <preset>
flow schedule add nightly-build --every 'daily 01:30' --kind script --target build_cook
flow schedule history --limit 20
```

- Script `--arg` appends fixed arguments. A script currently stores a single command; there is no persisted `steps` field.
- Cmd placeholders use `{{key}}` or `{{key:default}}`. Merge precedence is defaults < positional arguments < explicit `key=value`. Declare positional mappings with repeatable `--positional` when adding a cmd.
- Pipeline filtering (`flow <script> --grep phase`) uses only preset patterns. Saved paths/include/exclude apply to file searches through `flow grep run`.
- Saved presets do not universally support overwriting via `add/save`; inspect the command before replacing an existing resource.
- The scheduler reloads config and re-executes `flow <kind> run <target>`. Its history lives in `history.jsonl` beside the active config. Installing the scheduler is a separate action: `flow scheduler install`.

### Existing command chains

Use `flow chain` (alias `flow seq`) for sequential execution without introducing a new script schema:

```bash
flow chain                                  # interactive step picker
flow chain build_cook buildES_cook
flow chain -s 'kill-port 8080' -s restart_server
flow chain --dry-run build_cook restart_server
flow chain --keep-going build_cook buildES_cook
```

Each step re-invokes Flow and preserves the selected config. Quote an entire step when it has arguments, or use repeatable `--step/-s`. Default behavior stops at the first failure. A child `jump` cannot change the next step's cwd; use each resource's own cwd.

## UI development

The UI is embedded through `internal/ui/server.go`; there is no frontend build or CDN dependency. Static changes require rebuilding the Go binary and restarting the development UI to be visible.

| Concern | Source |
| --- | --- |
| Main navigation, resource tables/forms, run-output tabs | `internal/ui/static/index.html` |
| Shared warm-white/teal theme | `internal/ui/static/assets/theme.css` |
| TAPD workspace, HTML/SVG tree and interactions | `internal/ui/static/assets/agent-sessions.js` and `agent-sessions.css` |
| Resource CRUD / streamed execution | `internal/ui/handlers_crud.go` and `handlers_run.go` |
| Requirement/session HTTP interface | `internal/ui/handlers_agent_sessions.go` |

`capture.html` provided the layout inspiration; its missing original CSS is not a Flow runtime dependency. Maintain the established whitespace, grouped forms, light cards and SVG icons when extending the UI.

- For resource fields, update config types/validation, CLI behavior, the corresponding `FORMS` definition/table rendering, and JSON round trips. Session features use their dedicated UI module and shared service instead of generic resource forms.
- Preserve concurrent run-output tabs, stop/cancel controls and schedule history. Streaming execution uses SSE; the session page does not run interactive Codex inside the log panel.
- The local server is intended for loopback access. New session write endpoints require JSON bodies where applicable and reject cross-origin writes.
- Escape displayed labels, URLs and metadata. Preserve modal focus handling, keyboard tree navigation and narrow-screen layouts.

## TAPD and Codex sessions

The feature is available through `flow agent-session` and UI **需求与会话** at `/#agent-sessions`. Shared behavior belongs in `internal/agentsession`; `cmd/agent_session.go` and the HTTP handlers call that service.

```bash
flow agent-session tapd add 'https://tapd.example/story/123' --title 'Replay updates'
flow agent-session tapd list --json
flow agent-session tapd edit <requirement-id> --notes 'Updated scope'
flow agent-session list --local --json
flow agent-session link <session-id-or-prefix> --tapd <requirement-id>
flow agent-session sync
flow agent-session list --tapd <requirement-id> --json
flow agent-session show <session-id-or-prefix>
flow agent-session fork <session-id-or-prefix>
flow agent-session unlink <session-id-or-prefix> --tapd <requirement-id>
flow agent-session tapd rm <requirement-id>
```

### Identity and relationship invariants

- Persist the full Codex `thread.id`: this is the ID for `codex resume`. Codex `sessionId` identifies the shared session-tree root and must not replace a fork's thread ID.
- `forkedFromId` supplies ordinary fork ancestry. `parentThreadId` describes an internal subagent relationship; exclude internal subagents from the user-facing session inventory/tree.
- UI labels normally show eight ID characters and extend on collision. Stored IDs and copied commands stay complete. `link` and `fork` resolve short inputs against a complete local listing; ambiguous prefixes fail. Full IDs can be associated even if unavailable locally.
- `tapd_requirements` stores a stable requirement ID, URL, title, notes, explicit session entrypoints and excluded branches. Preserve URL query parameters; do not invent TAPD-specific parsing or authentication.
- `agent_sessions` stores canonical session metadata and parent links once. The same session can appear under several requirements. Local display labels are shared and do not rename the conversation in Codex.
- Effective membership expands ordinary fork descendants from each requirement's entrypoints. Importing a child adds readable ancestors as context nodes, not implicit requirement membership.
- Unlink removes the branch from only that requirement and records an exclusion; refresh must not restore it. Explicitly linking that node again clears its exclusion. Deleting a requirement does not delete Codex sessions.
- Reject ancestry cycles. Preserve missing parent IDs as placeholders and existing metadata on a failed scan. Availability describes the last successful discovery, not whether the agent is currently running.
- Refresh discovers external forks. The graph supports folding, pan, zoom and fit; initially deep branches fold below the third session level.

### Codex adapter and fork recovery

`codex.go` resolves `codex` from Flow's PATH and inherits the process environment and `CODEX_HOME`. It starts `codex app-server --listen stdio://` with an argument array, performs `initialize`/`initialized`, and uses `thread/list`, `thread/read` and `thread/fork`.

- Discovery handles pagination and active/archived records. Read metadata without full turns. Fork creates a persistent child, inherits source configuration and does not send `turn/start`.
- Keep process cancellation/cleanup and JSON-RPC notification handling intact. A fake UUID or copying the parent ID is not a successful fork; use the returned child thread ID.
- `operations.go` stores request UUIDs and outcomes in `<config-path>.agent-operations/`, using separate locks and atomic writes. Do not hold the main config lock while calling Codex.
- `created` means the child exists but still needs saving into Flow; retry the same request ID to save it without another RPC. `completed` is idempotent. `pending/unknown` must not automatically create another child; a definite rejection is `failed`.
- For unknown results, refresh metadata and let the user explicitly identify the child in the pending-operations panel. Resolution verifies parent ID and creation time. Do not silently choose among candidates or create a fresh request merely to retry an uncertain operation.
- Missing/incompatible Codex should surface a concrete reason. Full-ID association and requirement editing remain usable without discovery/fork.

```bash
flow agent-session operations
flow agent-session fork <parent-id> --request-id <original-request-id>
```

HTTP entrypoints are `/api/tapd-requirements` and `/api/agent-sessions`. Reads use GET; association, sync, fork and recovery use explicit write requests. Consult `README.md` and the handlers for individual routes and bodies.

## Build, verification and delivery

```bash
cd /dsm/oev/flow
go test ./...
go vet ./...
make build
./bin/flow ui --no-open
```

Use `GOCACHE=/tmp/flow-go-cache` if the normal Go cache is read-only. Run checks appropriate to the change; documentation-only updates do not need the application test suite.

For association/concurrency changes:

```bash
go test -race ./internal/agentsession ./internal/config ./internal/ui
```

For Codex adapter changes, the opt-in test uses a temporary `CODEX_HOME` and synthetic history, verifies persisted fork/read/resume across processes, and starts no model turn:

```bash
FLOW_CODEX_INTEGRATION=1 go test ./internal/agentsession -run TestInstalledCodexForkIntegration -v
```

For UI changes, browser acceptance uses temporary config and `tests/fake-codex.py`; it requires Node.js, Python 3 and Playwright with Chromium:

```bash
FLOW_UI_BINARY="$PWD/bin/flow" node tests/ui-agent-sessions.cjs
```

Optional environment variables: `FLOW_PLAYWRIGHT_MODULE` (existing module location), `FLOW_CHROMIUM_PATH` (browser executable), `FLOW_UI_ARTIFACTS` (screenshots/results). The test starts a loopback server; a sandbox that forbids listening needs the normal tool approval mechanism for that test.

Distinguish unit/API tests, browser tests with a fake Codex, and real isolated Codex verification in reports. The adapter was exercised with `codex-cli 0.154.0-alpha.6.2` during the 2026-09-14 implementation; verify the currently resolved binary for later compatibility work.

`make build` produces a local binary. `make install` replaces `~/.local/bin/flow` and may stop/restart the scheduler; use it when installation is in the authorized task scope, not as an automatic consequence of source edits. Likewise, a development or documentation task does not itself authorize commit/push. Reuse authorization already given in the session.
