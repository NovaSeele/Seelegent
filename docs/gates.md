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
  commands replace the tasks' own and prove those tasks. `gate: false` turns all commands off; the lane and hygiene
  steps still run.
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
lane is checked like a new claim) and the next worker prompt starts with the new gate and lane. A followup by the
lead or the user resets the automatic fix count.

## The gate phase

When a turn of a gated task ends well, the task stays `running` with `gatePhase {state, turn, since, childId?}`
while the daemon runs the gate. The phase survives a daemon restart (the new daemon runs the gate again) and ends
on cancel (a running step becomes `not_run`). Takeover is refused during the phase.

Steps, in order (`GateResult.steps`):

| Kind | What | Fails when |
|---|---|---|
| `lane` | the files the turns since the lead's instruction changed, against the lane | a file is outside it (`lanes: enforce`; `warn` only warns; `off` has no step) |
| `hygiene` | the milestone 12 hygiene findings still open ([safety.md](safety.md)) | any red finding |
| `auto` | syntax of the changed `.json`, `.go`, `.js`/`.cjs`/`.mjs` files (`gateAutoChecks`) | a parse error the HEAD version did not have |
| `doctrine` | each doctrine check | its command exits non-zero |
| `task` | each lead or spec gate command | its command exits non-zero |
| `detect` | each detected command | its command exits non-zero |

The `gates: false` setting keeps only the lane and hygiene steps. Commands run through the platform shell
(`cmd /c` on Windows, `sh -c` elsewhere). The lead's commands run in the task's folder (`cd <dir> &&` from the
repository root when the task works in a sub folder, as in a monorepo package), a detected command in the folder
of the project file it came from, and a spec's `Gate:` and `Feature gate:` lines at the repository root, the same
place a regression run uses. The worker's brief prints each command as the gate runs it. They run with the daemon's
environment plus `CI=1`, `SEELEGENT_GATE=1`, `SEELEGENT_TASK` and `SEELEGENT_TURN_FILES` (the changed files, one
per line; left out, also when inherited, if longer than 16000 characters), `gateTimeoutSec` each, in their own
process tree (killed on timeout or cancel). The full output goes to `tasks/<id>/gate-t<turn>-<n>.log`; the step
keeps the last `gateOutputTailBytes`.

Gate commands run outside the git guard and without `SEELEGENT_WORKER=1`: test suites legitimately run
`git checkout` or mutating Seelegent commands in their own temp repos, and the guard would fail them for no fault
of the worker. The trade-off is that a gate runs code the worker can edit (package scripts, Makefiles, tests) in
the shared folder, unguarded; the lead reviews what the worker changed in them.

Each step is published as a `gate` task event (`phase: start | step | settling | result`) and stored on the turn
(`Turn.gate`), so the dashboard shows it live. The `start` event carries the plan (each step's kind, command and
folder), so a view names the steps in its own language.

**Settling.** When the gate fails while another worker of the same repository is at work (running, not in its own
gate phase), the failure may be that worker's half-done change. The task waits (`gatePhase.state: settling`) until
the others stop, `gateSettleMinutes` at most, polling every `gateSettlePollMs`, then runs the gate once more
(`settled: true`). A failing doctrine check whose output names only files other active tasks changed (their real
changes, not their lanes) is marked `foreign` with `causedBy`. It still counts: the gate stays red, so the lead sees
it. It only spares the task an automatic fix turn it could do nothing with; the result carries a warning and the
attention detail and report name the tasks it may be caused by.

**Red.** While `autoFixes < budget`, the daemon opens an automatic fix turn (`by: "gate"`) whose message lists the
failing steps with their output tails and the files other tasks are changing. The worker fixes, the gate runs
again. When the budget is used up the task ends `done` with the red gate and the attention reason `gate_red`
(`hygiene` when the hygiene step is the only failure). Its detail says in plain English which steps failed
("command failed: npm test; syntax check failed: a.json"); the dashboard builds its own text from the steps.

**Green.** With the verifier on and changed files, the verifier runs; otherwise the task is done.

## The verifier

A hidden child task (`kind: verify`, plan mode, `verifierTier`, preferring another account of the backend) gets the
task title, the latest instruction, the spec lines, the changed files, the diff (60 KB at most) and the green gate
commands, and answers `## Verdict` `accept` or `reject` with `## Reasons` (`path:line - why`). Each cited
`path:line` is checked against the repository; the report counts the citations that do not match
(`verifier.unverified_cites`). The child is never listed, is accepted automatically, and is cancelled with its
parent.

A reject with budget left opens an automatic fix turn (`by: "verifier"`) with the reasons. A reject without budget
ends the task with the attention reason `verifier_rejected`. A verifier that fails or answers without a verdict is
recorded as `error` and does not hold the task. `verify` events (`phase: start | verdict`) follow it.

## Lanes and claims

A lane is a list of files, folders (`dir/`) and globs relative to the repository root; `..` and absolute paths are
refused. A task's lane is a claim while the task is pending or running, and for `laneHoldHours` after it ends
while it is not reviewed. `GET /lanes` lists the claims of a repository.

| `lanes` | A new claim that overlaps another task's | Files changed outside the lane |
|---|---|---|
| `enforce` (default) | refused: `409 conflict`, `details.reason: "lane_conflict"`, `details.conflicts` (task, paths); `forceLane` claims anyway (`lane` event `forced`) | lane step red |
| `warn` | allowed with a `lane` event `warning` | lane step `warn` |
| `off` | not checked | no lane step |

Delegate checks and creates the claim under one lock, so two delegates cannot claim the same files at once. The
milestone 12 overlap warnings (`fileOverlaps`) still report files several tasks changed, lane or not;
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
are active (`409`, `details.activeTaskIds`) unless `force` (then the run is marked `noisy`); one run per project at
a time. A dirty worktree is marked `dirty`. Progress is published as the SSE event `proof.run`. Runs left running by
a stopped daemon are marked abandoned (`error`) at the next start.

`GET /proof/stats` counts gated tasks, gate runs, first runs that were red, red gates fixed before the lead's
review, automatic fix turns, budgets used up, lane refusals, forced claims, lane violations, the mean lead followups
per task with and without a gate, and the verifier's verdicts (with `slipped`: accepted turns the lead then followed
up or rejected).

## Reports

`TaskReport` gains `gate` (state, `auto_fix_turns`, failing steps with their output tail and, for a foreign step,
`caused_by`, `not_run`, warnings),
`last_gate` (while the gate runs or a fix turn works), `lane` (`paths`, `out_of_lane`, warnings, forced), `bar`
(`changed`) and `verifier` (`verdict`, `reasons`, `unverified_cites`). `TaskSummary.gate` gives the list views the
state and step counts.
