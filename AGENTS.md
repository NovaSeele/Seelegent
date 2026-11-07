# Seelegent: notes for coding agents

Seelegent is a Go daemon + CLI + MCP server in one binary, with a Vue 3 + TypeScript dashboard embedded from
`web/dist`. Full guide: [docs/development.md](docs/development.md).

## Layout

- `cmd/seelegent/main.go` only calls `internal/cli.Execute()`.
- `internal/<pkg>/` — one job per package: `buildinfo`, `cli` (thin cobra commands; see [docs/cli.md](docs/cli.md)),
  `fsutil` (atomic writes, file locks, path keys), `envx` (variable lookup in env lists, PATH/PATHEXT command
  lookup; stdlib only), `config` (data paths, settings, config.json; see [docs/configuration.md](docs/configuration.md)),
  `store` (SQLite, migrations in `store/migrations/NNNN_name.sql`), `logx` (slog setup, log rotation, secret
  redaction), `platform` (hidden consoles, process trees, kill-tree, sign-in terminals; per-OS files), `launch`
  (detached worker launcher behind the hidden `seelegent __launch`), `account` (accounts, providers, profiles, API
  keys, run environment), `event` (normalized turn events), `backend` (agy/claude/codex commands, env rules and
  stream parsers), `clock` (injectable time with a fake for tests), `quota` (quota readers per backend, quota
  cache, background refresher), `pool` (account selection, leases, rest periods, waiting, handover), `usage`
  (hourly snapshots, attempt usage, history and summary), `api` (DTOs shared with the dashboard; `go generate
  ./internal/api` writes `web/src/api/types.gen.ts` through `api/tsgen`), `task` (task engine: model, event log,
  runner, recovery; see [docs/task-engine.md](docs/task-engine.md)), `brief` (worker briefs, golden files in
  `testdata/`), `memory` (project notes, review history), `role` (worker roles and skills), `gitx` (git status,
  diffs, branches and commits for the task views), `guard` (the git guard: command-line reader, hooks, git shim)
  and `snapshot` (turn snapshots, work at risk, restore; both in [docs/safety.md](docs/safety.md)), `proof` (spec
  parsing, lanes, doctrine, proof ledger, regression runs) and `gate` (gate plans, commands, fix prompts; both in
  [docs/gates.md](docs/gates.md)), `member` (department members), `notes` (review notes on a task's diff),
  `meeting` (meetings, minutes, dissent memory), `contest` (contests: plan, scoring, judge, pick), `worktree` (the
  worktrees Seelegent owns and their environment), `answer` (reading members' answers: sections, citations,
  redaction) and `hook` (event hooks; all in [docs/team.md](docs/team.md)), `github` (access tiers, token store,
  REST client and `gh` runner, hosts, redaction) and `ticket` (repositories, inbox, triage, tickets, outbox, pull
  request scan, checks; both in [docs/github.md](docs/github.md)), `automation` (scheduled delegations, cron,
  confirmation; see [docs/automations.md](docs/automations.md)), `signin` (account sign-in sessions), `httpapi` (HTTP API v1, SSE
  and the embedded dashboard; see [docs/api.md](docs/api.md)), `client` (typed API v1 client, daemon discovery
  and autostart), `ops` (what the MCP tools and the CLI share: worker guard, lead session, waits, active tasks),
  `doctor` (installation checks), `mcpserver` (MCP tools for the lead over the client; see
  [docs/mcp.md](docs/mcp.md)), `app`
  (composition root: builds and starts the services), `testutil/fakecli` (the test binary as launcher and fake
  worker CLI), `setup` (registers Seelegent with the lead hosts: MCP entries, lead skill, reminder hook; see
  [docs/setup.md](docs/setup.md)), `migrate` (imports the old Node version's data folder), `update` (`seelegent
  update`: release check, verified download, in-place replacement and rollback; see [docs/release.md](docs/release.md)).
- `web/` — Vue app with its own `package.json`; `web/embed.go` embeds `web/dist`.
  `web/src/`: `main.ts`, `App.vue`, `router/`, `api/`, `stores/` (Pinia), `i18n/` (`locales/<lang>/<ns>.json`),
  `styles/` (tokens), `components/ui/`, `components/`, `features/<area>/`, `composables/`.
- `docs/`, `scripts/`, `.github/workflows/`.

## Rules

- Domain packages never import `httpapi`, `mcpserver`, `cli` or `app`. Declare interfaces where they are used;
  only `app` wires concrete types.
- No package-level mutable state: dependencies come through constructors. Every blocking call takes a
  `context.Context`.
- New Go or npm dependencies need the lead's OK.
- Files under the data directory are written with `fsutil.WriteFileAtomic`; compare or hash paths through
  `fsutil.Key`, never raw strings. A store that cannot be read is an error, never a silent reset to empty. Log
  through `logx` so secrets are masked.
- Frontend: `<script setup lang="ts">`, strict TypeScript, no `any`, no `v-html` with task or worker text,
  components under about 300 lines, all user-visible text through vue-i18n, colours only through tokens,
  keyboard accessible and responsive.
- Development runs use port 47831 and `SEELEGENT_HOME=<temp dir>`; never touch the installed daemon on 47821.
- Tests use temp dirs and random ports, never real accounts.
- Conventional commits ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Stage paths
  explicitly (no blind `git add -A`), keep the tree clean, never push.

## Commands

```sh
go build ./... && go vet ./... && go test ./...
make lint                                   # golangci-lint v2 (pinned)
cd web && pnpm install && pnpm lint && pnpm typecheck && pnpm test && pnpm build
make build                                  # or: pwsh -File scripts/build.ps1 (Windows)
```
