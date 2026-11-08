# Configuration

Seelegent keeps its data in one directory and its settings in one JSON file in that directory. The code that owns
both is `internal/config`; defaults live only in `config.Defaults()`.

## Data directory

The data directory is `$SEELEGENT_HOME` when that variable is set, otherwise `~/.seelegent`. Directories are created
with owner-only permissions when a command starts, never earlier.

| Path               | What it holds                                                                          |
| ------------------ | -------------------------------------------------------------------------------------- |
| `config.json`      | the settings described below                                                           |
| `seelegent.db`     | the SQLite database (WAL mode, so `seelegent.db-wal` and `seelegent.db-shm` sit beside it): accounts, quota cache, rest periods, usage history (hourly snapshots kept 30 days, attempt usage kept 60 days), tasks, and the team: members, review notes, meetings, contests and event hooks (the old version kept these in `members.json`, `hooks.json`, `meetings/`, `contests/` and `notes/`) |
| `profiles/<id>/`   | one isolated CLI profile per worker account (same place as the old version)            |
| `profiles/<id>/home/` | the home directory the worker CLI sees, with links into your real home (`linkIntoProfiles`) |
| `profiles/<id>/seelegent-key` | API key of an API-key account: a DPAPI blob on Windows, a 0600 file elsewhere; never inside `home/` |
| `github/token`     | the GitHub token set from the dashboard ([github.md](github.md)): a DPAPI blob for the current Windows user on Windows; elsewhere **plain text** in a 0600 file, readable by any program running as you (see below) |
| `logs/`            | `daemon.log` and its rotated copies `daemon.log.1` … (JSON lines, secrets masked)      |
| `logs/hooks.log`   | output of the lead's event hooks, rotated like the daemon log ([team.md](team.md))     |
| `run/daemon.lock`  | single-instance lock of the daemon                                                     |
| `run/daemon.port`  | port of the running daemon (removed when it stops cleanly; see [mcp.md](mcp.md))       |
| `run/start.lock`   | held by an MCP server while it starts the daemon, so several start only one            |
| `backups/`         | copies taken before migrations and imports                                             |
| `tasks/<id>/t<turn>-a<attempt>/` | files of one worker attempt: `launch/` (launch spec, stream, stderr, exit record), the prompt of CLIs that read it on standard input, a long agy brief |
| `tasks/<id>/t<turn>-a<attempt>/mask` | the secrets the attempt's output is masked for (an API-key account's key), kept while the worker runs so a restarted daemon still masks them: a DPAPI blob on Windows, a 0600 file elsewhere |
| `snapshots/`       | turn snapshots of the work at risk: content-addressed file copies (`blobs/`) and one manifest per task turn (`tasks/<id>/`), never anything inside your repository ([safety.md](safety.md)) |
| `guard/bin/`       | the git shim workers without a pre-tool hook get first on `PATH` ([safety.md](safety.md)) |
| `contests/<id>/<label>.json` | the stored copy of a contest variant's changes, written once its task stops ([team.md](team.md)) |
| `contests/<id>/<label>.patch` | the patch of the variant that was picked and applied, kept as the record of what was applied |
| `contests/tmp/`    | temporary indexes of contests                                                          |
| `worktrees/contests/<id>/<label>/` | the worktree of one contest variant. Once its task stops, its content is committed inside it and the folder removed (never forced); a removal that fails is retried at the next start |
| `worktrees/tickets/<repo>/` | the worktrees of a connected repository's tickets ([github.md](github.md))    |
| `update/`          | downloads and the lock of `seelegent update` ([release.md](release.md))                |
| `setup-created.json` | the host files and folders `seelegent setup` created, so `setup --remove` deletes them only when they are empty again ([setup.md](setup.md)) |

Outside Windows, `profiles/<id>/seelegent-key`, `github/token` and a running attempt's `mask` are plain text
protected only by their 0600 mode: there is no OS key store Seelegent uses there. Anyone who can read your files (a program running as you, root, a
backup that does not keep modes) can read them. Prefer a fine-grained GitHub token limited to the repositories
Seelegent works on, or the `gh` CLI, whose own credential store Seelegent then uses instead of a token.

## Environment variables

| Variable          | Effect                                                                                       |
| ----------------- | -------------------------------------------------------------------------------------------- |
| `SEELEGENT_HOME`  | data directory instead of `~/.seelegent`                                                     |
| `SEELEGENT_PORT`  | daemon port instead of the `port` setting; never written to `config.json`. Development uses 47831 |
| `SEELEGENT_AGY`, `SEELEGENT_CLAUDE`, `SEELEGENT_CODEX` | path of that worker CLI instead of searching its install locations and `PATH` |

