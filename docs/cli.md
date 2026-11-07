# Command line

`seelegent <command> [flags]`. The commands are thin: they talk to the daemon through `internal/client` and share
their logic with the MCP tools through `internal/ops` (see [mcp.md](mcp.md)). Code: `internal/cli`, one file per
group (`delegate.go`, `tasks.go`, `memory.go`, `system.go`, `doctor.go`, `daemon.go`, `mcp.go`, `setup.go`,
`migrate.go`); the checks of `doctor` are `internal/doctor`, the work of `setup` and `migrate` is `internal/setup`
and `internal/migrate` ([setup.md](setup.md)).

## Common rules

| | |
|---|---|
| `--json` | Prints the result as JSON on stdout, one shape per command (below). Errors then go to stdout too, as `{"error": "...", "code": "...", "details": {...}}`. Without `--json`, results go to stdout and errors to stderr as `Error: ...`. |
| Exit codes | `0` ok; `1` runtime or API error, refusal inside a worker, `doctor` with a failed check; `2` usage error (unknown command or flag, missing or wrong arguments); `3` `seelegent daemon` found the data directory locked by another daemon or its port taken. |
| Error codes | the API's code (`not_found`, `invalid_argument`, `bad_state`, ...), `not_available` (an option or feature of a later version), `forbidden` with `details.caller: "worker"`, `daemon_unavailable` (no daemon, another program on the port, a Seelegent daemon of another data directory on the port, a daemon that holds its lock without answering), `daemon_stopped` (the daemon was stopped during a wait and did not come back; the tasks keep running), `daemon_locked` (exit 3), `usage` (exit 2), `error`. |
| `@file` | A prompt, message, memory text or objection that starts with `@` is read from that file, relative to `--cwd` when the command has it, else to the current folder. |
| Flags | Normal flags (`--name value` or `--name=value`) after or between the arguments; `--` ends the flags. Unknown flags are an error, `run` included. |

Environment:

| Variable | Effect |
|---|---|
| `SEELEGENT_HOME` | Data directory (default `~/.seelegent`). |
| `SEELEGENT_PORT` | Daemon port when the settings do not set one (default 47821; development 47831). |
| `SEELEGENT_NO_AUTO_START=1` | Commands never start the daemon (except `restart`); they fail with a message naming the variable. |
| `SEELEGENT_WORKER=1` | Set by the daemon for its workers: see the worker guard. No command starts a daemon then. |
| `SEELEGENT_LEAD` | The lead sent with `delegate`/`run` (`session.lead`, named in the worker brief): `claude`, `codex`, `opencode`, `gemini`, `agy` or `cli`. Without it (or with another value) the host is detected as the MCP server and the old CLI did: `CLAUDE_CODE_SESSION_ID` → `claude` (so a `seelegent run` from Claude Code's shell names Claude Code), `CODEX_SESSION_ID`/`CODEX_THREAD_ID` → `codex`, `GEMINI_SESSION_ID`/`GEMINI_CLI_SESSION` → `gemini`, else `cli`. |
| `SEELEGENT_SESSION` | Lead session id sent with `delegate`/`run` (the host's own session ids are not sent). |

Starting the daemon: the task commands, `ui` and `memory` find the daemon and start it (detached, `seelegent
daemon --port <port>`) when it is not running, as the MCP server does. `status`, `stop` and `doctor` never start it.
`restart` always does, even with `SEELEGENT_NO_AUTO_START=1`. Commands that act through the daemon refuse to run
when `config.json` cannot be read ("the settings cannot be read: ..."); `status`, `stop` and `doctor` go on with
the defaults.

One daemon per data directory: the daemon's health answer names its data directory (`homeId`, a hash). A
Seelegent daemon of another data directory on this one's port is never used, adopted or stopped: every command
reports "port N is served by a Seelegent daemon of another data directory (pid X)" (`daemon_unavailable`) and
nothing is started. An older build that does not report its data directory is refused the same way, with "port N
is served by an older Seelegent build (pid X) that does not report its data directory: stop it with that build". `stop` also asks a daemon to stop only while this data directory's lock is held or its port
file exists.

### Worker guard (old B12)

With `SEELEGENT_WORKER=1`, `delegate`, `run`, `followup`, `review`, `cancel`, `memory append|replace`, `stop`,
`restart`, `ui`, `daemon` (`serve`), `setup` (a dry run too), `migrate` and `restore` print exactly `Seelegent is not available inside a Seelegent worker.` and exit 1 (with `--json`:
`{"error": that text, "code": "forbidden", "details": {"caller": "worker"}}`). The check runs before the flags are
parsed and before any call. `status`, `wait`, `task`, `ls`, `memory read`, `doctor`, `version`, `skill` and the
hidden `hook claude-agent` (Claude Code's reminder hook, which may run inside a worker and only prints advice) stay
allowed; the daemon's own guard decides what a worker may read.

## Commands

| Command | What it does | `--json` prints |
|---|---|---|
| `daemon [--port N] [--dev-origin URL]` (alias `serve`) | Runs the daemon in the foreground until Ctrl+C or `seelegent stop`. | – |
| `mcp` | The MCP server for a lead host ([mcp.md](mcp.md)). | – |
| `ui [--no-open]` | Starts the daemon if needed, opens the dashboard in the browser (Windows, macOS, Linux) and prints `Dashboard: <url>`. | `{dashboard}` |
| `status` | Daemon, backends, accounts (labels and quota, never e-mails) and the tasks that need you. Prints `not running` (exit 0) when no daemon runs; a daemon that holds its lock without answering, or another program on the port, is exit 1. | `{running, dashboard?, health?, backends: Backend[], accounts: Account[], activeTasks: TaskSummary[]}` |
| `stop` | Asks the daemon to shut down (`POST /api/v1/daemon/stop`) and waits up to 15 s until its lock is free and its port silent; prints `stopped` or `not running`. Open waits are answered at once (`daemon_stopping`), so this takes about a second, and a lead that was waiting does not start the daemon again (see waits below). Removes a port file left by a daemon that is gone. A daemon that holds its lock but does not answer is reported with its pid (when recorded) and the log path, exit 1. Nothing is ever killed by a pid read from a file (old B3, B14). | `{ok, stopped}` |
| `restart` | `stop`, then start the daemon and wait until it answers. Prints `Daemon restarted.` | `{ok: true, restarted: true}` |
| `delegate [flags] <prompt\|@file>` | Creates a task and waits for its report. Flags: `--cwd`, `--title`, `--backend`, `--provider`, `--model`, `--effort`, `--mode full\|plan`, `--profile`, `--spec`, `--task T001,T002` (needs `--spec`), `--after t1,t2`, `--after-mode accepted\|done`, `--allow-git`, `--no-gate`, `--no-lane`, `--wait N` (default 600; 0 returns at once; a task with `--after` does not wait unless `--wait` is given). | `TaskReport` |
| `run [flags] <prompt\|@file>` | Legacy: `delegate` with a 3600 s wait; always prints the report JSON. Same flags as `delegate` except `--wait`. | `TaskReport` |
| `followup <id> [flags] <message\|@file>` | Sends the next instruction and waits for the report. Flags: `--by lead\|user`, `--model`, `--effort`, `--task`, `--profile`, `--allow-git`, `--force` (start a task waiting for its prerequisites; the message may then be empty), `--no-gate`, `--no-lane`, `--wait N` (default 600). | `TaskReport` |
| `wait <id...> [--any] [--seconds N] [--no-wake]` | Waits (default 600 s) and prints the reports. `--no-wake` keeps waiting when a task needs the lead. | `WaitResult` with `reports` |
| `review <id> accepted\|rejected [--notes T] [--answer-objection T\|@file]` | Records the verdict; the answer to an objection is appended to the notes. | `TaskSummary` |
| `cancel <id> [--wait N]` | Cancels and waits up to N s (default 15, max 60) for the worker to stop. | `TaskReport` read after the cancel (`cancelling: true` while it stops) |
| `task <id> [--steps N] [--full]` | The report, with the last N worker tool steps; `--full` the whole task with its turns. | `TaskReport` + `recent_steps`, or `TaskDetail` + `recent_steps` |
| `ls [--active] [--blocked] [--project P] [--limit N]` | Tasks, newest first (limit 1..200, default 50). | `TaskPage` |
| `restore <id> --turn N [--file=<path>]...` | Puts back the content files had when turn N started, from its snapshot ([safety.md](safety.md)); without `--file`, every work-at-risk file of the turn not restored yet. Paths are relative to the repository root. Prints `restored`/`skipped` lines. Refused while the task is active. | `RestoreResult` |
| `memory read\|append\|replace --cwd D [text\|@file]` | A project's notes for workers. | `Memory` |
| `doctor` | Checks the installation (below); exit 1 when a check fails. | `[{name, status: ok\|warn\|fail, message, fix?}]` |
| `version` | Version, commit and build date. | `buildinfo.Info` |
| `setup [--host a,b] [--dry-run] [--remove] [--no-skill] [--hooks] [--force] [--exe path]` | Registers Seelegent with the lead hosts on this machine ([setup.md](setup.md)). Exit 1 when an item is `conflict` or `error`; with `--json` the report is then the only document printed. | `setup.Report` |
| `skill` | Prints the lead skill `setup` installs. | `{name, version, file, content}` |
| `migrate [--from dir] [--dry-run]` | Imports the old (Node) Seelegent's data ([setup.md](setup.md)). Exit 1 when an item is `error`; with `--json` the report is then the only document printed. | `migrate.Report` |

Waits (`wait`, `delegate`, `followup`, `run`; the same code as the MCP tools, `ops.WaitReports`):

- Long waits are split into calls of at most 3000 s (the API's limit).
- A connection lost without a word (the daemon crashed or was killed) is ridden out: the daemon is found or started
  again and the wait goes on, all within the wait's own time.
- A daemon that answers `503 daemon_stopping` (it was stopped, or Ctrl+C) is only looked for again, never started,
  for up to 40 s (bounded by the wait's own time): after `seelegent restart` the wait goes on against the new
  daemon; after a plain `stop` it ends with `daemon_stopped` ("the Seelegent daemon was stopped during the wait; the
  tasks keep running: wait again later"), exit 1. Nothing in that wait starts a daemon until a daemon is found
  again; from then on the rules above apply again (a later crash is ridden out, a later stop starts a new grace).

### Not in this version

There are no `meeting`, `consult`, `issues`, `notes`, `automations`, `contest`, `proof` or `update` commands.
Options of later milestones keep their old names but are
refused before anything is sent, with exit 1 and code `not_available`: `--gate <cmd>` (`--no-gate` is accepted),
`--lane <paths>` (`--no-lane` is accepted), `--force-lane`, `--verify`, `--member`, and the contest options
`--variants`, `--variant`, `--judge`, `--no-judge`, `--deadline`.

## doctor

It changes nothing: it never starts the daemon, never migrates the database (it opens it query-only through
`store.Inspect`), never touches a running daemon's data directory, and never prints an e-mail address or key. Only
when no daemon of the data directory runs does it check writability by writing a probe file
(`.doctor-write-test`, atomically) and removing it again. Checks, in order:

| Name | ok | warn | fail |
|---|---|---|---|
| `data_dir` | exists, and writable (probe) or in use by its running daemon (nothing written) | does not exist yet | not a folder, or not writable |
| `settings` | `config.json` loads (or is absent) | unknown keys | cannot be read |
| `database` | schema current, or no database yet | older schema (the daemon migrates it) | newer schema, or cannot be opened |
| `daemon` | answers (version, address, pid) | not running; or another version than this command | holds its lock without answering; another program, or another data directory's daemon, on the port |
| `port_file` | no stale port file | a port file left by a daemon that is gone (never a fail) | – |
| `dashboard` | embedded | not part of this build | – |
| `git` | found (the `gitx` resolver) | – | not found |
| `backend_<id>` | enabled and its CLI found (`backend.Find`), or disabled | – | enabled but not found |
| `accounts` | counts: total, enabled and (daemon running) usable | no enabled account, or counts unknown | – |
| `path` | no relative PATH entries | relative entries (a worker would resolve them in the project folder) | – |

There is no host registration check (MCP entries, skills) yet; `seelegent setup --dry-run` shows what is
registered.

## Decisions

- `status` does not start the daemon (the old one did): it reports state, so `stop` then `status` shows `not
  running`.
- `stop` prints `stopped`, not the old `stopped <pid>`, and never signals a process: the old CLI killed the pid in
  `daemon.pid`, which could hit an unrelated process after a reboot (B3/B14).
- The old `run` parsed its own flags (`--k v` pairs went straight into the request, unknown ones too, and `@file`
  was read relative to the process folder only). It is now `delegate` with normal flags and `@file` relative to
  `--cwd`.
- With `--json` every command prints one documented shape; the old CLI printed whatever the operation returned.
- Exit code 2 for usage errors (the old CLI used it too); cobra's own errors (unknown command or flag) are mapped
  onto it.
- After `daemon_stopping` a wait looks for a returning daemon for 40 s, not for the rest of its own time: a plain
  `stop` should end a lead's 600 s wait within a minute with a clear error, while a `restart` (its stop takes a
  second or two, the start up to 20 s) still fits. A restart that takes longer ends the wait with
  `daemon_stopped`; the task is not touched, so `wait` again picks it up.

## Tests

`internal/cli/commands_test.go` runs every command in process (`run` with test dependencies) against a real daemon
built through `internal/app` with the fake worker CLI, a temp `SEELEGENT_HOME` and a random port; `stop` and
`restart` run a real `seelegent daemon` in the test process, also with a wait open (stop is quick and starts
nothing; a restart carries the wait on), and home B is pointed at home A's daemon (refused, never stopped).
`cli_test.go` covers exit codes and usage errors.
