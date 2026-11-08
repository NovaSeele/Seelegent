# MCP server

`seelegent mcp` is the MCP server a lead host (Claude Code, Codex, Antigravity, OpenCode) starts. It speaks MCP over
stdin and stdout and forwards every tool to the daemon's HTTP API v1 (see [api.md](api.md)). It holds no state of
its own: tasks, accounts and memory live in the daemon, so a host restart loses nothing.

Code: `internal/mcpserver` (tools), `internal/client` (typed API client, discovery and autostart),
`internal/ops` (what the tools share with the command line: the worker guard table, the lead session, the tasks
that need the lead, recent steps, the wait that survives a restart, the `not_available` refusal, naming a repo
and an issue),
`internal/cli/mcp.go` (the command). The tool list is `internal/mcpserver/tools.json`, embedded in the binary. The
command line ([cli.md](cli.md)) offers the same operations as commands.

## Starting it from a host

```json
{ "mcpServers": { "seelegent": { "command": "seelegent", "args": ["mcp"] } } }
```

or `claude mcp add seelegent -- seelegent mcp`. stdout carries only MCP messages; logs go to stderr (the server's
own lines, and only warnings and errors of the MCP SDK).

| Environment | Effect |
|---|---|
| `SEELEGENT_HOME` | Data directory (default `~/.seelegent`). Passed on to a daemon the server starts. |
| `SEELEGENT_PORT` | Daemon port when the settings do not set one (default 47821; development uses 47831). |
| `SEELEGENT_NO_AUTO_START=1` | Never start the daemon; tools fail with a message saying so. |
| `SEELEGENT_WORKER=1` | Set by the daemon for its workers. The server never starts a daemon and refuses mutating tools (see the worker guard). |
| `SEELEGENT_SESSION`, else `CLAUDE_CODE_SESSION_ID` | Lead session id sent with `delegate` (`session.id`). |
| `CLAUDE_CODE_HOST_SESSION_ID` | Host session id sent with `delegate` (`session.hostId`). |
| `SEELEGENT_LEAD` | Lead host sent with `delegate` (`session.lead`: `claude`, `codex`, `opencode`, `gemini`, `agy` or `cli`); the worker brief says "The lead for this task is Claude Code." and so on. Without it (or with another value) the host is detected: `CLAUDE_CODE_SESSION_ID` → `claude`, `CODEX_SESSION_ID`/`CODEX_THREAD_ID` → `codex`, `GEMINI_SESSION_ID`/`GEMINI_CLI_SESSION` → `gemini`, else `cli`. |

The server reads `config.json` without writing it. If it cannot be read, the server still starts and every
available tool answers with "the settings cannot be read: ..." so the host sees why.

## One daemon per data directory

`seelegent daemon` takes `<home>/run/daemon.lock` before it opens anything else (log, store, recovery) and holds it
until it exits. A second daemon on the same home waits up to 3 s (a restarting daemon releases the lock once it has
closed), then exits with code 3 and "another Seelegent daemon is already running for <home> (port N)". While it
listens, the daemon records its port (and, on a second line, its pid) in `<home>/run/daemon.port`; it removes the
file when it stops cleanly. The pid is only shown in messages (`seelegent stop`, `doctor`); nothing kills a process
by it. `seelegent stop` asks the daemon to shut down through `POST /api/v1/daemon/stop`.

## Finding and starting the daemon

1. If `run/daemon.port` exists and a daemon holds `run/daemon.lock`, probe that port first. A port file whose lock
   is free (the daemon crashed) is ignored, and a port that does not answer, or answers as something else, falls
   back to step 2. So a daemon started with `--port`, or before the port setting changed, is found.
2. Probe `GET /api/v1/health` on the configured port. A daemon that answers `ok` with `apiVersion: 1` and this data
   directory's `homeId` is used. Anything else that answers (another program, an older Seelegent, another API
   version, a Seelegent daemon of another data directory: "port N is served by a Seelegent daemon of another data
   directory (pid X)", or an older build without `homeId`: "... an older Seelegent build (pid X) that does not
   report its data directory: stop it with that build") is reported, never used or stopped, and nothing is started.
   A port that accepts but does not answer within 1.5 s is reported as a busy or hung daemon (or another program),
   and nothing is started either.
