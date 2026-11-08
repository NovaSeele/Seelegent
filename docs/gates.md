# Gates, lanes, the verifier and proof

Milestone 13. A gate is what a task must pass, after a worker report, before it counts as done. Lanes keep parallel
workers off each other's files. The verifier is an optional second, read-only worker that reviews a green gate.
The proof ledger remembers which Spec Kit tasks passed their gate, so a feature's requirements can be shown as
proven or not.

| Part | Code |
|---|---|
| Spec parsing (`spec.md`, `tasks.md`), lanes, doctrine, frozen bar, evidence check, ledger, regression runs | `internal/proof` |
| Gate plan, command runner, syntax checks, detected commands, fix prompts | `internal/gate` |
| Gate phase, automatic fix turns, lane claims, verifier, statistics | `internal/task` (`gates.go`, `gatephase.go`, `verifier.go`, `report_gate.go`, `gate_stats.go`, `contention.go`) |
| Routes | `internal/httpapi/proof.go` ([api.md](api.md)) |
| MCP `proof` tool and the `gate`/`lane`/`verify`/`force_lane` options | `internal/mcpserver` ([mcp.md](mcp.md)) |
| `seelegent proof` and the gate flags | `internal/cli` ([cli.md](cli.md)) |
| Settings (`gates`, `gateAutoChecks`, `gateTimeoutSec`, `gateMaxAutoFixes`, `gateSettleMinutes`, `gateSettlePollMs`, `gateOutputTailBytes`, `hygiene`, `lanes`, `laneHoldHours`, `verifier`, `verifierTier`) | [configuration.md](configuration.md) |

## Which tasks have a gate

Every edit task (`mode: full`) has one; plan tasks and the verifier's own tasks do not, and a plan task that asks
for `gate`, `gateCommands`, `lane`, `forceLane` or `verify: true` is refused (`400`). The gate runs in the task's
git repository (`Task.repoRoot`, the root above `cwd`); outside a repository it runs in `cwd` and has no lane.

When the task starts, its proof is resolved and frozen (`TaskDetail.proof`):

- **Commands**, in this order: the lead's `gateCommands` (MCP `gate: "cmd"` or a list, CLI `--gate`); otherwise the
  `Gate:` commands of the spec tasks named in `task_ids` and the feature's `Feature gate:` line; plus, with
  `gate: true` (`--gate-detect`), the test, lint and build commands found in the repository (`go.mod`,
  `package.json` scripts `lint`, `typecheck`, `test`, `build`, `Cargo.toml`, a pytest setup, a Makefile with a
  `test`, `check` or `lint` target; the nearest project folder at or above `cwd`). With spec task ids, the lead's
  commands replace the tasks' own and prove those tasks. `gate: false` turns the gate phase off: no commands, no
  automatic fix turn, no settling (with `verify: true` the phase still runs, for the verifier alone). The lane is
  still checked after each turn with the rules of [the lane check](#the-lane-check), as information only: warn
  `outside_lane` scope findings on the turn and the report's `lane` (`out_of_lane`, and a warning "outside the lane
  (gate off, information only)").
- **Doctrine**: the checks of the repository's `.seelegent/doctrine.json`
  (`{"version": 1, "checks": [{"id", "run", "principle"?, "when"?: [globs]}]}`), frozen so a worker cannot loosen
  them. A check with `when` runs only when a changed file matches. A file that cannot be read is reported as
  `doctrineError` and fails the doctrine step.
- **Lane**: the lead's `lane`, or the paths the spec tasks name (back-quoted paths in their `tasks.md` lines: a path
  with a folder, a glob, or a file name with an extension, even one the task has yet to create; spec documents of
  the feature are left out), unless `lane: false`.
- **Bar**: hashes of what defines "done": the gate commands, the constitution
  (`.specify/memory/constitution.md`), the doctrine, the feature's `spec.md`, `plan.md`, `tasks.md` lines of its
  tasks (ticks ignored) and `contracts/`. A worker that changes any of them shows up as `bar.changed` in the report
  and as a gate warning.
