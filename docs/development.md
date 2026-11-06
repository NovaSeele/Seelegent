# Development

Seelegent is one Go binary (daemon, CLI, MCP server) with a Vue 3 + TypeScript dashboard in `web/` that is
embedded into the binary at build time. The binding design is the architecture document kept by the lead;
[AGENTS.md](../AGENTS.md) summarises the rules that matter while coding.

## Prerequisites

| Tool    | Version                     | Notes                                                                                                                                                                                                                                 |
| ------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go      | 1.21 or newer               | `go.mod` pins `toolchain go1.27.3` (minimum language version 1.26); with the default `GOTOOLCHAIN=auto` an older local Go downloads it on first use. If `go env GOTOOLCHAIN` prints `local`, run Go commands with `GOTOOLCHAIN=auto`. |
| Node.js | 22.12 or newer (CI uses 24) |                                                                                                                                                                                                                                       |
| pnpm    | 11.20.0                     | Pinned in `web/package.json` (`packageManager`); `corepack enable` picks it up.                                                                                                                                                       |
| Git     | any recent                  |                                                                                                                                                                                                                                       |
| make    | GNU make                    | Optional on Windows: `scripts/build.ps1` does the same build.                                                                                                                                                                         |

golangci-lint does not need to be installed: `make lint` runs the pinned v2 release with `go run`.

## Build

macOS / Linux (or Windows with make):

```sh
make build          # pnpm install + build web/dist, then go build into bin/seelegent
bin/seelegent version
```

Windows without make (PowerShell 7 or Windows PowerShell 5.1):

```powershell
pwsh -File scripts/build.ps1            # -SkipWeb reuses the current web/dist
.\bin\seelegent.exe version
```

`go build ./...` also works on a fresh clone: `web/dist/.gitkeep` keeps the embedded directory present, and the
binary then serves no dashboard. Version, commit and date come from `-ldflags -X
github.com/NovaSeele/Seelegent/internal/buildinfo.{version,commit,date}`; without them the binary reports `dev`
and the VCS data that Go embeds.

## Test and lint

| What             | make                                                    | Without make                                                                                                                                       |
| ---------------- | ------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go tests         | `make test` (`GOTESTFLAGS=-race` for the race detector) | `go test ./...`                                                                                                                                    |
| Go lint          | `make lint`                                             | `go vet ./...` and `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...` (set `GOTOOLCHAIN=go1.27.3` for that command) |
| Web tests        | `make test-web`                                         | `cd web; pnpm test`                                                                                                                                |
| Web lint + types | `make lint-web`                                         | `cd web; pnpm lint; pnpm typecheck`                                                                                                                |
| Format           | `make fmt`                                              | `golangci-lint fmt ./...`, `cd web; pnpm format`                                                                                                   |

golangci-lint must be built with the module's toolchain (go1.27.3); a binary built with an older Go refuses to
lint this module. `make lint` and CI take care of that.

Process-tree code (`internal/platform`, `internal/launch`) behaves differently per OS, and some tests run on one
OS only (the Linux child subreaper, `/proc` socket lookup; the Windows named job). On Windows, check the other
systems with `GOOS=linux go vet ./...` and `GOOS=darwin go vet ./...` (golangci-lint honours `GOOS` too when run as
an installed binary rather than through `go run`), and run the Linux tests in WSL from a cross-compiled test
binary: `GOOS=linux go test -c -o <tmp>/platform.test ./internal/platform`, then run it inside WSL from a Linux
directory such as `/tmp`.

## Dev ports and the dashboard

- The installed Seelegent daemon uses port 47821 and `~/.seelegent`. Development runs never touch them: run the
  development daemon on **port 47831** with **`SEELEGENT_HOME` set to a temp dir**, for example
  `SEELEGENT_HOME=$(mktemp -d) SEELEGENT_PORT=47831 go run ./cmd/seelegent daemon` (the daemon arrives in
  milestone 7; `make dev-daemon` is a placeholder until then).
- `cd web; pnpm dev` starts Vite on http://localhost:5173 with hot reload. It proxies `/api` to
  `http://127.0.0.1:47831`, so the dashboard talks to the development daemon. The proxy keeps the browser's
  `Origin` (`http://localhost:5173`); the daemon's origin check must accept it in development.
- `pnpm build` writes `web/dist`; the next `go build` embeds it (package `web`, `web.FS()`).

## Toolchain decisions

- **TypeScript 6.0**, not 7. TypeScript 7.0 (the native port) is the newest release, but its npm package no longer
  ships the JavaScript compiler API: vue-tsc 3.3 (through `@volar/typescript`, peer `typescript <7`) and
  typescript-eslint 8.72 (peer `typescript <6.1.0`) cannot run on it. 6.0.3 is the newest version both support.
  Revisit when vue-tsc and typescript-eslint support TypeScript 7.
- The ESLint config is `eslint.config.js`, not `.ts`: a TypeScript config file would need `jiti`, an extra
  dependency.
- Vue tests are colocated as `*.spec.ts` next to the code; `tsconfig.vitest.json` type-checks them,
  `tsconfig.app.json` excludes them.

## Quality gates

Every branch passes these before it is merged (CI runs them on Linux, Windows and macOS):

- Go: `gofmt` / `goimports` (through golangci-lint), `go vet`, `golangci-lint run`, `go test ./...` with `-race`
  on Linux and macOS, coverage of domain packages at least 70%.
- Web: `pnpm lint` (ESLint + Prettier check), `pnpm typecheck` (vue-tsc), `pnpm test` (Vitest), `pnpm build`.
- Backends are tested against a fake CLI that replays recorded streams; tests never use real accounts, and they
  use temp data dirs and random ports.
- The repository stays clean: no scratch files and no generated output except `web/src/api/types.gen.ts`
  (generated by tygo and committed; CI fails if it is stale). `web/dist` is ignored apart from its `.gitkeep`.

## Git workflow and commits

- `main` always builds and passes the checks. Work happens on short branches (`chore/…`, `feat/…`, `fix/…`,
  `docs/…`, `ci/…`) that the lead reviews and merges with `git merge --no-ff`.
- Conventional commit messages, for example `feat(task): add the wait endpoint` or `fix(pool): …`. Keep commits
  small; each one builds.
- Every commit message ends with a blank line and
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Stage the paths you changed explicitly and check `git status` before committing. Nothing is pushed until the
  lead asks the user.
