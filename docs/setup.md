# Setup and migration

Two commands connect Seelegent to the machine and to the data of the old (Node) version:

- `seelegent setup` registers the `seelegent mcp` server with the lead hosts, installs the lead skill and, on
  request, the Claude Code reminder hook.
- `seelegent migrate` imports the old version's settings, accounts, memory, history, roles, members, hooks and
  meetings.

Both refuse to run inside a Seelegent worker (`SEELEGENT_WORKER=1`), with the command line's worker guard: exit 1,
code `forbidden`, `details.caller: "worker"` ([cli.md](cli.md)). `seelegent skill` prints the lead skill and stays
allowed. `--json` is the global flag of the command line; extra arguments are a usage error (exit 2).

## seelegent setup

```text
seelegent setup [--host a,b] [--dry-run] [--remove] [--no-skill] [--hooks] [--force] [--exe path] [--json]
```

| Flag | Meaning |
|---|---|
| `--host` | Only these hosts (ids below). Without it, every detected host. A named host that is not installed is reported as `skipped`. |
| `--dry-run` | Report every action without writing a file or running a host CLI. |
| `--remove` | Remove what setup added: the MCP entry, the skill and the hook. An entry or skill that is not Seelegent's is left alone. |
| `--no-skill` | Leave the skill alone. |
| `--hooks` | Also install the Claude Code reminder hook. `--remove` always removes it. |
| `--force` | Replace an entry or skill named `seelegent` that another program wrote (the old one is backed up first). |
| `--exe` | The executable the hosts start. Default: the running `seelegent`, with symbolic links resolved. It must be named `seelegent` (`seelegent.exe` on Windows), because setup recognizes its own entries by that name: with any other name (a `seelegent-dev.exe` build, a test binary) setup refuses before writing anything, a dry run too, and says to install or copy the binary as `seelegent` or to pass `--exe`. `--remove` is not checked. |
| `--json` | Print the report as JSON (`setup.Report`). When an item failed, the report is the only document printed (exit 1). |

Each host gets the entry `<exe> mcp` under the name `seelegent`. The entry's environment carries
`SEELEGENT_LEAD`, which tells the MCP server which lead host started it. Its value is one the server knows: `claude`
(Claude Code), `codex`, `opencode`, `agy` (Antigravity CLI and the Antigravity app) and `gemini` (Gemini CLI). Claude
Desktop, Cursor and Windsurf have no value of their own, so their entries get no `SEELEGENT_LEAD`. The server then
detects the host from its session environment or reports the lead as `cli`. Every host's format has a per-server
environment (`env`, or `environment` for OpenCode). The values come from the server's list of leads, `api.Leads()`.
The executable must be a stable path that updates replace in place. Setup warns when it is a `go run` build or sits
in a `versions/` folder (old C-B1: entries that pointed at a versioned `cli.js` broke after the second update).

### Report

Every host and item (`mcp`, `skill`, `hook`, and on remove `folder`) gets one line with an action:

| Action | Meaning |
|---|---|
| `added`, `updated`, `removed` | The change was made (in a dry run: would be). |
| `unchanged` | Already as wanted; nothing was written. A second run reports this everywhere. |
| `skipped` | Nothing to do: the host is not installed, nothing to remove, the entry there is not Seelegent's (on remove), or the host has no skill folder. |
| `manual` | Setup cannot edit the file safely; `snippet` says what to add by hand (a JSON file with comments, no `claude` CLI on PATH, a Codex layout setup does not edit). |
| `conflict` | An entry or skill named `seelegent` belongs to another program. Nothing was written. Use `--force` or rename it. |
| `error` | The item failed; nothing was written for it. |

The exit code is 1 when any item is `conflict` or `error`. Other items still run.

### What setup changes

- Seelegent's entry is recognised by its command: a file named `seelegent` (or `seelegent.exe`, `.cmd`, `.bat`) with
  the single argument `mcp`, or the old version's `node …seelegent…/cli.js mcp`. Anything else under the name
  `seelegent` is foreign.
- An existing Seelegent entry is updated in place. Its other keys and any extra environment variables (for example
  `SEELEGENT_HOME`) are kept. A `"disabled": true` is turned off.
- Every file is backed up to `<file>.seelegent.bak` before it changes. The suffix is not `.bak` because Codex keeps its
  own `config.toml.bak`. Writes are atomic. A config file that is a symbolic link is written through the link.