- **Verify**: the request's `verify`, else the `verifier` setting.
- **Budget**: `gateMaxAutoFixes`.

A followup that changes `gate`, `gateCommands`, `lane`, `verify` or the spec task ids resolves the proof again (a new
lane is checked like a new claim) and the next worker prompt starts with the new gate and lane. A followup never
takes gate commands from the working tree, which the worker can edit: one that leaves `gate`, `gateCommands` and
the task ids alone runs exactly the frozen commands; a new `gate` or `gateCommands` runs what the lead asked for,
with the spec tasks' `Gate:` lines and feature gate as they were frozen (an empty `gateCommands` list, which is
not the same as leaving it out, clears the lead's commands, so those lines run again); new task ids read `tasks.md` only for the
tasks they add. The first proof also freezes a hash of every `tasks.md` line of the feature, so an added task is
taken only while its line is the one frozen: a line changed since gives no commands and no paths and shows as
`tasks.md#<id>` in `barChanged`; a task `tasks.md` did not have at the freeze is read now and shows in `barChanged`
on every turn, so the lead sees its line was never frozen. The bar is frozen again only for what the lead changed
(the gate commands, the unchanged lines of added tasks), so a `tasks.md` line the worker edited stays in
`barChanged` on every turn. A followup by the
lead or the user resets the automatic fix count.

## The gate phase

When a turn of a gated task ends well, the task stays `running` with `gatePhase {state, turn, since, childId?,
wait?, position?, stops?}` while the daemon runs the gate. The phase survives a daemon restart (the new daemon runs the gate again) and ends
on cancel (a running step becomes `not_run`). Takeover is refused during the phase. A followup by the lead or the
user during the phase (the worker's turn has ended; the gate runs, waits for a slot or for memory, settles, or the
verifier reviews) supersedes it: the gate's commands are stopped as on a cancel, the gate is stored `skipped` with
the warning "superseded by a followup: the gate stopped before it ended", a running verifier is cancelled, and the
new turn starts. A followup while the worker's turn runs is refused (`409`) with what the task is doing ("the
worker's turn is still running", "the gate is running", "its gate waits for the other workers of the repository").

Steps, in order (`GateResult.steps`):

| Kind | What | Fails when |
|---|---|---|
| `lane` | the net change since the lead's instruction against the lane ([the lane check](#the-lane-check)) | a file a worker touched is outside it (`lanes: enforce`; `warn` only warns; `off` has no step); a file outside it that no worker edit or command touched only warns |
| `hygiene` | the milestone 12 hygiene findings still open ([safety.md](safety.md)) | any red finding |
| `auto` | syntax of the changed `.json`, `.go`, `.js`/`.cjs`/`.mjs` files (`gateAutoChecks`) | a parse error the HEAD version did not have |
| `doctrine` | each doctrine check | its command exits non-zero |
| `task` | each lead or spec gate command | its command exits non-zero |
| `detect` | each detected command | its command exits non-zero |

