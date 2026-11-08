# Task engine

The task engine (`internal/task`) takes work from the lead, runs it on worker CLIs through the account pool, keeps a
complete event log of every task and survives daemon restarts. The HTTP API and the MCP server are thin layers over
`task.Service`; the contract is `contracts/api-v1.md` §3 of the spec workspace.

## Words

- **Task**: one piece of work the lead delegated, in one project folder (`cwd`).
- **Turn**: one message to the worker (the delegate prompt, then each followup or answer) and the worker's answer.
- **Attempt**: one run of the worker CLI for a turn on one account. A turn has several attempts when an account runs
  out of quota, needs a sign-in or fails transiently.
- **Run**: the goroutine that works on one task's open turn, attempt by attempt. At most one per task.

## Status

| From | To |
|---|---|
| `waiting_deps` | `queued` (prerequisites met, or forced), `blocked_deps`, `cancelled` |
| `blocked_deps` | `queued` (forced), `waiting_deps` (a prerequisite was reopened), `cancelled` |
| `queued` | `running` (an account was taken), `waiting_quota`, `failed`, `cancelled` |
| `running` | `done`, `failed`, `cancelled`, `waiting_quota` |
| `waiting_quota` | `running`, `queued`, `failed`, `cancelled` |
| `done`, `failed`, `cancelled` | `queued` (followup), `running` (takeover) |

`queued`, `running` and `waiting_quota` are *active*: a run owns the task. `Task.SetStatus` refuses any other move.
A task taken over by the user (`manual`) is `running` with no run; it is left alone until its console ends.

## Storage

SQLite, migration `0004_tasks.sql` (and `0005_memory_roles.sql` for memory, review history and roles):

- `tasks`: the columns lists filter and sort on (status, project, cwd, backend, times, `last_event_seq`) plus `data`,
  the JSON of the rest of the record.
- `turns`, `attempts`: one row each; an attempt keeps its full brief in `prompt` (read only on request) and its
  launch directory and committed stream offsets in `data`.
- `tasks.title_fold` (migration `0006_task_search.sql`): the title lower-cased with its diacritics and combining
  marks removed (`foldText`), which the title search matches, so "tieng viet" finds "Tiếng Việt" in NFC or NFD. It is
  written on every save; at start `BackfillFold` fills only the rows where it is empty. A later change to `foldText`
  needs a migration that empties `title_fold` so those rows are recomputed.
- `tasks.kind` and the turn digest (migration `0011_task_list.sql`: `turn_count`, `tokens_used`, `files_changed`,
  `gate_turn`, `gate_state`, `last_account_id`, `last_account_label`): what a list entry shows from the turns,
  written on every save, so lists, Boot and summaries do not load turns. Rows stored before the migration have
  `turn_count` -1 and their lists load their turns; once the daemon listens, a background backfill stores their
  digest in batches of 100 (resting 50 ms between batches, stopped by the daemon's close and resumed at the next
  start). A task whose turns cannot be read is logged and keeps -1 (its list entries keep reporting the error).
- `task_events`: the append-only log. `seq` starts at 1 per task and grows by one, assigned in the transaction that
  writes the event.

Every change goes through `Service.mutate`: load, change, recompute attention, save the task with its turns and
append the events in **one transaction**, then publish on the bus. The log and the record never disagree.

## A run

1. **Account.** If the task already has a conversation, `pool.Handover` decides: stay with the owner, move to another
   account, or wait. Otherwise the pool picks the best free account (`Prefer` from the role first, `Exclude` the
   accounts that needed a sign-in this turn or failed transiently more than twice). No account at all: the task is
   `waiting_quota` with `waiting: no_account` and a blocked flag for the user, and looks again when the pool changes or
   after a minute. Every account resting or busy: `waiting: resting` (with `waitingUntil`) or `waiting: busy`, with
   the pool's line in `waitDetail` (for busy the full accounts, `acc6: 2 of 2 running`); the `waiting_quota` event
   carries both as `reason` and `detail`, and is written again only when one of them or the time changes.
   A task without `provider` never takes an API-key account (keys cost money), but when one of its backend would be
   free with `provider` set, `waitDetail` names that provider (at most three, each once; "and a model" when the
   provider maps no tier), and the report carries it as `wait_detail`.
   A hand-back turn waits for the conversation's own account while it rests or is busy instead of moving. When that
   account cannot run at all any more (signed out, disabled, removed, its models no longer allowed), the hand-back
   runs on another account in a new conversation, whose brief says plainly that the console's context is not
   available and that it must find out from the files what changed.
