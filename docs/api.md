# HTTP API

The daemon serves the dashboard and its JSON API on `127.0.0.1:<port>` (port: `seelegent daemon --port`, else
`SEELEGENT_PORT`, else the settings' `port`, default 47821; development uses 47831, see
[development.md](development.md)).

**The binding contract is `contracts/api-v1.md` in the spec workspace** (`~/.seelegent/specs/Seelegent-spec/`). This
page only maps it onto the code and records the places where the code had to choose; it does not repeat the
endpoint tables.

## Where things live

| Contract | Code |
|---|---|
| §1 rules: Host check, `X-Seelegent` + same-origin, body limit, unknown fields, enums, paths, error shape, keyset lists | `internal/httpapi/server.go` (`serve`), `decode.go`, `errors.go` |
| §1.3 worker guard | `internal/httpapi/guard.go` (classifier over the task engine's worker process trees) |
| §2 system (`/health`, `/state`, `/backends`, `/models`, `/logs`, `/projects`, `/spec`, `POST /daemon/stop`) | `system.go` |
| §3 tasks, wait, task git, repo git | `tasks.go`, `git.go` |
| §4 accounts, quota, sign-in, keys, providers, usage | `accounts.go`, `convert.go` |
| §5 settings, memory, roles | `settings.go` |
| §6 M13: `/lanes`, `/contention`, `/proof/*` | `proof.go` (over `httpapi.Proof`, the proof service, and `httpapi.Gates`, the task engine); behaviour in [gates.md](gates.md) |
| §5 `/members*`, `/hooks*`; §6 M14: `/tasks/{id}/consult`, `/tasks/{id}/notes*`, `/meetings*`, `/contests*` | `team.go` (over `httpapi.Members`, `Consults`, `Notes`, `Meetings`, `Contests`, `Hooks`; each optional, its paths answer `501` without it); behaviour in [team.md](team.md) |
| §6 M15: `/github/*`, `/automations*` | `github.go` (over `httpapi.Tickets`, the ticket service, and `httpapi.Automations`, the automation service; each optional, its paths answer `501` without it); behaviour in [github.md](github.md) and [automations.md](automations.md) |
| §7 SSE | `hub.go` (ids, ring, client queues), `stream.go` (the response), `feeds.go` (task bus and account watcher) |
| §8 static dashboard | `web.go` |
| DTOs | `internal/api`; `go generate ./internal/api` writes `web/src/api/types.gen.ts` (`internal/api/tsgen`), and a test fails when the file is stale |

`internal/app/http.go` builds the server with the real services and runs it; `internal/cli/daemon.go` is the
foreground `seelegent daemon` command.

## Request checks, in order

1. Host must be `127.0.0.1:<port>`, `localhost:<port>` or `[::1]:<port>`, else `421 misdirected_request`. This applies
   to the API, the stream and the dashboard.
2. `POST PUT PATCH DELETE` under `/api/` need `X-Seelegent: 1`, and an `Origin` header, when present, must be the
   daemon's own origin, else `403 forbidden`.
3. The caller is classified. A process inside a running worker's process tree is a worker and may only use the §1.3
   allow-list (`403 forbidden`, `details.caller: "worker"`).
4. Routing. Bodies are read through a 1 MiB limit (`413 too_large`); unknown fields, wrong types and unknown enum
   values are `400 invalid_argument` with `details.field`. An unknown path under `/api/v1/` is `404 not_found`; a
   path that exists, asked with a method it does not take (`DELETE /api/v1/health`), is `405` with an `Allow`
   header listing the methods it takes and the body code `invalid_argument` with `details.reason:
   "method_not_allowed"` and `details.allow` (the error codes stay the contract's set).

`seelegent daemon --dev-origin http://localhost:5173` adds one loopback `http` origin to checks 1 and 2, so the Vite
dev server (whose proxy keeps the browser's Host and Origin) can reach a development daemon. Without the flag nothing
extra passes. The flag refuses anything that is not `http://localhost|127.0.0.1|[::1]:<port>`, and the daemon logs a
Warn line naming the extra origins at start-up.

Every answer, refusals included, carries `X-Frame-Options: DENY`, `Content-Security-Policy: frame-ancestors 'none'`
and `Referrer-Policy: no-referrer`. A request whose client went away gets nothing written (logged at Debug), never a
500.

Files a worker can plant are read with `fsutil.ReadRegular`/`OpenRegular`, which never block on a FIFO and never
follow a symbolic link: `GET /spec` answers 400 for a `tasks.md` that is not a regular file or is over 4 MiB, `GET
/logs` answers 503 for a log that is not a regular file, and the git views count a FIFO as 0 lines and show a link
as its target, the way git stores it. Git diffs run with `--no-textconv`, so no diff driver from the repository runs
in the daemon.

## Decisions where the contract left room

- A domain error that carries details (`APIDetails()`) keeps them in the error body: a lane conflict is `409` with
  `{reason: "lane_conflict", conflicts}`, a refused regression run `409` with `{reason: "active_tasks",
  activeTaskIds}` or `{reason: "run_in_progress", runId}`.
- `GET /proof/stats` without `cwd` counts every project; the other proof routes need an absolute `cwd`. A
  regression run answers `202` at once and reports its progress as `proof.run` frames, whose `ProofRun.repoRoot`
  says which repository it belongs to.

- Team routes (M14, [team.md](team.md)). The contract fixed the paths; these are the choices made:
  - `PATCH /members/{id}` merges the body onto the stored member (a field left out keeps its value) and refuses a
    changed `id`. A member that is off is `409 conflict` with `details.reason: "member_disabled"` wherever new work
    would go to it; an unknown member in a request is `400` with `details.reason: "member_missing"`, and a role (the
    request's or the member's) that does not exist for the task's project is `400` with `details.reason:
    "role_missing"` (`role`, and `member` when it was the member's role).
  - `POST /meetings/estimate` and `POST /contests/estimate` (additions) take the body of the open or create call and
    answer the plan without starting anything.
  - `GET /meetings/{id}/wait` and `GET /contests/{id}/wait` take `seconds` 0..1500 (default 600) and return once the
    meeting or contest needs the lead or has ended; `GET .../events` is the step log (`TeamLogEntry[]`). Lists take
    `limit` 1..200 (default 50).
  - `POST /tasks/{id}/review` takes `answerObjection`: accepting a task with an unanswered peer objection is `409
    conflict` with `details` `{reason: "peer_objection", consults}` without it.
  - `POST /contests/{id}/pick` whose patch does not apply is `409 conflict` with `{reason: "patch_conflict", winner,
    files}`; nothing was changed. `GET /contests/{id}/variants/{label}/diff` carries the raw `diff` (CLI and MCP) and
    `files`, the same diff parsed per file, so the dashboard never reads diff text (§3.3).
  - `POST /tasks` takes `member`; `POST /tasks/{id}/followup` takes `member` too (the task runs as that member from
    that turn on). Links to meetings, contests and tickets (`meetingId`, `contestId`, `variant`, `parentId`) are set
    by the daemon only: `POST /tasks` has no such fields, and unknown fields are `400` (old C-B21).
  - SSE: `members.changed` is sent on every change of the members, through the API or not (`member.Options.Changed`);
    `meeting.updated` and `contest.updated` carry the summary; `notes.changed` carries `{taskId}`; `hook.ran` carries
    the `HookRun`. The worker allow-list is unchanged: a worker may call none of the team routes.

- `GET /git/branches` answers `null` for a folder that does not exist, keyed by the cwd as given. A batch of 50 cwds
  should not fail because one project folder was deleted. A relative cwd is still `400`.
- `GET /tasks/{id}/turns/{n}/outside-diff` and `POST /tasks/{id}/turns/{n}/restore` (an addition to the contract,
  body `RestoreRequest` `{files?, force?}`, answer `RestoreResult` `{restored, skipped, backup?}`) work from the turn
  snapshots ([safety.md](safety.md)). A file changed after the turn ended is skipped with
  `code: "changed_since_turn"` unless `force`; `backup` names the snapshot that keeps what the restored files held
  before. A turn
  without snapshots is `404 not_found`; a restore while the task is active or taken over is `409 bad_state`; a
  daemon without a snapshot store answers both with `503 unavailable`. Restore is a mutating route, so a worker is
  refused it like every route outside the allow-list. A restore that wrote any file logs a `restored` event
  (`{files, backup}`, the event's `turn` is the turn restored from).
- `TurnGit` is `null` for a turn without snapshots; `Turn.workAtRisk`, `hygiene` and `scope` are empty lists until
  the turn's checks ran, and `TaskDetail.fileOverlaps` is always a list.
- `POST /tasks/{id}/git/suggest-message` builds the suggestion locally and at once. It still answers `202 TaskGit`
  and publishes `task.updated`, so clients written for a slow suggestion keep working.
- `TaskGit` follows the task from its start (bugs.md F70): the task records `gitStart` (head and branch) with its
  baseline at the first turn, and `commits`, `commitsMore`, `committedFiles`, `startState`, `startHead`,
  `startBranch`, `branchChanged` and `head` describe what it committed since. `files` stays the uncommitted changes
  counted against HEAD, and only those can be committed; `committedFiles` are view only. A task from before
  `gitStart` uses its turn-1 snapshot head, but only in the repository that snapshot was taken in and only when it
  names a head; else `startState` is `unknown`. `unreachable` means the start is gone or no longer an ancestor of
  HEAD (a rebase or a reset); then no commits are listed and diffs compare with HEAD.
- `commits` lists every commit in start..HEAD, the task's or not. Each gets the `turn` that was running when it was
  committed (by committer time); one made while none of the task's turns ran (someone else's, or between turns) has
  no turn and `outsideTurns: true`; `ownCommits` counts the range's commits made during the turns (listed or older),
  so the others number `commits` plus `commitsMore` minus `ownCommits`. Attribution is by time only: another task's commit made while this task's turn
  `n` ran is also given turn `n`. `committedFiles` keeps only the task's files among those the range changed: a file
  its turns touched, or a file under the task's folder that a commit made during one of its turns changed. A file
  only commits outside the turns changed is never listed. A committed file's diff compares the working tree with
  the start, so it also holds others' commits to that same file.
- When the commits since the start cannot be read (git times out on a big repository, an object is missing), the
  error is logged and `TaskGit` still answers with `files`: `historyState` is `history_unavailable`, `commits` and
  `committedFiles` are empty, and a later read tries again.
- `GET /tasks/{id}/git/diff` takes `base=start` (the default: the working tree against the task's start, or the
  empty tree when the repository had no commit then) or `base=head` (the uncommitted list); any other `base` is
  `400`. It accepts `turn` but does not use it; per-turn changes come from the turn snapshots (`outside-diff`).
- `GET /tasks/{id}/git/commits/{sha}` (an addition to the contract) answers `GitCommitDetail`: the commit's files and
  parsed diffs against its first parent, 1 MiB in all (`truncated`), with `--no-textconv` and `--no-ext-diff` like
  the other diffs. The sha is checked as 4–64 hex digits before git sees it (`400`, field `sha`), resolved with
  `rev-parse --verify`, and answered only when it is in start..HEAD: any other commit, a known one or not, is
  `404 not_found` with `details.reason: "not_in_task"`, and a task whose start is unknown or unreachable is
  `409 bad_state` with `details.reason` `start_unknown` or `start_unreachable`. Diffs are not redacted, the same as
  the other diff endpoints.
- `RepoStatus` adds `upstream`, `ahead`, `behind` and `upstreamGone` (the upstream is set but its ref is gone), read
  from `git status --porcelain=v2 --branch`. The Git view shows them and the repository's other uncommitted files.
- `GET /git/diff` takes `base` as `HEAD` or 7–64 hex digits (SHA-256 repositories).
- `Account.email` is never set: no backend reports it reliably yet.
- `Provider.models` is the allow-list exactly as last set through `models`, never the tier models, and
  `Provider.allowedModels` is the pattern enforced. A list becomes `^(?:a|b)$`; a hand-written pattern (old data, or
  sent as `allowedModels`) answers `models: []`. Both empty allow any model, and the tier models are always allowed
  on top. In a request a non-empty `models` wins over `allowedModels`; `allowedModels` alone sets a pattern;
  `models: []` clears both (an `allowedModels` sent with it that only repeats the stored list's pattern is ignored,
  so a client that sends the whole provider back can still clear the list). Neither touches `tiers`. The list is
  stored in `providers.models`; rows from before that column get it back from their pattern when the pattern was
  built from a list. `Account.models` of an API-key account is its provider's tier models followed by that list.
- `POST /automations/{id}/confirm-token` takes `{revision}`, the `Automation.revision` the dashboard shows, and
  answers `409 bad_state` when the automation changed since (`400` without it), so a token enables exactly what
  the user read (F34). It and `POST /automations/{id}/confirm`, `PUT` and `DELETE /github/token` answer only
  the dashboard's own requests: `403 forbidden` with `details.reason: "dashboard_only"` otherwise.
- A `POST /tasks` body with `ticketId` is a `400` (C-B21): ticket tasks start only through the ticket routes.
- `PATCH /settings` applies the JSON merge patch to the daemon's in-memory settings and writes `config.json`
  atomically. Unknown keys, type errors and validation errors are reported together in one `400`. A body that is not a JSON object (`null`, an array, broken JSON) is a `400 invalid_argument` "the settings patch must be a JSON object", never "config file cannot be parsed".
- `/state` reads `lastEventId` before the snapshot. A frame newer than the id can repeat a change the snapshot holds
  (harmless), but no change can be missing from both.
- SSE: the `hello` frame carries an id like every frame (contract §7: "every frame"). On a fresh connect it is the
  current id; on a resume it is the client's id. A `reset` frame carries the current id, so a reconnect after it
  resumes from there. `task.updated` is coalesced per task for 100 ms; account frames are sent when the account's
  JSON changed, checked on pool and task notices, after account writes and every 30 s. An account added to,
  removed from or moved to another provider is followed by `providers.changed` (because `Provider.accounts` counts
  them), whichever of the handler and that watcher sees the change first. Accounts cannot change provider through
  the API today; the watcher covers it if they ever do.
- `POST /wait` takes the cwd for its report from an optional `cwd` query parameter. A flag that needs the lead and
  was already raised when the wait began does not end it, unless the optional `since` query parameter (RFC 3339) is
  given and the flag was raised at or after it: `delegate` and `followup` (CLI and MCP, and the MCP tools that start
  or follow up a task: `notes` send, `issues` triage and ticket tasks) pass a mark taken before they created or
  followed up the task, so a task blocked as soon as it is created (`no_accounts`) returns its report at once
  instead of after the whole wait. A wait whose client went away writes nothing. When the daemon shuts down (`Server.Close`, on `POST /daemon/stop` and on Ctrl+C) every open
  wait ends at once with `503 daemon_stopping` (a wait that ended on its own just before is answered normally), so
  `http.Server.Shutdown` does not sit out the waits; the clients take that code to mean "do not start the daemon
  again" (see [cli.md](cli.md) and [mcp.md](mcp.md)). The other long waits do the same: `GET /meetings/{id}/wait`,
  `GET /contests/{id}/wait` and `POST /tasks/{id}/cancel` with `waitSeconds` (the cancel itself is recorded before
  its wait, and the next daemon finishes it; that answer then carries `details.cancelRecorded: true` and says so in
  its message, and the CLI and the MCP `cancel` tool report the cancel as recorded instead of failing).
- `GET /health` carries `homeId` (`config.HomeID`: 16 hex digits of the SHA-256 of `fsutil.Key(home)`), so a client
  of another data directory refuses this daemon instead of using or stopping it.
- `capabilities.takeover` is true for backends that can resume a session; `apiKey` for every backend but agy;
  `listModels` for codex.
- Sign-in runs in a terminal window. Once the window is open the session is `in_terminal` ("finish signing in in the
  terminal window that opened"); no backend reaches `waiting_code` or `open_url` today, because the window takes the
  code itself and no URL is captured. `POST /accounts/{id}/login/code` therefore answers `409 bad_state` (`404` without
  a sign-in). Logout recreates the account's profile folder empty.
- `POST /daemon/stop` only signals (`httpapi.Options.Stop`, wired to `App.RequestStop`) and answers `202
  DaemonStop`; `seelegent daemon` then leaves its wait and runs the same shutdown as Ctrl+C (port file removed first,
  `App.Close`, lock released last), and `http.Server.Shutdown` lets the answer finish first. A server built without
  `Stop` answers `503 unavailable`. The worker guard denies it like every route outside the allow-list. The daemon
  also writes its pid on the second line of `run/daemon.port`; `seelegent stop` and `doctor` only show it.
- A worker may call `GET /spec` only with its own task's cwd (or repository root), and `dir`, relative or absolute,
  must lie inside that cwd or be its own task's spec folder. Paths are compared as `fsutil` keys with links and
  junctions resolved, so an absolute `dir` of another project or a link inside the cwd that leads out of it is
  `403 forbidden` like a foreign cwd. `GET /memory` takes no other path.

## The dashboard

Every non-API `GET` is the dashboard. An existing file is served with the MIME type of its extension. A missing
path with an extension is `404`. Any other path is `index.html` (history routing). Files under `assets/` are cached
for a year (their names carry a hash); everything else is `no-cache`. A binary built without `web/dist` answers
`404 the dashboard is not part of this build`. `Health.build` and `hello.build` are the first 12 hex digits of a
SHA-256 over the bundle, so the dashboard can reload after an upgrade.