- JSON files are edited by splicing: setup replaces, inserts or deletes only the bytes of the `seelegent` member (or
  of the reminder hook in `settings.json`), so every other byte stays, including one-line objects of other servers,
  key order, number spelling, the BOM, line endings and the final newline. New lines follow the file's indentation
  and line ends; in a one-line file the entry is written on one line. A file with comments or trailing commas (JSONC)
  is never rewritten: the item is `manual`.
- Codex's `config.toml` is edited line by line. Only the lines of `[mcp_servers.seelegent]` change; comments, other
  tables, CRLF line ends, a BOM and a missing final newline stay. New tables go between the old version's markers
  (`# >>> seelegent …` / `# <<< seelegent <<<`). Setup refuses layouts it cannot edit safely: the table twice,
  sub-tables such as `[mcp_servers.seelegent.env]`, dotted keys or inline tables that define `mcp_servers.seelegent`
  (old C-B14). The item is then `error`, and `manual` on remove.
- Claude Code is changed only through its own CLI (`claude mcp add seelegent --scope user -e SEELEGENT_LEAD=claude --
  <exe> mcp`, and `claude mcp remove --scope user seelegent`), because Claude Code rewrites `~/.claude.json` while it
  runs. Setup reads the file before and after, backs it up first, and checks that the entry appeared. Without the CLI
  the item is `manual` with the command to run. An update runs `remove` and then `add`; when the `add` fails, setup
  adds the previous entry back with the CLI. If that fails too, it copies the backup over `~/.claude.json` only when
  the file is still exactly what the `remove` left (nothing Claude Code wrote since is lost); otherwise the error names
  the backup to restore by hand.
- Setup records the host files and folders it creates (they did not exist before) in `setup-created.json` in the
  data folder. `--remove` deletes such a file once it holds nothing but an empty server list (for example
  `{"mcpServers": {}}`, an empty `settings.json`, or a `config.toml` with only white space), together with its
  `.seelegent.bak` when that holds nothing else either. It then deletes the recorded folders that are empty, deepest
  first, reported as `folder` items with host `setup` (for example `~/.agents/skills` and `~/.claude/skills`). Files
  and folders that existed before setup are never deleted; a recorded file the user has added to stays, and stays
  recorded. A dry run reports the folders that are empty already, but not the files it would delete. An unreadable
  `setup-created.json` stops the run: fix or delete it.

### Hosts

| Id | Host | Detected by | MCP config | Skill folder |
|---|---|---|---|---|
| `claude` | Claude Code | `claude` on PATH, `~/.claude.json` or `~/.claude/` | via the `claude` CLI; read from `$CLAUDE_CONFIG_DIR/.claude.json`, else `~/.claude/.config.json` if present, else `~/.claude.json` | `~/.claude/skills` (`$CLAUDE_CONFIG_DIR/skills`) |
| `claude-desktop` | Claude Desktop | its settings folder | `claude_desktop_config.json` in `%APPDATA%\Claude` (Windows; the Store build's `%LOCALAPPDATA%\Packages\Claude_*\LocalCache\Roaming\Claude` when it holds the file), `~/Library/Application Support/Claude` (macOS), `$XDG_CONFIG_HOME/Claude` (Linux) | none |
| `codex` | Codex CLI | `codex` on PATH or `~/.codex/` | `$CODEX_HOME/config.toml` (default `~/.codex/config.toml`), with `tool_timeout_sec = 3600` | `~/.agents/skills` |
| `opencode` | OpenCode | `opencode` on PATH or its config folder | `$XDG_CONFIG_HOME/opencode/opencode.json` (`.jsonc` when only that exists), key `mcp`, `{"type": "local", "command": [exe, "mcp"], "environment": {…}, "enabled": true}` | `~/.agents/skills` |
| `agy` | Antigravity CLI | `agy` on PATH, `$SEELEGENT_AGY`, `%LOCALAPPDATA%\agy\bin\agy.exe`, a Store app's private copy, or `~/.gemini/antigravity-cli/` | `~/.gemini/config/mcp_config.json` | `~/.gemini/config/skills` |
| `antigravity` | Antigravity (desktop app) | `~/.gemini/antigravity/` | `~/.gemini/antigravity/mcp_config.json` | none |
| `gemini` | Gemini CLI | `gemini` on PATH | `~/.gemini/settings.json`, with `"timeout": 3600000` | none |
| `cursor` | Cursor | `~/.cursor/` | `~/.cursor/mcp.json` | none |
| `windsurf` | Windsurf | `~/.codeium/windsurf/` | `~/.codeium/windsurf/mcp_config.json` | none |

Codex and OpenCode share `~/.agents/skills`: the skill is written once. On remove it stays while another detected host
that is not being removed still uses that folder.

How sure each entry is:

- **Certain** (used by the old version, or checked on this machine): Claude Code (CLI and `~/.claude.json`), Codex
  `config.toml` and `tool_timeout_sec`, the Antigravity CLI's `~/.gemini/config/mcp_config.json`, the skill folders of
  Claude Code, Codex/OpenCode and agy, and Claude Desktop's `%APPDATA%\Claude\claude_desktop_config.json`.
- **From the hosts' documentation, not run here**: OpenCode's `mcp` format and folder, Gemini CLI's `settings.json`
  and its `timeout` key (milliseconds), Cursor's `~/.cursor/mcp.json`, Windsurf's `~/.codeium/windsurf/mcp_config.json`,
  and Claude Desktop on macOS.
- **Uncertain**: the Antigravity desktop app's `~/.gemini/antigravity/mcp_config.json` (the file exists on this
  machine, but which build reads it is not confirmed), Claude Desktop on Linux (there is no official Linux build), and
  where the Store build of Claude Desktop reads its config when both copies exist. Tool timeouts: only Codex and
  Gemini CLI have a documented per-server key. Long `wait` calls may still hit a host-side limit in the other hosts
  (old C-B5); the lead can keep `wait_seconds` lower there.

