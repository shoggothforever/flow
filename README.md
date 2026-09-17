# flow

A personal command-line workflow accelerator. Bookmark project directories,
run favourite scripts, searches and parametrised commands, and automate recurring
tasks from one versionable configuration file.

```
flow project add  <alias> <path>          # bookmark a directory
flow jump         <alias>                 # cd into it (via shell wrapper)
flow script run   <alias> [-- args ...]   # named scripts (build/run/test/...)
flow cmd run      <alias> key=val ...     # parametrised commands ({{key}})
flow grep run     <name>                  # saved ripgrep/grep presets
flow              <alias> [args...]       # lazy dispatch — drops `<kind> run`
flow                                      # zero-arg unified picker
```

## Install

Build and install the binary:

```bash
make install                         # installs to ~/.local/bin/flow
```

Wire up the shell integration so `flow jump` actually changes your directory:

```bash
echo 'eval "$(flow shell init bash)"' >> ~/.bashrc      # bash
echo 'eval "$(flow shell init zsh)"'  >> ~/.zshrc       # zsh
```

Open a new shell or `source` your rc file to pick it up.

### Codex development skill

The repository backs up the Flow Codex skill in `skills/flow-cli/SKILL.md`.
After cloning, install it into your Codex user skills directory if you want
Codex to use the Flow-specific development guidance:

```bash
mkdir -p "$HOME/.codex/skills"
cp -R skills/flow-cli "$HOME/.codex/skills/"
```

`Agent.md` is a project-root archive of the same guidance. Keep it aligned
when changing the skill.

## Configuration

Single JSON file. Resolution order (first wins):

1. `--config /path/to/config.json` flag
2. `FLOW_CONFIG` environment variable
3. `~/.config/flow/config.json` (default)

The schema lives in `internal/config/schema.go`. A minimal example:

```json
{
  "schema_version": 2,
  "projects": [
    {"alias": "oev",  "path": "/dsm/oev"},
    {"alias": "cube", "path": "/dsm/oev/CubeSandbox", "tags": ["go","rust"]}
  ],
  "scripts": [
    {"alias": "build", "cwd": "@cube", "command": "make build"}
  ],
  "cmds": [
    {
      "alias": "k-logs",
      "template": "kubectl logs -n {{ns:default}} -l app={{app}} --tail={{tail:200}}"
    }
  ],
  "greps": [
    {"name": "todos", "pattern": "TODO", "paths": ["@cube"], "include": ["*.go","*.rs"]}
  ]
}
```

The leading `@` in `cwd` / `paths` is shorthand for "this project alias",
so when you move a checkout you only update one place (`flow project rm`/`add`).

## Commands

| Command                                | What it does                                                        |
| -------------------------------------- | ------------------------------------------------------------------- |
| `flow project add/list/rm/show`        | manage project bookmarks                                            |
| `flow jump <alias>`                    | print `__FLOW_CD__:<path>` for the shell wrapper to cd into         |
| `flow script add/list/rm`              | manage named scripts                                                |
| `flow script run <alias> [-- args...]` | run a script in its configured working directory                    |
| `flow chain <step>...`                 | run an ad-hoc sequence without saving a permanent script            |
| `flow cmd add/list/rm`                 | manage parametrised commands (`{{key}}` / `{{key:default}}`)        |
| `flow cmd run <alias> key=val ...`     | render and run; `--dry-run` prints without executing                |
| `flow grep save/list/rm`               | manage saved search presets                                         |
| `flow grep run <name>`                 | run rg (preferred) or grep with the saved options                   |
| `flow find [flags]`                    | locate files by name/type/mtime/size/regex (one-off query)          |
| `flow find save/list/show/rm`          | manage saved find presets                                           |
| `flow find run <name> [flags]`         | run a saved preset; any flag overrides the saved value              |
| `flow shell init bash\|zsh`            | print the shell wrapper enabling `flow jump`                        |
| `flow ui [--addr 127.0.0.1:7777]`      | start a local web UI to manage and run all resources in the browser |
| `flow run`                             | unified picker: pick a script / cmd / grep / find / jump            |
| `flow config path/edit/validate`       | inspect / edit / validate the config                                |
| `flow config migrate`                  | upgrade an older config to the current schema version               |
| `flow config export/import`            | save or restore a versionable config snapshot                       |

All commands honour `-v` for verbose logging on stderr.

### Version the config in this repository

Export the active personal config into the Flow source tree, then commit it with
the rest of the repository:

```bash
cd /path/to/flow
flow config export                 # writes ./flow.config.json
git add flow.config.json
git commit -m "update personal flow config"
```

`export` validates the active config and atomically updates only the JSON
snapshot. Scheduler history, locks, temporary files, and logs are not included.

On another machine, clone the source repository and import the tracked snapshot:

```bash
cd /path/to/flow
flow config import
flow config validate
flow scheduler install
```

`import` validates and migrates the snapshot before atomically replacing the
active config. Machine-specific project paths can then be adjusted with
`flow project rm` / `flow project add`.

## Behaviour notes

- **Cwd resolution**: any `cwd` field accepts `@alias`, a literal path,
  `~`-prefixed paths, and `$VAR` / `${VAR}` substitution.
- **Atomic writes**: every change goes through `Mutate(fn)` which loads the
  config under an `flock`, runs your function, validates, and persists via a
  temp-file rename. Concurrent invocations cannot corrupt the file.
- **Search backend**: `flow grep run` prefers `rg`, falls back to `grep`.

### Interactive picker

Don't want to memorise aliases? Most commands fall back to an in-terminal
list when you omit the name argument:

```bash
flow jump          # arrow-down to the project, Enter to cd
flow script run    # pick from your saved scripts and run
flow cmd run       # picks a cmd; you'll be prompted for any {{placeholders}}
flow grep run      # pick a saved grep preset
flow find run      # pick a saved find preset
flow run           # unified menu over scripts / cmds / greps / finds / jumps
flow               # same as `flow run` (the laziest entry point)
```

### Lazy dispatch (typing less)

Anywhere you would type `flow <kind> run <alias> [...]` you can drop the
`<kind> run` and just type the alias:

```bash
flow tail-cxb --grep phase,spawn-elite     # = flow script run tail-cxb --grep ...
flow build_cook                            # = flow script run build_cook
flow phase                                 # = flow grep run phase
flow recent-go --limit 50                  # = flow find run recent-go --limit 50
```

Flags pass through verbatim. Aliases are resolved against scripts → cmds →
greps → finds; if the same name is registered in multiple kinds
the CLI will tell you and ask you to use the explicit form. If the alias
doesn't exist, you get cobra's normal `unknown command "..."` error so
typos still surface fast.

### Ad-hoc chains

When you want to combine existing flow aliases for one-off execution without
saving a new script, use `flow chain`:

```bash
flow chain                                      # interactive picker: select steps one by one
flow chain restart_server build_cook
flow chain 'kill-port 8080' restart_server
flow chain -s 'kill-port 8080' -s 'tail-cxb --grep doors'
flow chain --keep-going build_cook buildES_cook restart_server
```

Each step re-invokes `flow`, so lazy dispatch, cmd prompts, grep filters,
and `--config` all behave the same as when run directly. Simple aliases can
be passed as separate positional steps; quote the whole step or use `--step`
when it needs its own args/flags. With no steps, `flow chain` opens a picker;
select resources one by one, then choose `[run]` to execute the selected chain.

Inside the picker:

| Key            | Action                                  |
| -------------- | --------------------------------------- |
| ↑ / k, ↓ / j   | move selection                          |
| PgUp / PgDn    | move by page                            |
| Home / End     | jump to first / last                    |
| Any letter     | append to the filter (fzf-style fuzzy)  |
| Backspace      | remove last filter character            |
| Ctrl-U         | clear filter                            |
| Enter          | confirm                                 |
| Esc / Ctrl-C   | cancel                                  |

The picker opens `/dev/tty` directly, so it still works when the
surrounding command's stdin/stdout are redirected. When no tty is
available (CI, scripts), commands print a clear error telling you to
pass the name explicitly, so existing automation never breaks.

### Web UI (`flow ui`)

If editing JSON or remembering aliases gets tedious, run:

```bash
flow ui                    # starts on 127.0.0.1:7777, opens your browser
flow ui --addr 127.0.0.1:8080 --no-open
```

HTML, CSS and JavaScript assets are embedded in the Go binary; the UI requires
no frontend build or external CDN.
It binds to loopback only (the server explicitly refuses non-loopback hosts
since there is no auth) and reads/writes the **same** config file the CLI
uses, so changes show up immediately in `flow project list` and friends.

Tabs cover projects, scripts, parametrised commands, grep/find presets, and
schedules. Each row has:

- **Edit** — opens an inline form (with `@alias` autocomplete on cwd/paths).
- **Delete** — removes the entry.
- **Run** (Scripts/Cmds/Greps/Finds) — executes the entry on the server and
  streams stdout/stderr back over Server-Sent Events into a log panel at
  the bottom of the page. Cmd placeholders are prompted for at run time.

