# Seelegent: notes for coding agents

Seelegent is a Go daemon + CLI + MCP server in one binary, with a Vue 3 + TypeScript dashboard embedded from
`web/dist`. Full guide: [docs/development.md](docs/development.md).

## Layout

- `cmd/seelegent/main.go` only calls `internal/cli.Execute()`.
- `internal/<pkg>/` — one job per package: `buildinfo`, `cli` (thin cobra commands), and later `app` (composition
  root), `config`, `store`, `task`, `pool`, `backend`, `httpapi`, `api` (DTOs, source for tygo), `mcpserver`,
  `setup`, `migrate`, ...
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
