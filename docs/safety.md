# Safety: git guard, snapshots and the post-turn checks

Workers run in folders where other people (the user, the lead, other workers) keep uncommitted work. Seelegent
protects that work in three layers:

1. the **git guard** refuses the git commands that can throw it away, before they run;
2. **snapshots** record the uncommitted files before and after every turn, so a loss is noticed and can be undone;
3. the **post-turn checks** report what the turn did besides its own edits (work at risk, hygiene, scope,
   contention with other tasks) in the task, its report and its attention state.

None of them ever fails a task. They inform the lead and the dashboard; the lead decides.

## Git guard

Package `internal/guard` is pure: it reads a command line (POSIX shells, PowerShell, cmd.exe; substitutions,
subshells, wrappers such as `env`, `xargs`, `timeout`, `find -exec`; interpreters such as `bash -c`,
`powershell -EncodedCommand`, `node -e`, `python -c`) and decides. It refuses:

- always: `reset`, `restore`, `switch`, `revert`, `rebase`, `merge`, `pull`, `push`, `cherry-pick`, `am`, `rm`,
  `mv`, `prune`, `filter-branch`, `filter-repo`, `update-ref`, `update-index`, `read-tree`, `checkout-index`,
  `merge-file`, `replace`;
- `stash` except `list` and `show`; `clean` without `-n`; `checkout` except `checkout -b|-B <new> [<start>]`;
  `commit -a|--all|--amend`; `branch` and `tag` deletes, moves and forced writes; `apply` unless it only checks;
  `reflog expire|delete`; `worktree remove|prune|move`; `submodule deinit|update|absorbgitdirs`; `config` writes
  of keys that run programs or redirect git (aliases, includes, `core.hooksPath`, `core.editor`, filters and
  drivers, ...); `sparse-checkout` writes; `bisect` steps; `rerere forget|clear|gc`; `notes remove|prune`;
  `refs migrate`; `symbolic-ref -d`;