### Skill

The skill is rendered from the template embedded in the binary (`internal/setup/skill/SKILL.md.tmpl`; the golden copy
is `internal/setup/testdata/SKILL.md.golden`) and written to `<skill folder>/seelegent/SKILL.md`. It teaches only the
tools and options that work in this version. The tools and options that answer `not_available` are named once, as
such (old F10). The front matter carries `metadata.generator: seelegent`. Setup updates only a skill with that mark
and treats any other `SKILL.md` there as a conflict. `seelegent skill [--json]` prints the same text.

The template lives in the setup package rather than a top-level `skill/` folder because `go:embed` cannot reach
outside the package directory.

### Reminder hook (`--hooks`)

The old version's hook (section 5.7) printed a Claude Code hook config in a shape Claude Code does not accept (old
C-B2). This version installs it into `~/.claude/settings.json` in the documented shape:

```json
{"hooks": {"PreToolUse": [{"matcher": "Agent", "hooks": [{"type": "command", "command": "\"<exe>\" hook claude-agent"}]}]}}
```

`seelegent hook claude-agent` reads the hook input and, before the Agent tool starts a subagent that could write code,
adds a reminder to the lead's context to delegate through Seelegent instead. It never blocks the tool. It stays
silent for read-only subagents (Explore, Plan, claude-code-guide, statusline-setup) and for input it cannot read.
Other parts of the old 5.7 hooks (lead hooks on task events) belong to the daemon and are not part of setup.

## seelegent migrate

```text
seelegent migrate [--from <old dir>] [--dry-run] [--json]
```

The old version kept its data in `$SEELEGENT_HOME` or `~/.seelegent`, the same folder this version uses, so `--from`
defaults to the data folder. Stop the daemon first; migrate takes the daemon's lock and refuses while it runs.

What is imported:

