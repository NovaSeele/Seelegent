# Command line

`seelegent <command> [flags]`. The commands are thin: they talk to the daemon through `internal/client` and share
their logic with the MCP tools through `internal/ops` (see [mcp.md](mcp.md)). Code: `internal/cli`, one file per
group (`delegate.go`, `tasks.go`, `proof.go`, `memory.go`, `team.go` (members, consult, notes, hooks),
`meeting.go` (meeting, contest), `system.go`, `doctor.go`, `daemon.go`, `mcp.go`, `setup.go`, `migrate.go`,
`update.go`); the checks of `doctor` are `internal/doctor`, the work of `setup` and
`migrate` is `internal/setup` and `internal/migrate` ([setup.md](setup.md)), the work of `update` is
`internal/update` ([release.md](release.md)).

## Common rules

| | |
|---|---|
| `--json` | Prints the result as JSON on stdout, one shape per command (below). Errors then go to stdout too, as `{"error": "...", "code": "...", "details": {...}}`. Without `--json`, results go to stdout and errors to stderr as `Error: ...`. |
| Exit codes | `0` ok; `1` runtime or API error, refusal inside a worker, `doctor` with a failed check; `2` usage error (unknown command or flag, missing or wrong arguments); `3` `seelegent daemon` found the data directory locked by another daemon or its port taken. |
| Error codes | the API's code (`not_found`, `invalid_argument`, `bad_state`, ...), `not_available` (an option or feature of a later version), `forbidden` with `details.caller: "worker"`, `daemon_unavailable` (no daemon, another program on the port, a Seelegent daemon of another data directory on the port, a daemon that holds its lock without answering), `daemon_stopped` (the daemon was stopped during a wait and did not come back; the tasks keep running), `daemon_locked` (exit 3), `usage` (exit 2), `checksum_mismatch`, `dev_build`, `no_previous`, `schema_too_new`, `schema_unknown` and `migrated_then_failed` (`update`; `schema_too_new` also from `version --check-database`), `error`. |
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
`restart`, `ui`, `daemon` (`serve`), `setup` (a dry run too), `migrate`, `restore`, `proof run`, `proof cancel-run`,
`consult`, `meeting` (all but `list`, `status` and `wait`), `notes` (all but `list`), `contest pick` and `contest
cancel`, `members add|set|remove` and `hooks set|test` print exactly `Seelegent is not available inside a Seelegent worker.` and exit 1 (with `--json`:
`{"error": that text, "code": "forbidden", "details": {"caller": "worker"}}`). The check runs before the flags are
parsed and before any call. `status`, `wait`, `task`, `ls`, `memory read`, the other `proof` commands, `doctor`, `version`, `skill` and the
hidden `hook claude-agent` (Claude Code's reminder hook, which may run inside a worker and only prints advice) stay
allowed, and so do the read commands of `members`, `meeting`, `contest`, `notes` and `hooks`; the daemon's own guard decides what a worker may read. `update` is refused the same way in every mode,
`--check` included (it replaces the executable and restarts the daemon that runs the worker).

## Commands

| Command | What it does | `--json` prints |
|---|---|---|
| `daemon [--port N] [--dev-origin URL]` (alias `serve`) | Runs the daemon in the foreground until Ctrl+C or `seelegent stop`. | – |
| `mcp` | The MCP server for a lead host ([mcp.md](mcp.md)). | – |
| `ui [--no-open]` | Starts the daemon if needed, opens the dashboard in the browser (Windows, macOS, Linux) and prints `Dashboard: <url>`. | `{dashboard}` |
| `status` | Daemon, backends, accounts (labels and quota, never e-mails) and the tasks that need you. Prints `not running` (exit 0) when no daemon runs; a daemon that holds its lock without answering, or another program on the port, is exit 1. | `{running, dashboard?, health?, backends: Backend[], accounts: Account[], activeTasks: TaskSummary[]}` |
| `stop` | Asks the daemon to shut down (`POST /api/v1/daemon/stop`) and waits up to 15 s until its lock is free and its port silent; prints `stopped` or `not running`. Open waits are answered at once (`daemon_stopping`), so this takes about a second, and a lead that was waiting does not start the daemon again (see waits below). Removes a port file left by a daemon that is gone. A daemon that holds its lock but does not answer is reported with its pid (when recorded) and the log path, exit 1. Nothing is ever killed by a pid read from a file (old B3, B14). | `{ok, stopped}` |
| `restart` | `stop`, then start the daemon and wait until it answers. Prints `Daemon restarted.` | `{ok: true, restarted: true}` |
| `delegate [flags] <prompt\|@file>` | Creates a task and waits for its report. Flags: `--cwd`, `--title`, `--backend`, `--provider`, `--model`, `--effort`, `--mode full\|plan`, `--profile`, `--spec`, `--task T001,T002` (needs `--spec`), `--after t1,t2`, `--after-mode accepted\|done`, `--allow-git`, `--member M` (the member's backend, model or tier and role; [team.md](team.md)), the gate flags below, the contest flags below, `--wait N` (default 600; 0 returns at once; a task with `--after` does not wait unless `--wait` is given). The text report ends with the gate, lane, bar and verifier lines. | `TaskReport`; a contest prints the contest |
| `run [flags] <prompt\|@file>` | Legacy: `delegate` with a 3600 s wait; always prints the report JSON. Same flags as `delegate` except `--wait`. | `TaskReport` |
| `followup <id> [flags] <message\|@file>` | Sends the next instruction and waits for the report. Flags: `--by lead\|user`, `--model`, `--effort`, `--task`, `--profile`, `--allow-git`, `--force` (start a task waiting for its prerequisites; the message may then be empty), `--member M` (the task runs as that member from this turn on), the gate flags below, `--wait N` (default 600). Gate flags on a followup change the gate from this turn on. | `TaskReport` |
| `wait <id...> [--any] [--seconds N] [--no-wake]` | Waits (default 600 s) and prints the reports. `--no-wake` keeps waiting when a task needs the lead. | `WaitResult` with `reports` |
| `review <id> accepted\|rejected [--notes T] [--answer-objection T\|@file]` | Records the verdict. Accepting a task a peer member objected to (consult verdict `object`) needs `--answer-objection`; the answer is recorded with the review. Accepting over a senior's objection prints a `Warning:` line naming it (`--json`: `warnings`). | `TaskSummary` |
| `cancel <id> [--wait N]` | Cancels and waits up to N s (default 15, max 60) for the worker to stop. | `TaskReport` read after the cancel (`cancelling: true` while it stops) |
| `task <id> [--steps N] [--full]` | The report, with the last N worker tool steps; `--full` the whole task with its turns. | `TaskReport` + `recent_steps`, or `TaskDetail` + `recent_steps` |
| `ls [--active] [--blocked] [--project P] [--limit N]` | Tasks, newest first (limit 1..200, default 50). | `TaskPage` |
| `restore <id> --turn N [--file=<path>]... [--force]` | Puts back the content files had when turn N started, from its snapshot ([safety.md](safety.md)); without `--file`, every work-at-risk file of the turn not restored yet. Paths are relative to the repository root. A file changed after the turn ended is skipped (`changed since the turn ended`) unless `--force`. Prints `restored`/`skipped` lines and the `backup` snapshot that keeps what the files held before. Refused while the task is active. | `RestoreResult` |
| `memory read\|append\|replace --cwd D [text\|@file]` | A project's notes for workers. | `Memory` |
| `proof index [--cwd D]` | Every Spec Kit feature of the repository (default: the current folder) with its requirement counts, and the latest regression runs ([gates.md](gates.md)). | `ProofIndex` |
| `proof matrix <feature>\|--feature F [--cwd D]` | The requirement matrix of a feature: each FR/SC with its status and covering tasks. | `ProofMatrix` |
| `proof run [<feature>] [--force] [--wait N] [--cwd D]` | Starts a regression run of every gate command (or one feature's); `--wait N` polls until it ends or N s pass (default 0). Refused (`conflict`, with the active task ids) while other tasks of the repository are active, unless `--force`. | `ProofRun` |
| `proof run-status <run-id> [--cwd D]` (alias `run_status`) | A regression run's state and results. | `ProofRun` |
| `proof cancel-run <run-id> [--cwd D]` (alias `cancel_run`) | Stops a running regression run; a finished one is printed as it is. | `ProofRun` |
| `proof stats [--since T] [--cwd D]` | Gate, lane and verifier statistics (`--since`: RFC 3339). | `ProofStats` |
| `proof lanes [--cwd D]` | The lane claims held in the repository and the `lanes` mode. | `LanesView` |
| `proof contention [--cwd D]` | Files several tasks of the repository changed recently. | `ContentionView` |
| `members list\|track` | The department members; `track` what each member's meetings and consults came to. | `Member[]`, `MemberTrack[]` |
| `members add --name N --backend B --rank R [flags]`, `members set <id> [flags]`, `members remove <id>` | Change the department. Flags: `--id`, `--name`, `--backend`, `--provider`, `--rank worker\|senior\|peer`, `--tier`, `--model`, `--role`, `--prefer a,b`, `--fallback a,b`, `--enabled`. `set` sends only the flags given. | `Member`; `remove` `{ok, removed}` |
| `consult <task-id> --member M [--focus T] [--wait N]` | A senior or peer member reads the finished task read-only and answers a verdict (`approve`, `concerns`, `object`) with findings; waits up to N s (default 600). | `Consult` |
| `notes list\|add\|resolve\|reopen\|delete\|send <task-id> ...` | Review notes on a task's diff. `add --file F --line N [--end-line N] [--side new\|old] <body\|@file>`; `send [--extra T\|@file] [--wait N]` sends the drafts and the notes the last turn left unaddressed as one followup and waits for the report. | `Note[]`, `Note`, `NotesSendResult` or `TaskReport` |
| `meeting open --attendee id[:rank] ... [--option T]... [--context a,b] [--rounds N] [--sealed T] [--title T] [--cwd D] [--estimate] [--wait N] <question\|@file>` | Opens a meeting of 1 to 6 members (`--estimate` only prints the most runs it takes). | `Meeting` (`RunEstimate` with `--estimate`) |
| `meeting list\|status <id>\|wait <id> [--seconds N]` | Meetings, one meeting's state and minutes, or a wait (default 600, at most 1500) until it needs you or ends. | `MeetingSummary[]`, `Meeting` |
| `meeting post <id> --answer q1=text ... [--note T]`, `decide <id> [--option K] [--rationale T] <decision>`, `act <id> [--member M] [--backend B] [--model M] [--cwd D] <brief>`, `cancel <id>`, `dissent-outcome <id> <dissent-id> vindicated\|dismissed` | The lead's side of a meeting: answer questions, decide, turn the decision into a task, cancel, settle a dissent later. | `Meeting`; `act` `MeetingActResult` |
| `contest list\|status <id>\|wait <id> [--seconds N]\|diff <id> <variant>` | Contests, one contest's scoreboard, judge and proposal, a wait until it is ready or ended, a variant's diff against the base. | `ContestSummary[]`, `Contest`, `ContestDiff` |
| `contest pick <id> [--winner V2]`, `contest cancel <id>` | Apply the winner (default: the proposal) to the working tree, or stop every variant; both remove the worktrees. | `Contest` |
| `issues repos` | The connected repos and the GitHub access tier ([github.md](github.md)). | `{access: GitHubAccess, repos: Repo[]}` |
| `issues connect <path>`, `issues init <path> [--name N] [--no-first-commit]`, `issues create-remote <repo> [--name N] [--public]` | Connect a repository, make one first, or create its GitHub repository (gh or token tier; private unless `--public`). `<repo>` is a repo id or the repository's folder. | `Repo` |
| `issues sync <repo>`, `issues list <repo> [--state open\|closed\|all]`, `issues add <repo> --title T [body\|@file]` | Refresh the inbox from GitHub, print it, add a local issue. | `SyncResult`, `Issue[]`, `Issue` |
| `issues suggest <repo> <key> assign\|meeting\|skip [--member M] [--reason T]` | Records a suggestion; nothing else happens. `<key>` is `gh#12`, `local#3` or a bare number (`gh#`). | `Issue` |
| `issues triage <repo> <key> assign\|meeting\|skip [flags]` | Decides an issue. `assign` makes a ticket (its own worktree and branch) with a task: `--prompt T\|@file`, `--member`, `--backend`, `--model`, `--gate`, `--gate-detect`, `--no-gate`, `--lane`, `--no-lane`, `--wait N` (default 0). `meeting` opens a meeting in that worktree: `--question`, `--attendee id[:rank]`, `--option`, `--context`. `skip` needs `--reason`. | `TriageResult`, or `TaskReport` with `--wait` |
| `issues tickets [--repo R] [--status s,...]`, `issues ticket <id>` | The tickets, or one with its tasks, gates, files and log. | `TicketSummary[]`, `Ticket` |
| `issues add-task <ticket> [flags] <prompt\|@file>`, `issues retry <ticket> [flags] [prompt\|@file]`, `issues fix-checks <ticket> [--member M] [--extra T\|@file] [--wait N]` | Another task in the ticket's worktree (the worker flags of `triage`), the task of a failed ticket again (another member, backend or model; the prompt is optional), or a task with the failing CI checks of its pull request. | `TicketTaskResult`, or `TaskReport` with `--wait` |
| `issues pr <ticket> [--title T] [--body T\|@file] [--commit-message T] [--ready]` | Commits, pushes the ticket branch and opens a draft pull request (`--ready`: not a draft). Refused when a changed file is secret or junk ([github.md](github.md)). In the manual tier it pushes and prints the compare link; record the pull request with `link-pr`. | `Ticket` |
| `issues link-pr <ticket> <number>`, `issues comment <ticket> <body\|@file>`, `issues drop <ticket> [--reason T]`, `issues cleanup <ticket>` | Record a pull request opened by hand, comment on the issue, end a ticket without a pull request, remove a done or dropped ticket's clean worktree (the branch stays). | `Ticket` |
| `issues shared-dirs <repo> [dir...] [--clear]` | The gitignored folders (such as `node_modules`) linked into new ticket worktrees; without folders it prints them. | `{id, path, sharedDirs}` |
| `automations list\|show <id>\|runs <id> [--limit N]` | The automations, one automation, its run history ([automations.md](automations.md)). | `Automation[]`, `Automation`, `AutomationRun[]` |
| `automations create --every E --at HH:MM \| --cron C [flags] <prompt\|@file>` | Saves a delegation on a schedule, disabled. Flags: `--name`, `--cwd`, `--weekday`, `--timezone`, `--member`, `--backend`, `--model`, `--precheck`, `--min-remaining` (default 20), `--use-before-reset`, `--grace` (default 60), `--gate`, `--gate-detect`, `--no-gate`, `--lane`, `--no-lane`. | `Automation` |
| `automations update <id> [flags] [prompt\|@file]` | Changes only the flags given. A changed prompt, folder, worker, gate, lane, precheck or schedule turns an enabled automation off until you confirm it again. | `Automation` |
| `automations enable <id>`, `automations disable <id>` | `enable` only asks: it prints the dashboard link where you confirm. `disable` turns it off and withdraws the request. | `AutomationEnableResult`, `Automation` |
| `automations run <id>`, `automations delete <id>`, `automations preview --every E --at HH:MM \| --cron C [--timezone Z] [--count N]` | One run now, only of an automation the user confirmed as it is (else `bad_state`, `details.reason: "not_confirmed"`; the overlap, quota and precheck rules still apply), delete with the history, or the next times a schedule fires. | `AutomationRun`, `{ok, deleted}`, `SchedulePreview` |
| `hooks list\|set <file.json>\|test <id>` | The event hooks: list, replace the list from a JSON file (`{"hooks": [...]}` or a list; ids kept), run one now with a `hook.test` event. | `Hook[]`, `HookRun` |
| `doctor` | Checks the installation (below); exit 1 when a check fails. | `[{name, status: ok\|warn\|fail, message, fix?}]` |
| `version [--check-database]` | Version, commit and build date. `--check-database` also reads the data folder's database read-only and fails with `schema_too_new` when this build cannot open it (the installers run it on a downloaded binary). | `buildinfo.Info` + `schemaVersion` (the number of database migrations in the build) + `databaseSchemaVersion` with `--check-database` (0 without a database) |
| `setup [--host a,b] [--dry-run] [--remove] [--no-skill] [--hooks] [--force] [--exe path]` | Registers Seelegent with the lead hosts on this machine ([setup.md](setup.md)). Exit 1 when an item is `conflict` or `error`; with `--json` the report is then the only document printed. | `setup.Report` |
| `skill` | Prints the lead skill `setup` installs. | `{name, version, file, content}` |
| `migrate [--from dir] [--dry-run]` | Imports the old (Node) Seelegent's data ([setup.md](setup.md)). Exit 1 when an item is `error`; with `--json` the report is then the only document printed. | `migrate.Report` |
| `update [--check\|--apply\|--rollback] [--version vX.Y.Z] [--force]` | `--check` (the default) compares this build with the latest release (or `--version`). `--apply` installs it in place of this executable, verified against the release's `checksums.txt`, restarting the daemon and refreshing the hosts that have an entry; `--version` picks any release, older ones too. `--rollback` puts back the executable the last update replaced, once. Progress lines go to stderr. See below and [release.md](release.md). | `update.CheckResult`, `update.ApplyResult`, `update.RollbackResult` |

`update` (old `src/update.js`, inventory 6.1):

- Releases come from `https://api.github.com/repos/NovaSeele/Seelegent/releases/latest` (`/releases/tags/<tag>` with
  `--version`), or from `SEELEGENT_UPDATE_URL`. Only `https` is accepted, or plain `http` for exactly `127.0.0.1`,
  `localhost` or `[::1]` (a local test server), never with `user@` in the address; every redirect is checked the
  same way.
- `--check` prints `{currentVersion, latestVersion, updateAvailable, devBuild?, releaseUrl?, asset?, message}`. A
  development build (`dev`, a version between tags, `-dirty`, a snapshot) sets `devBuild` and never
  `updateAvailable`; `--apply` then fails with `dev_build` unless `--version` names the release to install.
- `--apply` prints `{applied, updateAvailable, version, previousVersion, exe, previous?, daemonRestarted, hosts,
  warnings?, message}`; with nothing newer it prints `applied: false` and "already up to date", exit 0. A download
  whose SHA-256 does not match `checksums.txt` (or is not listed) is `checksum_mismatch`, and nothing changed. The
  executable must be an installed `seelegent` (`seelegent.exe`); a `go run` or test build is refused.
- `--rollback` prints `{ok, previousVersion, currentVersion, exe, daemonRestarted, hosts, warnings?, message}`, where
  `previousVersion` is the version rolled back from (it is discarded). Without a kept executable: `no_previous`.
- Before anything is stopped or replaced, `--apply` and `--rollback` compare the database's schema version (read
  read-only) with the `schemaVersion` the build to install reports: fewer migrations than the database has is
  `schema_too_new` (its daemon could not open the database, and migrations are never undone), `--force` or not. A
  build that does not report `schemaVersion` (one from before it) is `schema_unknown`; `--force` installs it anyway
  with a warning. Without a database there is nothing to compare.
- When the daemon ran, it is started from the new executable and must answer within two minutes (migrations may run
  first) as the new version. Otherwise that daemon is stopped (gracefully, or the exact process the update started
  is killed), the old executable is put back and its daemon started and checked; the error says what runs now. A
  failed `--rollback` is undone the same way and keeps the kept executable for another try. Ctrl+C during the start
  still finishes this undo.
- The undo first reads the database's schema again. When the failed daemon already ran migrations the old executable
  does not know, the old one could not open the database: nothing is put back, the new executable stays installed
  (the old one kept as after an update), no daemon runs and the code is `migrated_then_failed`; fix what
  `logs/daemon.log` says and run `seelegent restart`. A schema that cannot be read then is handled the same way
  (code `error`).
- Versions are printed as the binaries report them, with the `v` (`v1.2.3`); the old updater printed `1.2.3`. The
  old `method` and `installRoot` fields are gone: there is one install layout.

Waits (`wait`, `delegate`, `followup`, `run`; the same code as the MCP tools, `ops.WaitReports`):

- Long waits are split into calls of at most 3000 s (the API's limit).
- A connection lost without a word (the daemon crashed or was killed) is ridden out: the daemon is found or started
  again and the wait goes on, all within the wait's own time.
- A daemon that answers `503 daemon_stopping` (it was stopped, or Ctrl+C) is only looked for again, never started,
  for up to 40 s (bounded by the wait's own time): after `seelegent restart` the wait goes on against the new
  daemon; after a plain `stop` it ends with `daemon_stopped` ("the Seelegent daemon was stopped during the wait; the
  tasks keep running: wait again later"), exit 1. Nothing in that wait starts a daemon until a daemon is found
  again; from then on the rules above apply again (a later crash is ridden out, a later stop starts a new grace).

### Gate flags

`delegate`, `run` and `followup` take the gate options ([gates.md](gates.md)):

- `--gate <cmd>` (repeatable): the lead's gate commands. With `--spec --task` they replace those tasks' `Gate:` lines
  and the feature gate, and prove those tasks in the ledger.
- `--gate-detect`: add the test, lint and build commands found in the repository (`gate: true` of the MCP tool).
- `--no-gate`: no gate commands; the lane and hygiene checks still run. It cannot go with `--gate` or
  `--gate-detect` (usage error).
- `--lane a,b` (or repeated): the files and globs the worker may change, relative to the repository root; a folder
  covers everything under it. `--no-lane` drops the lane a spec would give (usage error with `--lane`).
- `--force-lane`: claim the lane even where another active task holds it (recorded as forced).
- `--verify` / `--verify=false`: run, or skip, the second-opinion verifier once the gate is green; left out, the
  `verifier` setting decides.

A plan task (`--mode plan`) refuses `--gate`, `--gate-detect`, `--lane`, `--force-lane` and `--verify`.

### Contest flags

`delegate` and `run` with `--variants N` (2 to 5) or `--variant spec` (repeated; `backend[:model]`, or
`member=id,provider=id,backend=b,model=m`) start a contest instead of a task ([team.md](team.md)): each variant runs
the brief in its own worktree. `--judge M` names the judge (default: the first enabled peer), `--no-judge` scores by
machine only, `--deadline N` sets the minutes (default 60). A contest refuses `--after` and `--mode plan`.

### No token and no confirmation here

The GitHub token is stored, and an automation is confirmed, only in the dashboard: the command line (like the MCP
tools) has no command for either, and the daemon refuses those routes to any caller that is not the dashboard page
([github.md](github.md), [automations.md](automations.md)).

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