2. **Brief.** The same conversation gets a short continuation; a fresh conversation (first attempt, another account,
   or a retry that never got a conversation) gets the full brief from `internal/brief`, with role, project memory,
   review history, constitution, the git baseline, earlier turns, prerequisite context, and the "you continue an
   interrupted task" section when an attempt came before. A plan-mode brief says how the backend of this attempt
   (not the one the task started on) reads files (`Capabilities.PlanShell`, `brief.PlanRule`): agy's and claude's
   plan modes refuse shell commands, so they read with their file tools; codex has no file tools and runs a plan
   turn in its `read-only` sandbox, where shell commands that only read (`rg -n`, `Get-Content`, `git diff`, tests
   that write nothing) work and every write is refused, so it reads with the shell. A hidden run (consult, meeting,
   judge, verifier) gets the same rule at the top of each fresh conversation (`brief.ReadOnlyRun`), and on codex
   also the advice to take its citation line numbers from `rg -n` or `Select-String`; below it comes the worker
   brief's data-folder rule (`brief.DataRule`: do not explore the data folder or Seelegent's processes, with the
   run's folder as the exception when it lies inside the data folder). Before the first attempt of a
   task the baseline is taken:
   `git status --porcelain` (literal pathspecs, `core.fsmonitor` off, no shell, 20 s timeout) lists the files under
   the task folder that already have uncommitted changes, and the brief tells the worker not to revert them. With it
   the task records `gitStart`, the HEAD commit and branch it starts from (an empty head in a repository without
   commits, an empty branch when detached, nothing outside a repository); followups keep the first start, and the Git
   view lists the commits made since it ([api.md](api.md)). The
   baseline is taken right before the first launch, once an account is leased (`Pool.Wait`), so files someone
   changed while the task waited for an account are in it, and the worker is told to leave them too. A retry on the
   same
   conversation resends the request itself when the failed attempt never reported a session, and otherwise the retry
   note together with the request, so a followup is never lost.
3. **Launch.** `account.Manager.Env` builds the environment (an API key that is missing is an `auth` outcome, not an
   engine error), the backend builds the command, and `launch.Start` runs it under the detached launcher in
   `tasks/<id>/t<turn>-a<n>/launch`. The launch context ends only on a cancel or a takeover: a daemon that stops
   during a launch leaves the attempt open for the next daemon, and an attempt cancelled from outside the engine
   counts as interrupted, not as a cancelled task.
4. **Follow.** The stream and stderr are tailed from the start. Message deltas of one step are **coalesced for 250 ms**
   and written as one `worker` event; any other event flushes them first. Each write carries the stream and stderr
   offsets it covers, committed in the same transaction (`attempts.data.streamOffset`, `stderrOffset`).
   Writes are **group commits** (M19): a line's events join a batch and wake the follower's writer, which stores the
   whole batch in one transaction and publishes it as one bus batch. The first line of a read chunk is written at
   once; the lines parsed while that write runs (the rest of the chunk) go into the next one, so no timer delays an
   event. A batch always ends on a line boundary, at most 256 events (a line that fills it writes it itself), and the
   events, their seqs and their SSE frames are the ones a write per line gave.
   A launch directory removed while the worker runs (Unix allows it; the launcher keeps its lock on the unlinked
   file) is not taken for an ended worker: the daemon asks the launcher it knows (from `launch.Start`, or from a
   record that checked out) to stop with SIGTERM, kills what is left of the tree after a grace period, and fails the
   attempt (`launch.ErrGone`) only once the tree has ended, so the task keeps its lane until nothing writes.
5. **Outcome.** The parser's closing events, `attempt_end` and the decision are one transaction:

| Outcome | What happens |
|---|---|
| ok | turn `ok`, task `done` (a hand-back turn also logs `handback`); a gated task stays `running` in its gate phase first (below) |
| quota | the account rests for that quota group (`pool.MarkExhausted`), next attempt |
| auth | the account is marked signed out (`pool.MarkAuthFailed`), next attempt on another account |
| credits | the key's credit or billing ran out (class `credits`, never a quota rest): the account is left out with no end time (`pool.MarkNoCredit`) until the user stores a new key or edits the account (switches it off and on after a top-up); next attempt on another account, or, when no other account can run the task, the task fails at once naming the key ("the anthropic key has no credit left; top up or change the key") instead of waiting |
| transient | `retry` event, wait 15 s then 45 s on the same account (only its own timer wakes it), then another account |
| background_wait | continue once with the background-wait prompt; the second time the turn ends `ok` |
| error / timeout | turn and task `failed` |
| cancelled | turn and task `cancelled` |

A turn makes at most one attempt per account of the backend plus three, not counting attempts that ended on a quota
limit: those rest the account and resume or hand over, so the turn waits rather than fails. A runaway loop of limits
ends the turn after 50 of them. After every attempt the usage is recorded and a background quota read of the account
is requested. Codex reports the thread's running total at `turn.completed`, so a resumed codex attempt records what
it added to the total the conversation's previous attempt reported (kept as the outcome's `threadUsage`); a total
below that one is taken as is.

6. **Gate.** An edit task whose turn ended `ok` keeps its run: the task stays `running` with `gatePhase` set while
   the gate runs, settles, or the verifier child works ([gates.md](gates.md)). A red gate or a verifier reject with
   budget left opens an automatic fix turn (`by: "gate"` or `"verifier"`) on the same run; otherwise the task ends
   `done` with the gate result on the turn. `endRun` starts the run again while `gatePhase` is set, so a phase left
   by a stopped daemon is picked up by the next one. Takeover is refused during the phase; a cancel ends it
   (`not_run` steps, verdict `error`) and cancels the verifier child.

### Worker process trees and their limits

The launcher starts the worker as the root of a process tree (`platform.StartWorkerTreeWith`), with the limits that
travel in `launch.json` as `tree` (settings `workerPriority`, `workerMemoryLimitMB`, `workerCPURatePercent`, read at
each launch):

| Limit | Windows (Job Object) | Unix |
|---|---|---|
| priority `low` | `JOB_OBJECT_LIMIT_PRIORITY_CLASS` with `BELOW_NORMAL_PRIORITY_CLASS`, plus the same creation flag: every process of the job runs below normal and cannot raise itself; the launcher stays at normal | the launcher lowers its own nice to 10 before it starts the worker, so every descendant inherits it with no race. Linux keeps nice per thread: the launcher locks the starting goroutine to its thread, lowers that thread and forks from it |
| memory cap | `JOB_OBJECT_LIMIT_JOB_MEMORY` (memory committed by the whole tree) | none |
| CPU cap | CPU rate control, hard cap, as a percentage of the whole machine | none |

When the memory cap refuses an allocation the job posts `JOB_OBJECT_MSG_JOB_MEMORY_LIMIT` to its completion port.
The launcher drains the port on its cancel ticker (so the per-process packets do not pile up in kernel memory over
a long turn) and once more after the worker exits; it then notes the hit in `stderr.txt` and writes
`memoryLimitHit: true` into `exit.json`, and the turn fails saying so. The job's report on that port decides: a
query of the job's limit violations does not help, as it only covers notification limits, which a hard cap never
lets usage pass. Only when the port could not be set up does a worker that failed by itself after the job's peak
memory reached 90% of the cap count as a hit; a tree the launcher stopped (cancel, timeout) never does. A limit
the system refuses is left out and named in `stderr.txt` ("the system refused these limits ..."). A worker's own
test and lint runs are bounded only by this cap; the start check and the memory watchdog act on new starts and
gate commands, never on a running worker.

### Resources: the start check and the memory watchdog

