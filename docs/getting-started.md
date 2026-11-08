# Getting started

This guide takes you from nothing to a first task that a worker wrote, the lead reviewed and you accepted. It
assumes a lead host (Claude Code, Codex or another MCP host) and at least one worker CLI are already installed. The
[README](../README.md) has the short version.

## 1. Install

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

The installer:

1. reads the latest release from GitHub, downloads the archive for your OS and processor, and refuses to go on
   unless its SHA-256 matches the release's `checksums.txt`;
2. puts the binary at `%LOCALAPPDATA%\Programs\seelegent\bin\seelegent.exe` (Windows) or `~/.local/bin/seelegent`
   (Linux, macOS) and adds that folder to your PATH;
3. imports the old (Node) version's data on the first install, when it finds some
   ([section 8](#8-coming-from-the-old-version));
4. runs `seelegent setup` for every lead host it finds ([section 3](#3-connect-your-lead-hosts)).

Useful options: `--version v1.2.3` (`-Version` on Windows) installs a given release, `--install-dir DIR`
(`-InstallDir`) another folder, `--no-modify-path` (`-NoPath`) leaves PATH alone, `--no-setup` (`-NoSetup`) skips
setup. Each also works as an environment variable; see [release.md](release.md#installing). To pass options through
the one-liners:

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh -s -- --no-setup
```

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1))) -NoSetup
```

Open a new terminal so it sees the new PATH, then check the install:

```sh
seelegent version
seelegent doctor
```

`doctor` checks the data folder (`~/.seelegent`), the settings, the database, the daemon, git, each enabled worker
CLI, the accounts and your PATH. A `warn` is fine on a fresh install (no daemon running yet, no account yet); a
`fail` comes with a fix.

## 2. Accounts and sign-in

Open the dashboard:

```sh
seelegent ui
```

It starts the daemon when it is not running and opens http://127.0.0.1:47821/ in your browser (`--no-open` only
prints the address).

### Turn on the backends you want

The Antigravity CLI (`agy`) is on by default, with Gemini models for the four tiers. Claude Code (`claude`) and Codex
(`codex`) are off: open **Settings › Backends**, turn the backend on and give each tier (`fast`, `standard`,
`strong`, `max`) a model id. A tier without a model cannot be used on that backend. `allowedModels` limits which
model ids a worker may get. Every setting is described in [configuration.md](configuration.md).

### Add an account

On the **Accounts** page choose **Add account**. Step 1 asks for the backend and the kind:

| Kind | What it means |
|---|---|
| Own sign-in | A profile folder of its own under `~/.seelegent/profiles/<id>/`. You sign in once; the CLI keeps its tokens there. Add several to use several subscriptions of the same backend. |
| This computer's sign-in | Uses the sign-in `agy` already has in your home folder. Only for `agy`, and at most one such account (its id is `system`). |
| API key | Pays per token through an API provider. Only for `claude` and `codex`. Set the provider up first under **Settings › API providers** (presets: Anthropic, DeepSeek, Moonshot Kimi, Z.ai GLM, OpenRouter, OpenAI, Groq, a local Ollama, or a custom Anthropic- or OpenAI-compatible endpoint). |

Step 2 asks for a name, a priority (higher numbers are used first, −1000 to 1000) and, for an API key, the key. The
key can also be added later; afterwards only its last characters are shown.

### Sign in

For an account with its own sign-in, choose **Sign in**. Seelegent runs the worker CLI's own sign-in for that
profile. Depending on the CLI, the dialog asks you to finish in your browser (with an **Open sign-in page** button
and a link to copy), in a terminal window that opened, or to paste a code the sign-in page shows. The dialog updates
by itself when the sign-in is done. Seelegent never sees your password.

After a sign-in the account's quota is read (every 10 minutes after that). The account card shows its quota windows,
when they reset, a chip such as "1 of 2 slots busy", and its state: **Ready**, **Checking quota**, **Sign-in
required**, **No API key**, **Plan cannot use this backend**, **Off**, "Same sign-in as …" (signed in to the same
identity as an older account, which gets the work), and **Resting** or "Out of quota, back …" until a window resets.
The account strip on the **Watch** page uses shorter labels, such as **Busy** and **Duplicate**. Turn **Use for
work** off for an account you want to keep but not use for now.

## 3. Connect your lead hosts

`seelegent setup` registers the `seelegent` MCP server with every lead host it detects and installs the lead skill
where the host reads skills. The installer has already run it; run it again after installing a new host.

```sh
seelegent setup --dry-run      # show what would change
seelegent setup                # every detected host
seelegent setup --host claude,codex
seelegent setup --remove       # undo
```

| Host | `--host` id | What setup writes |
|---|---|---|
| Claude Code | `claude` | `claude mcp add --scope user seelegent ...` through Claude Code's own CLI; the skill in `~/.claude/skills/seelegent/` |
| Claude Desktop | `claude-desktop` | `claude_desktop_config.json` in Claude's settings folder |
| Codex CLI | `codex` | `[mcp_servers.seelegent]` in `~/.codex/config.toml`, with `tool_timeout_sec = 3600`; the skill in `~/.agents/skills/` |
| OpenCode | `opencode` | the `mcp` key of `opencode.json`; the skill in `~/.agents/skills/` |
| Antigravity CLI | `agy` | `~/.gemini/config/mcp_config.json`; the skill in `~/.gemini/config/skills/` |
| Antigravity (app) | `antigravity` | `~/.gemini/antigravity/mcp_config.json` |
| Gemini CLI | `gemini` | `~/.gemini/settings.json`, with a one-hour tool timeout |
| Cursor | `cursor` | `~/.cursor/mcp.json` |
| Windsurf | `windsurf` | `~/.codeium/windsurf/mcp_config.json` |

Every file is backed up to `<file>.seelegent.bak` before it changes, and setup edits only its own entry. Running it
again reports `unchanged`. A file it cannot edit safely (a JSON file with comments, say) is reported as `manual`, with
the snippet to add by hand. Claude Code users can add `--hooks` to install a reminder that nudges the lead to delegate
through Seelegent instead of starting its own coding subagents.

**Restart each host** afterwards so it starts the MCP server. In the host, the `seelegent` server should list 17
tools (`delegate`, `wait`, `followup`, `review`, ...). The details of every host are in [setup.md](setup.md).

A host without MCP can drive Seelegent through the command line: every tool except `profiles` has a `seelegent`
command ([cli.md](cli.md)); worker roles are edited on the **Roles** page.

## 4. A tour of the dashboard

The rail on the left (a bottom bar on phones) has eleven pages. `Ctrl+K` (`Cmd+K` on macOS) opens the command
palette, `Ctrl+,` the settings; each rail item's tooltip shows its own shortcut.

- **Watch** is the home page: what **needs you**, what is running (with the current step, tokens and changed files),
  what finished recently, and a strip of accounts with their state.
- **Tasks** lists every task with filters and search. Open one to see its header (status, account, model, actions),
  the banner that says why it needs attention, the gate and safety panels, and five views:
  - **Terminal**: the conversation, every tool call and the gate steps as they happen;
  - **Tree**: turns, their attempts and the steps of each (tool, file or command, duration);
  - **Graph**: the steps over time, one lane per stage, with a mark where a turn or a hand-over to another account
    starts;
  - **Files**: every file the worker read or edited, grouped by folder;
  - **Git**: what the task committed since it started (its commits, each with its own diff, or the files they
    changed against the start), its uncommitted changes file by file with review notes on lines, the changes made
    outside the edit tools, a **Commit…** dialog that commits only this task's uncommitted files, and the rest of
    the repository: other uncommitted files and how far the branch is ahead of or behind its upstream. Commits made
    while none of the task's turns ran (someone else's, or between turns) are listed apart under **Other commits
    since the task started**, and their files are not counted as the task's. Commits are matched to turns by time,
    so in a repository where several tasks commit at once, another task's commit during a turn is shown as that
    turn's. A notice says when the branch changed since the start, the start can no longer be found, or the commits
    could not be read this time.

  From a task you can **Follow up**, **Accept** or **Reject**, **Cancel task**, or **Take over**: the worker's
  conversation opens in a console on the same account; when you close it the worker writes a hand-back report.
- **Issues**, **Meetings**, **Contests**, **Automations** and **Proof** hold the GitHub flow, the team features and
  the gate records ([github.md](github.md), [team.md](team.md), [automations.md](automations.md),
  [gates.md](gates.md)).
- **Accounts** manages accounts and sign-ins; **Usage** shows tokens, cost, tasks and turns for the last 7 or 30 days,
  the top projects and accounts, and each account's quota over time.
- **Roles** are worker roles (rules, output style, skills, default tier) per project or global; **Members** are the
  department's named workers.
- **Settings** is a dialog with search. Appearance (26 themes, fonts, sizes, motion) and Language are kept in this
  browser. Workers, Backends, Quota, Gates, Workspace safety and Service edit the daemon's `config.json` and save as
  you change them. API providers, Hooks and Memory are kept in the daemon's database; Hooks and Memory have their own
  **Save** button. Notifications, Keyboard shortcuts and Experiments show "Not available yet".

The dashboard speaks English, Tiếng Việt, 简体中文, Español, 한국어, 日本語 and Русский (**Settings › Language**).

## 5. The first task

Open your lead host in a project folder that is a git repository and ask for work the way you would ask a
colleague, mentioning Seelegent:

> Use Seelegent to add a `--verbose` flag to the CLI that prints each step. Have a worker write it, then check the
> diff and run the tests before you accept it.

The lead skill tells the lead what to do next:

1. `delegate` with a brief written like a ticket (goal, files, constraints, acceptance criteria, how to verify) and
   the absolute project folder as `cwd`. The task appears on **Watch** and **Tasks** at once.
2. The daemon picks an account (priority first, then the fewest running tasks, then the most quota left), writes the
   full brief (the lead's text, the project memory, the review history, the files that already had uncommitted
   changes) and runs the worker CLI once, non-interactively.
3. When the worker answers, the daemon runs the post-turn checks and the gate. A red gate goes back to the worker as
   an automatic fix turn, twice at most by default.
4. The lead gets the report: the status, the worker's answer, the changed files, the gate result and, when the task
   needs attention, why.

You can do the same from a terminal:

```sh
seelegent delegate --cwd . --model fast --gate-detect "Add a --verbose flag to the CLI that prints each step"
seelegent ls --active
seelegent task <id> --steps 20
```

`--wait 0` returns at once with the task id; `seelegent wait <id>` waits later.

A good first brief is small: one file or one function, with the command that proves it works. Use the `fast` tier
for lookups and mechanical edits, `standard` for most work, and `strong` or `max` for hard, multi-file changes.

## 6. Follow up and review

A task stays open until it gets a verdict. Read the diff (the **Git** view, or the lead reads it), run the tests, then:

- **Follow up** with concrete fixes. The same worker continues its conversation, on the same account when it still
  has quota, or on another account with a handover of what happened so far.
  - From the lead: the `followup` tool.
  - From the dashboard: the **Follow up** box under the task (you can pick another tier or model for that turn).
  - From a terminal: `seelegent followup <id> "Also handle the empty input case"`.
- **Leave review notes** on lines of the diff in the **Git** view, then send them all as one followup. Each note
  follows its line across turns and shows whether the next turn addressed it.
- **Accept** or **reject** the task. The verdict and its notes go into the project's history, which later briefs
  show to workers. From the lead: `review`; from a terminal: `seelegent review <id> accepted --notes "..."`.

Accepting releases the task's lane, so other tasks may claim those files. Accepting does not commit anything (only
the GitHub flow commits, inside ticket worktrees): commit the accepted work yourself, or with the **Commit…** dialog
of the Git view, which commits only the files you tick.

To keep facts every worker should know (commands, conventions, pitfalls), add them to the project's memory: the
`memory` tool, `seelegent memory append --cwd . "..."`, or **Settings › Memory**.

## 7. When something is stuck

Tasks that need someone are at the top of **Watch** and in the **Needs you** filter of **Tasks**, with a banner that
says why:

| Banner | What to do |
|---|---|
| Has a question | The worker ended with a `## Blocked` question. Answer it with a followup. |
| Stalled, Looping, Worker not responding | Look at the **Terminal** view. Follow up with a hint, or cancel and delegate a smaller brief. |
| Needs sign-in | Every account of the backend is signed out: sign one in on **Accounts**. |
| No accounts left | No enabled account can run the task (signed out, out of quota, the model not allowed). Add or turn on an account, or wait for a reset. |
| Waiting for quota | Not a problem: the task starts by itself when an account's window resets. |
| Gate failed | The automatic fix turns are used up. Read the failing steps in the gate panel, then follow up or reject. |
| Hygiene problem | The turn changed line endings or a BOM, or left a stray file. Follow up to fix it. |
| Work may be lost | The turn may have lost uncommitted lines. Check the files in the safety panel and **Restore** them from the snapshot taken before the turn. |
| Review rejected it | The second-opinion verifier rejected the change; its reasons are in the gate panel. |
| A peer objects | A peer member objected in a consult. Answer the objection, or reject. |
| Earlier task failed | A task this one waits for failed or was rejected. Follow up with `force` to start it anyway, or cancel it. |

More places to look:

- `seelegent status` shows the daemon, the backends, each account's quota and the tasks that need you.
- `seelegent task <id> --steps 30` prints the last worker steps; `--full` the whole task.
- The daemon's log is `~/.seelegent/logs/daemon.log` (JSON lines, secrets masked). `seelegent restart` restarts the
  daemon; running workers keep running and are picked up again.
- `seelegent doctor` after any change to your setup. If the lead says the daemon cannot be reached, `doctor` says
  what kind of thing holds the port (another program, a Seelegent daemon of another data folder, or an older
  Seelegent build), with its pid when it is a Seelegent daemon.
- A lead whose `wait` times out loses nothing: the task keeps running, and `wait` picks it up again.
- `seelegent cancel <id>` stops a task and its worker.

## 8. Coming from the old version

The old Node version used the same data folder (`~/.seelegent`) and the same port. Stop its daemon before you install
(`seelegent stop` while the old version is still installed). On the first install the installer runs
`seelegent migrate`, which imports settings, providers, accounts with their profiles and API keys, project memory and
review history, roles, members, hooks, meeting records and automations. Every imported automation is off: turn
each one on again on the **Automations** page after reading what it runs. Tasks, review notes, contests, proof
records, usage history and the GitHub folder are not imported; connect your repositories again on **Issues** and
store the GitHub token again there.

`seelegent migrate --dry-run` shows what would be imported, and running `migrate` again is safe: it reports
`unchanged` for what it imported before and never overwrites data the new version already has. The old folder's
files are never changed. On Windows, if the old launcher is in the install folder, it is kept as `seelegent-node`.
See [setup.md](setup.md#seelegent-migrate).

## Next

- [README](../README.md): the feature overview and the safety model.
- [Docs index](README.md): every reference page.