3. When nothing answers, take `<home>/run/start.lock`, probe again (another MCP server may have just started it).
   While another daemon holds `run/daemon.lock` without answering yet (still starting, or shutting down), keep
   probing and use it as soon as it answers; nothing is started while the lock is held, and a holder that never
   answers ends with an error that says where it is. Once the lock is free, start `seelegent daemon --port <port>`
   detached, hidden, in the data directory, and poll health. One 20 s deadline covers the whole step: waiting for
   `start.lock`, for the lock holder and for the started daemon to answer; a `start.lock` still held at the end
   gives "another process has been starting the daemon for 20 s" and where the daemon is. A daemon that exits first
   points at `logs/daemon.log`. One that exits with code 3 (another daemon took the lock meanwhile) leads to the
   running daemon when it answers, else to "the daemon for <home> runs on port X; this MCP looked on port Y", or
   "the previous daemon was still shutting down; try again shortly".

Inside a worker (`SEELEGENT_WORKER=1`) step 3 never happens: a daemon started there would live in the worker's
process tree, on the worker profile's data directory. The error says the daemon running the worker cannot be reached.

Discovery starts in the background when the server starts and is retried on each tool call, so a daemon that was
stopped or restarted is found again. `wait`, `delegate` and `followup` also ride out a daemon restart in the middle
of a wait: an unreachable, not yet running or slow daemon is retried until the wait's own deadline. Finding or
starting the daemon again counts against that time and is cut off at the deadline, and the daemon found is asked
to wait only for what is left, so a restart never stretches the wait past `wait_seconds`; a daemon that is not
back by then ends the call with `daemon_unavailable`.

