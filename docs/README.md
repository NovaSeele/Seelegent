# Seelegent documentation

Start with the [README](../README.md) for what Seelegent is, then [getting-started.md](getting-started.md) for a
first run and the [usage guide](usage.md) for everyday work. The other pages describe what the current version
does, one area each.

## For users

- [Getting started](getting-started.md): install, accounts and sign-in, connecting the lead hosts, a dashboard tour,
  the first task, followup and review, and what to do when a task is stuck.
- [Usage guide](usage.md): everyday work, task by task: accounts, delegating, the dashboard, quota, gates, the git
  guard, the team, GitHub tickets, automations, resources, troubleshooting, update and uninstall.
- [Setup and migration](setup.md): what `seelegent setup` writes for each lead host, the lead skill and reminder
  hook, and what `seelegent migrate` imports from the old version.
- [Installation, updates and releases](release.md): what the installers do, their options, `seelegent update` and
  rollback, and why a database is never migrated backwards.
- [Configuration](configuration.md): the data folder, `config.json` and every setting with its default.
- [Safety](safety.md): the git guard, turn snapshots and restore, work at risk, hygiene and scope checks.
- [GitHub](github.md): connecting repositories, the issue inbox, tickets, the secret scan before a push, draft pull
  requests and CI checks.
- [Automations](automations.md): scheduled delegations, why they start off, schedules, quota and precheck rules.

## For lead agents

- [MCP server](mcp.md): the 17 tools, their arguments and results, how the server finds and starts the daemon.
- [Gates, lanes and proof](gates.md): the gate after each report, automatic fix turns, lanes, the verifier and the
  proof matrix.
- [Team](team.md): members and ranks, consults, review notes, meetings, contests and event hooks.
- The lead skill: `seelegent skill` prints the guide `seelegent setup` installs for the lead hosts.

## Reference

- [Command line](cli.md): every `seelegent` command, its flags, `--json` output and exit codes, and `doctor`.
- [HTTP API](api.md): how the daemon's API v1 and event stream map onto the code.
- [Task engine](task-engine.md): task states, turns and attempts, attention, dependencies, takeover and restart
  recovery.

## For contributors

- [Contributing](../CONTRIBUTING.md): how to report bugs, the checks a pull request must pass, and the pull request
  flow. Security problems go through [SECURITY.md](../SECURITY.md); everyone follows the
  [Code of Conduct](../CODE_OF_CONDUCT.md).
- [Development](development.md): prerequisites, build, tests and lint, development ports, the git workflow.
- [Web UI layout](ui-layout.md): breakpoints, page widths and the navigation rail of the dashboard.
- [AGENTS.md](../AGENTS.md): package layout and coding rules for agents working on this repository.
- [Third-party notices](../THIRD_PARTY_NOTICES.md) and the [license](../LICENSE).
