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
- `task_events`: the append-only log. `seq` starts at 1 per task and grows by one, assigned in the transaction that
  writes the event.

Every change goes through `Service.mutate`: load, change, recompute attention, save the task with its turns and
append the events in **one transaction**, then publish on the bus. The log and the record never disagree.

## A run

1. **Account.** If the task already has a conversation, `pool.Handover` decides: stay with the owner, move to another
   account, or wait. Otherwise the pool picks the best free account (`Prefer` from the role first, `Exclude` the
   accounts that needed a sign-in this turn or failed transiently more than twice). No account at all: the task is
   `waiting_quota` with `waiting: no_account` and a blocked flag for the user, and looks again when the pool changes or
   after a minute.
2. **Brief.** The same conversation gets a short continuation; a fresh conversation (first attempt, another account,
   or a retry that never got a conversation) gets the full brief from `internal/brief`, with role, project memory,
   review history, constitution, earlier turns, prerequisite context, and the "you continue an interrupted task"
   section when an attempt came before.
3. **Launch.** `account.Manager.Env` builds the environment (an API key that is missing is an `auth` outcome, not an
   engine error), the backend builds the command, and `launch.Start` runs it under the detached launcher in
   `tasks/<id>/t<turn>-a<n>/launch`.
4. **Follow.** The stream and stderr are tailed from the start. Message deltas of one step are **coalesced for 250 ms**
   and written as one `worker` event; any other event flushes them first. Each write carries the stream and stderr
   offsets it covers, committed in the same transaction (`attempts.data.streamOffset`, `stderrOffset`).
5. **Outcome.** The parser's closing events, `attempt_end` and the decision are one transaction:

| Outcome | What happens |
|---|---|
| ok | turn `ok`, task `done` (a hand-back turn also logs `handback`) |
| quota | the account rests for that quota group (`pool.MarkExhausted`), next attempt |
| auth | the account is marked signed out (`pool.MarkAuthFailed`), next attempt on another account |
| transient | `retry` event, wait 15 s then 45 s on the same account (only its own timer wakes it), then another account |
| background_wait | continue once with the background-wait prompt; the second time the turn ends `ok` |
| error / timeout | turn and task `failed` |
| cancelled | turn and task `cancelled` |

A turn makes at most one attempt per account of the backend plus three. After every attempt the usage is recorded
and a background quota read of the account is requested.

## Attention

`computeAttention` maps the task and what the run knows about its worker onto the contract's states:

- `blocked`: prerequisites failed (`deps_blocked`), no account can run it (`no_accounts`, for the user), a done turn
  with no answer (`no_response`), a failed task (`failed`).
- `question`: the answer has a `## Blocked` section (its text and list items become the report's question).
- `stalled`: no output for `stallMinutes`, one tool call open for `stallToolMinutes`, or no conversation 3 min after
  launch.
- `background_wait`: the CLI waits for a command it started in the background.
- `info` / `review`: a finished turn waits for the lead's verdict.

A watcher re-checks running tasks every 30 s and finished ones every 10 min (flags decay after
`attentionDecayHours`). It only raises and clears flags; it never stops a worker. A dismissed flag comes back only
when the worker produces something new or a new turn starts.

## Waits and the bus

All waits share one notifier (`Bus.Changed`): no listener per wait. A wait ends when its tasks are finished (all, or
with `any` the first), when a still-pending task **newly** needs the lead (flags raised before the wait began do
not count), on its timeout, or when its context ends. Ending a wait never touches a task (a cancelled MCP wait used
to cancel workers). SSE subscribers get bounded buffers and are told when they fell behind.

## Dependencies

A task with `after` starts `waiting_deps`. After every change that can matter (a turn ends, a review, a cancel)
`checkDeps` re-evaluates the dependents: ready when every prerequisite is accepted (or, with `afterMode: done`, done
and not rejected), blocked when one failed, was cancelled or was rejected. When a dependent starts, the prerequisites'
titles, verdicts and answers are fixed into its brief. A followup with `force` starts a waiting or blocked dependent;
its message, if any, joins the first request.

## Takeover

`Takeover` marks the task `manual`, stops the run and waits for the worker to exit (no worker and console in one
conversation), then opens a console that resumes the conversation with the task's own backend and account. The
console counts on the account's slots. When it ends, a hand-back turn runs on the same backend, account and
conversation and asks the worker to summarize what the user did. A cancel during the takeover ends the task; the
console then ends without a hand-back. After a restart the console is watched through its exit file.

## Restart recovery

`Start` picks up what an earlier daemon left. Nothing is killed: there are no process ids, only launch directories.

- An attempt whose launcher still runs is **attached** again (`reattach` event) and counted once on its account
  (`pool.Reattach` is idempotent; the lease is released when the worker exits).
- An attempt that finished meanwhile is **replayed** from its files (`resumed` event).
- In both cases the stream and stderr are read from the start so the parser sees the whole turn, and lines at or
  before the committed offsets are not logged again.
- An attempt whose worker never started is closed as interrupted; the turn goes on with a new attempt and the full
  brief.
- A cancel the earlier daemon recorded is carried out; takeover consoles are watched again; dependents are
  re-evaluated.

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
