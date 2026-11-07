# Seelegent

**English** · [Tiếng Việt](README.vi.md) · [简体中文](README.zh-CN.md) · [Español](README.es.md) ·
[한국어](README.ko.md) · [日本語](README.ja.md) · [Русский](README.ru.md)

Seelegent lets one lead coding agent hand work to worker coding CLIs that run on your own accounts. You talk to the
lead (Claude Code, Codex, Antigravity, OpenCode or another MCP host); the lead designs the work, writes a brief and
delegates it over MCP; Seelegent runs it as a normal non-interactive CLI call (`agy`, `claude` or `codex`) on the
account with the most quota left, checks the result, and hands the report back for review. It is for people who
have several subscriptions or API keys and want one agent to plan and review while the others write the code.

Seelegent is not a proxy and does not pool accounts behind one endpoint. Every run is an ordinary CLI call on an
account you own, in its own isolated profile, and each provider's terms still apply to each account.

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
- **The dashboard** (http://127.0.0.1:47821/) shows what needs you, what runs, every task's steps and diff, accounts
  and quota, and everything below.

## Requirements

- **OS:** Windows 10 or 11, Linux or macOS, on x64 or ARM64. Windows is the most tested platform.
- **Git.** Seelegent needs it for snapshots, the Git view, contests and GitHub tickets.
- **At least one worker CLI**, installed: the Antigravity CLI (`agy`, Gemini models, the default backend), Claude
  Code (`claude`) or Codex (`codex`). The `claude` and `codex` backends are off until you turn them on in Settings.
- **A lead host** with MCP: Claude Code, Claude Desktop, Codex CLI, OpenCode, the Antigravity CLI or app, Gemini CLI,
  Cursor or Windsurf. A host without MCP can use the `seelegent` command instead.
- Optional: the GitHub CLI (`gh`), signed in, for the GitHub features.

## Install

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

The installer downloads the latest release, checks its SHA-256 against the release's `checksums.txt`, puts
`seelegent` on your PATH (`%LOCALAPPDATA%\Programs\seelegent\bin` on Windows, `~/.local/bin` elsewhere), imports the
old version's data if it finds any, and runs `seelegent setup` for the lead hosts on this machine. Open a new
terminal and restart your lead hosts afterwards. Options such as `--version`, `--install-dir` and `--no-setup` are in
[docs/release.md](docs/release.md#installing).

## First steps

1. **Check the install:** `seelegent doctor`.
2. **Open the dashboard:** `seelegent ui` starts the daemon if needed and opens http://127.0.0.1:47821/.
3. **Add an account** on the **Accounts** page: pick the backend and the kind (its own sign-in, this computer's
   sign-in, or an API key through a provider). Sign-in runs the worker CLI's own sign-in, in your browser or in a
   terminal window; Seelegent never sees your password. To use `claude` or `codex`, turn the backend on and set its
   tier models under **Settings › Backends** first.
4. **Connect your lead host** (the installer already did this): `seelegent setup` adds the `seelegent` MCP server
   to every lead host it finds and installs the lead skill where the host reads skills. `--host claude,codex` picks
   hosts, `--dry-run` shows the changes first, `--remove` undoes them. Restart the host.
5. **Delegate.** In a project folder, ask your lead something like *"Use Seelegent to add input validation to the
   signup form, then review the diff."* The lead calls `delegate`, waits for the report, reads the diff, runs the
   tests, and then sends a `followup` or closes the task with `review`. From a terminal:
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`.

[docs/getting-started.md](docs/getting-started.md) walks through each step in detail.

## Features

**Workers and accounts.** Three backends (`agy`, `claude`, `codex`) and three kinds of account: an isolated profile
with its own sign-in, the sign-in already on this computer, or an API key of a provider (Anthropic, DeepSeek, Moonshot
Kimi, Z.ai GLM, OpenRouter, OpenAI, Groq, a local Ollama, or a custom endpoint). The pool reads each account's quota
windows and prefers higher priority, fewer running tasks and more quota left. Model tiers `fast`, `standard`,
`strong` and `max` map to real models per backend. See [configuration](docs/configuration.md).

**Tasks.** A task is a conversation: `delegate`, then `followup` as often as needed, then `review` with `accepted`
or `rejected`. Tasks run in parallel, can wait for each other (`after`), get the project's memory and review history
in every brief, can run as a worker role (rules, output style, skills, default tier), and can take a Spec Kit feature
folder and task ids instead of the whole spec. Tasks that stall, loop or ask a question wake the lead early, and tasks
that need you (a sign-in, no account left) go to the top of the dashboard. You can take a task over in a console
and hand it back. See [task engine](docs/task-engine.md).

**Gates, lanes and proof.** After every worker report the daemon runs the task's gate: the lead's commands, the
spec's `Gate:` lines or the repository's detected test, lint and build commands, the project doctrine, syntax checks
and the hygiene check. A red gate goes back to the worker as an automatic fix turn (2 by default) before the lead is
woken. Lanes list the files a task may change, so parallel workers stay off each other's files. An optional verifier
reviews a green gate read-only. The Proof page shows which Spec Kit requirements are proven and runs regression
runs. See [gates](docs/gates.md).

**Team.** Members are named workers with a backend, a model or tier, a role and a rank (`worker`, `senior`,
`peer`). Ask a senior or peer for a second opinion on a finished task (a peer's objection blocks acceptance until
the lead answers it), leave review notes on diff lines and send them as one followup, hold a meeting of up to six
members before a hard decision (a peer may record a formal dissent), or run one brief as a contest of 2 to 5
variants in separate worktrees and apply the winner. Event hooks run your own shell commands when tasks start,
finish or need you. See [team](docs/team.md).

**GitHub.** Connect a repository, sync its open issues into an inbox (or add local ones), and triage each one into a
ticket: its own worktree and branch, worked by one or more tasks. Once the work is accepted, you or the lead ask
Seelegent to commit, push the ticket branch and open a draft pull request; it then follows the CI checks and can
start a task to fix failing ones. It uses `gh` when it is signed in, else a token you store in the dashboard, else
a manual mode with a compare link. See [GitHub](docs/github.md).

**Automations.** Saved delegations on a schedule (hourly, daily, weekdays, weekly or cron, in a time zone), with a
precheck command and quota rules: a run is skipped when quota is low, and an optional extra run uses a weekly quota that
is about to reset unused. See [automations](docs/automations.md).

**Dashboard.** Pages for Watch, Tasks, Issues, Meetings, Contests, Automations, Proof, Accounts, Usage, Roles and
Members, plus a searchable Settings dialog. Each task has Terminal, Tree, Graph, Files and Git views. 26 colour
themes, seven interface languages, a command palette (`Ctrl+K`, `Cmd+K` on macOS) and keyboard shortcuts. It is
embedded in the binary and loads nothing from the internet.

**Command line.** Every MCP tool has a command, and every command takes `--json`: `seelegent --help`, and
[docs/cli.md](docs/cli.md).

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
  `reset`, `restore`, `stash`, `checkout` and `switch` (creating a branch is fine), `clean`, `rebase`, `merge`,
  `pull`, `push`, `commit -a` and `--amend`, and more, also inside `bash -c`, PowerShell, pipes into a shell or git
  aliases. The lead can lift it for one turn with `allow_git`.
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

These are guards, not a sandbox: worker CLIs run as your user. Review what they changed.

## Data and privacy

- Everything runs on your machine. The daemon listens on `127.0.0.1` only. There is no telemetry.
- Data lives in `~/.seelegent` (set `SEELEGENT_HOME` to move it): `config.json` (settings), `seelegent.db` (tasks,
  accounts, usage, team, tickets, automations), `profiles/` (one isolated profile per account), `snapshots/`,
  `worktrees/` and `logs/`. See [docs/configuration.md](docs/configuration.md).
- Seelegent never asks for passwords. Each worker CLI signs in with its own flow and keeps its tokens in the account's
  profile. API keys and the GitHub token are each stored in their own file, encrypted with DPAPI on Windows and as
  owner-only (0600) plain text elsewhere; the dashboard shows only their last four characters, and logs mask secrets.
- What leaves the machine: the worker CLIs talk to their own providers (that is the work), quota is read through the
  CLIs (and, for Claude sign-ins, from Anthropic's usage endpoint with that account's own sign-in), API-key accounts
  call the provider you configured, and GitHub is called only for repositories you connect (syncing, a poll of
  their issues and pull requests every 10 minutes, which `githubPollMinutes: 0` turns off, and the pushes, pull
  requests and comments you or the lead ask for) and by the installers and `seelegent update`.

## Update and uninstall

`seelegent update` tells you whether a newer release exists; `seelegent update --apply` installs it in place (checked
against `checksums.txt`, daemon restarted, lead hosts refreshed) and `seelegent update --rollback` goes back once.
The database is never migrated backwards, so a release older than your data is refused. See
[docs/release.md](docs/release.md#updating).

To uninstall:

1. `seelegent stop`, then `seelegent setup --remove` (removes the MCP entries, the lead skill and the hook).
2. Delete the binary and the kept `seelegent.previous` next to it: `%LOCALAPPDATA%\Programs\seelegent\bin\` on
   Windows, `~/.local/bin/seelegent*` elsewhere. Remove that folder from your PATH (the user `Path` on Windows, the
   `export PATH=...` line the installer added to your shell profile elsewhere).
3. Optionally delete `~/.seelegent`. It holds your account profiles (and their sign-ins), keys, tasks and history.

## Migrating from the old version

The old Node version kept its data in the same folder. On the first install the installer runs `seelegent migrate`,
which imports settings, providers, accounts with their profiles and API keys, project memory and review history,
roles, members, hooks, meeting records and automations (every automation off, to confirm again). Tasks, review
notes, contests, proof records, usage history and the GitHub folder are not imported: connect your repositories and
store the token again. The old folder is never changed; the old launcher is kept as `seelegent-node` on Windows.
Stop the old daemon before you install, and run `seelegent migrate --dry-run` to see what would be imported. See
[docs/setup.md](docs/setup.md#seelegent-migrate).

## Troubleshooting

- `seelegent doctor` checks the data folder, settings, database, daemon, git, each enabled worker CLI, accounts and
  PATH, and says how to fix what fails. It never changes anything.
- `seelegent status` shows the daemon, accounts with quota and the tasks that need you; `seelegent restart` restarts
  the daemon. Its log is `~/.seelegent/logs/daemon.log`.
- `seelegent setup --dry-run` shows what is registered with each lead host. Restart a host after setup or update.
- "Seelegent is not available inside a Seelegent worker." means a worker tried to call Seelegent; that is refused
  on purpose.
- A task stuck in **Needs you** says why on its banner: answer its question, sign an account in, restore files at
  risk, or follow up on a red gate.

## Documentation

All docs are listed in [docs/README.md](docs/README.md): a getting-started guide for users, the lead's reference,
the command line, the HTTP API, and the contributor guide.

## License

Seelegent is source-available under the
[Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-ALv2)](LICENSE).

- You may use, change and share it for any purpose, including inside a company, and build and sell your own products
  with it.
- You may not offer Seelegent itself, or a changed copy, to others as a commercial product or service that competes
  with it.
- Two years after each version is released, that version is also available under the Apache License 2.0.

The [LICENSE](LICENSE) file has the exact terms. Third-party works shipped with Seelegent are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