| Old data | Becomes |
|---|---|
| `settings.json` | `config.json`. The old top-level `tiers` and `allowedModels` win for agy, as they did in the old version. A value this version refuses is reset to its default, and the report names it. Keys this version does not know are not copied; the old file keeps them. An empty `allowedModels` for codex or claude allowed no model in the old version; it becomes the pattern of the backend's tier models (`^$` without tiers). |
| `settings.json` `providers` | Providers in the database, same ids. `label` becomes the name and `harness` the backend; an unknown preset is dropped. An empty `allowedModels` (no model allowed in the old version) becomes the tier models' pattern. |
| `accounts.json` | Accounts with the same ids (`acc<N>`, `system`) and creation times. The id counter moves past them. OpenCode and `removed` accounts are skipped. `auth: apikey` becomes `api-key`. Labels are cleaned and priorities clamped to the allowed range. An account of this version that already uses the id is the old one (`unchanged`, imported by an earlier run) only when backend, kind, provider and creation time match (the label instead, when the old record has no time); otherwise both are left as they are and the old one is reported `kept` with the reason: add it again and sign it in. |
| `profiles/<id>/home` | Used in place when the old and new data folders are the same. Otherwise it is copied without links, special files and the old key file, and the `linkIntoProfiles` entries are linked again. An existing target folder is kept. The copy goes into `<home>.migrating` and is renamed into place when complete, so a copy that fails halfway leaves nothing a later run would take for a full profile. |
| `profiles/<id>/home/seelegent-key.json` | The API key goes into this version's key store (DPAPI on Windows), and the account gets its hint. The key is never printed; the report shows only the hint. The old plain-text file stays where it is until you delete it. |
| `memory/<key>.md` | The project's notes, without the old header. Notes over the 64 KB cap are an error for that project. |
| `memory/<key>.history.md` | Review history with the original minute. A verdict already recorded at the same time for the same task is not added twice. |
| `roles.json`, `memory/<key>.roles.json` | Global and project roles. A role that does not validate is an error for that role only. |
| `members.json` | Department members with the same ids. An OpenCode member is skipped and `opencode` is dropped from the fallback backends. A member whose id exists is `unchanged` when it is the same and `kept` otherwise. |
| `hooks.json` | Lead hooks, added after the existing ones and given ids. `on` may be one event or a list; `enabled` defaults to true as before. A hook with the same command, events and project as an existing one is not added again. An unknown event is an error for that hook only. |
| `meetings/<id>.json` | Meeting records: options, rounds, answers, questions, decision, dissents with their watched files and signals, and usage, so the dissent memory and the members' track records go on. A meeting that was still running is imported as `cancelled`. The minutes are written again in English from the record; the old `regression` signals are dropped (D19). The `.jsonl` event logs are not imported. |
| `automations.json` | Automations with the same ids and creation times, **every one off** with no enable request, whatever it was before (old bug D8): turn each on from the dashboard after reading what it runs ([automations.md](automations.md)). `gate: false` becomes no gate, a gate command or list becomes the gate commands, `lane: false` becomes no lane. An automation whose folder no longer exists is an error for that automation only. One whose id exists is `unchanged` when it does the same and `kept` otherwise. The run history (`automations/<id>.jsonl`) is not imported. |

Project keys (old F6): the old key is `<base name>-<8 hex digits of SHA-1>` of the lower-cased absolute path. This
version keys projects differently, so migrate rebuilds the old key from every folder the old data records: the memory
header `<!-- cwd: … -->`, the `cwd` of a roles file, and the `cwd` and `projectCwd` of every old task. It accepts a
folder only when the rebuilt key matches. Notes whose folder is not recorded anywhere are skipped (a file written by
`memory replace` has no header). So are notes whose folder no longer exists.

Not imported, reported as `skipped`: tasks (their worker conversations cannot continue here, and the old
`tasks/<id>.json` files stay), the review notes of those tasks, contests (their variants ran in the old daemon's
worktrees), the run history of automations, the GitHub folder (connected repositories, issues, tickets and the
token: connect the repositories again and store the token again on the Issues page), proof records, usage history
and quota caches.

Rules:

- The old folder is never changed or deleted. When it is the data folder, migrate only adds this version's files
  (`config.json`, `seelegent.db`, `profiles/<id>/seelegent-key`).
- `--dry-run` writes nothing. It runs against a temporary copy of the database and reports what would be imported.
- A second run reports `unchanged` for everything imported before. Data this version already has under the same name
  and with a different value is `kept`, never overwritten. Settings count as already there when `config.json` differs
  from the defaults.
- Each file, account, project and role is its own item, so an unreadable piece is reported and the rest goes on. The
  exit code is 1 when any item is `error`.

## Bugs of the old version covered here

- C-B1: setup registers the stable executable, never a versioned path or `node cli.js`, and warns about unstable
  paths. `seelegent update` replaces that executable in place and reruns setup for the hosts that have an entry
  ([release.md](release.md)).
- C-B2: the hook is installed in the shape Claude Code accepts.
- C-B5: `tool_timeout_sec = 3600` for Codex and `timeout: 3600000` for Gemini CLI. The other hosts have no known key.
- C-B12: setup and migrate refuse to run inside a worker.
- C-B14: inline-table and dotted forms of `mcp_servers.seelegent` are detected and refused. Every write is atomic with a
  backup.
- C-B7 (BOM in `current.txt`): the installer writes no version file at all and is plain ASCII
  ([release.md](release.md)). Setup itself keeps a BOM it finds in a host config and never adds one.
- F6 and F10: see above.
