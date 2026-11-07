# Automations

An automation is a saved delegation on a schedule: a prompt, the folder it runs in, the worker (a member, or a
backend and model), the gate, the lane, an optional precheck command, quota rules and a schedule. Milestone 15,
package `internal/automation` (`automation.go` the store and the rules, `scheduler.go` the timer, `cron.go` the
schedules, `quota.go` the quota source). Automations and their runs are rows of `seelegent.db` (migration
`0010_github_automations.sql`); `seelegent migrate` imports the old version's `automations.json` with every automation
off ([setup.md](setup.md)).

Routes: `/automations*` ([api.md](api.md)); MCP tool `automations` ([mcp.md](mcp.md)); `seelegent automations ...`
([cli.md](cli.md)); the Automations page of the dashboard.

## Turning one on (old bug D8)

An automation runs a prompt with no one watching, on a schedule, as long as it stays on. In the old version the
lead could create one already enabled. Now:

1. **Every automation is created off.** `enabled: true` in a create or a `PATCH`, and `POST /automations/{id}/enable`,
   only record a request (`enableRequested`) and answer a `confirmUrl` (`/automations?confirm=<id>` on the
   dashboard). That is all the MCP tool and the CLI can do; their answer tells the lead to hand the link to the user.
2. **Only the dashboard turns one on.** The confirm dialog reads the automation fresh and shows everything a run
   does: the prompt in full, the folder, the worker, the gate and its commands, the lane, the precheck, the quota
   rules and the schedule. *Turn on* asks for a one-time token (`POST /automations/{id}/confirm-token {revision}`)
   and spends it at once (`POST /automations/{id}/confirm {token}`).
3. **The token enables exactly what was shown.** `revision` is the automation's `Revision` as the dialog shows it
   (a hash of the prompt, folder, worker, gate, lane, precheck, schedule, `quota.useBeforeReset` and
   `graceMinutes`). The daemon refuses a token for a
   revision that is not the current one, and refuses the confirmation when the automation changed after the token
   was issued (`409 bad_state`); the dialog then shows the new content to confirm again. A token works once, for
   five minutes, for one automation.
4. **A change of what a run does, or when, turns it off again.** Editing the prompt, folder, worker, gate, lane,
   precheck, schedule, `quota.useBeforeReset` (it adds a run outside the schedule) or `graceMinutes` (how late a
   missed run may start) of an enabled automation turns it off and records an enable request; renaming it or
   changing `quota.minRemaining` does not (that floor can only skip a run the confirmed schedule allows). Turning it off (`PATCH enabled: false`) works from anywhere.

The confirm routes answer only the dashboard's own requests (`dashboardOnly`): the `Origin` is the daemon's own,
`Sec-Fetch-Site` is `same-origin`, `Sec-Fetch-Mode` is `cors` or `same-origin`, and the request carries
`X-Seelegent-UI: 1`; anything else is `403 forbidden` with `reason: "dashboard_only"`. A worker is refused by the
worker guard before that. The same rule covers the GitHub token routes.

**What this does not stop.** These headers keep the lead's tools, a worker and a web page in a browser away from
the confirmation. A program running as the same user can still forge them, as it could edit the database itself;
the daemon does not try to defend against the user's own account.

## Schedules

A preset (`every`: `hourly` at a minute, `daily` or `weekdays` at `HH:MM`, `weekly` on a weekday at `HH:MM`) or a
5-field cron expression (`minute hour day-of-month month day-of-week`; `*`, lists, ranges and steps). The two day
fields follow Vixie cron: when either one starts with `*` (`*`, `*/2`) a day must match both, otherwise a day
matching either one runs. So `0 9 */2 * 1` runs on Mondays that fall on an odd day, `0 9 1,15 * 1` on the 1st, the
15th and every Monday, and `0 9 * * 1` on Mondays only. `timezone` is `local` or an IANA zone; a time that does not exist on a
daylight-saving day runs as late as the gap is long (02:30 becomes 03:30), and a repeated hour runs once. `POST /automations/preview`
answers the next run times as the scheduler computes them (the editor shows them as you type). A schedule no date
matches within five years (`0 6 31 2 *`) is refused on create and edit (`400 invalid`); its preview has no runs.
The dashboard reads cron expressions with the same rules (`web/src/features/automations/cronText.ts`): it names the
wrong field under the cron box, refuses to save a schedule that never runs, and puts common shapes in words
(`0 6 1 * *` is "06:00 on day 1 of every month"); times show in the automation's zone with the viewer's own in
brackets.

The timer sleeps until the earliest next run (at most a minute, so the master switch and the before-reset rule are
seen). A run missed while the daemon was off still starts if it is at most `graceMinutes` late (default 60);
otherwise it is recorded as `skipped_late`, once. The master switch `automationsEnabled` (settings, System) stops
every scheduled run; *run now* still works.

A due run is written as `pending` in the same transaction that moves the automation to its next run time, and the
run's outcome later replaces that row. A stop between the two (the daemon closed while the quota or the precheck was
being checked) leaves the row `pending`; the next start turns it into `error` with a detail that says the daemon
stopped while the run was starting and that it may have started a task (check the task list). It is not started
again on its own: the stop may have come after the task was made, and an unattended run must never happen twice. *Run now* runs it if it is still needed.

## A run

*Run now* (`POST /automations/{id}/run`) runs only what the user confirmed: an automation never confirmed in the
dashboard, or changed since (`confirmed: false`), is refused with `409 bad_state`, `details: {reason:
"not_confirmed", confirmUrl}`, because the lead's tools can create and edit automations and running one at once
would run a prompt the user never saw. The dashboard shows *Review and turn on* in its place. A confirmed
automation runs now also while it is off or the master switch is off.

A precheck follows the shell convention of `grep -q`, `test` and `git diff --quiet`: exit code 0 runs, 1 means
"nothing to do" and skips the run quietly, and anything else is a broken precheck the user should look at.

Every run, scheduled or *run now*, goes through the same checks, and every outcome is recorded (the newest 500 per
automation are kept):

| Outcome | When |
|---|---|
| `skipped_overlap` | the task of the previous run is still pending, or a run is starting |
| `skipped_quota` | the best usable account of the backend has less than `quota.minRemaining` % left (default 20) in the bucket that limits it; with no quota known for the backend the run starts |
| `skipped_precheck` | the precheck command (run in the folder through the shell, 60 s at most, its process tree killed after) answered "skip": it exited with code 1; the detail carries the end of its output |
| `precheck_failed` | the precheck itself broke: any other non-zero exit code (2 for a usage error, 126 or 127 for a missing command, 128 for a git error), killed, timed out or could not start; the run is skipped and the detail carries the end of its output |
| `error` | the delegation itself failed (no folder, no account, a lane conflict) |
| `pending` | the scheduler decided on the run and it has not started yet (see above) |
| `started` | a task was delegated; `taskId` names it (the task's `automationId` links back) |

**Use quota before it resets** (`quota.useBeforeReset`): when a weekly bucket of the backend resets within 24 hours
with at least 40 % left, one extra run starts for that bucket (trigger `before_reset`). The bucket counts as used
only when the run started (old bug D9: it was marked before the run, so a skipped run used it up); after a skipped
run the rule tries again 30 minutes later.

## Events

`automation.updated` carries the automation after every change (including `running` turning false when its task
ends); `automation.deleted {id}` follows a delete.
