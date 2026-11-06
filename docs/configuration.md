# Configuration

Seelegent keeps its data in one directory and its settings in one JSON file in that directory. The code that owns
both is `internal/config`; defaults live only in `config.Defaults()`.

## Data directory

The data directory is `$SEELEGENT_HOME` when that variable is set, otherwise `~/.seelegent`. Directories are created
with owner-only permissions when a command starts, never earlier.

| Path               | What it holds                                                                          |
| ------------------ | -------------------------------------------------------------------------------------- |
| `config.json`      | the settings described below                                                           |
| `seelegent.db`     | the SQLite database (WAL mode, so `seelegent.db-wal` and `seelegent.db-shm` sit beside it): accounts, quota cache, rest periods, usage history (hourly snapshots kept 30 days, attempt usage kept 60 days) |
| `profiles/<id>/`   | one isolated CLI profile per worker account (same place as the old version)            |
| `profiles/<id>/home/` | the home directory the worker CLI sees, with links into your real home (`linkIntoProfiles`) |
| `profiles/<id>/seelegent-key` | API key of an API-key account: a DPAPI blob on Windows, a 0600 file elsewhere; never inside `home/` |
| `logs/`            | `daemon.log` and its rotated copies `daemon.log.1` … (JSON lines, secrets masked)      |
| `run/daemon.lock`  | single-instance lock of the daemon                                                     |
| `run/daemon.port`  | port of the running daemon                                                             |
| `backups/`         | copies taken before migrations and imports                                             |

## Environment variables

| Variable          | Effect                                                                                       |
| ----------------- | -------------------------------------------------------------------------------------------- |
| `SEELEGENT_HOME`  | data directory instead of `~/.seelegent`                                                     |
| `SEELEGENT_PORT`  | daemon port instead of the `port` setting; never written to `config.json`. Development uses 47831 |
| `SEELEGENT_AGY`, `SEELEGENT_CLAUDE`, `SEELEGENT_CODEX` | path of that worker CLI instead of searching its install locations and `PATH` |

## How the file is read and written

- A missing `config.json` means "all defaults"; it is written the first time a setting is saved.
- Every key is optional. A missing key, or a key set to `null`, takes its default.
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
| `backends.codex.sandbox` | `read-only`, `workspace-write`, `danger-full-access` | `"workspace-write"` | Sandbox mode passed to codex. |
| `backends.claude.enabled` | boolean | `false` | Allow tasks on the Claude Code CLI. |
| `backends.claude.allowedModels` | regular expression | `""` | Model ids allowed on claude. |
| `backends.claude.tiers.<tier>` | model id | `""` | Model of each tier on claude; empty leaves the tier unavailable. |

`allowedModels` is a case-insensitive Go (RE2) regular expression, matched anywhere in the model id unless you anchor
it with `^` and `$`. Every model a worker gets must match it, including the tier models (a tier model that does not
match is a validation error). An **empty** pattern allows only the models listed in that backend's `tiers`. RE2 has
no look-ahead: write `"^gemini-3\\.8-"` (the JSON spelling of `^gemini-3\.8-`) rather than `"^(?!gemini-4)"`.

### Accounts, quota and timeouts

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `maxConcurrentPerAccount` | integer 1–64 | `2` | Turns that may run at the same time on one account. |
| `quotaRefreshMinutes` | integer ≥ 1 | `10` | How often quota is read from every account (each cycle ±10 % so accounts are not all read at the same instant). A change applies from the next cycle. |
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
| `loopRepeat` | integer 2–100 | `4` | Flag a worker that repeats the same tool call this many times in a row. |
| `attentionDecayHours` | integer ≥ 1 | `24` | Drop an unanswered attention flag after this long. |

### Gates, lanes and safety

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `gates` | boolean | `true` | Run the gate commands after each worker report; `false` leaves only the lane and hygiene steps. |
| `gateAutoChecks` | boolean | `true` | Add cheap syntax checks of changed files to the gate. |
| `gateTimeoutSec` | integer 1–86400 | `300` | Time limit of one gate command. |
| `gateMaxAutoFixes` | integer 0–10 | `2` | Automatic fix turns per lead instruction (shared by the gate, the verifier and background-wait continuations). |
| `gateSettleMinutes` | integer ≥ 0 | `10` | How long a red gate waits for other running tasks in the same repository before it runs again. |
| `gateSettlePollMs` | integer ≥ 100 | `15000` | How often that wait checks the other tasks. |
| `gateOutputTailBytes` | integer 1–1048576 | `2000` | Output tail kept for each gate step. |
| `hygiene` | boolean | `true` | Add the hygiene step (line endings, BOM, junk and unreported files) to the gate. |
| `lanes` | `off`, `warn`, `enforce` | `"enforce"` | How file lanes are enforced. |
| `laneHoldHours` | integer ≥ 0 | `12` | How long a finished but unreviewed task keeps its lane claim. |
| `verifier` | boolean | `false` | Default of the per-task `verify` option (an LLM review of a green gate). |
| `verifierTier` | tier | `"fast"` | Model tier of the verifier. |
| `gitGuard` | boolean | `true` | Install the git guard that stops workers from destroying uncommitted work. |
| `codeGraph` | boolean | `true` | Build a code graph of the repository and offer it to workers. |
| `snapshotRetentionDays` | integer ≥ 1 | `14` | How long work-at-risk snapshots of reviewed tasks are kept. |
| `contentionWindowHours` | integer ≥ 1 | `24` | How far back tasks count when warning that several tasks touch the same files. |

### GitHub and automations

| Setting | Type | Default | Meaning |
| --- | --- | --- | --- |
| `githubPollMinutes` | integer ≥ 0 | `10` | How often pull request states and GitHub inboxes are polled; `0` turns polling off. |
| `automationsEnabled` | boolean | `true` | Master switch of scheduled automations (each automation is still created disabled). |

Upper limits not listed above (for example a week for minute settings) only catch typos.

## Keys of the old `settings.json` that are not settings any more

The old Node daemon kept its settings in `settings.json`. `seelegent migrate` imports it; these keys are not part of
`config.json` (if they appear there they are kept and reported as unknown):

| Old key | Why |
| --- | --- |
| `tiers`, `allowedModels` (top level) | legacy mirrors of `backends.agy.tiers` and `backends.agy.allowedModels` |
| `updates` | never read by the old daemon |
| `providers` | API-key provider profiles are deferred to the accounts milestone, which decides where they live |
| `backends.opencode` | the opencode backend was removed |