A red lane does not stop the steps after it: the commands still run, so their failures can get an automatic fix
turn while the lane waits for the lead. The `gates: false` setting keeps only the lane and hygiene steps. Commands run through the platform shell
(`cmd /c` on Windows, `sh -c` elsewhere). The lead's commands run in the task's folder (`cd <dir> &&` from the
repository root when the task works in a sub folder, as in a monorepo package), a detected command in the folder
of the project file it came from, and a spec's `Gate:` and `Feature gate:` lines at the repository root, the same
place a regression run uses. The worker's brief prints each command as the gate runs it. They run with the daemon's
environment plus `CI=1`, `SEELEGENT_GATE=1`, `SEELEGENT_TASK` and `SEELEGENT_TURN_FILES` (the changed files, one
per line; left out, also when inherited, if longer than 16000 characters), `gateTimeoutSec` each, in their own
process tree (killed on timeout or cancel). In a Rust repository they also get the `RUSTUP_TOOLCHAIN` the worker
got: when every Cargo folder within three levels picks the same toolchain (`rustup show active-toolchain`, run
with the real home and `RUSTUP_AUTO_INSTALL=0`) and the daemon's environment does not set one, that toolchain is
pinned, so a dependency's own `rust-toolchain.toml` cannot switch compilers mid-build (error E0514). Regression
runs get the same pin. With `testParallelism` N above 0 they also get `-p=N` appended to `GOFLAGS` (unless the
daemon's `GOFLAGS` sets `-p` already), `CARGO_BUILD_JOBS=N` and `PYTEST_XDIST_AUTO_NUM_WORKERS=N` (unless the
daemon's environment sets them); a `-p`, `-j` or `-n` on the command line still wins. Worker turns and regression
runs get the same variables. The full output goes to `tasks/<id>/gate-t<turn>-<n>.log`; the step
keeps the last `gateOutputTailBytes`.

Gate commands run outside the git guard and without `SEELEGENT_WORKER=1`: test suites legitimately run
`git checkout` or mutating Seelegent commands in their own temp repos, and the guard would fail them for no fault
of the worker. The trade-off is that a gate runs code the worker can edit (package scripts, Makefiles, tests) in
the shared folder, unguarded; the lead reviews what the worker changed in them.

**Priority.** With `gatePriority: low` each command's process tree runs at low priority: the Job Object's
`BELOW_NORMAL_PRIORITY_CLASS` on Windows, `nice -n 10` on Unix (when there is no `nice` program, the process group
gets nice 10 right after the start; a process the command started before that and moved out of its group keeps the
daemon's priority). The auto step's syntax checks run in the daemon and keep its priority.

**Stopped for low memory.** When the memory watchdog stops a gate (`criticalFreeMemoryMB`), the command that runs
is killed with its tree, as for a cancel, but the step gets `stopped: "low_memory"` (state `error`, the reason as
its tail when the command printed nothing) instead of a cancel, the steps after it are `not_run`, and the run
counts as finished, not cut short. The task then runs the whole gate again once memory is back; after
`GateLowMemoryStopsMax` (3) stops in one gate phase the gate ends in `error` with `reason: "low_memory"`. A run
that only shows a stopped step therefore never reads as green.

**Slots and memory.** Each gate run (all its commands, one after the other) and each regression run command takes
one of `gateConcurrency` slots (`gate.Slots`, shared by every task; the effective value, auto from the machine
unless set; `0` = no limit). Slots are given in the order
they were asked for; while a gate waits for one its phase is `queued` with `wait: "slot"` and its place in line
(`position`, 1 = next), published as a `gate` event with `phase: "queued"`. That event is logged when the wait
starts, when what it waits for changes, and when the position moves: every move at position 3 or less, further
back only when the position halved, so a long queue logs about log2(k)+3 events per gate instead of k. With a
slot in hand, a gate checks memory next: below `minFreeMemoryMB` / `minFreeCommitMB` (or, while the machine is low
on memory, below the resume level) it gives the slot back and waits with `wait: "memory"`, so a gate that cannot
start never blocks one that could.
`gateTimeoutSec` counts from the moment a command starts, never while it is queued. A lowered `gateConcurrency`
stops nothing: the runs that hold a slot finish and the next ones wait. A queued phase is no attention flag; the
dashboard shows the wait. Each regression run command is admitted the same way (`AdmitHeavy`: a slot, then
free memory, giving the slot back while it waits for memory), and the memory watchdog may stop it like a gate run;
it then fails with "stopped: machine low on memory" in its tail. Such a stop is no result: the regression run
runs the command again once memory is back, and after 3 stops of one command it ends `cancelled` with an `error`
saying so, without recording anything for that command's spec tasks (no false error in the ledger).

A gate command may ask the daemon for a regression run (`seelegent proof run`). The CLI sends the gate's task id
(`SEELEGENT_TASK` of the gate environment) as the request's `gateTask`, and while that task's gate holds a slot
the regression's commands share it instead of waiting for a second one (they still wait for memory), so the gate
cannot wait on its own slot; while that gate holds its slot, its own task does not count as another active task
either (no `--force` needed for it). The run is not tied to the gate: started without `--wait`, or still going when
the gate times out, it goes on after the gate ends, so for the command running then it may exceed
`gateConcurrency` by one (at most one run per project), while its next commands wait for a slot and the memory
watchdog watches it like any heavy command. An id the daemon does not know (Seelegent's own tests inside a gate start their own
daemon, which never saw the outer task) and a gate that holds no slot change nothing: the commands wait for a slot
as usual. Only a request sent without the id (an HTTP client of its own that drops it) can still wait on the slot
its gate holds, until `gateTimeoutSec` ends the gate command.

Each step is published as a `gate` task event (`phase: start | step | settling | result`) and stored on the turn
(`Turn.gate`), so the dashboard shows it live. The `start` event carries the plan (each step's kind, command and
folder), so a view names the steps in its own language.

**Settling.** When a doctrine check or a gate command (`task`, `detect`) fails while another worker of the same repository is at work (running, not in its own
gate phase), the failure may be that worker's half-done change. The task waits (`gatePhase.state: settling`) until
the others stop, `gateSettleMinutes` at most, polling every `gateSettlePollMs`, then runs the gate once more
(`settled: true`). A failing doctrine check whose output names only files other active tasks changed (their real
changes, not their lanes) is marked `foreign` with `causedBy`. A red lane, hygiene or syntax step never settles:
it depends on the task's own turns, which another worker cannot change, so the gate ends at once. It still counts: the gate stays red, so the lead sees
it. It only spares the task an automatic fix turn it could do nothing with; the step, the attention detail and the
report name the tasks it may be caused by.

**Red.** While `autoFixes < budget` and a step other than the lane failed, the daemon opens an automatic fix turn
(`by: "gate"`) whose message gives the first such step with its output tail and the files other tasks are
changing, and ends with "Do not read or search Seelegent's data folder or its processes: everything you need is in
this message." The worker fixes, the gate runs again. The lane never gets a fix turn: the lead decides about files
outside it. A gate red only on its lane ends at once, whatever budget is left: the task is `done` with the attention
reason `gate_red` (who: lead), its detail and the gate warning name the files ("files outside the lane: ... No
automatic fix turn for the lane: the lead decides"), and the lead widens the lane with a followup's `lane`, accepts
or rejects. When the lane is red together with another step, the fix prompt names the lane and the files outside it
as information, never as work, with the line "Do NOT delete, revert or move any file to get back inside the lane:
the lead decides about those files. Leave them as they are and name them under ## Open issues." No fix prompt tells
a worker to undo, revert or delete its own files: the junk-file hint only lets it remove a leftover it is sure its
own commands made. A followup after the lead rejected a task with files outside its lane starts with "Do not delete
or move files unless the message below names that exact file and says so; the lead rejected for: <notes>". When the budget is used up the task ends `done` with the red gate and the attention reason `gate_red`
(`hygiene` when the hygiene step is the only failure). Its detail says in plain English which steps failed
("command failed: npm test; syntax check failed: a.json"); the dashboard builds its own text from the steps.
An automatic fix turn whose gate still has exactly the line ending and BOM findings (`eol_changed`, `bom_changed`,
`bom_new_file`: same files, same kinds) of the gate that started it is stuck on them. With budget left, the steps
those findings stopped run anyway (the hygiene step counts as passed for that run, then shows its findings again).
When the stuck findings are the task's only failures the loop ends at once, whatever budget is left: the task ends
the same way, with the warning "the automatic fix turn left the same line ending and BOM findings". When another
step fails too, fix turns go on for it; their prompts leave the stuck files out, which stay red for the lead.

**Green.** With the verifier on and changed files, the verifier runs; otherwise the task is done.

## The verifier

A hidden child task (`kind: verify`, plan mode, `verifierTier`, preferring another account of the backend) gets the
task title, the latest instruction, the spec lines, the changed files, the diff (60 KB at most) and the green gate
commands, and answers `## Verdict` `accept` or `reject` with `## Reasons` (`path:line - why`). Each cited
`path:line` is checked against the repository; the report counts the citations that do not match
(`verifier.unverified_cites`). The child is never listed, is accepted automatically, and is cancelled with its
parent.

A reject with budget left opens an automatic fix turn (`by: "verifier"`) with the reasons and the line "Do not
delete, revert or move files to answer this review unless a reason above names that exact file and change: the lead
decides about files outside your lane." A reject without budget
ends the task with the attention reason `verifier_rejected`. A verifier that fails or answers without a verdict is
recorded as `error` and does not hold the task. `verify` events (`phase: start | verdict`) follow it.

## The lane check

The lane step (and, for a task whose gate is off, the post-turn lane findings) judges what changed since the lead's
latest instruction (field report 4):

- **Net change.** The files that some turn since the instruction changed are compared between the start snapshot of
  the instruction's turn and the end snapshot of the latest turn, by their manifest entries (hash, else size and
  time; [safety.md](safety.md)). A file created and deleted again, or changed and restored, is no change. When the
  snapshots cannot tell (one is missing or truncated, or HEAD moved between them) every file a turn changed counts:
  the check fails closed.
- **Touched by a worker.** A file outside the lane counts against it only when a worker plausibly touched it: its
  edit tools reported it, or one of its shell commands writes, moves or deletes it as `guard.WriteTargets` reads the
  command (redirections, `rm`, `mv`, `cp`/`Copy-Item` destinations, `sed -i`, and PowerShell's `Remove-Item`,
  `Set-Content`, `Out-File`, `New-Item`, `Move-Item`, `Copy-Item` with `-Path`, `-LiteralPath` or positional paths).
  Relative paths are resolved against the task's folder joined to the folder an earlier `cd`, `chdir`,
  `Set-Location`, `sl` or `pushd` of the same line moved to (`popd`/`Pop-Location` go back), over `&&`, `;` and `|`:
  `cd app && rm -rf dist` touches `app/dist`. A removed folder counts for every file under it, a glob for the files it
  matches, and a target with a variable after a written-out folder (`src/$name.ts`) for that folder. The edit tools'
  files and the commands' paths go through the same helper (`repoRel`: Git Bash `/c/...` paths, then canonical form
  on both sides, so 8.3 short names, junctions, letter case and separators do not matter); the `unreported_file`
  hygiene check uses the same reading. Any other file outside the lane (the lead or the user editing the same tree,
  another program) is listed as `unseen` with the warning "changed during the turn but not by a worker edit or
  command Seelegent saw": the step is `warn` at most, and the report and the gate warnings still name it. The
  accepted cost: a file a worker's command changes as a side effect (a build that rewrites a lock file, a script that
  writes a file it does not name) is only a warning too.