That holds for a connection lost without a word (a crash). A daemon that is shutting down (`seelegent stop`,
`restart`, Ctrl+C) answers the open wait with `503 daemon_stopping` instead; the wait then only looks for a daemon
that comes back (never starts one) for up to 40 s within its own time. After a restart it goes on against the new
daemon; after a plain stop the call ends with code `daemon_stopped` ("the Seelegent daemon was stopped during the
wait; the tasks keep running: wait again later"). Nothing in that wait starts the daemon until a daemon is found
again; from then on the usual rules apply again (a later crash is ridden out, a later stop starts a new grace).
The waits of `meeting` (`wait`, and `open` with `wait_seconds`) and `contest` (`wait`, and `delegate` with variants
and `wait_seconds`) follow the same rules.

## Tools

`tools/list` returns 17 tools, the same names and input schemas as the old Node server (sorted by name, as the SDK
does), with two additions: `gate` of `delegate` and `followup` also takes `true`, and `proof` has the actions
`cancel_run` and `contention`. Every tool is built (milestone 15 added `issues` and `automations`); the
"Not available in this version of Seelegent yet." mechanism stays for a daemon route that answers 501.

| Tool | Routes | Now |
|---|---|---|
| `delegate` | `POST /tasks`, then `POST /wait?cwd=`; with `variants` or `variant_specs`: `POST /contests`, then `GET /contests/{id}/wait` | yes. `member` runs the task as that member ([team.md](team.md)). `variants`/`variant_specs` start a contest, with `judge` (a member id, or `false` for machine scoring only) and `deadline_minutes`; a contest cannot take `after`. `gate` (a command, a list, `true` for the detected commands, `false`), `lane` (paths, or `false` for `noLane`), `force_lane` and `verify` map onto `gate`/`gateCommands`/`lane`/`noLane`/`forceLane`/`verify` ([gates.md](gates.md)) |
| `followup` | `GET /tasks/{id}` (only to check `provider`), `POST /tasks/{id}/followup`, `POST /wait` | yes; `member` moves the task to that member; `gate: []` clears the lead's gate commands (sent as `gateCommands: []`), so the spec's frozen `Gate:` lines or the detected commands run again |
| `wait` | `POST /wait` (`reports: true`) | yes |
| `review` | `POST /tasks/{id}/review` | yes; `answer_objection` is sent as `answerObjection`, which accepting a task a peer objected to needs; a senior's objection comes back as a `Warning:` line |
| `cancel` | `POST /tasks/{id}/cancel` | yes; 202 is reported as `cancelling` |
| `task` | `GET /tasks/{id}/report` (or `GET /tasks/{id}` with `full`), `GET /tasks/{id}/events?tail=` | yes |
| `status` | `GET /accounts`, `GET /tasks?status=`, `GET /backends` | yes |
| `memory` | `GET`, `POST` (append), `PUT` (replace) `/memory` | yes |
| `profiles` | `GET /roles`, `POST /roles`, `DELETE /roles/{id}`, `GET /roles/skills` | yes |
| `members` | `GET /members` | yes: id, name, backend, model, tier, rank, provider, role, enabled and why a member cannot be used |
| `proof` | `GET /proof/index`, `GET /proof/matrix`, `POST /proof/run` (then `GET /proof/runs/{id}` every 500 ms while `wait_seconds` lasts, at most 3000 s), `GET /proof/runs/{id}`, `POST /proof/runs/{id}/cancel`, `GET /proof/stats`, `GET /lanes`, `GET /contention` | yes: actions `matrix` (needs `feature`), `index`, `run` (`feature?`, `force?`, `wait_seconds?`, default 0), `run_status` and `cancel_run` (need `run_id`), `stats` (`since?`), `lanes`, `contention`; `cwd` defaults to the server's folder. `run` and `cancel_run` are mutating for the worker guard |
| `meeting` | `/meetings*` | yes: `open` (`estimate_only` answers the run estimate; `wait_seconds` default 0), `status`, `wait` (default 600, at most 1500), `list`, `post` (`answers`, `note`), `decide`, `act` (`items` become tasks), `cancel`, `dissent_outcome`. The slim view carries the minutes and the pending questions; `full` returns the whole record |
| `consult` | `POST /tasks/{id}/consult`, then `GET /tasks/{id}` every second while `wait_seconds` lasts (default 600) | yes: a senior or peer member reads the finished task read-only and answers a verdict with findings |
| `notes` | `/tasks/{id}/notes*` | yes: `list`, `add` (`file`, `line`, `end_line?`, `side?`, `body`; by the lead), `resolve`, `reopen`, `delete`, `send` (the drafts and unaddressed notes as one followup, `extra` after them; then waits like `followup`) |
| `contest` | `/contests*` | yes: `status` (all contests without `contest_id`), `wait` (in daemon waits of at most 1500 s), `pick` (`winner`), `cancel`; `full` returns the whole record |
| `issues` | `/github/repos*`, `/github/tickets*`, `GET /github/access` | yes ([github.md](github.md)): `repo` is a repo id or a connected repository's folder (matched with `fsutil.Key`); `key` a bare number means `gh#n`. `list`, `add`, `suggest` and `triage` answer the old slim issue (`key`, `title`, `labels`, `suggestion`, `decision`, `ticket` as id and status), the ticket actions the old slim ticket (checks as state, head and failing names, and `refused`: why the last `pr` stopped); `full: true` returns the records. `triage` sends `by: "lead"`; `decision` becomes `action`, `gate`/`lane` map as in `delegate`. `triage` assign, `add_task`, `retry` (a failed ticket's task again) and `fix_checks` wait like `delegate` only when `wait_seconds` is given (default 0) and then add the report as `task`. `repo_settings` without `shared_dirs` reads the setting. No action reaches the token |
| `automations` | `/automations*` | yes ([automations.md](automations.md)): `create` never enables (the daemon creates every automation disabled); `enable` is `POST /automations/{id}/enable`, which only records the request and answers `{automation, confirm, confirmUrl, message}`: the user confirms in the dashboard. `disable` is `PATCH {enabled: false}`, which also withdraws a request. `update` sends only the arguments given; `min_remaining` and `use_before_reset` keep the other quota field. `runs` limit 1..200 (default 20). The slim view is the old one plus `enableRequested` |

