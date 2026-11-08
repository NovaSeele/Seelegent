<div align="center">

<img src="docs/media/logo.svg" alt="Seelegent" width="344">

**One lead agent plans and reviews. Worker coding CLIs on your own accounts write the code.**

[Install](#install) · [Quick start](#quick-start) · [Usage guide](docs/usage.md) · [Docs](docs/README.md) ·
[Contributing](CONTRIBUTING.md)

**English** · [Tiếng Việt](README.vi.md) · [简体中文](README.zh-CN.md) · [Español](README.es.md) ·
[한국어](README.ko.md) · [日本語](README.ja.md) · [Русский](README.ru.md)

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/NovaSeele/Seelegent)](https://github.com/NovaSeele/Seelegent/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/NovaSeele/Seelegent)](go.mod)
[![Platforms: Windows | Linux | macOS](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#requirements)
[![CI](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml/badge.svg)](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml)

</div>

<p align="center">
  <a href="docs/media/hero.mp4"><img src="docs/media/hero.webp" width="900"
    alt="A lead agent delegates a task through Seelegent; the dashboard shows the worker's steps, the gate and the diff"></a>
</p>

Seelegent lets one lead coding agent hand work to worker coding CLIs that run on your own accounts. You talk to the
lead (Claude Code, Codex, Antigravity, OpenCode or another MCP host); the lead designs the work, writes a brief and
delegates it over MCP; Seelegent runs it as a normal non-interactive CLI call (`agy`, `claude` or `codex`) on the
account with the most quota left, checks the result, and hands the report back for review. It is for people who
have several subscriptions or API keys and want one agent to plan and review while the others write the code.

Seelegent is not a proxy and does not pool accounts behind one endpoint. Every run is an ordinary CLI call on an
account you own, in its own isolated profile, and each provider's terms still apply to each account.

## Features

- **Delegate from the lead you already use.** The lead calls `delegate`, `wait`, `followup` and `review` over MCP,
  and Seelegent runs each turn as one ordinary worker CLI call and returns its report. → [MCP tools](docs/mcp.md)
- **See what needs you first.** The **Watch** page puts tasks with a question, a missing sign-in, a red gate or
  work at risk at the top, each with a banner that says what to do. →
  [When something is stuck](docs/getting-started.md#7-when-something-is-stuck)
- **Every step on record.** Each task has Terminal, Tree, Graph, Files and Git views: the conversation, every tool
  call, turns and attempts, and the diff with review notes on its lines. → [Task engine](docs/task-engine.md)
- **Many accounts, one pool.** Own sign-ins, this computer's sign-in or API keys are picked by priority, running
  tasks and quota left, and a task whose account runs out moves to another one with a handover. →
  [Configuration](docs/configuration.md)
- **Gates that send red work back.** After every report the daemon runs the tests, lint and build, returns a red
  gate to the worker as an automatic fix turn, and the **Proof** page tracks which Spec Kit requirements are
  proven. → [Gates, lanes and proof](docs/gates.md)
- **Your uncommitted work stays put.** The git guard refuses the git commands that can throw work away, and
  snapshots before and after every turn let you restore files a turn may have lost. → [Safety](docs/safety.md)
- **A team, not only workers.** Named members with ranks give second opinions, hold meetings before hard
  decisions, and run one brief as a contest of 2 to 5 variants in separate worktrees. → [Team](docs/team.md)
- **From issue to draft pull request, or on a schedule.** Triage GitHub issues into tickets with their own worktree
  and branch, open a draft pull request after a secret scan, and run saved delegations on a schedule. →
  [GitHub](docs/github.md) · [Automations](docs/automations.md)
- **Sized to your machine.** Gate concurrency, memory thresholds and test parallelism are computed from this
  machine's memory and CPUs; set any of them, or go back to auto, in **Settings › Resources**. →
  [Resources](docs/configuration.md#resources)
- **Set up in one command, checked by another.** `seelegent setup` registers the MCP server and the lead skill with
  nine lead hosts, and `seelegent doctor` checks the install and says how to fix what fails. →
  [Setup](docs/setup.md) · [doctor](docs/cli.md#doctor)

### See it in action

Each preview links to a short video.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/delegate.mp4"><img src="docs/media/delegate.webp" alt="The lead delegates a task and gets the worker's report back"></a><br>
      <b>Delegate</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/needs-you.mp4"><img src="docs/media/needs-you.webp" alt="The Watch page lists the tasks that need you, with a banner on each"></a><br>
      <b>Needs you</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/task-detail.mp4"><img src="docs/media/task-detail.webp" alt="A task's Terminal, Tree, Graph, Files and Git views"></a><br>
      <b>Task detail</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/accounts.mp4"><img src="docs/media/accounts.webp" alt="The Accounts page with quota windows and sign-in"></a><br>
      <b>Accounts and quota</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/gates-proof.mp4"><img src="docs/media/gates-proof.webp" alt="A red gate goes back to the worker as a fix turn; the Proof page"></a><br>
      <b>Gates and proof</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/git-guard.mp4"><img src="docs/media/git-guard.webp" alt="The git guard refuses a destructive git command; files at risk are restored"></a><br>
      <b>Git guard and restore</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/team.mp4"><img src="docs/media/team.webp" alt="Members, a consult, a meeting and a contest"></a><br>
      <b>Team</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/github-automations.mp4"><img src="docs/media/github-automations.webp" alt="An issue triaged into a ticket and a draft pull request; an automation schedule"></a><br>
      <b>GitHub and automations</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/resources.mp4"><img src="docs/media/resources.webp" alt="Settings, Resources: auto values for this machine, Set and Use auto"></a><br>
      <b>Resources</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/setup-doctor.mp4"><img src="docs/media/setup-doctor.webp" alt="seelegent setup registers the lead hosts; seelegent doctor checks the install"></a><br>
      <b>Setup and doctor</b>
    </td>
  </tr>
</table>

## Requirements

- **OS:** Windows 10 or 11, Linux or macOS, on x64 or ARM64. Windows is the most tested platform.
- **Git.** Seelegent needs it for snapshots, the Git view, contests and GitHub tickets.
- **At least one worker CLI**, installed: the Antigravity CLI (`agy`, Gemini models, the default backend), Claude
  Code (`claude`) or Codex (`codex`). The `claude` and `codex` backends are off until you turn them on in Settings.
- **A lead host** with MCP: Claude Code, Claude Desktop, Codex CLI, OpenCode, the Antigravity CLI or app, Gemini CLI,
  Cursor or Windsurf. A host without MCP can use the `seelegent` command instead.
- Optional: the GitHub CLI (`gh`), signed in, for the GitHub features.

## Install

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

**Linux and macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

The installer downloads the latest release, checks its SHA-256 against the release's `checksums.txt`, puts
`seelegent` on your PATH (`%LOCALAPPDATA%\Programs\seelegent\bin` on Windows, `~/.local/bin` elsewhere), imports the
old version's data if it finds any, and runs `seelegent setup` for the lead hosts on this machine. Open a new
terminal and restart your lead hosts afterwards. Options such as `--version`, `--install-dir` and `--no-setup` (sh)
or `-Version`, `-InstallDir` and `-NoSetup` (PowerShell) are in [docs/release.md](docs/release.md#installing). To
build from source, see [CONTRIBUTING.md](CONTRIBUTING.md#development-setup).

## Quick start

1. **Check the install:** `seelegent doctor`. A `warn` is fine on a fresh install; a `fail` comes with a fix.
2. **Connect your lead hosts:** the installer already ran `seelegent setup`, which adds the `seelegent` MCP server to
   every lead host it finds and installs the lead skill. Run it again after installing a new host (`--dry-run`
   shows the changes first, `--remove` undoes them), then restart the host.
3. **Add an account:** `seelegent ui` starts the daemon if needed and opens http://127.0.0.1:47821/. On
   **Accounts**, choose **Add account**, pick the backend and the kind, then **Sign in**: the worker CLI's own
   sign-in runs, in your browser or a terminal window, and Seelegent never sees your password.
4. **Delegate.** In a project folder, ask your lead something like *"Use Seelegent to add input validation to the
   signup form, then review the diff."* The lead calls `delegate`, waits for the report, reads the diff, runs the
   tests, and then sends a `followup` or closes the task with `review`. From a terminal:
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`.

The [usage guide](docs/usage.md) covers everyday work task by task, and
[docs/getting-started.md](docs/getting-started.md) walks through the first run in detail.

## How it works

```mermaid
flowchart LR
    U([You]) --> L[Lead agent]
    L -->|MCP: delegate, wait, followup, review| M[seelegent mcp]
    M -->|HTTP on 127.0.0.1| D[Seelegent daemon]
    D -->|one run per turn| A[agy]
    D --> C[claude]
    D --> X[codex]
    U -->|browser| B[Dashboard]
    B --- D
```

- **One binary.** `seelegent` is the daemon, the command line, the MCP server the lead host starts (`seelegent
  mcp`) and the web dashboard, all in one file.
- **The daemon** listens on `127.0.0.1:47821` only. It picks an account, runs the worker, records every step, runs
  the checks and keeps going across restarts: running workers are picked up again by the next daemon.
- **Workers** are the worker CLIs you already use, each account in its own profile folder. When an account runs out
  of quota in the middle of a task, it rests until its window resets and the task moves to another account with a
  handover.
- **The dashboard** (http://127.0.0.1:47821/) has pages for Watch, Tasks, Issues, Meetings, Contests, Automations,
  Proof, Accounts, Usage, Roles and Members, plus a searchable Settings dialog. It offers 26 colour themes, seven
  interface languages, a command palette (`Ctrl+K`, `Cmd+K` on macOS) and keyboard shortcuts. It is embedded in the
  binary and loads nothing from the internet.
- **The command line.** Every MCP tool except `profiles` has a command (worker roles are edited on the **Roles**
  page and through the `profiles` tool), and every command takes `--json`: `seelegent --help`, and
  [docs/cli.md](docs/cli.md).

### What a task goes through

**Accounts.** Three backends (`agy`, `claude`, `codex`) and three kinds of account: an isolated profile with its own
sign-in, the sign-in already on this computer (`agy` only), or an API key of a provider (`claude` and `codex` only:
Anthropic, DeepSeek, Moonshot Kimi, Z.ai GLM, OpenRouter, OpenAI, Groq, a local Ollama, or a custom endpoint). The
pool reads each account's quota windows and prefers higher priority, fewer running tasks and more quota left. Model
tiers `fast`, `standard`, `strong` and `max` map to real models per backend.

**Tasks.** A task is a conversation: `delegate`, then `followup` as often as needed, then `review` with `accepted`
or `rejected`. Tasks run in parallel, can wait for each other (`after`), get the project's memory and review history
in every brief, can run as a worker role (rules, output style, skills, default tier), and can take a Spec Kit feature
folder and task ids instead of the whole spec. Tasks that stall, loop or ask a question wake the lead early, and tasks
that need you (a sign-in, no account left) go to the top of the dashboard. You can take a task over in a console
and hand it back.

**Gates, lanes and proof.** After every worker report the daemon runs the task's gate: the lead's commands, the
spec's `Gate:` lines or the repository's detected test, lint and build commands, the project doctrine, syntax checks
and the hygiene check. A red gate goes back to the worker as an automatic fix turn (2 by default) before the lead is
woken. Lanes list the files a task may change, so parallel workers stay off each other's files. An optional verifier
reviews a green gate read-only.

**Team, GitHub and automations.** Members are named workers with a backend, a model or tier, a role and a rank
(`worker`, `senior`, `peer`); a peer's objection in a consult blocks acceptance until the lead answers it. Event
hooks run your own shell commands on task, account and daemon events. A connected repository's issues become
tickets; once the work is accepted, Seelegent commits, pushes the ticket branch, opens a draft pull request and
follows its CI checks. It uses `gh` when it is signed in, else a token you store in the dashboard, else a manual
mode with a compare link. Automations run on a schedule (hourly, daily, weekdays, weekly or cron, in a time zone)
with a precheck command and quota rules.

### MCP tools for the lead

| Tool | What it does |
|---|---|
| `delegate` | Start a task (or a contest) and wait for the worker's report. |
| `wait` | Wait for one or more tasks; `any: true` returns at the first. |
| `followup` | Send the next instruction or fixes on a task. |
| `review` | Close a task as `accepted` or `rejected`, with notes. |
| `cancel` | Stop a running or queued task. |
| `task` | A task's report and its recent worker steps. |
| `status` | Accounts and quota, active tasks, backends. |
| `memory` | Per-project notes included in every brief. |
| `profiles` | Worker roles: rules, output style, skills, default tier. |
| `proof` | Requirement matrix, regression runs, lane claims, statistics. |
| `members` | The department members. |
| `consult` | A second opinion on a finished task from a senior or peer. |
| `notes` | Review notes on a task's diff, sent as one followup. |
| `meeting` | A structured meeting of members before a decision. |
| `contest` | Follow a contest, pick the winner. |
| `issues` | GitHub issues to tickets, worktrees and draft pull requests. |
| `automations` | Scheduled delegations. |

`seelegent setup` also installs the lead skill, which teaches the lead this loop; `seelegent skill` prints it. Details:
[docs/mcp.md](docs/mcp.md).

## Safety

Workers run in folders where you and other workers keep uncommitted work. Seelegent protects it in layers; none of
them fails a task on its own, they tell the lead and the dashboard what happened. See [docs/safety.md](docs/safety.md).

- **Git guard.** Workers cannot run git commands that can throw away uncommitted work or rewrite history:
  `reset`, `restore`, `stash`, `checkout` (except `checkout -b` to create a branch), `switch`, `clean`, `rebase`,
  `merge`, `pull`, `push`, `commit -a` and `--amend`, and more, also inside `bash -c`, PowerShell, pipes into a
  shell or git aliases. The lead can lift it for one turn with `allow_git`.
- **Snapshots.** Before and after every turn Seelegent records the repository's uncommitted files, without touching
  your index, stash or refs. Files the turn may have lost show up as *work at risk*, with a Restore button in the
  dashboard and `seelegent restore` on the command line.
- **Post-turn checks.** Line-ending and BOM changes, stray files, writes outside the project and overlaps with other
  tasks are reported on the task.
- **What workers cannot do.** A worker cannot call Seelegent itself (it cannot start, change or review tasks),
  cannot turn on an automation and cannot store a GitHub token. Only you, in the dashboard, can do the last two.
- **Before a push.** Seelegent pushes one commit of its own, never the worker's commits, and only after scanning
  every file it adds: `.env` files, keys and certificates, credential files, tokens in file content, Windows device
  names such as `nul` and files over 5 MB refuse the push, with no override. It never force-pushes, merges or closes
  issues.
- **Automations start off.** Every automation is created disabled. It runs only after you confirm exactly what it
  does in the dashboard, and any change to what it runs or when turns it off again.

These are guards, not a sandbox: worker CLIs run as your user. Review what they changed. To report a security
problem, see [SECURITY.md](SECURITY.md).

## Data and privacy

- Everything runs on your machine. The daemon listens on `127.0.0.1` only. Seelegent itself sends no telemetry; the
  worker CLIs follow their own settings.
- Data lives in `~/.seelegent` (set `SEELEGENT_HOME` to move it): `config.json` (settings), `seelegent.db` (tasks,
  accounts, usage, team, tickets, automations), `profiles/` (one isolated profile per account, with its API key),
  `github/` (the token), `snapshots/`, `contests/` (stored contest diffs), `worktrees/` (only for contests and
  tickets), `run/` and `logs/`. See [docs/configuration.md](docs/configuration.md).
- Seelegent never asks for passwords. Each worker CLI signs in with its own flow and keeps its tokens in the account's
  profile. API keys and the GitHub token are each stored in their own file, encrypted with DPAPI on Windows and as
  owner-only (0600) plain text elsewhere; the dashboard shows only their last four characters, and logs mask secrets.
- What leaves the machine: the worker CLIs talk to their own providers (that is the work), quota is read through the
  CLIs (and, for Claude sign-ins, from Anthropic's usage endpoint with that account's own sign-in), API-key accounts
  call the provider you configured, and GitHub is called only for repositories you connect (syncing, a poll of
  their issues and pull requests every 10 minutes, which `githubPollMinutes: 0` turns off, and the pushes, pull
  requests and comments you or the lead ask for), to check your GitHub sign-in when you open **Issues**, and by the
  installers and `seelegent update`.

## Update and uninstall

`seelegent update` tells you whether a newer release exists; `seelegent update --apply` installs it in place (checked
against `checksums.txt`, daemon restarted, lead hosts refreshed) and `seelegent update --rollback` goes back once.
The database is never migrated backwards, so a release older than your data is refused. See
[docs/release.md](docs/release.md#updating).

To uninstall:

1. `seelegent stop`, then `seelegent setup --remove` (removes the MCP entries, the lead skill and the hook).
2. Delete the binary and the kept `seelegent.exe.previous` (`seelegent.previous`) next to it:
   `%LOCALAPPDATA%\Programs\seelegent\bin\` on Windows, `~/.local/bin/seelegent*` elsewhere. Remove that folder from
   your PATH (the user `Path` on Windows, the `export PATH=...` line the installer added to your shell profile
   elsewhere).
3. Optionally delete `~/.seelegent`. It holds your account profiles (and their sign-ins), keys, tasks and history.

### Migrating from the old version

The old Node version kept its data in the same folder. On the first install the installer runs `seelegent migrate`,
which imports settings, providers, accounts with their profiles and API keys, project memory and review history,
roles, members, hooks, meeting records and automations (every automation off, to confirm again). Tasks, review
notes, contests, proof records, usage history and the GitHub folder are not imported: connect your repositories and
store the token again. The old folder is never changed. On Windows, if the old launcher is in the install folder, it
is kept as `seelegent-node`. Stop the old daemon before you install, and run `seelegent migrate --dry-run` to see
what would be imported. See [docs/setup.md](docs/setup.md#seelegent-migrate).

## Troubleshooting

- `seelegent doctor` checks the data folder, settings, database, daemon, git, each enabled worker CLI, accounts,
  PATH and free memory against the resource thresholds, and says how to fix what fails. It changes nothing apart
  from a write test in the data folder (`.doctor-write-test`, removed again).
- `seelegent status` shows the daemon, accounts with quota and the tasks that need you; `seelegent restart` restarts
  the daemon. Its log is `~/.seelegent/logs/daemon.log`.
- `seelegent setup --dry-run` shows what is registered with each lead host. Restart a host after setup or update.
- "Seelegent is not available inside a Seelegent worker." means a worker tried to call Seelegent; that is refused
  on purpose.
- A task stuck in **Needs you** says why on its banner: answer its question, sign an account in, restore files at
  risk, or follow up on a red gate.

## Documentation

- [Usage guide](docs/usage.md): everyday work, task by task.
- [Getting started](docs/getting-started.md): the first run, step by step.
- [Docs index](docs/README.md): setup, configuration, safety, GitHub, automations, the lead's reference (MCP tools,
  gates, team), the command line, the HTTP API, the task engine and the contributor guide.

## Contributing and security

Contributions are welcome: [CONTRIBUTING.md](CONTRIBUTING.md) covers the development setup, the checks a pull request
must pass and the commit style. Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md). Please report
security problems privately, as [SECURITY.md](SECURITY.md) describes, not in a public issue.

## License

Seelegent is source-available under the
[Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-ALv2)](LICENSE).

- You may use, copy, change and share it for any purpose other than a competing use, including inside a company,
  for non-commercial education and research, and in professional services you provide to someone who uses it.
- A competing use is making Seelegent available to others in a commercial product or service that substitutes for
  it (or for another product or service the licensor offers with it), or that offers the same or substantially
  similar functionality.
- Two years after each version is made available, that version is also available under the Apache License 2.0.

The [LICENSE](LICENSE) file has the exact terms. Third-party works shipped with Seelegent are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