- **Writes Seelegent cannot read fail closed.** A worker command that writes where the line does not spell out
  (`Remove-Item $env:OUT -Recurse`, `del %OUT%\x`, `echo x > "$LOG"`, `cd $(mktemp -d) && touch x`, a write to the
  repository root or the task's folder as a whole such as `cp -r ../tpl/* .`) claims no file but marks the
  instruction's turns: an out-of-lane file that no edit or readable command explains then counts against the lane
  too (red), and the step's tail names the command ("counted: no worker edit or readable command names them, but a
  worker's command wrote where Seelegent could not read the path"). Files other tasks of the repository changed
  during the turn are no turn's change in the first place, so they never reach the lane. Writes under the temporary
  folder's variable (`$env:TEMP`, `%TEMP%`, `$TMPDIR`) and to the null devices (`$null`, `nul`, `/dev/null`) are
  neither paths nor unknown. Not followed: a `cd` inside a subshell or a command substitution is taken as if it
  moved the rest of the line too.
- The changed-file list of the turn (`files_changed`) is not narrowed: it still holds every file the snapshots saw
  change.

## Lanes and claims

A lane is a list of files, folders and globs relative to the repository root. A plain path (no `*` or `?`) covers
that path and everything under it, with or without a trailing `/`, whether or not the folder exists yet: `src/x`
covers `src/x/a.vue` but not `src/xy.ts`. Two plain paths overlap when one is the other or lies under it; a glob
and a plain path when the glob can match the plain path or a file under it (`src/x/*.ts` and `src/*/a.ts` overlap
`src/x`, `src/*.ts` does not). `.` and `..` segments are
resolved first (`src/../lib` is `lib`, `a/./b` is `a/b`); then a path that is absolute, leaves the repository or is
the root itself is refused (`400`), and such a token of a spec task is left out of its lane.

A task's lane is a claim while the task is pending (queued, waiting for quota or for its dependencies, however
long) or running, and for `laneHoldHours` after it ends while it is not reviewed (accepting or rejecting it
releases the claim). `GET /lanes` lists the claims of a repository. A task leaving its dependencies does not check
its lane again: its claim was held the whole time, so an overlap can only be one the lead forced or one
`lanes: warn` allowed.

