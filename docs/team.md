# Team: members, consults, review notes, meetings, contests and hooks

Milestone 14 gives the lead a department: named members it can hand work to, ask for a second opinion, gather in
a meeting or set against each other in a contest, and review notes the user and the lead leave on a task's diff.
Everything lives in `seelegent.db` (migration `0009_team.sql`); the old version's JSON files are imported by
`seelegent migrate` ([setup.md](setup.md)).

| Package | What it does |
|---|---|
| `internal/member` | the members store (`members`) |
| `internal/task` (`members.go`, `consult.go`) | delegate and followup by member, the member fallback, consults, hidden runs |
| `internal/notes` | review notes (`review_notes`), their anchors and the send |
| `internal/meeting` | meetings (`meetings`), their briefs, minutes and the dissent memory |
| `internal/contest` | contests (`contests`): quota plan, worktrees, scoring, judge, pick, cleanup |
| `internal/worktree` | the worktrees Seelegent owns: their environment (contract §6.3), sealing and safe removal |
| `internal/answer` | reading members' answers: sections, citations, the attendee redactor |
| `internal/hook` | event hooks (`hooks`), their runner and `logs/hooks.log` |

Routes: `/members*`, `/tasks/{id}/consult`, `/tasks/{id}/notes*`, `/meetings*`, `/contests*`, `/hooks*`
([api.md](api.md)); MCP tools `members`, `consult`, `notes`, `meeting`, `contest` and the `member` and contest
options of `delegate` ([mcp.md](mcp.md)); the matching commands ([cli.md](cli.md)). The step logs of meetings and
contests are rows of `team_log` (`GET /meetings/{id}/events`, `/contests/{id}/events`). Each service is optional in
the HTTP server: without it its paths answer `501 not_implemented`.

## Members

A member is a named worker: a backend (`agy`, `claude`, `codex`) or an API-key provider, a model or a tier, a worker
role, preferred accounts, fallback backends and a rank. The id is lower-case letters, digits and dashes (at most
40), made from the name when left out, and never changes. `PATCH /members/{id}` merges the body onto the stored
member.

| Rank | In a meeting | In a consult |
|---|---|---|
| `worker` | a witness: answers, cannot dissent | cannot be consulted |
| `senior` | an adviser: a decision against its final stance is recorded as advice not taken | `object` is a warning |
| `peer` | may dissent formally after the decision | `object` blocks `review accepted` until answered |

Using a member (`delegate`/`followup` with `member`):

- What the request names wins; the member fills the rest: backend or provider, model or tier (its model only on
  its own backend), role and preferred accounts.
- An unknown member is `400 invalid_argument`; a member that is off (or whose provider is gone, `invalid`) is
  `409 conflict` with `details.reason: "member_disabled"`.
- When no account of the member's backend can run the task, the task moves to the next of its fallback backends,
  forward only. A task on a provider, or on a backend the lead chose outside the list, stays.
- A followup with `member` moves the task to that member from this turn on (a new backend starts a new
  conversation).

`GET /members/track` is each member's record: meetings, dissents, concessions, signals, vindicated and dismissed
dissents, advice not taken, consults and objections.

## Hidden runs