Argument handling follows the old server, with one change: an argument the tool's input schema does not have is
refused with a tool error (`invalid_argument`) that names it and lists the valid ones (the old server ignored it, so
`tier: "strong"` ran on the default tier; tiers go in `model`). The schemas do not say `additionalProperties: false`:
hosts that pass tool schemas through the Gemini API (agy, Antigravity, Gemini CLI) have rejected that keyword, and
one refused schema hides every tool on that host. Numbers
and booleans may arrive as strings, a single string is accepted where a list is expected, wait seconds are clamped
(`delegate`/`followup`/`wait` 0..3000, default 600, and 0 for a `delegate` with `after`; `cancel` 0..60, default 15).
`mode: full` maps to the API's `edit`. `lane: false`, `verify: false` and `force_lane: false` mean "off" and are
accepted. Relative paths resolve against the server's working directory. `followup`, `wait` and `task` build reports
with the task's own project, not the server's working directory.

## Results

- `content[0]`: the result as JSON text, the shape the old server returned (a `TaskReport` for the task tools).
- `content[1]`: a one- or two-line summary.
- `structuredContent`: the same JSON (a list is wrapped as `{"items": [...]}`).

Before `delegate` starts waiting it tells the host the new task id: a progress notification when the call carries a
progress token, and a log message (`notifications/message`, `{task_id, status, message}`) when the host asked for
log messages. A host that cancels the wait still knows the task.

Errors are tool results with `isError: true`. Their text starts with "Seelegent error: " as before, and the
structured content is `{error: {code, message, status, details}}`:

| Case | Text |
|---|---|
| Called from inside a worker (local guard, or the daemon answers 403 with `caller: worker`) | exactly "Seelegent is not available inside a Seelegent worker." (no prefix) |
| Route, tool or option not built yet (501, or refused locally) | "Seelegent error: ... is not available in this version of Seelegent yet; nothing was done (...)" (code `not_available`) |
| Other API error | "Seelegent error: <message>" with the API's code and details |
| No daemon, foreign program (or another data directory's daemon) on the port, busy daemon, start failed | the reason and what to do (code `daemon_unavailable`) |
| The daemon was stopped during a wait and did not come back | "Seelegent error: the Seelegent daemon was stopped during the wait; ..." (code `daemon_stopped`) |

## Worker guard

Two layers (contract §1.3). The daemon is the authority: it recognises a caller inside a worker's process tree and
answers 403 with `details.caller: "worker"`, which the server shows as the old refusal text. The server also keeps
the old local guard (`ops.Mutating`, called from `internal/mcpserver/guard.go`): when `SEELEGENT_WORKER=1` is in its environment (the daemon
sets it for every worker, so an MCP server a worker starts inherits it), it refuses mutating calls before talking to
the daemon, with the same text and `{error: {code: "forbidden", details: {caller: "worker"}}}`:

- always: `delegate`, `followup`, `review`, `cancel`, `consult`;
- `meeting` unless the action is `status`, `list` or `wait`; `issues` unless `repos`, `list`, `tickets` or
  `ticket`; `memory` unless `read`; `notes` unless `list`; `automations` unless `list` or `runs`; `contest` unless
  `status` or `wait`;
- `profiles` with `upsert` or `delete`; `proof` with `run` or `cancel_run`.

`wait`, `task`, `status`, `members` and the read actions go on to the daemon, which decides what a worker may see.

## Cancellation

When the host sends `notifications/cancelled`, the tool's context ends: the HTTP call or the wait stops and the tool
returns. Cancelling a tool call never cancels a task, not even the one `delegate` just created; use the `cancel` tool
for that.

## Tests

`internal/mcpserver/unit_test.go` checks the tool schemas against the inventory, the argument-to-request mapping
against a stub API, the locally refused options, the task id announcement and the local worker guard
(`guard_test.go` pins the old `isMutating` table). `e2e_test.go` runs the tools over in-memory MCP transports
against a real daemon built through `internal/app` with the fake worker CLI and a temp data directory.
`internal/client` tests discovery and start against fake daemons: several servers racing to start one daemon, the
port file, stale port files, waiting for a daemon that holds the lock, and a started daemon that exits for the lock.
`internal/cli` runs the mcp command (autostart off, and inside a worker), two daemons on one data directory, and
every command of [cli.md](cli.md).