## How the file is read and written

- A missing `config.json` means "all defaults"; it is written the first time a setting is saved.
- Every key is optional. A missing key, or a key set to `null`, takes its default. For the five auto settings
  under [Resources](#resources) the default is computed from the machine, and it is never written to the file.
- A UTF-8 byte order mark at the start (Notepad, Windows PowerShell 5.1's `Set-Content -Encoding UTF8`) is
  skipped; saves never write one.
- A file that is not valid JSON, has a value of the wrong type, or holds a value out of range is an **error**: the
  daemon refuses to start and names the file, line and column or the setting. The file is never replaced by
  defaults, so a typo cannot wipe your settings.
- Keys this version does not know (from the old daemon or a newer Seelegent) are kept when the file is saved and
  reported as unknown. A key that differs from a known one only in letter case (`Port`) is an error.
- Saves are atomic (temporary file, flush, rename) and go through a lock file (`config.json.lock`), so the daemon
  and a CLI command cannot overwrite each other's changes.
- Known settings are written first, in the order of the table below, then the unknown keys.

## Settings

Durations use the unit in their name. "Tier" means one of `fast`, `standard`, `strong`, `max`.

### Daemon

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `port` | integer 1–65535 | `47821` | HTTP port of the daemon on 127.0.0.1. `SEELEGENT_PORT` overrides it. |

### Models and backends

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `defaultTier` | tier | `"standard"` | Tier used when the lead names neither a model nor a tier. |
| `backends.agy.enabled` | boolean | `true` | Allow tasks on the Antigravity CLI (Gemini). |
| `backends.agy.allowedModels` | regular expression | `"^gemini-3\\.8-"` | Model ids allowed on agy (see below). |
| `backends.agy.tiers.fast` | model id | `"gemini-3.8-flash-low"` | Model of the fast tier on agy. |
| `backends.agy.tiers.standard` | model id | `"gemini-3.8-flash-medium"` | Model of the standard tier on agy. |
| `backends.agy.tiers.strong` | model id | `"gemini-3.8-flash-high"` | Model of the strong tier on agy. |
| `backends.agy.tiers.max` | model id | `"gemini-3.8-flash-high"` | Model of the max tier on agy. |
| `backends.codex.enabled` | boolean | `false` | Allow tasks on the OpenAI Codex CLI. |
| `backends.codex.allowedModels` | regular expression | `""` | Model ids allowed on codex. |
| `backends.codex.tiers.<tier>` | model id | `""` | Model of each tier on codex; empty leaves the tier unavailable. |
| `backends.codex.sandbox` | `read-only`, `workspace-write`, `danger-full-access` | `"workspace-write"` | Sandbox mode passed to codex (`--sandbox`). On Windows a `read-only` or `workspace-write` turn also gets `-c windows.sandbox=unelevated` (see below). |
| `backends.claude.enabled` | boolean | `false` | Allow tasks on the Claude Code CLI. |
| `backends.claude.allowedModels` | regular expression | `""` | Model ids allowed on claude. |
| `backends.claude.tiers.<tier>` | model id | `""` | Model of each tier on claude; empty leaves the tier unavailable. |

`allowedModels` is a case-insensitive Go (RE2) regular expression, matched anywhere in the model id unless you anchor
it with `^` and `$`. Every model a worker gets must match it, including the tier models (a tier model that does not
match is a validation error). An **empty** pattern allows any model. The old version refused every model on an
empty pattern for codex, claude and providers (agy allowed any, as now), although its dashboard showed "any model";
`seelegent migrate` turns such an old empty pattern into one that allows only the tier models (`^$` when no tier is
set). RE2 has no look-ahead: write `"^gemini-3\\.8-"` (the JSON spelling of
`^gemini-3\.8-`) rather than `"^(?!gemini-4)"`.

A codex turn runs `codex exec --json --sandbox <mode> [-c windows.sandbox=unelevated] [--model <m>]
--skip-git-repo-check [resume <id>] -` with the brief on standard input (plan mode is always `read-only`, which runs
the shell commands that only read; the brief tells a codex plan turn to read files with them). The exec
options come before `resume`, which a continued turn adds: `codex exec resume` of codex-cli 0.167.0 has no
`--sandbox` and refuses it. The `-c` override is
added on Windows for `read-only` and `workspace-write`: codex-cli 0.167.0 with no `[windows]` table in its
`config.toml` refuses every shell command of such a turn ("rejected: blocked by policy"). `windows.sandbox` is the
key that version reads (`elevated`, `unelevated` or `mxc`; the old `experimental_windows_sandbox` and
`elevated_windows_sandbox` features are removed); `elevated` needs a one-time administrator setup, so Seelegent uses
`unelevated`. The value is written bare: codex takes a value that is not valid TOML as a literal string, and a
double quote could not pass through the `codex.cmd` shim npm installs. When codex's policy still refuses commands,
they are listed in the turn's denied actions (`denied_actions` in the report), and a turn in which every shell command
was refused fails instead of ending done.

### Accounts, quota and timeouts

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `maxConcurrentPerAccount` | integer 1–64 | `2` | Turns that may run at the same time on one account. |
| `quotaRefreshMinutes` | integer ≥ 1 | `10` | How often quota is read from every account (each cycle ±10 % so accounts are not all read at the same instant). A change applies at once: the next cycle comes one new interval after the last one ended, or right away when that time has passed. Accounts marked signed out are read every 6 hours instead, or at once after a sign-in or a refused run. When the daemon is idle (no pending task, no open dashboard, and no dashboard, CLI or MCP call for 30 minutes) the cycles stretch to 6× the interval, at most 60 minutes; the first call after that reads at once when the last cycle is older than the interval (the call does not wait for it). After a worker run that ended well the account is read only when its quota is over 2 minutes old; after a failed run, always. |
| `minRemaining` | number 0 ≤ x < 1 | `0.03` | An account is skipped for a quota group once its remaining fraction is at or below this; it rests until the used-up windows reset. |
| `passiveCooldownMinutes` | integer ≥ 1 | `60` | Rest after a quota error for API-key accounts whose provider cannot report the reset time. |
| `usageHistory` | boolean | `true` | Record hourly usage snapshots (kept 30 days). The usage summary per task and account is recorded either way. |
| `taskTimeoutMinutes` | integer ≥ 1 | `45` | Hard limit of one worker turn. |
| `linkIntoProfiles` | list of relative paths | `.gitconfig`, `.git-credentials`, `.npmrc`, `.ssh`, `.config`, `.cargo`, `.rustup`, `.m2`, `.gradle`, `.nuget`, `go` | Entries of your real home directory linked into every isolated profile so worker commands keep their configuration. `[]` links nothing. |

### Attention

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `stallMinutes` | integer ≥ 1 | `10` | Flag a running turn that produced no event for this long. |
| `stallToolMinutes` | integer ≥ 1 | `25` | Flag a single tool call running this long. |
| `loopRepeat` | integer 2–100 | `4` | Flag a worker as looping when the same tool call (same tool and parameters) comes back this many times within its last 12 tool calls with no progress in between (a successful edit seen once), or alternates this many times with the same other call. Takes effect at once. Effective up to 12: the check looks at the last 12 tool calls, so a value above 12 turns it off (such values are still accepted, so existing files keep loading); five failed calls in a row are flagged whatever the value. |
| `attentionDecayHours` | integer ≥ 1 | `24` | Drop an unanswered attention flag after this long. |

### Gates, lanes and safety

Gates, lanes, the verifier and the proof ledger are described in [gates.md](gates.md).

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `gates` | boolean | `true` | Run the gate commands after each worker report; `false` leaves only the lane and hygiene steps. |
| `gateAutoChecks` | boolean | `true` | Add syntax checks of the changed `.json`, `.go` and `.js` files to the gate (a parse error the HEAD version did not have fails it). |
| `gateTimeoutSec` | integer 1–86400 | `300` | Time limit of one gate command. |
| `gateMaxAutoFixes` | integer 0–10 | `2` | Automatic fix turns per lead instruction, shared by the gate and the verifier; a lead or user followup starts the count again. |
| `gateSettleMinutes` | integer ≥ 0 | `10` | How long a red gate waits for other running tasks in the same repository before it runs again. |
| `gateSettlePollMs` | integer ≥ 100 | `15000` | How often that wait checks the other tasks. |
| `gateOutputTailBytes` | integer 1–1048576 | `2000` | Output tail kept for each gate step. |
| `hygiene` | boolean | `true` | Add the hygiene step (line endings, BOM, junk and unreported files) to the gate. |
| `lanes` | `off`, `warn`, `enforce` | `"enforce"` | How file lanes are enforced: `enforce` refuses an overlapping claim (unless `forceLane`) and fails the gate for files outside the lane, `warn` only reports, `off` ignores lanes. |
| `laneHoldHours` | integer ≥ 0 | `12` | How long a finished but unreviewed task keeps its lane claim. |
| `verifier` | boolean | `false` | Default of the per-task `verify` option (an LLM review of a green gate). |
| `verifierTier` | tier | `"fast"` | Model tier of the verifier. |
| `gitGuard` | boolean | `true` | Install the git guard that stops workers from destroying uncommitted work. |
| `codeGraph` | boolean | `true` | Build a code graph of the repository and offer it to workers. Not implemented yet: the value is stored and validated, but has no effect. |
| `snapshotRetentionDays` | integer ≥ 1 | `14` | How long the turn snapshots and attempt folders of a finished task are kept after its last change ([safety.md](safety.md)). |
| `contentionWindowHours` | integer ≥ 1 | `24` | How far back tasks count when warning that several tasks touch the same files. |

### Resources

How much of the machine Seelegent's worker trees, gate commands and the daemon itself may use. Every change applies
without restarting the daemon: a running worker or gate command keeps the limits it started with, the next one gets
the new values (the "Applies" column). A memory reading the system cannot give never holds anything back.

`gateConcurrency`, `minFreeMemoryMB`, `minFreeCommitMB`, `criticalFreeMemoryMB` and `testParallelism` are **auto**
by default: while the key is absent from `config.json` (or `null`), the daemon computes the value from the machine,
so one default suits a 4 GB laptop and a 128 GB workstation. A value you set wins, and `0` still means off or
unlimited; setting the key to `null` again (`PATCH /api/v1/settings` with `null`, or "Use auto" in the dashboard)
returns it to auto. In the dashboard, "Set" opens the field at the value in use, and leaving it unchanged keeps
the setting auto: to pin the current value, type a different number and then the one you want, or write it into
`config.json` (or `PATCH` it). A computed value is never written to `config.json`. `GET /api/v1/health`
(`resources`, each with a `source`, and `machine`) and `seelegent doctor` (check `resources`) show the values in use.
The source is
`explicit` for a value you set, `auto` for one computed from the machine, and `derived` for an auto memory threshold
computed from the other one you set (see the last point below).

| Auto setting | Formula | 8 GB, 4 CPUs (Windows, 12 GB commit) | 16 GB, 16 CPUs (Linux) | 31 GB, 24 CPUs (Windows, 36 GB commit) |
| --- | --- | --- | --- | --- |
| `minFreeMemoryMB` | 12.5% of the physical memory, from 512 to 2048 | 1024 | 2048 | 2048 |
| `criticalFreeMemoryMB` | half of `minFreeMemoryMB` | 512 | 1024 | 1024 |
| `minFreeCommitMB` | Windows with a known commit limit: 10% of it, from 1024 to 4096; else `0` | 1228 | 0 | 3686 |
| `testParallelism` | a quarter of the CPUs, from 1 to 8 | 1 | 4 | 6 |
| `gateConcurrency` | an eighth of the CPUs or one per 8 GB of memory, whichever is less, from 1 to 4 | 1 | 2 | 3 |

- The machine is read once when the daemon starts and again when the settings change, never per memory reading, so
  the auto thresholds hold still. Physical memory is the cgroup memory limit when that is lower (containers; cgroup
  v2, else the v1 `memory.limit_in_bytes`); the
  commit limit is rounded down to a whole GB, so a growing page file does not move them.
- The CPUs are the ones the daemon may use: `GOMAXPROCS` (which follows the cgroup CPU limit on Linux), or, when
  `GOMAXPROCS` is set in the daemon's environment, the logical CPUs lowered to the cgroup v2 `cpu.max` (v1:
  `cpu.cfs_quota_us` / `cpu.cfs_period_us`).
- When only one of `minFreeMemoryMB` and `criticalFreeMemoryMB` is set, the other is derived so the floor stays below
  the minimum: an auto floor is half the minimum you set (half the auto minimum when yours is `0`), and an auto
  minimum is twice a floor you set at or above the auto minimum. Such a value has the source `derived`. A floor set
  alone may be at most 524288 (so that twice it stays in range); the rule "the floor must be below the minimum" is
  checked when you set both.

| Setting | Type | Default | Applies | Meaning |
| --- | --- | --- | --- | --- |
| `workerPriority` | `low`, `normal` | `"low"` | next launch | Process priority of worker trees. `low` is `BELOW_NORMAL_PRIORITY_CLASS` for the whole Job Object on Windows (every descendant included; the launcher itself stays at normal priority) and nice 10 on Unix (the launcher lowers its own nice before it starts the worker, so every descendant inherits it). Low priority only shares the CPU: it does not lower memory or disk pressure. |
| `gatePriority` | `low`, `normal` | `"low"` | next command | Process priority of gate commands, proof regression runs, automation prechecks and event hooks. git, quota reads and `gh` always run at normal priority. Low priority makes gates slower on a busy machine, so they may need a larger `gateTimeoutSec`. |
| `maxConcurrentWorkers` | integer 0–256 | `0` | now | Worker turns that may run at the same time over all accounts (`maxConcurrentPerAccount` still caps each account); `0` = no cap. |
| `gateConcurrency` | integer 0–64 or `null` | auto | now | Gate commands and regression runs that may run at the same time; the others wait in order with the gate phase `queued`, and their timeout starts only when their command starts. `0` = unlimited. |
| `minFreeMemoryMB` | integer 0–1048576 or `null` | auto | next check | A new worker turn or gate command waits while the available physical memory is below this many MB (the task shows status `waiting_quota` with `waiting: "memory"`, a gate phase `queued` with wait `memory`); `0` = off. |
| `minFreeCommitMB` | integer 0–1048576 or `null` | auto | next check | The same for the available commit (RAM plus page file). Windows only: elsewhere commit is unknown and never holds anything. `0` = off. |
| `criticalFreeMemoryMB` | integer 0–1048576 or `null` | auto | next check | Emergency floor. While free physical memory or free commit is below it, the newest running gate command is stopped (its gate is queued again and the turn does not fail; after 3 such stops in one gate phase the gate ends in error), every new start waits, and the dashboard shows that the machine is low on memory. Workers are never stopped for it. While both are set and on it must be below `minFreeMemoryMB`. `0` = off. |
| `workerMemoryLimitMB` | integer 0–1048576 | `0` | next launch | Windows only: memory cap of each worker tree (Job Object). A turn that reaches it fails with a message saying so; edits already on disk stay. `0` = off. |
| `workerCPURatePercent` | integer 0–100 | `0` | next launch | Windows only: hard cap of each worker tree's CPU, as a percentage of the whole machine (Job Object CPU rate control). `0` = off. |
| `testParallelism` | integer 0–64 or `null` | auto | next full brief, next gate | Parallelism hint for tests and builds: full briefs get a "Shared machine" section (test only the packages you changed; cap `-p`, `-j`, `--maxWorkers`, `cargo -j`, `pytest -n`), and the environments of worker turns, gate commands and regression runs get `-p=N` appended to `GOFLAGS` (unless the daemon's `GOFLAGS` sets `-p` already), `CARGO_BUILD_JOBS=N` and `PYTEST_XDIST_AUTO_NUM_WORKERS=N` (unless the daemon's environment sets them). These are defaults only: a `-p`, `-j` or `-n` on a command line wins. `0` = no hint and no variables. |
| `daemonMemoryLimitMB` | integer 0–65536 | `0` | now | Soft memory limit of the daemon process (Go's `debug.SetMemoryLimit`): the garbage collector works harder near it, the daemon is never stopped for it. A `GOMEMLIMIT` in the daemon's environment wins. `0` = off. |

### GitHub and automations

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `githubPollMinutes` | integer ≥ 0 | `10` | How often pull request states and GitHub inboxes are polled; `0` turns polling off. A poll with no connected GitHub repository and no open ticket does nothing (it does not even check the access, which may run `gh`). |
| `githubEnterpriseHosts` | list of strings | `[]` | GitHub Enterprise Server hosts whose remotes count as GitHub besides `github.com`: a host name (optionally with a port) or an `https` URL; `http` only for a loopback address. Any other host is never sent the token ([github.md](github.md)). |
| `automationsEnabled` | boolean | `true` | Master switch of scheduled automations (each automation is still created disabled). |

Upper limits not listed above (for example a week for minute settings) only catch typos.

## Keys of the old `settings.json` that are not settings any more

The old Node daemon kept its settings in `settings.json`. `seelegent migrate` imports it; these keys are not part of
`config.json` (if they appear there they are kept and reported as unknown):

| Old key | Why |
| --- | --- |
| `tiers`, `allowedModels` (top level) | legacy mirrors of `backends.agy.tiers` and `backends.agy.allowedModels` |
| `updates` | never read by the old daemon |
| `providers` | API-key provider profiles live in the `providers` table of `seelegent.db`; `seelegent migrate` imports them there ([setup.md](setup.md)) |
| `backends.opencode` | the opencode backend was removed |
