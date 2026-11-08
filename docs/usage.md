# Usage guide

This guide is for everyday work with Seelegent, one task at a time: what to do, where to click, which command to
run. Each section ends with a link to the page that has every detail. New to Seelegent? Do the first run with
[Getting started](getting-started.md), then come back here.

- [Install and set up](#install-and-set-up)
- [Add accounts](#add-accounts)
- [Delegate from the lead](#delegate-from-the-lead)
- [Watch tasks in the dashboard](#watch-tasks-in-the-dashboard)
- [Follow up and review](#follow-up-and-review)
- [Quota and handover](#quota-and-handover)
- [Gates and proof](#gates-and-proof)
- [The git guard and restoring work](#the-git-guard-and-restoring-work)
- [Work as a team](#work-as-a-team)
- [GitHub tickets](#github-tickets)
- [Automations](#automations)
- [Resources](#resources)
- [Doctor and troubleshooting](#doctor-and-troubleshooting)
- [Update and uninstall](#update-and-uninstall)

## Install and set up

1. Install with one line. Windows (PowerShell):

   ```powershell
   irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
   ```

   Linux and macOS:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
   ```

2. Open a new terminal and run `seelegent doctor`. A `warn` is fine on a fresh install (no daemon, no account yet); a
   `fail` comes with a fix.
3. The installer has already run `seelegent setup`, which registers the `seelegent` MCP server and the lead skill
   with every lead host it finds (Claude Code, Claude Desktop, Codex CLI, OpenCode, the Antigravity CLI and app,
   Gemini CLI, Cursor, Windsurf). Run it again after you install a new host:

   ```sh
   seelegent setup --dry-run          # show what would change
   seelegent setup --host claude,codex
   ```

   Restart each host afterwards. Its `seelegent` server should list 17 tools. Claude Code users can add `--hooks`
   for a reminder that nudges the lead to delegate instead of starting its own coding subagents.
4. `seelegent ui` starts the daemon when needed and opens the dashboard at http://127.0.0.1:47821/.

<p align="center">
  <a href="media/setup-doctor.mp4"><img src="media/setup-doctor.webp" width="720"
    alt="seelegent setup registers the lead hosts; seelegent doctor checks the install"></a>
</p>

More: [installer options](release.md#installing), [what setup writes for each host](setup.md#seelegent-setup).

## Add accounts

1. **Turn on the backends you want.** `agy` (Antigravity CLI, Gemini models) is on by default. For `claude` or
   `codex`, open **Settings › Backends**, turn the backend on and give each tier (`fast`, `standard`, `strong`,
   `max`) a model id.
2. **For an API key**, set the provider up first under **Settings › API providers** (presets for Anthropic,
   DeepSeek, Moonshot Kimi, Z.ai GLM, OpenRouter, OpenAI, Groq, a local Ollama, or a custom endpoint).
3. On **Accounts**, choose **Add account**, pick the backend and the kind (its own sign-in, this computer's sign-in
   for `agy`, or an API key), then give it a name and a priority (higher numbers are used first).
4. For an account with its own sign-in, choose **Sign in**. The worker CLI's own sign-in runs, in your browser or a
   terminal window, or asks you to paste a code. Seelegent never sees your password.

Add several accounts of the same backend to use several subscriptions. Turn **Use for work** off to keep an account
without using it for now.

<p align="center">
  <a href="media/accounts.mp4"><img src="media/accounts.webp" width="720"
    alt="The Accounts page with quota windows and sign-in"></a>
</p>

More: [accounts and sign-in](getting-started.md#2-accounts-and-sign-in), [backend settings](configuration.md).

## Delegate from the lead

Open your lead host in a project folder (a git repository) and ask for work the way you would ask a colleague,
mentioning Seelegent:

> Use Seelegent to add a `--verbose` flag to the CLI that prints each step. Have a worker write it, then check the
> diff and run the tests before you accept it.

The lead skill tells the lead what to do. The loop uses four MCP tools:

| Tool | When the lead uses it |
|---|---|
| `delegate` | Start a task with a brief written like a ticket (goal, files, constraints, acceptance criteria, how to verify) and the project folder as `cwd`. It waits for the worker's report (600 seconds by default). |
| `wait` | Wait for one or more running tasks; `any: true` returns at the first that ends. |
| `followup` | Send concrete fixes or the next step; the same worker continues its conversation. |
| `review` | Close the task as `accepted` or `rejected`, with notes that later briefs show to workers. |

Pick a tier for the work: `fast` for lookups and mechanical edits, `standard` for most work, `strong` or `max` for
hard, multi-file changes. Several tasks can run in parallel; give each its own files (a lane) so they do not edit
the same ones.

Without an MCP host, the same loop works from a terminal:

```sh
seelegent delegate --cwd . --model fast --gate-detect "Add a --verbose flag to the CLI that prints each step"
seelegent delegate --cwd . --wait 0 "..."        # return at once with the task id
seelegent wait <id> --seconds 600
seelegent ls --active
```

Facts every worker should know (build commands, conventions, pitfalls) go into the project's memory, which is added
to every brief: the `memory` tool, `seelegent memory append --cwd . "..."`, or **Settings › Memory**.

<p align="center">
  <a href="media/delegate.mp4"><img src="media/delegate.webp" width="720"
    alt="The lead delegates a task and gets the worker's report back"></a>
</p>

More: [every MCP tool and its arguments](mcp.md), [every command](cli.md), [the lead skill](setup.md#skill).

## Watch tasks in the dashboard

- **Watch** is the home page: what **needs you** at the top, what is running (current step, tokens, changed files),
  what finished recently, and a strip of accounts with their state.
- **Needs you** lists tasks with a banner that says what to do: answer a question, sign an account in, restore
  files at risk, or follow up on a red gate. The same tasks are under the **Needs you** filter of **Tasks**.
- **Tasks** lists every task with filters and search. Open one for its header (status, account, model, actions),
  its gate and safety panels, and five views: **Terminal** (the conversation and every tool call), **Tree** (turns,
  attempts and steps), **Graph** (steps over time), **Files** (what the worker read or edited) and **Git** (the
  task's commits and uncommitted changes, file by file).
- **Take over** opens the worker's conversation in a console on the same account; when you close it, the worker
  writes a hand-back report.

`Ctrl+K` (`Cmd+K` on macOS) opens the command palette and `Ctrl+,` the settings.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="media/needs-you.mp4"><img src="media/needs-you.webp" alt="The Watch page lists the tasks that need you, with a banner on each"></a><br>
      <b>Needs you</b>
    </td>
    <td width="50%" align="center">
      <a href="media/task-detail.mp4"><img src="media/task-detail.webp" alt="A task's Terminal, Tree, Graph, Files and Git views"></a><br>
      <b>Task detail</b>
    </td>
  </tr>
</table>

More: [a tour of the dashboard](getting-started.md#4-a-tour-of-the-dashboard), [task states and
attention](task-engine.md#attention).

## Follow up and review

A task stays open until it gets a verdict. Read the diff in the **Git** view (or let the lead read it), run the
tests, then:

- **Follow up** with concrete fixes: the **Follow up** box under the task (you can pick another tier or model for
  that turn), the lead's `followup`, or `seelegent followup <id> "Also handle the empty input case"`.
- **Leave review notes** on lines of the diff in the **Git** view and send them all as one followup. Each note
  follows its line across turns and shows whether the next turn addressed it.
- **Accept** or **Reject**: the buttons on the task, the lead's `review`, or
  `seelegent review <id> accepted --notes "..."`.

Accepting does not commit anything. Commit the accepted work yourself, or with the **Commit…** dialog of the Git
view, which commits only the files you tick.

More: [follow up and review](getting-started.md#6-follow-up-and-review), [review notes](team.md#review-notes).

## Quota and handover

- The daemon reads every account's quota windows every 10 minutes (`quotaRefreshMinutes`) and picks the account
  for each turn by priority, then the fewest running tasks, then the most quota left.
- An account at or below 3% of a window (`minRemaining`) rests until that window resets. When an account runs out
  in the middle of a task, the task moves to another account with a handover of what happened so far.
- When no account can run a task, it shows **Waiting for quota** (it starts by itself after a reset) or **No
  accounts left** (add or turn on an account).
- `seelegent status` (or the lead's `status`) shows each account's quota and the active tasks; **Usage** shows
  tokens, cost, tasks and turns over 7 or 30 days, and each account's quota over time.

More: [quota settings](configuration.md#accounts-quota-and-timeouts), [a run, turn by turn](task-engine.md#a-run).

## Gates and proof

- After every worker report the daemon runs the task's **gate**: the lead's commands (`--gate`, or the `gate`
  argument of `delegate`), the spec's `Gate:` lines, or the repository's detected test, lint and build commands
  (`--gate-detect`, `gate: true`), plus syntax and hygiene checks.
- A red gate goes back to the worker as an automatic fix turn, twice at most by default, before the lead is woken.
  When the fix turns are used up the task shows **Gate failed**: read the failing steps in the gate panel, then
  follow up or reject.
- **Lanes** list the files a task may change (`--lane`), so parallel workers stay off each other's files.
- `--verify` runs a read-only second-opinion verifier once the gate is green.
- With a Spec Kit feature (`--spec` and `--task`), the **Proof** page shows which requirements are proven and runs
  regression runs of every gate command (`seelegent proof matrix`, `seelegent proof run`).

<p align="center">
  <a href="media/gates-proof.mp4"><img src="media/gates-proof.webp" width="720"
    alt="A red gate goes back to the worker as a fix turn; the Proof page"></a>
</p>

More: [gates, lanes and proof](gates.md).

## The git guard and restoring work

- Workers cannot run git commands that can throw away uncommitted work or rewrite history (`reset`, `restore`,
  `stash`, `checkout` except `checkout -b`, `switch`, `clean`, `rebase`, `merge`, `pull`, `push`, `commit -a`,
  `--amend` and more), also inside shells, interpreters, pipes or git aliases. The lead can lift the guard for one
  turn with `allow_git` (`--allow-git`) when a worker should commit.
- Before and after every turn Seelegent records the repository's uncommitted files, without touching your index,
  stash or refs. Files a turn may have lost show up as **work at risk** with the banner **Work may be lost**.
- Restore them with **Restore files** in the task's **Safety checks** panel (once the worker has stopped), or
  `seelegent restore <id> --turn N [--file path]`. The current content is kept in a backup snapshot first.
- Post-turn checks also report line-ending and BOM changes, stray files, writes outside the project and files other
  tasks changed too.

These are guards, not a sandbox: worker CLIs run as your user, so review what they changed.

<p align="center">
  <a href="media/git-guard.mp4"><img src="media/git-guard.webp" width="720"
    alt="The git guard refuses a destructive git command; files at risk are restored"></a>
</p>

More: [safety](safety.md).

## Work as a team

- **Members** are named workers with a backend, a model or tier, a role and a rank (`worker`, `senior`, `peer`).
  Add them with **New member** on the **Members** page or with `seelegent members add`; delegate to one with `--member` (the `member`
  argument).
- **Consult**: ask a senior or peer for a second opinion on a finished task (`consult`,
  `seelegent consult <id> --member M`). A peer's objection blocks acceptance until the lead answers it.
- **Meeting**: before a hard decision, up to six members answer the same question; the lead decides and can turn
  the decision into tasks (`meeting`, the **Meetings** page). A peer may record a formal dissent.
- **Contest**: run one brief as 2 to 5 variants in separate worktrees (`--variants 3`, or `--variant` per variant),
  score them, and apply the winner to your working tree (`seelegent contest pick`, the **Contests** page).
- **Event hooks** run your own shell commands on task, account and daemon events (**Settings › Hooks**,
  `seelegent hooks`).

<p align="center">
  <a href="media/team.mp4"><img src="media/team.webp" width="720"
    alt="Members, a consult, a meeting and a contest"></a>
</p>

More: [team](team.md).

## GitHub tickets

1. On **Issues**, connect a repository on this computer (or `seelegent issues connect`). Seelegent reaches GitHub
   through `gh` when it is signed in, else a token you store on **Issues** (only you can store it, in the
   dashboard), else a manual mode with a compare link.
2. **Sync** brings its open issues into the inbox; you can also add local issues.
3. **Triage** an issue: `assign` makes a ticket with its own worktree and branch (`seelegent/<n>-<slug>`) and starts
   a task there; `meeting` opens a meeting first; `skip` records why not.
4. Once the ticket's work is accepted, **pr** commits, pushes the ticket branch and opens a draft pull request,
   after a secret scan of every added file. Seelegent then follows the CI checks, and `fix-checks` starts a task with
   the failing ones.

Your own checkout is never changed, and Seelegent never force-pushes, merges or closes issues.

More: [GitHub](github.md).

## Automations

- An automation is a saved delegation on a schedule (hourly, daily, weekdays, weekly or cron, in a time zone), with
  an optional precheck command and quota rules: a run is skipped when quota is low, and an optional extra run uses
  a weekly quota that is about to reset unused.
- Every automation is created **disabled**. The lead (or `seelegent automations enable`) can only ask; you confirm
  exactly what it runs on the **Automations** page. Any change to what it runs or when turns it off again.
- `seelegent automations preview` shows the next run times, `run` starts one run now, `runs` shows the history.

<p align="center">
  <a href="media/github-automations.mp4"><img src="media/github-automations.webp" width="720"
    alt="An issue triaged into a ticket and a draft pull request; an automation schedule"></a>
</p>

More: [automations](automations.md).

## Resources

Seelegent shares the machine with your own programs, so it holds back new work when the machine is busy.

- Five settings are **auto** by default and computed from this machine's memory and CPUs (a container's limits
  included): `gateConcurrency` (gate commands at once), `minFreeMemoryMB` and `minFreeCommitMB` (a new worker turn or
  gate command waits below these), `criticalFreeMemoryMB` (the emergency floor that stops the newest gate command)
  and `testParallelism` (the `-p`/`-j` hint for tests and builds). A 4 GB laptop and a 128 GB workstation each get
  values that suit them.
- **Settings › Resources** shows each value in use and where it comes from. **Set** opens the field at the value in
  use so you can pin your own; **Use auto** returns a setting to the value computed for this machine.
- Other resource settings: worker and gate process priority (low by default), `maxConcurrentWorkers` over all
  accounts, and, on Windows, a memory and CPU cap per worker tree.
- A task held back for memory shows **Waiting for resources**. `seelegent doctor` (check `resources`) compares free
  memory with the thresholds and suggests values when the machine is usually below them.

<p align="center">
  <a href="media/resources.mp4"><img src="media/resources.webp" width="720"
    alt="Settings, Resources: auto values for this machine, Set and Use auto"></a>
</p>

More: [resource settings and their formulas](configuration.md#resources),
[the start check and the memory watchdog](task-engine.md#resources-the-start-check-and-the-memory-watchdog).

## Doctor and troubleshooting

- `seelegent doctor` checks the data folder, settings, database, daemon, git, each enabled worker CLI, accounts, PATH
  and free memory, and says how to fix what fails. It changes nothing apart from a write test in the data folder.
- `seelegent status` shows the daemon, the backends, each account's quota and the tasks that need you.
- `seelegent task <id> --steps 30` prints a task's last worker steps; `--full` the whole task.
- `seelegent restart` restarts the daemon; running workers keep running and are picked up again. Its log is
  `~/.seelegent/logs/daemon.log` (secrets masked).
- A lead whose `wait` times out loses nothing: the task keeps running, and `wait` picks it up again.
- `seelegent setup --dry-run` shows what is registered with each lead host. Restart a host after setup or update.
- "Seelegent is not available inside a Seelegent worker." means a worker tried to call Seelegent; that is refused on
  purpose.

More: [when something is stuck](getting-started.md#7-when-something-is-stuck), [doctor's checks](cli.md#doctor).

## Update and uninstall

- `seelegent update` tells you whether a newer release exists; `seelegent update --apply` installs it in place
  (checked against the release's `checksums.txt`, daemon restarted, lead hosts refreshed);
  `seelegent update --rollback` goes back to the version the last update replaced.
- To uninstall: `seelegent stop`, then `seelegent setup --remove`; delete the binary and the `.previous` copy next to
  it (`%LOCALAPPDATA%\Programs\seelegent\bin\` on Windows, `~/.local/bin/seelegent*` elsewhere) and remove that
  folder from your PATH. Optionally delete `~/.seelegent`, which holds your account profiles, keys, tasks and
  history.

More: [updating](release.md#updating), [the README's uninstall steps](../README.md#update-and-uninstall).
