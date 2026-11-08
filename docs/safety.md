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
subshells, wrappers such as `env`, `xargs`, `timeout`, `chrt`, `parallel`, `script -c`, `su -c`, `find -exec`,
cmd's `if`; interpreters such as `bash -c`, `powershell -EncodedCommand`, `node -e`, `python -c`; a script a
shell or an interpreter reads from standard input: `bash <<'EOF'`, `cat <<'EOF' | sh`, `sh -s <<< '...'`,
`printf 'git stash\n' | bash` (with `printf`'s and `echo`'s escapes decoded), `{ echo git stash; } | sh` and
`(...) | sh`, `echo git stash | xargs -I{} sh -c '{}'`, `pwsh -Command -`, `python3 - <<'EOF'`) and decides. A here-document fed to any other command (`cat` building a commit message, `tee`,
`sed`, a script file) is data. When the shell is not known (agy's hook reads every dialect), the lines of a POSIX
here-document are read only as POSIX reads them, except for agy on Windows: its `run_command` runs PowerShell,
where `<<` starts no here-document and the next lines run as commands, so there `cat <<'EOF'` followed by a line
`git reset --hard` is refused. The strings of inline code (`python -c`, `node -e`, a script fed to an interpreter) are read as
command lines only when the code can start a program (`os.system`, `subprocess`, `child_process`, `exec*`,
`spawn*`, `popen`, Perl's and Ruby's backticks, `qx`, `%x` and pipe `open`, ...); code that cannot, such as a
script that edits a file mentioning `git stash`, is data. Long options count by any prefix git accepts
(`commit --amen`, `branch --d`). The subcommand is read without a trailing `.exe`, which Git for Windows strips
(`git reset.exe --hard` runs `reset --hard`). It refuses:

- always: `reset`, `restore`, `switch`, `revert`, `rebase`, `merge`, `pull`, `push`, `cherry-pick`, `am`, `rm`,
  `mv`, `prune`, `filter-branch`, `filter-repo`, `update-ref`, `update-index`, `read-tree`, `checkout-index`,
  `merge-file`, `replace`, `send-pack`; except `update-index --refresh` or `--really-refresh`, alone or with only
  `-q`, `--unmerged`, `--ignore-missing` and `--ignore-submodules` (whole option names, no path), which only
  refreshes the stat information (every other `update-index`, such as `--add`, `--remove`, `--force-remove`,
  `--cacheinfo`, `--index-info`, `--assume-unchanged`, `--skip-worktree` or a path, stays refused);
- `stash` except `list` and `show`; `clean` without `-n`; `checkout` except `checkout -b <new> [<start>]` (`-B`
  resets an existing branch); `commit -a|--all|--amend`; `branch` and `tag` deletes, moves and forced writes;
  `apply` unless it only checks; `reflog expire|delete|drop` (the stash is a reflog); `worktree remove|prune|move`
  and `worktree add -B`; `replay` unless `--ref-action=print`; `fetch --update-head-ok` and `--stdin`, and a fetch
  with a refspec that can move a branch back or delete one. Every refspec that applies is checked: the command
  line's, the `--refmap` values, or else the `remote.<name>.fetch` refspecs of the remotes it reads (a name is a
  remote group, then a configured remote, even `a/b`, and only then a URL or a path, as git reads it; read with
  `git config --get-regexp` in the command's folder, with its `-C`/`--git-dir`/`--work-tree`; a configuration that
  cannot be read refuses the fetch). A destination (compared in lower case, as on a disk that ignores case) that is
  empty, `FETCH_HEAD` or under `refs/remotes/` or `refs/tags/` passes; any other is refused when it has a
  wildcard, when the refspec is forced (`+` or `--force`), when the fetch prunes (`--prune`, `-p`, `--prune-tags`,
  or `fetch.prune`/`remote.<name>.prune` set), or when it is under `refs/` outside `refs/heads/` (git updates such
  refs whatever their history). `remote add --mirror`, and `remote prune` and `remote update` by the same rule;
  writes (also `-c` and `--config-env`) of `remote.<name>.fetch` outside `refs/remotes/`, and of
  `remote.<name>.mirror`, `remote.<name>.prune[Tags]` and `fetch.prune[Tags]` other than false;
  `repack -a -d` without `-A` or `--cruft` (it drops unreachable objects); `submodule deinit|update|absorbgitdirs`;
  `config` writes of keys that run programs or redirect git (aliases, includes, `core.hooksPath`, `core.editor`,
  filters and drivers, ...) and of `gc.pruneExpire`, `gc[.<pattern>].reflogExpire[Unreachable]` and
  `help.autocorrect` (also as `-c`/`--config-env`; `git config <key>` and `git config get <key>` only read and
  pass), `config --edit` and `--rename-section` (the editor or the renamed section can bring any key);
  `sparse-checkout` writes; `bisect` steps; `rerere forget|clear|gc`; `notes remove|prune`; `refs migrate`;
  `symbolic-ref -d`; `gc --prune=<date>` (plain `gc` and `gc --prune` keep the default expiry);