- an alias that expands to any of the above (read with `git config --get alias.<name>` in the command's folder);
- fail closed: a line that mentions git and cannot be read (an unclosed quote, a variable used as the git
  subcommand or an option, nesting deeper than the limit).

Every refusal starts with `Blocked by Seelegent` (`guard.Marker`). The backend parsers look for that text and mark
the tool call `blocked`; the turn lists the command under `blockedCommands`, the report under `blocked_commands`,
and the dashboard's terminal and safety panel show it.

### Where it runs

| Worker | How |
|---|---|
| Claude Code, isolated account | A `PreToolUse` hook on `Bash` and `PowerShell` in the profile's own `settings.json` runs `seelegent __guard claude`. |
| agy, isolated account | An entry `seelegent-guard` in the profile's `hooks.json` for `run_command` and `send_command_input` runs `seelegent __guard agy` (on Windows through the batch file `.\seelegent-guard.cmd` next to `hooks.json`, as agy runs hooks with `cmd /c` from that folder; the `.\` keeps it working when `NoDefaultCurrentDirectoryInExePath` is set). |
| codex (any account) and system accounts of every backend | The git shim: `<data>/guard/bin` first on the worker's `PATH`. On Windows it is a copy of `seelegent.exe` named `git.exe`; elsewhere a `git` script that runs `seelegent __git`. It checks the arguments and runs the real git found on `PATH` without the shim folder. |

Hooks are written only into the worker's own profile (`Prepare` of the backend), never into the user's real
`~/.claude`, `~/.gemini` or `~/.codex`: a system account's profile is the user's real home, so it gets the shim
instead. Turning `gitGuard` off in the settings removes the hook at the next `Prepare`. `--allow-git` on
`delegate`/`followup` sets `SEELEGENT_GIT_GUARD=off` for that turn, which the hook and the shim both honour.

### Limits

- The shim only sees programs that look git up on `PATH`. A Git Bash login shell (`bash -l`) rebuilds `PATH` and
  may put its own `git` ahead of the shim; a script that calls git by its full path, a build tool or an IDE
  integration is not seen either.
- The guard reads command lines, not files: a command stored in a variable and run later, a script file the worker
  wrote, or a Makefile target are not read. Dynamic words in the git part are refused (fail closed), dynamic words
  elsewhere are not.
- codex has no pre-tool hook. Its own sandbox (`backends.codex.sandbox`, default `workspace-write`; plan mode is
  `read-only`) limits where it can write; the shim covers the git commands it runs through `PATH`. With
  `danger-full-access` only the shim remains.
- The snapshots below are the safety net for everything the guard cannot see.

## Snapshots

Package `internal/snapshot`. Before every turn of a task in a git repository (once the task's repository root is
known) and after it, the task engine records the repository's uncommitted files:

- `git status` (read-only, `GIT_OPTIONAL_LOCKS=0`) lists the changed, deleted and untracked files; nothing touches
  the user's index, stash, refs or objects (bugs.md D7, D25c);
- each file's bytes go into a content-addressed store in the data directory, `snapshots/blobs/<aa>/<sha256>`,
  written once with `fsutil.WriteFileAtomic`; a manifest per turn, `snapshots/tasks/<task>/t<n>-<kind>.json`
  (`start`, `end`, `restore-<time>`), names the files, their hashes and `HEAD`;
- limits: 8 MiB per file, 3000 files and 256 MiB per snapshot (`truncated` is set past them); untracked files under
  `node_modules`, `.venv`, `venv`, `__pycache__`, `.next`, `.nuxt`, `.turbo`, `.cache` and `.gradle` are not copied.

A failed snapshot is logged and the turn runs anyway; its checks are then skipped.

### Retention and the sweep

Every 6 hours (first run 2 minutes after the daemon starts) the sweep removes, for tasks that are no longer in the
database or that finished and were not updated for `snapshotRetentionDays` (default 14), their attempt folders
(`tasks/<id>/`) and their snapshot manifests, then every blob no manifest names any more (bugs.md F5). Tasks that
are not finished, and tasks with a running attempt or a watch, are never swept.

## Post-turn checks

They run when a turn ends, before the task's end is published, so a lead woken by the report already sees them.
The turn records `workAtRisk`, `hygiene`, `scope` and `git` (`changed`, `preexisting`); the report adds
`work_at_risk`, `hygiene`, `scope_warnings` and `file_overlaps`.

### Work at risk

Compares the start and end snapshots. A file is at risk when it was dirty before the turn and

- `outside_tools`: it lost at least one uncommitted line, was deleted, or is binary and changed, and the worker's
  edit tools did not touch it (a shell command, a formatter or another program did); or
- `large_removal`: the edit tools touched it and it lost at least 5 lines that were uncommitted before the turn.

Not at risk: files other tasks of the same repository changed while the turn ran, files of the task's own earlier
turns, scratch and junk files, and lock files that still exist. A turn with files at risk emits a `work_at_risk`
event, and when the task is done its attention is `work_at_risk` (unless `hygiene` comes first) until the files
are restored.

### Restore

`POST /tasks/{id}/turns/{n}/restore` (body `{"files": [...]}`, empty for every file at risk not restored yet) and
`seelegent restore <id> --turn N [--file=<path>]...` write back the content the files had when turn `n` started.
The current content is first kept in a `restore-<time>` snapshot, the repository is locked while it runs, and
paths are cleaned with `gitx.CleanPath` (no `..`, no absolute paths). It is refused (`409 bad_state`) while the
task is active or taken over. Restored files are marked `restored`. The report's `work_at_risk.restore` is the
command line to paste (POSIX quoting), `restore_argv` the same as arguments.

### Outside diff

`GET /tasks/{id}/turns/{n}/outside-diff` returns what turn `n` changed outside the edit tools: start snapshot
against the end snapshot (or the next turn's start, or the working tree while the turn runs), without the files the
edit tools touched, other tasks' files and scratch files. At most 20 files, 400 lines per file and 2000 lines in
all; a turn without snapshots is `404 not_found`.

### Hygiene

For the files the turn changed that were clean before it (not other tasks' files); line endings and BOM are
compared with `HEAD`, files over 2 MiB, binary and scratch files are skipped:

| Kind | Severity | When |
|---|---|---|
| `eol_changed` | red | the file's line endings changed (LF ↔ CRLF) |
| `bom_changed` | red | a byte order mark was added or removed |
| `bom_new_file` | red | a new file starts with a BOM while the repository's tracked files of that extension have none (first 20 checked) |
| `junk_file` | red | a new file that looks like a leftover: `*.orig`, `*.rej`, `*.tmp`, `*.bak`, `*~`, or a Windows device name such as `nul` (from `> nul` in a POSIX shell) |
| `unreported_file` | warn | a new file the report names neither by its path nor by its file name |

A red finding makes a done task's attention `hygiene`.

### Scope warnings

- `outside_repo_write` (warn): an edit tool, or a shell command's redirection, copy or move target
  (`guard.Writes`), wrote outside the task's folder, the repository, the temp folder and the worker's profile
  folders. Git Bash paths (`/c/...`) are mapped on Windows; dynamic words and `~` are skipped. At most 20.
- `unfetched_url` (warn): a URL in the report that appears nowhere in the prompt, the messages, the commands or the
  tool events of the turn (local hosts ignored). At most 10.

### Contention: file overlaps

`TaskDetail.fileOverlaps` and the report's `file_overlaps` list the files this task and another task of the same
repository changed in overlapping turns: `active` when both are still working, `recent` otherwise. Parent and child
tasks are not compared. The repository's recent tasks (up to 200, active first) are read through the task store and
kept 3 seconds per repository; the repository root itself is never cached (bugs.md D14). Running turns count the
files their edit tools touched so far.

## Attention

The safety work added these attention reasons (see [task-engine.md](task-engine.md) for the rest):

| Reason | State | Who | When |
|---|---|---|---|
| `looping` | `blocked` | lead | the last 5 tool calls failed, or the same call came back 4 times in the last 12 without a new successful edit in between (strict alternation with one other call counts only when that call is the same each time, bugs.md A9) |
| `auth` | `blocked` | user | a task waits for quota and every enabled account of its backend needs a sign-in |
| `no_accounts` | `blocked` | user | the task is queued, running or waiting for quota and no account of its backend can run work |
| `hygiene` | `blocked` | lead | the done task's latest turn has red hygiene findings |
| `work_at_risk` | `blocked` | lead | the done task has files at risk that were not restored |