| `lanes` | A new claim that overlaps another task's | Files changed outside the lane |
|---|---|---|
| `enforce` (default) | refused: `409 conflict`, `details.reason: "lane_conflict"`, `details.conflicts` (task, paths); `forceLane` claims anyway (`lane` event `forced`) | lane step red |
| `warn` | allowed with a `lane` event `warning` | lane step `warn` |
| `off` | not checked | no lane step |

Delegate and followup check and save the claim under one lock, so two requests cannot claim the same files at
once. The milestone 12 overlap warnings (`fileOverlaps`) still report files several tasks changed, lane or not;
`GET /contention` lists those of the last `contentionWindowHours`.

## Proof ledger and matrix

Green and red results of spec task commands are written to the ledger (SQLite `proof_entries`, per project key and
feature) with the task id, the git head and the commands. The matrix of a feature (`GET /proof/matrix`) joins
`spec.md` (user stories, `FR-nnn`, `SC-nnn`), `tasks.md` (`Covers:` and `Gate:` lines, ticks) and the ledger:

| Status | When |
|---|---|
| `unmapped` | no task covers the requirement |
| `in_progress` | a covering task is not ticked |
| `unproven` | all covering tasks are ticked, one has no gate or no result |
| `proven` | all covering tasks are ticked and green |
| `regressed` | a regression run failed a gate that was green before |
| `failing` | the last result of a covering task is red or error |

