# MCP server

`seelegent mcp` is the MCP server a lead host (Claude Code, Codex, Antigravity, OpenCode) starts. It speaks MCP over
stdin and stdout and forwards every tool to the daemon's HTTP API v1 (see [api.md](api.md)). It holds no state of
its own: tasks, accounts and memory live in the daemon, so a host restart loses nothing.

Code: `internal/mcpserver` (tools), `internal/client` (typed API client, discovery and autostart),
`internal/cli/mcp.go` (the command). The tool list is `internal/mcpserver/tools.json`, embedded in the binary.

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

The server reads `config.json` without writing it. If it cannot be read, the server still starts and every
available tool answers with "the settings cannot be read: ..." so the host sees why.

## One daemon per data directory

`seelegent daemon` takes `<home>/run/daemon.lock` before it opens anything else (log, store, recovery) and holds it
until it exits. A second daemon on the same home waits up to 3 s (a restarting daemon releases the lock once it has
closed), then exits with code 3 and "another Seelegent daemon is already running for <home> (port N)". While it
listens, the daemon records its port in `<home>/run/daemon.port`; it removes the file when it stops cleanly.

## Finding and starting the daemon

1. If `run/daemon.port` exists and a daemon holds `run/daemon.lock`, probe that port first. A port file whose lock
   is free (the daemon crashed) is ignored, and a port that does not answer, or answers as something else, falls
   back to step 2. So a daemon started with `--port`, or before the port setting changed, is found.
2. Probe `GET /api/v1/health` on the configured port. A daemon that answers `ok` with `apiVersion: 1` is used.
   Anything else that answers (another program, an older Seelegent, another API version) is reported and nothing is
   started. A port that accepts but does not answer within 1.5 s is reported as a busy or hung daemon (or another
   program), and nothing is started either.
3. When nothing answers, take `<home>/run/start.lock`, probe again (another MCP server may have just started it).
   While another daemon holds `run/daemon.lock` without answering yet (still starting, or shutting down), keep
   probing for up to 20 s and use it as soon as it answers; nothing is started while the lock is held, and a holder
   that never answers ends with an error that says where it is. Once the lock is free, start
   `seelegent daemon --port <port>` detached, hidden, in the data directory, and poll health for up to 20 s. A
   daemon that exits first points at `logs/daemon.log`. One that exits with code 3 (another daemon took the lock
   meanwhile) leads to the running daemon when it answers, else to "the daemon for <home> runs on port X; this MCP
   looked on port Y", or "the previous daemon was still shutting down; try again shortly".

Inside a worker (`SEELEGENT_WORKER=1`) step 3 never happens: a daemon started there would live in the worker's
process tree, on the worker profile's data directory. The error says the daemon running the worker cannot be reached.

Discovery starts in the background when the server starts and is retried on each tool call, so a daemon that was
stopped or restarted is found again. `wait`, `delegate` and `followup` also ride out a daemon restart in the middle
of a wait: an unreachable, not yet running or slow daemon is retried until the wait's own deadline.

## Tools

`tools/list` returns 17 tools, the same names and input schemas as the old Node server (sorted by name, as the SDK
does). Tools that are not built yet are listed with a description starting "Not available in this version of
Seelegent yet." and answer with a tool error that says nothing was done.

| Tool | Routes | Now |
|---|---|---|
| `delegate` | `POST /tasks`, then `POST /wait?cwd=` | yes, except `variants`/`variant_specs` (contests), gate commands or `gate: true`, lane paths, `verify: true`, `force_lane: true` and `member`: refused before any call (`not_available`) |
| `followup` | `GET /tasks/{id}` (only to check `provider`), `POST /tasks/{id}/followup`, `POST /wait` | yes, same limits as `delegate` (`member` aside) |
| `wait` | `POST /wait` (`reports: true`) | yes |
| `review` | `POST /tasks/{id}/review` | yes; `answer_objection` is appended to the notes |
| `cancel` | `POST /tasks/{id}/cancel` | yes; 202 is reported as `cancelling` |
| `task` | `GET /tasks/{id}/report` (or `GET /tasks/{id}` with `full`), `GET /tasks/{id}/events?tail=` | yes |
| `status` | `GET /accounts`, `GET /tasks?status=`, `GET /backends` | yes |
| `memory` | `GET`, `POST` (append), `PUT` (replace) `/memory` | yes |
| `profiles` | `GET /roles`, `POST /roles`, `DELETE /roles/{id}`, `GET /roles/skills` | yes |
| `members` | `/members*` (501) | not yet |
| `proof` | proof gate | not yet |
| `meeting` | meetings | not yet |
| `consult` | consult | not yet |
| `notes` | lead notes | not yet |
| `contest` | contests | not yet |
| `issues` | GitHub issues | not yet |
| `automations` | automations | not yet |

Argument handling follows the old server: unknown arguments are ignored, numbers and booleans may arrive as
strings, a single string is accepted where a list is expected, wait seconds are clamped (`delegate`/`followup`/`wait`
0..3000, default 600, and 0 for a `delegate` with `after`; `cancel` 0..60, default 15). `mode: full` maps to the
API's `edit`. `lane: false`, `verify: false` and `force_lane: false` mean "off" and are accepted. Relative paths
resolve against the server's working directory. `followup`, `wait` and `task` build reports with the task's own
project, not the server's working directory.

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
| No daemon, foreign program on the port, busy daemon, start failed | the reason and what to do (code `daemon_unavailable`) |

## Worker guard

Two layers (contract §1.3). The daemon is the authority: it recognises a caller inside a worker's process tree and
answers 403 with `details.caller: "worker"`, which the server shows as the old refusal text. The server also keeps
the old local guard (`internal/mcpserver/guard.go`): when `SEELEGENT_WORKER=1` is in its environment (the daemon
sets it for every worker, so an MCP server a worker starts inherits it), it refuses mutating calls before talking to
the daemon, with the same text and `{error: {code: "forbidden", details: {caller: "worker"}}}`:

- always: `delegate`, `followup`, `review`, `cancel`, `consult`;
- `meeting` unless the action is `status`, `list` or `wait`; `issues` unless `repos`, `list`, `tickets` or
  `ticket`; `memory` unless `read`; `notes` unless `list`; `automations` unless `list` or `runs`; `contest` unless
  `status` or `wait`;
- `profiles` with `upsert` or `delete`; `proof` with `run`.

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
`internal/cli` runs the mcp command (autostart off, and inside a worker) and two daemons on one data directory.