Run output is streamed into independent tabs, so several commands can stay
visible without blocking resource management.

### TAPD requirements and Codex sessions

Open **需求与会话** in the UI, or visit `/#agent-sessions`. Add a TAPD URL and
associate an existing session by its full ID, unique prefix (at least eight
characters), or the searchable local session picker. Optional titles and notes
help you find the requirement later. Flow stores links; it does not sign into TAPD.

Entering the workspace preloads the local session picker. Reopening it displays
previous results immediately while checking for updates. Complete discovery results
are cached in memory for 30 seconds and concurrent readers share one scan. Use the
picker's **刷新列表** to bypass the cache; **刷新本机会话** also always scans fresh
before syncing relationships. Fork attempts invalidate the cache, including uncertain
outcomes. Prefix association/fork checks still use a fresh, complete inventory.

The tree shows a requirement, its independent sessions, and their fork descendants.
Click a node to copy the full ID or `codex resume <ID>`, rename its local display
label, or create a persistent **Fork**. Node labels use collision-aware ID prefixes;
stored IDs and copied commands always use full Codex **thread IDs**.
Large trees initially fold below the third session level. Use the node controls,
zoom buttons, fit, or drag the canvas; arrow keys navigate focused nodes.

**刷新本机会话** discovers external forks and updates availability. A session may
belong to multiple requirements; ordinary fork descendants inherit those memberships.
Internal Codex subagents are excluded. Unlink removes a branch only from the current
requirement and records an exclusion so future refreshes do not reattach it.
Re-linking explicitly restores that branch. Ancestors imported only for context are
shown with dashed borders. Missing local sessions keep their IDs and relationships.

```bash
flow agent-session tapd add 'https://tapd.example/story/123' --title 'Replay updates'
flow agent-session tapd list --json
flow agent-session tapd edit <requirement-id> --notes 'Follow-up scope'
flow agent-session list --local --json
flow agent-session link <session-id-or-prefix> --tapd <requirement-id>
flow agent-session sync
flow agent-session list --tapd <requirement-id> --json
flow agent-session show <session-id-or-prefix>
flow agent-session fork <session-id-or-prefix>
flow agent-session unlink <session-id-or-prefix> --tapd <requirement-id>
flow agent-session tapd rm <requirement-id>
```

The CLI and UI share `tapd_requirements` and `agent_sessions` in the active Flow
configuration, using the same file lock and atomic writes as other resources.
`flow config export/import` includes these records. No conversation transcript is
stored in Flow. Removing a requirement or association does not delete Codex sessions.

The adapter resolves `codex` from Flow's `PATH`, inherits its environment and
`CODEX_HOME`, and uses the app-server stdio protocol (`thread/list`, `thread/read`,
`thread/fork`). Fork inherits the source configuration and creates a persistent
conversation without starting a model turn. If Codex is missing, full IDs can still
be saved; discovery and fork require a compatible installed Codex. Integration was
verified with `codex-cli 0.154.0-alpha.6.2`.

Fork request records live in `<config-path>.agent-operations/`, separate from config
snapshots. `flow agent-session operations` lists them. To retry **saving** a recorded
child, reuse the printed request ID:

```bash
flow agent-session fork <parent-id> --request-id <request-id>
```

A lost RPC response is treated as unknown and never automatically forks again.
Refresh sessions, then explicitly match a candidate child in the UI's pending
operations panel. Matching verifies both the parent ID and the child's creation time.

HTTP endpoints mirror these operations:

| Endpoint | Methods / purpose |
| --- | --- |
| `/api/tapd-requirements` | GET snapshot, POST create (`url`, `title`, `notes`) |
| `/api/tapd-requirements/{id}` | PATCH metadata, DELETE requirement |
| `/api/tapd-requirements/{id}/sessions` | POST link (`session_id`) |
| `/api/tapd-requirements/{id}/sessions/{session-id}` | DELETE branch association |
| `/api/agent-sessions` | GET saved sessions and requirement trees |
| `/api/agent-sessions/{id}` | GET metadata, PATCH local `label` |
| `/api/agent-sessions/status`, `/discover`, `/operations` | GET Codex status, local candidates, fork records |
| `/api/agent-sessions/sync` | POST `{}` to discover and save relationships |
| `/api/agent-sessions/{id}/fork` | POST with a UUID `request_id` |
| `/api/agent-sessions/operations/{request-id}/resolve` | POST with the identified child `session_id` |