- on a disk that ignores case, a new branch whose name matches an existing one in all but case (`Main` beside
  `main`): git writes the loose ref `refs/heads/Main`, which is the file of `main`, and `main` moves to the new
  branch's commit, packed or not. Checked for `branch <new>`, `checkout -b`, `worktree add -b` (and the branch
  `worktree add <path> [<branch>]` creates without `-b`) and every fetch destination under `refs/heads/` (also the
  short `src:Main`); `switch` and the `-B`, `-c`, `-m` forms are refused anyway. The branches are read with
  `git for-each-ref refs/heads/` in the command's folder: always on Windows and macOS (`core.ignorecase=false`
  does not change the disk), elsewhere when `core.ignorecase` is true. A list that cannot be read, or a branch
  name the hook cannot see (`git checkout -b $NAME`), is refused there;
- options, settings and URLs that make git run a command of the caller's choosing: `grep -O|--open-files-in-pager`,
  `difftool -x|--extcmd`, `--upload-pack`, `--receive-pack` and `--exec` on the transport commands (and `clone
  -u`), an `ext::` URL (also as a config value), `protocol.allow` and `protocol.<name>.allow`,
  `core.alternateRefsCommand`, `interactive.diffFilter`, risky keys in `clone -c|--config`, `--exec-path=<dir>`
  and `send-email`'s `--*-cmd` and `--smtp-server=<path>`; a reading command whose argument starts with an
  expansion where an option would (`git grep "-O$x"`) fails closed, one that starts with the expansion (`git grep
  "$pattern"`) is a pattern;
