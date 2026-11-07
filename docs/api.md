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
| §6 and `/members*`, `/hooks*` | `501 not_implemented` until their milestones (`server.go`, `reservedRoutes`) |
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
   values are `400 invalid_argument` with `details.field`.

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

- `GET /git/branches` answers `null` for a folder that does not exist, keyed by the cwd as given. A batch of 50 cwds
  should not fail because one project folder was deleted. A relative cwd is still `400`.
- `GET /tasks/{id}/turns/{n}/outside-diff` answers `503 unavailable` until the outside-change tracking milestone.
- `POST /tasks/{id}/git/suggest-message` builds the suggestion locally and at once. It still answers `202 TaskGit`
  and publishes `task.updated`, so clients written for a slow suggestion keep working.
- `GET /tasks/{id}/git/diff` accepts `turn` but shows the working-tree diff; per-turn diffs need the snapshot store of
  a later milestone.
- `Account.email` is never set: no backend reports it reliably yet.
- `Provider.models` is the allow-list exactly as last set through `models`, never the tier models, and
  `Provider.allowedModels` is the pattern enforced. A list becomes `^(?:a|b)$`; a hand-written pattern (old data, or
  sent as `allowedModels`) answers `models: []`. Both empty allow any model, and the tier models are always allowed
  on top. In a request a non-empty `models` wins over `allowedModels`; `allowedModels` alone sets a pattern;
  `models: []` clears both (an `allowedModels` sent with it that only repeats the stored list's pattern is ignored,
  so a client that sends the whole provider back can still clear the list). Neither touches `tiers`. The list is
  stored in `providers.models`; rows from before that column get it back from their pattern when the pattern was
  built from a list. `Account.models` of an API-key account is its provider's tier models followed by that list.
- `PATCH /settings` applies the JSON merge patch to the daemon's in-memory settings and writes `config.json`
  atomically. Unknown keys, type errors and validation errors are reported together in one `400`.
- `/state` reads `lastEventId` before the snapshot. A frame newer than the id can repeat a change the snapshot holds
  (harmless), but no change can be missing from both.
- SSE: the `hello` frame carries an id like every frame (contract §7: "every frame"). On a fresh connect it is the
  current id; on a resume it is the client's id. A `reset` frame carries the current id, so a reconnect after it
  resumes from there. `task.updated` is coalesced per task for 100 ms; account frames are sent when the account's
  JSON changed, checked on pool and task notices, after account writes and every 30 s. An account added to,
  removed from or moved to another provider is followed by `providers.changed` (because `Provider.accounts` counts
  them), whichever of the handler and that watcher sees the change first. Accounts cannot change provider through
  the API today; the watcher covers it if they ever do.
- `POST /tasks/wait` takes the cwd for its report from an optional `cwd` query parameter. A wait whose client went
  away writes nothing.
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
- A worker may call `GET /spec` with an absolute `dir`. The guard checks the task's cwd only, as the contract says,
  and `dir` must stay inside that cwd.

## The dashboard

Every non-API `GET` is the dashboard. An existing file is served with the MIME type of its extension. A missing
path with an extension is `404`. Any other path is `index.html` (history routing). Files under `assets/` are cached
for a year (their names carry a hash); everything else is `no-cache`. A binary built without `web/dist` answers
`404 the dashboard is not part of this build`. `Health.build` and `hello.build` are the first 12 hex digits of a
SHA-256 over the bundle, so the dashboard can reload after an upgrade.
