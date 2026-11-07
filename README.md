# Seelegent

Seelegent lets a lead coding agent (Claude Code, Codex, Antigravity and other MCP hosts) hand tasks to worker
coding CLIs running on your own accounts. It is a single local binary: a daemon that schedules and tracks the
work, a CLI, an MCP server for the lead agent, and an embedded web dashboard that shows tasks, accounts and
quota in real time.

> **Work in progress:** Go + Vue rewrite.

## Install

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

The installer downloads the latest release, checks its SHA-256, puts `seelegent` on your PATH, imports the data of
the old version if there is any, and registers Seelegent with the lead hosts it finds (`seelegent setup`). Restart
your lead host afterwards. `seelegent update` installs newer releases; see [docs/release.md](docs/release.md) for the
options.

## License

[FSL-1.1-ALv2](LICENSE) (Functional Source License, Version 1.1, with Apache 2.0 future license).