`GET /api/agent-sessions/discover?refresh=1` bypasses the discovery cache without
changing the Flow configuration.

### Locating files (`flow find`)

`flow grep` searches **inside** files; `flow find` locates the **files
themselves** by metadata.

```bash
# One-off queries
flow find --path @cube --type f --ext .go --modified-within 24h
flow find --path @cube --name "*_test.go"
flow find --path @cube --regex 'schema_v\d+\.go$'
flow find --path @cube --modified-after 2026-05-01 --modified-before 2026-05-28
flow find --path @cube --type f --size +1M --sort size --reverse

# Pipe-friendly
flow find --path @cube --ext .go --paths-only | xargs head -n1

# Save and rerun (any flag at run-time overrides the saved value)
flow find save recent-go --path @cube --type f --ext .go --modified-within 1d
flow find list
flow find show recent-go
flow find run recent-go
flow find run recent-go --modified-within 7d --limit 50
flow find rm recent-go
```

Filters that compose:

| Flag                                  | Meaning                                              |
| ------------------------------------- | ---------------------------------------------------- |
| `--path` / `-p` (repeatable)          | search root, accepts `@alias`                        |
| `--name <glob>` / `-n`                | basename glob, `*_test.go`                           |
| `--regex <re>`                        | RE2 against basename                                 |
| `--type f\|d\|l` / `-t`               | file / dir / symlink                                 |
| `--ext .go` (repeatable)              | extension whitelist                                  |
| `--modified-within 30m\|24h\|7d\|2w`  | relative window                                      |
| `--modified-after / --modified-before`| absolute `YYYY-MM-DD` (inclusive)                    |
| `--size +1M / -10K / 512`             | files only; `+` ≥, `-` ≤                             |
| `--exclude <pat>` (repeatable)        | extra excludes (in addition to `.git`, `node_modules`, ... ) |
| `--hidden`                            | include dotfiles & default skip list                 |
| `--sort mtime\|name\|size`            | sort key (default `mtime`, newest first)             |
| `--reverse` / `-r`                    | reverse sort                                         |
| `--limit N` / `-l`                    | stop after N hits                                    |
| `--paths-only`                        | print only paths (great with `xargs`/`fzf`)          |

## Development

```bash
make build       # ./bin/flow
make test        # go test ./...
make fmt vet
```

Session tests use an isolated fake stdio peer by default. The opt-in installed-Codex
test uses a temporary `CODEX_HOME` and synthetic history, and checks fork persistence
and resume without starting a model turn:

```bash
FLOW_CODEX_INTEGRATION=1 go test ./internal/agentsession -run TestInstalledCodexForkIntegration -v
```

Browser acceptance needs Node.js, Python 3 and an installed Playwright Chromium.
It starts a temporary loopback Flow server with a fake Codex executable and config;
it does not modify your personal Flow configuration or Codex history:

```bash
make build
FLOW_UI_BINARY="$PWD/bin/flow" node tests/ui-agent-sessions.cjs
```

Use `FLOW_PLAYWRIGHT_MODULE` for a Playwright module installed outside this repo,
`FLOW_CHROMIUM_PATH` for an existing Chromium executable, and `FLOW_UI_ARTIFACTS`
for screenshots and the test result JSON. If the default Go cache is read-only,
set `GOCACHE` to a writable temporary directory.

Layout of the code:

```
flow/
├── cmd/                 # cobra command tree (one file per resource)
├── internal/
│   ├── config/          # JSON schema, atomic store, flock, migration
│   ├── agentsession/    # TAPD associations, Codex discovery and persistent forks
│   ├── scheduler/       # recurring-task daemon and run history
│   ├── ui/              # embedded local web UI and JSON/SSE API
│   ├── picker/          # /dev/tty fuzzy picker
│   ├── shellintegr/     # bash / zsh wrapper functions (embedded)
│   ├── template/        # {{key}} / {{key:default}} renderer
│   ├── search/          # rg / grep backend selection + arg builder
│   ├── matcher/         # substring / prefix alias resolver
│   ├── errs/            # typed user/system errors + exit-code mapping
│   └── logx/            # slog wrapper, -v controls level
└── main.go
```

## Exit codes

| Code | Meaning                                                  |
| ---- | -------------------------------------------------------- |
| 0    | success                                                  |
| 1    | user error (bad args, missing entity, validation, etc.)  |
| 2    | system error (process execution, IO failure, etc.)       |
| 130  | interrupted by signal                                    |