- an alias that expands to any of the above (read with `git config --get alias.<name>` in the command's folder);
- a command that sets `GIT_CONFIG_PARAMETERS`, `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_<n>`, `GIT_CONFIG_GLOBAL`,
  `GIT_CONFIG_SYSTEM` or a variable whose value git runs (`GIT_ALLOW_PROTOCOL`, `GIT_SSH_COMMAND`, `GIT_SSH`,
  `GIT_PROXY_COMMAND`, `GIT_EXTERNAL_DIFF`, `GIT_EXEC_PATH`), or an editor, pager or password prompt git runs
  (`GIT_EDITOR`, `GIT_SEQUENCE_EDITOR`, `GIT_PAGER`, `GIT_ASKPASS`, `EDITOR`, `VISUAL`, `PAGER`, `SSH_ASKPASS`) to
  anything but empty, `cat`, `less`, `more` or `true` with options (`NAME=value git ...`, `env NAME=value`, `export`/`declare`, PowerShell's `$env:NAME = ...`,
  `Set-Item env:NAME` and `[Environment]::SetEnvironmentVariable`, cmd's `set` and `setx`, the environment of
  inline code that starts programs), whether or not the same line runs git: they define aliases and settings that
  the alias lookup above does not see, for that command and every later git command of the same shell;
- fail closed: a line that mentions git and cannot be read (an unclosed quote, a variable used as the git
  subcommand or an option, nesting deeper than the limit). "Mentions git" is any `git` in the line, also once the
  quotes and escape characters of every dialect are taken out, except inside the words `digit`, `legit`,
  `.gitignore`, `.gitattributes`, `.gitkeep`, `github`, `gitlab` and `gitea`, which name no git program or folder
  (field report 4, F11). The refusal quotes the place with some context (`mentions git: "…the digits and then git
  stash…"`), and the daemon log keeps the whole refused command (the task's events cut it short).

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
`delegate`/`followup` (MCP `allow_git`) applies for this command only: this turn and its automatic fix turns; a
followup must pass it again. It is stored on the turn it opens (`Turn.allowGit`): the gate's and the verifier's fix
turns inherit it from the turn they fix, retries and handovers run the same turn, and a daemon restart keeps it. For
such a turn the daemon sets `SEELEGENT_GIT_GUARD=off` in the worker CLI's environment and leaves the shim off `PATH`
(as whenever `gitGuard` is off). Any other new turn (a followup or answer without it, a takeover's handback) runs
guarded.

Only the hooks read `SEELEGENT_GIT_GUARD`. They inherit the environment of the worker CLI, which the daemon builds
(the variable is never passed through from the daemon's own environment); the commands the worker runs are child
processes of the CLI (Claude Code's `Bash`/`PowerShell` tools, agy's `run_command` terminals and the shells
`send_command_input` writes to), and a child cannot change its parent's environment. The shim runs inside those
commands, so `env SEELEGENT_GIT_GUARD=off git stash` would reach it: it ignores the variable, and `PATH` alone
decides whether a worker is guarded. The shim also runs git with `-c help.autocorrect=0`, so a misspelled
subcommand (`git stsh`) is not run as the command git guesses. git takes the last `-c` of a key, so a worker's own
`-c help.autocorrect=...` is refused; the `GIT_CONFIG_*` environment variables come before `-c` and cannot undo it.

### Limits

- The shim only sees programs that look git up on `PATH`. A Git Bash login shell (`bash -l`) rebuilds `PATH` and
  may put its own `git` ahead of the shim; a script that calls git by its full path, a build tool or an IDE
  integration is not seen either.
- The guard reads command lines, not files: a command stored in a variable and run later, a script file the worker
  wrote, or a Makefile target are not read. Dynamic words in the git part are refused (fail closed), dynamic words
  elsewhere are not. What is piped into a shell is read from simple commands, subshells, brace groups and compound
  commands (`for x in a; do echo git stash; done | sh`, `if`, `while`, `until`, `case`), and a shell inside a group
  on the read end of a pipe reads it too (`echo git stash | (cd x && bash)`); `xargs` reading its input from a file
  (`-a`, `--arg-file`) into a shell is not read.
- codex has no pre-tool hook. Its own sandbox (`backends.codex.sandbox`, default `workspace-write`; plan mode is
  `read-only`) limits where it can write; the shim covers the git commands it runs through `PATH`. The
  `workspace-write` sandbox also keeps `.git` read-only, so on codex every git command that writes `.git` (even
  `git update-index --refresh` or `git add`) fails there with a permission error; the git shim is the second layer.
  With `danger-full-access` only the shim remains. On Windows the sandbox is codex's unelevated one
  (`-c windows.sandbox=unelevated`, see [configuration.md](configuration.md)); commands it refuses are listed in the
  turn's denied actions, and a turn whose every command was refused fails. That includes a turn that edited files
  with apply_patch and whose only shell command (the tests, say) was refused: the worker could not verify its
  change, so the lead decides; the edits stay in the working tree for the lead to review.
- The hooks do not run git, so they cannot turn off `help.autocorrect`: when the user's git config sets it, a
  misspelled subcommand (`git stsh`) passes the hook as an unknown command and git runs the command it guesses.
  The shim is not affected. A worker cannot set it itself: `git config help.autocorrect <value>` and `-c` are refused.
- Configuration the alias lookup cannot see is caught only in the forms listed above. Set through a computed name
  (`export GIT_CONFIG_$N=...`), a script file, `set -a`, or through `HOME`/`XDG_CONFIG_HOME` pointing at another
  `.gitconfig`, it is not; and the aliases of `git -C <other repository>` are looked up in the command's folder.
- Inline code is read as commands only when it names a way to start a program. Code that reaches one through a
  computed name (`getattr(os, 'sys' + 'tem')`) or a module the guard does not know (Python's `sh`, `plumbum`)
  passes; code that does start a program has all its strings read, so it is refused when it also edits text that
  mentions a risky git command (the refusal then suggests the edit tool).
- Work at risk counts the lines an earlier turn added to a file (its end snapshot minus its start snapshot) as the
  task's own, so lines the user typed into that file while the turn ran count as the task's too, and a later turn
  that deletes them gives no warning (only other tasks' changes are told apart from the task's, not the user's).
- The hooks live in files of the worker's own profile (`settings.json`, `hooks.json`). A worker that edits those
  files, with its edit tools or a shell command, can remove the hook or (Claude Code's `env` setting) set
  `SEELEGENT_GIT_GUARD`; writes into the profile folders raise no scope warning.
- Forms the guard does not read: GNU `parallel` reading its commands from standard input or a file (`parallel <
  cmds`, `--arg-file`); a `ProcessStartInfo` whose `Arguments` are set as a property before
  `[Diagnostics.Process]::Start($psi)`; programs started through other language runtimes or APIs than the inline
  code of the interpreters above (a compiled helper, `Invoke-CimMethod Win32_Process`, `wmic process call create`,
  `schtasks`). Wrappers not in the list (any program that runs its arguments as a command) are not followed.
- The snapshots below are the safety net for everything the guard cannot see.

## Snapshots

Package `internal/snapshot`. Before every turn of a task in a git repository (once the task's repository root is
known) and after it, the task engine records the repository's uncommitted files. The start snapshot is taken right
before the turn's first launch, once an account is leased, not when the turn is queued: what others change while
the turn waits for an account or for memory is not the turn's work. Retries and handovers of the turn keep that
snapshot. A turn in which no worker ever ran (it waited and was cancelled, or every launch failed before the worker
started) gets no post-turn checks: no work at risk, no hygiene findings, no changed files. A snapshot:

- `git status` (read-only, `GIT_OPTIONAL_LOCKS=0`) lists the changed, deleted and untracked files; nothing touches
  the user's index, stash, refs or objects (bugs.md D7, D25c);
- each file's bytes go into a content-addressed store in the data directory, `snapshots/blobs/<aa>/<sha256>`,
  written once with `fsutil.WriteFileAtomic`; a manifest per turn, `snapshots/tasks/<task>/t<n>-<kind>.json`
  (`start`, `end`, `restore-<time>`), names the files, their hashes and `HEAD`;
- limits: 8 MiB per file, 3000 files and 256 MiB per snapshot (`truncated` is set past them). A file over 8 MiB is
  recorded by its size and its hash (read in a stream, up to 1 GiB), never its content, so a change to it is still
  noticed as work at risk but cannot be restored. Every file also records its modification time. The hashes of large
  files and the files the end snapshot reads outside the limits (below) share one read budget of 2 GiB per
  snapshot; past it, and for a file over 1 GiB, a file is compared by size and modification time only
  (`hashBudgetSpent` on the manifest, `Weak` on the risk): a same-size change that kept the time is missed, and a
  rewrite with the same bytes counts as a change. Untracked files under
  `node_modules`, `.venv`, `venv`, `__pycache__`, `.next`, `.nuxt`, `.turbo`, `.cache` and `.gradle` are not copied.
  The end snapshot reads the start snapshot's files and the files the edit tools changed first, outside those
  limits, so a turn that creates thousands of files cannot hide the files it destroyed; only the other dirty files
  can be cut. A turn whose start or end snapshot was cut gets a `snapshot_truncated` event, `snapshotTruncated` on
  the turn, `snapshot_truncated` in the report and a notice in the dashboard's safety panel: files past the limits
  were not checked (for a cut start snapshot, that includes work at risk).

A failed snapshot is logged and the turn runs anyway; its checks are then skipped.

### Retention and the sweep

Every 6 hours (first run 2 minutes after the daemon starts) the sweep removes, for tasks that are no longer in the
database or that finished and were not updated for `snapshotRetentionDays` (default 14), their attempt folders
(`tasks/<id>/`) and their snapshot manifests, then every blob no manifest names any more (bugs.md F5). Tasks that
are not finished, and tasks with a running attempt or a watch, are never swept. A snapshot names its blobs only
once it is saved, after they were written or reused, so every blob is dated with the store's clock when it is
written or reused, and the sweep keeps unnamed blobs dated within an hour before it started (`blobGrace`, well
above the 2- and 3-minute limits of a snapshot and of the post-turn checks). A temp file that a crash left between
a write and its rename (`.<name>.<n>.tmp`, in a task's folder or the blob store) is skipped, and removed once it is
older than that hour.

The `tasks/` folder is shared with the old Node version, whose tasks the database never knew (`seelegent migrate`
does not import them). A task folder is therefore removed only when it is provably this version's: never while the
old version's `tasks/<id>.json` or `tasks/<id>.jsonl` sits next to it; a task the database knows goes by the rule
above; a folder the database does not know goes only when everything directly in it is what this version writes
there (attempt folders `t<turn>-a<n>/`, console folders `console-<ms>/`, gate logs `gate-t<turn>-<step>.log`). The
old version's `<id>/a1-1.stream.jsonl` and anything else put there stay.

## Post-turn checks

They run when a turn ends, before the task's end is published, so a lead woken by the report already sees them.
The turn records `workAtRisk`, `hygiene`, `scope` and `git` (`changed`, `preexisting`); the report adds
`work_at_risk`, `hygiene`, `scope_warnings` and `file_overlaps`.

### Work at risk

Compares the start and end snapshots. A file is at risk when it was dirty before the turn and

- `outside_tools`: it lost at least one uncommitted line, was deleted, or is binary and changed, and the worker's
  edit tools did not touch it (a shell command, a formatter or another program did); or
- `large_removal`: the edit tools touched it and it lost at least 5 lines that were uncommitted before the turn.

Not at risk: files other tasks of the same repository changed while the turn ran, scratch and junk files, lock
files that still exist, and the task's own lines. In a file the task's edit tools changed in an earlier turn, the
lines each earlier turn added (its end snapshot minus its start snapshot) count as committed, so only lines someone
else added (the user, between the turns) can be reported lost; such a file that lost none of them, or a binary one,
is not reported. When an earlier turn's snapshots or content cannot be read, its files are left out of the check as
a whole. A turn with files at risk emits a `work_at_risk` event, and when the task is done its attention is
`work_at_risk` (unless `hygiene` comes first) until the files are restored.

### Restore

`POST /tasks/{id}/turns/{n}/restore` (body `{"files": [...]}`, empty for every file at risk not restored yet) and
`seelegent restore <id> --turn N [--file=<path>]...` write back the content the files had when turn `n` started.
The repository is locked while it runs, and paths are cleaned with `gitx.CleanPath` (no `..`, no absolute paths).
It is refused (`409 bad_state`) while the task is active or taken over.

A file whose content differs from the turn's end state (the turn's end snapshot, or the next turn's start
snapshot when the end one is missing; a file neither lists is compared with HEAD at that time) was changed by
someone after the turn ended. Restore leaves it alone and reports it as skipped with `code: "changed_since_turn"`
(`changed since the turn ended`); `{"force": true}` or `seelegent restore --force` overwrites it anyway. The report's
restore command never forces. The dashboard asks before every restore, lists the skipped files, and offers
"Restore anyway" after a second question.

Before writing, the current content of the files it is about to write is kept in a backup snapshot,
`t<turn>-restore-<time>`, which the result (`backup`), the CLI output and the dashboard name. Restored files are
marked `restored`, and a `restored` event (the files written and the backup) goes into the task's log.

To undo a restore, put the backup's content back by hand: the backup is
`<data dir>/snapshots/tasks/<task id>/<backup>.json`, a manifest whose `files` list each file's `path` and the
`hash` of the content it held (`missing: true` when it did not exist); that content is the blob
`<data dir>/snapshots/blobs/<first two characters of hash>/<hash>`. Copy each blob back to its path (delete the
file for a `missing` entry). The backup is kept as long as the task's other snapshots. The report's `work_at_risk.restore` is the command line to paste (POSIX quoting),
`restore_argv` the same as arguments.

### Outside diff

`GET /tasks/{id}/turns/{n}/outside-diff` returns what turn `n` changed outside the edit tools: start snapshot
against the end snapshot (or the next turn's start, or the working tree while the turn runs), without the files the
edit tools touched, other tasks' files and scratch files. At most 20 files, 400 lines per file and 2000 lines in
all; a turn without snapshots is `404 not_found`.

### Hygiene

For the files the turn changed (not other tasks' files); files over 2 MiB, binary and scratch files are skipped, and
the new-file kinds only look at files that were clean before the turn. Line endings and BOM judge what the turn did:
they are a finding only when the file's style matches none of its content at the start of the turn (when the start
snapshot kept it, that is when it was dirty then), its `HEAD` blob as committed and its `HEAD` blob as a checkout
writes it. Line endings are also no finding when git normalizes the file on commit (`core.autocrlf` true or input,
the `text` and `eol` attributes; asked through `git hash-object --path`, which writes nothing) and the LF form keeps
the style: those line endings never reach a commit. So an LF working copy under `core.autocrlf=true` is never
flagged against the CRLF checkout form, while an LF file that gets CRLF lines where nothing normalizes is. A file
that was dirty before the turn but whose start content the snapshot did not keep is not checked.

- For a file that was dirty at the start of the turn, `HEAD`'s style still counts besides its start content: a
  worker that returns the user's dirty CRLF file to `HEAD`'s LF is not flagged. This is intended: the committed
  style is always a legitimate one, and a fix turn that undoes an earlier turn's line ending change must pass.
- `hash-object` does not read the index, so it misses git's "safer autocrlf" rule: an automatic conversion
  (`core.autocrlf`, `text=auto` with or without `eol`) is skipped when the index version of the file already has a
  CR, and `git add` keeps the CRLF lines. The check applies that rule itself (`git check-attr text eol` and the
  index blob read raw); an explicit `text` or an `eol` without `text=auto` still normalizes.
- Known edge: with `core.safecrlf=true` and mixed line endings, `hash-object` reports the file as normalized while
  `git add` refuses it (the conversion is not reversible). The check then finds nothing; the user's `git add` says
  so instead.

| Kind | Severity | When |
|---|---|---|
| `eol_changed` | red | the file's line endings changed (LF ↔ CRLF) |
| `bom_changed` | red | a byte order mark was added or removed |
| `bom_new_file` | red | a new file starts with a BOM while the repository's tracked files of that extension have none (first 20 checked) |
| `junk_file` | red | a new file that looks like a leftover: `*.orig`, `*.rej`, `*.tmp`, `*.bak`, `*~`, or a Windows device name such as `nul` (from `> nul` in a POSIX shell) |
| `unreported_file` | warn | a new file the worker made (one of its edit tools' files, or a path its shell commands write) that the report names neither by its path (from the repository root or from the task's folder) nor by its file name; a file only the snapshots show, such as one the lead edited during the turn, is not judged |

A red finding makes a done task's attention `hygiene`.

### Scope warnings

- `outside_repo_write` (warn): an edit tool, or a shell command's redirection, copy or move target or a file
  `sed -i`/`perl -i` edits in place (`guard.Writes`, joined to the folder a `cd` of the same line moved to), wrote
  outside the task's folder, the repository, the temp folder and the worker's profile folders. Git Bash paths
  (`/c/...`) are mapped on Windows; dynamic words and `~` are skipped. At most 20.
- `outside_lane` (warn): a task whose gate is off (`gate: false`) has no lane step; its lane is checked after each
  turn with the gate's rules ([gates.md](gates.md#the-lane-check)) and each file outside it is a warning, with the
  detail `outside the lane` (a worker touched it) or `outside the lane, changed during the turn but not by a worker
  edit or command Seelegent saw`.
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