The engine reads memory through `Options.Resources` (`task.Sampler` over `platform.Memory`: a reading is cached
for 2 s, and `Changed` fires when one crosses a threshold or the settings change). A reading the system cannot give
(`Known` false; commit is unknown when `AvailCommitMB` is 0) never holds anything. Every threshold below is the
effective value (`config.EffectiveResources`: the user's value, or the auto value computed from the machine profile
the daemon reads at start and on each settings change; see [configuration.md](configuration.md#resources)), so it
does not move between readings.

- **Start check.** Before a run asks the pool for an account, it compares free physical memory with
  `minFreeMemoryMB` and free commit with `minFreeCommitMB` (each `0` = off). Below either the task is
  `waiting_quota` with `waiting: "memory"`, a `waitDetail` with both numbers, and
  `memoryWait {since, availableMB, needMB}`, and the run waits without touching the pool (no lease, no account
  signal) until the sampler signals, the machine leaves the low-memory state, or 10 s pass. A `waiting_quota` event
  (`reason: "memory"`, `availableMB`, `needMB`) is written when the wait starts or its threshold changes, not on
  every reading. The same check runs again right after the lease, as the guard for memory that fell while the run
  waited for an account: below the threshold it releases the lease (nothing is launched) and waits the same way.
  A pool wait in between keeps `memoryWait`, so a wait that goes back to memory keeps its `since` and writes no
  new event; a launch or the end of the run clears it. The summary and the report show `waiting` only for this
  reason.
  After `ResourcesWaitAfter` (10 min) the task carries an `info` flag for the user, `resources_wait`, with
  `params {minutes, availableMB, needMB}`: it does not need the lead and wakes no wait.
- **Herd spacing.** While free memory is below twice `minFreeMemoryMB` (or commit below twice `minFreeCommitMB`),
  worker starts are spaced 5 s apart: a run that comes too soon releases its lease and sleeps the difference,
  without a status change.
- **Concurrency.** `maxConcurrentWorkers` caps the leases over all accounts in the pool (`Unavailable.Capped`
  and `Running` say so); a release wakes the waiters, and a lowered cap stops nothing.
- **Watchdog.** A goroutine checks memory on every sampler signal and every 2 s while a task runs, a heavy command
  (gate run or regression run command, `TrackHeavy`) is in flight or the machine is low. Otherwise it arms no timer
  and sleeps until a run or a heavy command starts. With `criticalFreeMemoryMB` above 0, the machine turns **low**
  when free physical memory, or free commit when it is known, drops below it, and turns back only at the resume level: physical at
  `minFreeMemoryMB` (twice the critical line when that is off), commit at `minFreeCommitMB` (likewise). Between the
  two lines nothing flips, so a reading that swings around the critical line does not flicker. While low, every
  new start waits at the resume level instead of the `minFree` thresholds, `LowMemory()` is true (GET /health and
  the dashboard), and `OnLowMemory` publishes each flip.
  **One stop per episode:** when the machine turns low, the newest heavy command in flight (the last gate run or
  regression run command to start) is cancelled with the cause `gate.ErrLowMemory`; nothing else is stopped until the machine has come back to the
  resume level and fallen again. The stopped gate is not a failure: its step shows `stopped: "low_memory"`, the
  phase counts `stops` (kept on the step as well, so a daemon restart keeps the count), the gate goes back to
  `queued` with `wait: "memory"` and runs again from the start once memory is back. The third stop in one gate
  phase (`api.GateLowMemoryStopsMax`) ends the gate in `error` with `reason: "low_memory"` and no fix turn. A
  stopped regression run command fails with "stopped: machine low on memory" at the end of its tail. Workers
  are never stopped: a worker's own test and lint runs are bounded only by `workerMemoryLimitMB`.
- **Daemon limit.** `daemonMemoryLimitMB` sets the daemon's soft memory limit (`debug.SetMemoryLimit`) at start and
  whenever it changes; `0` lifts the one it set; a `GOMEMLIMIT` in the daemon's environment wins.

Every resources setting applies without a restart: the pool, the gate slots and the sampler are notified on each
settings change.

## Attention

`computeAttention` maps the task and what the run knows about its worker onto the contract's states:

- `blocked`: prerequisites failed (`deps_blocked`), no account can run it (`no_accounts`, for the user), every
  account needs a sign-in (`auth`, for the user), the worker repeats itself (`looping`: the same call `loopRepeat`
  times in its last 12 without progress, or five failed calls in a row; an edit that reports only its file is told
  apart by the file's content hash, or counts as new when the file cannot be read), a done turn with no answer
  (`no_response`), red hygiene findings (`hygiene`), files at risk not restored (`work_at_risk`), a failed task
  (`failed`), a red gate with no fix budget left (`gate_red`; `hygiene` when hygiene is its only red step), a
  verifier reject with no budget left (`verifier_rejected`), a peer member's objection the lead has not answered
  (`peer_objection`, detail: who objects and the first required change). `hygiene` and `work_at_risk` come from the post-turn
  checks ([safety.md](safety.md)), the gate reasons from [gates.md](gates.md).
- `question`: the answer has a `## Blocked` section (its text and list items become the report's question).
- `stalled`: no output for `stallMinutes`, one tool call open for `stallToolMinutes`, or no conversation 3 min after
  launch.
- `background_wait`: the CLI waits for a command it started in the background.
- `info` / `review`: a finished turn waits for the lead's verdict.

`detail` is an English sentence for the lead's tools. Next to it, a flag carries `params` with the values it was
built from (minutes, tool, count, backend, the blocking task, the refused actions), so the dashboard words it in the
reader's language and keeps `detail` behind a "Seelegent said:" disclosure only when there is nothing to build from
(F14). Two flags with the same reason, detail and params are the same flag; a change in params alone is a new one.

A watcher re-checks running tasks every 30 s and finished ones that still carry a flag every 10 min (flags decay after
`attentionDecayHours`). It only raises and clears flags; it never stops a worker. A dismissed flag comes back only
when the worker produces something new or a new turn starts.

## Waits and the bus

All waits share one notifier: each wait registers for its own tasks (`Bus.TasksChanged`) and wakes only when one of
their records changes. A wait ends when its tasks are finished (all, or
with `any` the first), when a still-pending task **newly** needs the lead (flags raised before the wait began do
not count, except those raised at or after the wait's `since` mark: the create-then-wait paths, `delegate` and
`followup`, pass a mark taken before the creation, so a task blocked at once by `no_accounts` wakes them), on its
timeout, or when its context ends. Ending a wait never touches a task (a cancelled MCP wait used
to cancel workers). SSE subscribers get bounded buffers and are told when they fell behind.

## Dependencies

A task with `after` starts `waiting_deps`. After every change that can matter (a turn ends, a review, a cancel)
`checkDeps` re-evaluates the dependents: ready when every prerequisite is accepted (or, with `afterMode: done`, done
and not rejected), blocked when one failed, was cancelled or was rejected. When a dependent starts, the prerequisites'
titles, verdicts and answers are fixed into its brief. A followup with `force` starts a waiting or blocked dependent;
its message, if any, joins the first request.

## Members, consults and hidden runs

The team features ([team.md](team.md)) use the engine as they find it; what they add:

- **Member.** `DelegateRequest.member` fills the backend or provider, model or tier, role and preferred accounts the
  request leaves out, and the task records `member`. `FollowupRequest.member` moves the task to another member from
  that turn on. When no account of the task's backend can run it, `memberFallback` moves it to the next of the
  member's fallback backends (forward only; not for a provider or a backend outside the member's list).
- **Kinds.** Tasks have a `kind`: `normal`, or the hidden runs `verify`, `consult`, `meeting` and `judge`. Hidden
  runs are plan-mode children (`parentId`), take no baseline, gate, lane or snapshots, are left out of every list
  and of lane claims, and are closed by the engine when they end (`CloseHiddenRun`), so they never ask for a
  review. Contest variants are normal tasks with `contestId` and `variant`, in the contest's worktree.
- **Consults.** `Consult` starts a `consult` child on the member's backend and follows it; the parsed verdict,
  findings and required changes are stored on the parent (`Task.Consults`). `Review` refuses `accepted` while a
  peer objection has no answer (`PeerObjectionError`, a conflict with `reason: "peer_objection"`) unless the request
  carries `answerObjection`, which is stored on the objection and appended to the review notes.
- **Locks.** `claimMu` (the lane claim check and the task's save, one step) is always taken before a task's lock.
  No child task (verifier, consult, judge, attendee, variant) is created while a task lock is held: the parent's
  lock is released first. Meetings, contests and notes keep their own locks and call the engine without a task
  lock; the engine reaches them only through the bus. `TestConsultDuringFollowup` runs a consult and a followup
  on the same task at once.

## Takeover

`Takeover` marks the task `manual`, stops the run and waits for the worker to exit (no worker and console in one
conversation), then opens a console that resumes the conversation with the task's own backend and account. The
console counts on the account's slots. When it ends, a hand-back turn runs on the same backend, account and
conversation (another account only when that one cannot run at all, see "A run") and asks the worker to summarize
what the user did.

A cancel during the takeover ends the task and frees the console's slot at once. While this daemon still watches
the console, followups are refused until the console itself has ended, and it ends without a hand-back. The cancel
forgets the console in the task's record, so a daemon restarted after it no longer watches that window: followups
then run even if the user left it open.

On Linux and macOS the console script records exit code 129 when its window is closed (SIGHUP). The console's
process (pid and start time) is stored; a console that is gone without an exit code ends the takeover with exit
code 129, also when that is found after a restart. After a restart the console of a takeover that was not
cancelled is watched again.

A console whose command has not started 90 s after it was opened (no process recorded, no exit code: on macOS the
Automation permission was refused, the daemon has no desktop, the terminal emulator is broken) never opened: the
takeover is reverted as if the console could not open (the task goes back to its status before the takeover, an
active one to `cancelled`, with an `error` event saying why) and the watch ends. On Unix a terminal launcher that
exits with an error within 3 s is reported at once as no terminal.

## Restart recovery

`Start` picks up what an earlier daemon left. Nothing is killed: there are no process ids, only launch directories.

- An attempt whose launcher still runs is **attached** again (`reattach` event) and counted once on its account
  (`pool.Reattach` is idempotent; the lease is released when the worker exits).
- An attempt that finished meanwhile is **replayed** from its files (`resumed` event).
- In both cases the stream and stderr are read from the start so the parser sees the whole turn, and lines at or
  before the committed offsets are not logged again. The output is masked for the secrets the worker was started
  with (an API-key account's key, kept in the attempt folder's `mask` file until the attempt ends: a DPAPI blob on
  Windows like the stored keys, a plain 0600 file elsewhere, see [configuration.md](configuration.md)) and for the account's current ones, so removing or changing the key while no daemon runs never
  unmasks it. A worker whose mask list cannot be written is not started. A mask list that exists but cannot be
  read fails closed: that attempt's output is also masked by the key patterns of the log redaction (`sk-…`,
  `AIza…`, GitHub and Slack tokens, bearer credentials, `token=…` and the like, and email addresses), and the
  `reattach` or `resumed` event carries `noticeCode: "mask_unreadable"` saying so (the dashboard words the code;
  `notice` holds the same in English for logs and API clients).
- An attempt whose worker never started is closed as interrupted; the turn goes on with a new attempt and the full
  brief.
- A cancel the earlier daemon recorded is carried out; takeover consoles are watched again; dependents are
  re-evaluated.
- A task in its gate phase runs its gate again from the start (a verifier child already running is awaited).
- An active task whose record cannot be read is logged and skipped, so the others still recover;
  `Service.Unreadable` lists it for doctor.

`Close` stops following and returns; workers keep running under their launchers.

## Composition

`internal/app` builds the engine with the store, account manager, quota cache and refresher, pool, usage store,
project memory, roles, backends (`backend.Find`, overridable for tests) and terminal consoles, and owns their
`Start` and `Close`.

## Tests

`internal/testutil/fakecli` turns a test binary into the detached launcher and a fake worker CLI that replays queued
scripts. The engine tests (`internal/task/*_test.go`) run the real launcher, pool and account manager with a fake
clock; `internal/app` has an end-to-end test of the wired daemon (delegate, followup, review, quota handoff, cancel,
restart).