`GET /proof/index` lists every feature under `specs/` (and features outside the repository the ledger knows) with
its counts and the latest regression runs.

A regression run (`POST /proof/run`) runs every spec gate command, feature gate and doctrine check again from the
repository root and records the results with source `regression`. It is refused while other tasks of the repository
are active (`409`, `details.activeTaskIds`; a task waiting for its dependencies has no worker and does not count)
unless `force` (then the run is marked `noisy`); one run per project at a time. A dirty worktree is marked `dirty`.
Progress is published as the SSE event `proof.run`. Runs left running by a stopped daemon are marked abandoned
(`error`) at the next start.

`GET /proof/stats` counts gated tasks, gate runs, first runs that were red, red gates fixed before the lead's
review, automatic fix turns, budgets used up (a gate red only through foreign steps spent none), lane refusals,
forced claims, lane violations, the mean lead followups per task with and without a gate, and the verifier's
verdicts (with `slipped`: accepted turns the lead then followed up or rejected).

## Reports

`TaskReport` gains `gate` (state, `auto_fix_turns`, failing steps with their output tail and, for a foreign step,
`caused_by`, `not_run`, warnings),
`last_gate` (while the gate runs or a fix turn works), `lane` (`paths`, `out_of_lane`, warnings, forced), `bar`
(`changed`) and `verifier` (`verdict`, `reasons`, `unverified_cites`). `TaskSummary.gate` gives the list views the
state and step counts.