Consults, meeting attendees and contest judges run as tasks of kind `consult`, `meeting` and `judge` (the
verifier's `verify` too). They are read-only (plan mode), have no gate, lane or snapshots, are never listed and are
read through what started them. When one ends the engine closes it, so it never waits for the lead. A run whose
record was never stored (a consult whose task started a new turn meanwhile, a meeting whose insert failed, a
cancelled meeting's running turns) is cancelled and closed once its worker has stopped. Open starts a meeting's
first round before it stores the meeting, so at startup the meeting service also stops and closes the open
`meeting` runs whose meeting does not exist.

## Consults

`POST /tasks/{id}/consult {member, focus?}` asks a senior or peer member for a second opinion on a finished task.
The member's hidden run gets the brief, the report and the diff (cut at 40 000 bytes) and answers a verdict
(`approve`, `concerns`, `object`), findings that cite `path:line` (each citation is checked against the repository)
and the changes it requires. The record is stored on the task (`TaskDetail.consults`, and in the report).

A peer's `object` blocks `review accepted`: the review answers `409 conflict` with `details.reason:
"peer_objection"` until the lead sends `answerObjection` (MCP `answer_objection`, CLI `--answer-objection`). The
answer is stored on the objection and added to the review notes. Rejecting needs no answer. While the objection
stands the finished task's attention is `blocked` with reason `peer_objection` (for the lead). A senior's `object`
does not block: accepting over it answers the task summary with `warnings` naming it (MCP `Warning:` lines, the
CLI prints them, the dashboard shows them in the acceptance toast). Only each senior's latest finished consult
counts, so a senior who approves in a later consult no longer warns. The dashboard shows the consults in a "Member opinions" panel of the task; an unanswered peer
objection disables Accept and the banner's "Answer objection" sends the answer with the acceptance.

## Review notes

A note sits on a line (or a range) of the task's diff, on the new side unless `side: "old"`, written by the user
or the lead. It keeps the text of its line (`anchorText`, the first 500 characters; a longer line is matched on
the same start); after each turn it follows that text within 50 lines
(`currentLine`, always sent: `line` when there is nothing to look for, 0 when the line is gone; such a note is
listed but not anchored in the diff). Its status is computed when it is read:

| Status | Meaning |
|---|---|
| `draft` | not sent yet; the next send includes it |
| `outdated` | its line changed before it was sent |
| `sent` | the turn it was sent with still runs |
| `addressed` | that turn ended and the line changed |
| `unaddressed` | that turn ended and the line is unchanged; the next send includes it again |
| `resolved` | closed by the user or the lead (`PATCH {state: "resolved"}`; `open` reopens it) |

`POST /tasks/{id}/notes/send {by, extra?}` sends the drafts and the unaddressed notes as one followup: grouped by
author, numbered, each with its place and its line, then `extra`. It is refused while the task runs. Every change
publishes `notes.changed {taskId}`.

## Meetings

`POST /meetings` opens a meeting on a question, in a project folder:

- 1 to 6 attendees (members that are on, each with an optional rank override), 0 or 2 to 6 options (labeled A to F),
  up to 20 files to read first, 1 to 5 rounds (default 3), and optionally the lead's sealed position, hashed at
  open and shown from round 2 as one more anonymous position.
- `POST /meetings/estimate` answers the most worker runs it takes without starting anything.

Each attendee is one hidden read-only run; each round is a turn of it. Attendees get neutral labels (P1, P2, ...)
in a random order. Round 1 is blind. From round 2 each attendee sees the others' positions under their labels,
with identity hints removed (brand words, and the members' ids, names, models and providers as whole words when
they are distinctive: an ordinary word such as `api`, `fast` or `max` is never blanked, old bug D20), and answers
the strongest opposing argument. An answer is a stance, an option, a confidence, arguments, claims citing
`path:line` (every citation is checked: `verified`, `located`, `quote_mismatch`, `bad_range`, `missing_file`,
`outside_repo`), risks, proposed actions and up to five questions to the lead. An attendee that edits files
although it is read-only is recorded (`editedFiles`).

After a round:

- questions to the lead make the meeting `waiting_lead`; `POST /meetings/{id}/post {answers, note?}` answers them
  and starts the next round (or goes to `deciding` after the last one);
- otherwise the meeting goes to `deciding` after the last round, or earlier once every attendee present held its
  position for a round (same option, confidence within 10 points);
- a round 1 in which every attendee was absent fails the meeting. An absent answer says why in `absentReason`
  (`failed`: the run failed or could not start; `cancelled`; `gone`: the run or its turn no longer exists) with the
  detail in `error`; the dashboard shows a failed run as a failure.

`POST /meetings/{id}/decide {decision, rationale?, option?}` records the decision. A senior whose final stance
differs is recorded as advice not taken; a peer whose stance differs gets one more turn to dissent formally or
concede (`dissent`), and the meeting closes when those are in; a peer whose turn fails or answers nothing usable is
recorded as `absent` (neither a dissent nor a concession). Without options free-text stances cannot be
compared, so every senior counts as advice not taken and every peer gets that turn, where it can concede (as in the
old version); give options when the record matters. For 30 days a dissent watches the files it cites:
a later task that changed one of them and was rejected, or ended with a red gate, is added to it as a signal (with
the task's title). The lead settles a dissent with `POST /meetings/{id}/dissents/{did}/outcome {outcome:
vindicated|dismissed}`; `open` takes the outcome back (the dashboard's Undo). The old
`regression` signal could never fire and is gone (old bug D19).

`POST /meetings/{id}/act {items}` turns the decision into up to 20 delegated tasks linked to the meeting
(`meetingId`). An item whose text already has a task gets that task back, so a retried act creates only what
is missing; a task that failed or was cancelled does not count, and acting on its text again starts a new one.
`POST /meetings/{id}/cancel` stops the runs and keeps the record. `GET /meetings/{id}/wait?seconds=`
(0 to 1500, default 600) returns once the meeting needs the lead or has ended. The minutes (`Meeting.minutes`) are
English markdown written from the record; the dashboard draws the structured fields instead. Every change
publishes `meeting.updated` with the meeting's summary.

## Contests

`delegate` with `variants` (2 to 5) or `variant_specs` (`POST /contests`) runs one brief as several variants:

1. **Plan.** Without specs the variants take the enabled backends with free slots in turn (agy first), then their
   tiers. The pool must run at least two in parallel; a spec the lead named on a pool whose best account is under
   20 % remaining is refused (`409`, details), other variants that cannot run are dropped and reported in
   `estimate`. `POST /contests/estimate` answers the plan without starting. A contest cannot take `after` or plan
   mode.
2. **Base.** A snapshot of the user's working tree (HEAD, uncommitted and untracked files) through a temporary index
   under `contests/tmp`; the user's index and working tree are not touched.
3. **Worktrees.** Each variant is a normal task in its own detached worktree under
   `worktrees/contests/<id>/<label>/`, given the main checkout's environment (contract §6.3): the gitignored files
   named in `.worktreeinclude` are copied, the shared folders (such as `node_modules`) linked, and what was left out
   is reported with a reason. The brief's paths are moved into the worktree.
4. **Deadline.** `deadlineMinutes` (default 60, at most a day) cancels the variants still running.
5. **Score.** Once every variant ended, each is scored from its diff against the base: gate, automatic fix turns,
   files, lines, files outside the lane, tokens, time and the verifier. A variant is out when its task is missing,
   failed, was cancelled (by the deadline or otherwise), was rejected, broke its lane, had a red gate, or changed
   nothing. The qualified ones rank by verifier accept, fewer fix turns, fewer files outside the lane, a smaller
   diff, fewer tokens.
6. **Judge.** Unless `noJudge`, the judge (the member named, else the first enabled peer) reads the best three as
   anonymous letters with their diffs, read-only, and answers a winner, a ranking and reasons citing
   `[B path:12] "quote"`; every citation is checked against that variant's tree. The judge's winner is proposed when
   it is qualified and at least one of the citations about the winner itself is verified; otherwise the machine's,
   with `proposal.reasonCode` (`unverified`, `judge_failed`, `judge_running`) saying why.
7. **Pick.** `POST /contests/{id}/pick {winner?}` (default: the proposal) commits the winner's worktree as it is
   through a temporary index and `git commit-tree` (the worktree itself is not touched), writes that commit's diff
   against the base to `contests/<id>/<label>.patch` (kept: the record of what was applied) and, under the
   repository lock, runs `git apply --check` then
   `git apply` on the working tree only (never the index). A patch that does not apply changes nothing:
   `409 conflict` with `details.reason: "patch_conflict"` and the files, kept as `lastPickError`. After a pick the
   winner's task is accepted and the other finished variants rejected (losing adds nothing to the review history).
   The dashboard applies the proposal unless another qualified variant is chosen; when none qualifies it offers
   "Apply a variant anyway" for a disqualified one that still has work (red gate, lane, rejected).

A followup on a variant while the contest is ready reopens it; it is scored again when the variant ends.

Cleanup: once a variant's task stops running, its changes are stored in Seelegent's data folder
(`contests/<id>/<label>.json`: the diff against the base cut at 400 000 bytes, the files and line counts, and a
SHA-256 of the diff), written atomically; only then is its worktree removed. A worktree whose changes cannot be
stored stays, with the failure in `cleanupError`, so the worktree and its stored copy are never both gone. Its
removal: the shared-folder links first, then any other
link a worker made (removed as links, never followed), then everything left is committed inside the worktree with
its own index, and only then plain `git worktree remove` runs, which refuses a dirty worktree. Committing after the
links are gone means a worker that edited `.gitignore` can no longer get a linked folder committed and applied to
the user's tree (old bug D21). A removal that fails is recorded on the variant (`cleanupError`) and retried at the
next start. `POST /contests/{id}/cancel` stops the variants and the judge; the user's working tree is not touched.

Every change publishes `contest.updated` with the summary. `GET /contests/{id}/variants/{label}/diff` answers the
variant's diff against the base (cut at 400 000 bytes, `truncated` then) as text and parsed per file (`files`, for
the dashboard): from git while the worktree exists, from the stored copy after cleanup.

The commits a contest leaves in the user's repository (`applied.commit`, a variant's `commit`, the commit made
before `git worktree remove`) are referenced by nothing: Seelegent adds no branch, tag or ref, so `git gc` may prune
them (after about two weeks, or at once with `--prune=now`). They are informational only; nothing reads them once
the worktree is gone. A removed variant without a stored copy (a contest cleaned up before copies were kept, or a
worktree folder deleted by hand) answers `404 not_found` with `details.reason: "variant_data_gone"`; a stored copy
that does not match its checksum answers `409 bad_state` with `"variant_data_damaged"`. Neither reads as an empty
diff. There is no way to delete a contest yet, so its folder stays with the contest's record.

## Event hooks

A hook is a shell command (`cmd.exe /d /s /c` on Windows, `/bin/sh -c` elsewhere) the daemon starts on events:
`task.created`, `task.started`, `task.blocked`, `task.unblocked`, `task.done`, `task.failed`, `task.reviewed`,
`task.cancelled`, `task.takeover`, `task.handback`, `account.exhausted`, `account.needs_login`, `daemon.started`,
`daemon.reattached` (and `hook.test`). It gets the event as JSON on stdin and `SEELEGENT_EVENT`,
`SEELEGENT_TASK_ID`, `SEELEGENT_PORT` in its environment, runs in the task's folder (else the home folder), and is
killed with its process tree after `timeoutSeconds` (default 20, at most 600). A run ends when its command exits,
even if a child it left behind still holds the output (2 s later at most). On stop the daemon gives running hooks
5 s, then kills them ("the daemon stopped"). `project` limits it to tasks of one
folder. At most 50 hooks; four run at once and 50 events wait, the oldest dropped (and logged) beyond that. Each
run's output start (4 KiB, secrets masked) goes to `logs/hooks.log` (2 MiB, two old copies) and to `hook.ran`.

Hooks have stable ids: `PUT /hooks` keeps the ids it gets and gives new hooks one, and `POST /hooks/{id}/test`
runs a hook by id, never by list index, so an edited list cannot run the wrong hook (old bug C-B20).

## Locks

A team service never holds a task's lock while it creates tasks. The order is the engine's: `claimMu` (lane claims)
before any task lock, and child tasks (consults, judges, meeting attendees, variants) are created after the parent's
lock is released. Meetings and contests have their own lock per service or per contest; they call the engine
without holding a task lock, and the engine never calls back into them while holding one (they follow the task
bus).

## Decisions

- Meetings, contests and notes are SQLite rows, never a JSON file rewritten whole; a record that cannot be read is
  an error, never an empty reset (old bug D6).
- Contests and review notes are not imported from the old version: the variants ran in worktrees of the old daemon,
  and the notes' tasks are not imported. A meeting that was still running is imported as `cancelled`; its minutes
  are written again.
- A contest diff carries `files`, so the dashboard never reads unified diff text (contract §3.3).
- Peers are the default judges and the only rank whose objection blocks; a senior's objection is a warning, a
  worker is never asked.
