# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

See [Agent.md](Agent.md) for the archived Flow development skill, including the TAPD/Codex session feature and its validation workflow.

## Build / test / run

```bash
make build                    # produces ./bin/flow (with -ldflags version/commit/date)
make install                  # installs to ~/.local/bin/flow; restarts the systemd --user
                              # `flow-scheduler.service` if it was running so the new binary
                              # takes effect.
make test                     # go test ./...
make fmt vet tidy clean

# Single test / package
go test ./internal/config/... -run TestStoreLoad
```

## Big-picture architecture

flow is a Cobra CLI that bundles project bookmarks, scripts, parametrised cmds, grep/find presets, and a recurring-task scheduler — all driven from one JSON file.

### Args rewrite happens before Cobra

`main.go` calls `cmd.RewriteArgs()` *before* `cmd.Execute()`. `cmd/dispatch.go` mutates `os.Args`:

- `flow` (no args) → `flow run` (the unified picker).
- `flow <token> ...` where `<token>` is a saved alias → `flow <kind> run <token> ...`. Resolution precedence: `script > cmd > grep > find` (defined in `kindOrder`). Collisions print a typed user error and exit 1.
- The rewriter does its own minimal flag parse (`-v`, `--config[=<path>]`) so it can load the config the user is about to use without depending on Cobra.

When adding a new resource kind that should support lazy dispatch, register a `Find<Kind>` lookup on `*config.Config` and add the kind to `kindOrder`/`lookupAlias` in `dispatch.go`.

### Config is a single JSON document, mutated under flock

`internal/config/schema.go` defines `Config` (Projects, Scripts, Cmds, Greps, Finds, Schedules) and `CurrentSchemaVersion`. `internal/config/store.go` resolves the path (flag → `FLOW_CONFIG` → `~/.config/flow/config.json`), reads, and persists.

Every mutation goes through `Store.Mutate(fn)`:

1. Acquire an advisory `flock` on the file.
2. Load + decode.
3. Run `fn(*Config)`.
4. Validate.
5. Write to `<path>.tmp` and `os.Rename` over the original.

CLI commands and the UI server share this path, so concurrent edits cannot corrupt the file. `migrate.go` upgrades older schema versions; bump `CurrentSchemaVersion` and add a migration step for non-additive changes.

### `@alias` cwd / path resolution

Any field that takes a path (`Script.Cwd`, `CmdAlias.Cwd`, `GrepPref.Paths`, `FindPref.Paths`, …) accepts:

- `@<alias>` — resolved against `Config.Projects` so moving a checkout only requires `flow project rm`/`add`.
- `~`, `~user`, `${VAR}` / `$VAR` — standard shell expansion.
- A literal path.

When adding a new path-bearing field, route it through the same resolver helpers in `cmd/helpers.go` so it picks up `@alias` and env expansion automatically.

### Errors and exit codes

`internal/errs` defines `errs.User(...)`, `errs.System(...)`, `errs.Interrupted(...)`. `cmd/root.go::Execute` formats with `errs.Format` and exits via `errs.ExitCode`:

| Code | Meaning |
| ---- | ------- |
| 0    | success |
| 1    | user error (bad args, missing entity, validation, missing prerequisite) |
| 2    | system error (exec/IO failure) |
| 130  | interrupted by signal |

User errors should always carry a `Hint:` clause — see existing call sites for the convention.

### Scheduler re-execs flow

`internal/scheduler` is a single-process daemon (one per config file, guarded by flock). On each tick it reloads the config so UI/CLI edits land within ~5 s without restart, computes next-fire times via `internal/cron`, and **re-execs** `flow <kind> run <target>` rather than importing runner packages. This keeps the daemon small and means new flags/fixes flow through automatically. History is appended to `history.jsonl` (fsync after each line).

`make install` stops the systemd unit before swapping the binary and restarts it after — preserve that behaviour when changing the install flow.

### UI server

`internal/ui/server.go` embeds `static/` via `go:embed`, exposes `/api/{config,meta,projects,scripts,cmds,greps,finds,schedules}` for CRUD and `/api/run/...` (Server-Sent Events) for streaming exec output. It refuses non-loopback `addr` values — the loopback assertion is intentional because there is no auth.

The UI shares `*config.Store` with the CLI, so all writes go through the same flock + atomic-rename path.

### Picker

`internal/picker` opens `/dev/tty` directly (so it works under stdin/stdout redirection) and falls back with a clear error when no tty is available. Used wherever an alias positional is omitted; the same component drives `flow run`, `flow chain`, and per-resource pickers.

### Shell integration

`flow jump` prints a sentinel line `__FLOW_CD__:<path>`. The bash/zsh wrapper functions emitted by `flow shell init <shell>` (embedded in `internal/shellintegr`) parse that sentinel and `cd` for the user. Without sourcing the wrapper, `flow jump` is just a path printer.

## Layout

```
cmd/                 cobra command tree (one file per resource + dispatch.go for lazy alias rewriting)
internal/config/     JSON schema, atomic Store, Mutate(), migration
internal/scheduler/  recurring-task daemon, history.jsonl
internal/cron/       cron expression parser
internal/ui/         embedded static page + JSON/SSE API
internal/picker/     /dev/tty fuzzy picker
internal/finder/     `flow find` filesystem walker
internal/search/     `flow grep` rg/grep backend selection
internal/template/   {{key}} / {{key:default}} renderer with positional fills
internal/shellintegr/embedded bash/zsh wrappers
internal/matcher/    substring/prefix alias resolution
internal/errs/       typed errors + exit-code mapping
internal/logx/       slog wrapper, -v controls level
```
