// Package task is the task engine: tasks, their turns and attempts, the append-only event log of every task, and
// the service that delegates work to worker CLIs, follows it, and recovers it after a daemon restart.
//
// A task is one piece of work the lead delegated. Each message to the worker (the delegate prompt, then followups)
// is a turn; each run of the worker CLI for a turn on one account is an attempt. Tasks, turns and attempts are
// stored in SQLite (migration 0004_tasks.sql) and every change is written together with the events that describe
// it, in one transaction, so the event log and the task record never disagree. Events get a per-task seq that
// starts at 1 and grows by one; worker message deltas are coalesced before a seq is assigned (coalesce.go).
//
// The status of a task follows the transition table in state.go. The engine never kills a process it cannot prove
// it started: workers run under the detached launcher (internal/launch), cancel is a file the launcher acts on, and
// after a restart a live launch is attached again, a finished one replayed, and a missing one resumed from the
// stored brief. See docs/task-engine.md.
package task
