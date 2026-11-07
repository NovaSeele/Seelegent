// Package task is the task engine: tasks, their turns and attempts, the append-only event log of every task, and
// the service that delegates work to worker CLIs, follows it, and recovers it after a daemon restart.
//
// A task is one piece of work the lead delegated. Each message to the worker (the delegate prompt, then followups)
// is a turn; each run of the worker CLI for a turn on one account is an attempt. Tasks, turns and attempts are
// stored in SQLite (migration 0004_tasks.sql) and every change is written together with the events that describe
// it, in one transaction, so the event log and the task record never disagree. Events get a per-task seq that
// starts at 1 and grows by one; worker message deltas are coalesced for 250 ms before a seq is assigned.
//
// Files:
//
//   - task.go, state.go: the model and the status transition table.
//   - repo.go: SQLite persistence (tasks, turns, attempts, events, committed stream offsets).
//   - bus.go: the in-process notifier that waits and the SSE stream share.
//   - service.go: Service, Options and the interfaces the engine needs (accounts, pool, usage, memory, roles,
//     consoles); mutate is the one way a task changes.
//   - delegate.go, control.go, query.go, wait.go, report.go, takeover.go: the service API.
//   - runner.go, follow.go: one run per task: account choice, launch, following the worker, outcome handling.
//   - attention.go: what a task needs from the lead or the user; never stops a worker.
//   - deps.go: tasks that wait for others.
//   - recovery.go: what a restarted daemon does with the tasks an earlier one left.
//
// The engine never kills a process it cannot prove it started: workers run under the detached launcher
// (internal/launch), cancel is a file the launcher acts on, and after a restart a live launch is attached again, a
// finished one replayed from its files, and an attempt whose worker never started closed as interrupted so the turn
// goes on with a new attempt. See docs/task-engine.md.
package task
