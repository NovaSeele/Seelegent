# Contributing to Seelegent

Thank you for helping. This page covers the development setup, the checks a change must pass, and how a change gets
into `main`. The full developer guide is [docs/development.md](docs/development.md); the package layout and coding
rules are in [AGENTS.md](AGENTS.md). Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting bugs and asking for features

- **Bugs:** open an issue with the Seelegent version (`seelegent version`), your OS, the worker CLIs and lead host
  involved, what you did, what you expected and what happened. The output of `seelegent doctor` helps. Mask keys,
  tokens, e-mail addresses and personal paths before you paste logs (`~/.seelegent/logs/daemon.log` already masks
  secrets, but check).
- **Features:** open an issue that describes the problem first, then the change you have in mind.
- **Security problems:** do not open a public issue. Follow [SECURITY.md](SECURITY.md).

## Development setup

Seelegent is one Go binary (daemon, CLI and MCP server) with a Vue 3 + TypeScript dashboard in `web/` that is
embedded at build time. It builds and runs on Windows, Linux and macOS.

| Tool | Version | Notes |
|---|---|---|
| Go | 1.21 or newer | `go.mod` needs Go 1.26 or newer and pins `toolchain go1.27.3`. Any Go from 1.21 on works with the default `GOTOOLCHAIN=auto`: it downloads go1.27.3 on first use. If `go env GOTOOLCHAIN` prints `local`, set `GOTOOLCHAIN=auto`. |
| Node.js | 22.12 or newer | CI uses Node 24. |
| pnpm | 11.20.0 | Pinned in `web/package.json`; `corepack enable` picks it up. |
| Git | any recent | |
| make | GNU make | Optional on Windows: `scripts/build.ps1` does the same build. |

golangci-lint does not need to be installed: `make lint` runs the pinned release with `go run`.

Build:

```sh
make build                    # macOS, Linux, or Windows with make: bin/seelegent with the dashboard embedded
pwsh -File scripts/build.ps1  # Windows without make
```

`go build ./...` also works on a fresh clone; the binary then serves no dashboard.

## Checks

Run these before you open a pull request:

```sh
go build ./... && go vet ./... && go test ./...
make lint                                   # golangci-lint v2 (pinned)
cd web && pnpm install && pnpm lint && pnpm typecheck && pnpm test && pnpm build
```

The web commands are needed when you changed something under `web/`. When you change a DTO in `internal/api`, run
`go generate ./internal/api` (or `make generate`) and commit the regenerated `web/src/api/types.gen.ts`; a test
fails when it is stale.

### Keep heavy runs light

A development machine often runs Seelegent's own workers too, and full-width test and lint runs at the same time
can freeze it. The make targets run narrow by default (`make test` uses `-p 4`, `make lint` 4 jobs and
`GOMEMLIMIT=2GiB`); raise them on an idle machine (`make test TEST_P=12`).

- Never run the tests and the linter at the same time, and while iterating test only the packages you changed
  (`go test -p 4 ./internal/task`).
- Run long checks at low priority: `nice -n 10 go test -p 4 ./...` on Linux and macOS; on Windows start a shell
  with `cmd /c start "" /belownormal pwsh`.
- See [Light mode](docs/development.md#light-mode) for the details.

## Running a development daemon

The installed Seelegent uses port **47821** and `~/.seelegent`. Development runs must never touch them: use port
**47831** and a temporary `SEELEGENT_HOME`.

```sh
make dev-daemon
# or
SEELEGENT_HOME=$(mktemp -d) go run ./cmd/seelegent daemon --port 47831 --dev-origin http://localhost:5173
```

On Windows (PowerShell):

```powershell
$env:SEELEGENT_HOME = (New-Item -ItemType Directory "$env:TEMP\sg-dev-$(Get-Random)").FullName
go run ./cmd/seelegent daemon --port 47831 --dev-origin http://localhost:5173
```

Then `cd web && pnpm dev` serves the dashboard with hot reload on http://localhost:5173 and proxies the API to the
development daemon. With the same `SEELEGENT_HOME` and `SEELEGENT_PORT=47831`, `seelegent status`, `stop` and
`doctor` talk to that daemon.

## Rules for code

- Read [AGENTS.md](AGENTS.md) first: one job per package, domain packages never import `httpapi`, `mcpserver`,
  `cli` or `app`, no package-level mutable state, every blocking call takes a `context.Context`.
- **No new dependencies** (Go modules or npm packages) without the maintainer's OK. Ask in the issue first.
- **Tests use temp dirs and random ports, never real accounts.** Backends are tested against the fake CLI in
  `internal/testutil/fakecli`.
- Files under the data directory are written with `fsutil.WriteFileAtomic`; log through `logx` so secrets are
  masked.
- Dashboard: `<script setup lang="ts">`, strict TypeScript with no `any`, all visible text through vue-i18n,
  colours only through tokens, keyboard accessible and responsive.
- Leave the tree clean: no scratch files, no generated output other than `web/src/api/types.gen.ts`.
- Never commit secrets, e-mail addresses or personal paths, in code, tests, docs or examples.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary`, for example
`feat(task): add the wait endpoint` or `fix(pool): skip accounts that are resting`. Types in use: `feat`, `fix`,
`docs`, `test`, `style`, `refactor`, `perf`, `build`, `chore`, `ci`. Keep commits small; each one builds. Stage
the paths you changed explicitly and check `git status` before committing.

## Pull requests

Direct pushes to `main` are blocked: every change lands through a pull request.

1. **Fork** the repository (or, with write access, create a branch). Name the branch after the change:
   `feat/…`, `fix/…`, `docs/…`, `chore/…`, `ci/…`.
2. Make the change, run the [checks](#checks), and push the branch to your fork.
3. **Open a pull request into `main`.** Give it a Conventional Commits title and fill in the
   [template](.github/pull_request_template.md): what changed, the linked issue, and how you tested it.
4. **CI must pass.** The required checks are the jobs of the `CI` workflow
   ([.github/workflows/ci.yml](.github/workflows/ci.yml)):
   - `go (ubuntu-latest)`, `go (windows-latest)`, `go (macos-latest)`: build, vet and tests (with `-race` on Linux
     and macOS);
   - `lint`: `go mod tidy -diff` and golangci-lint;
   - `web`: install, lint, typecheck, tests and build of the dashboard;
   - `release`: the GoReleaser config, a snapshot build of every archive, and the installer scripts parse.
5. **Review.** Every review thread must be resolved before the merge. Push fixes as new commits; force-pushes to
   `main` are not allowed.
6. **Merge.** The maintainer merges with a squash or a merge commit, and the branch is deleted after the merge.

### Checklist

- [ ] `go build ./...`, `go vet ./...` and `go test ./...` pass.
- [ ] `make lint` passes.
- [ ] When `web/` changed: `pnpm lint`, `pnpm typecheck`, `pnpm test` and `pnpm build` pass.
- [ ] Docs updated for any change users or lead agents can see (`docs/`, `README.md`).
- [ ] No new dependencies without the maintainer's OK.
- [ ] The title follows Conventional Commits.
- [ ] No secrets, e-mail addresses or personal paths in the diff.

## License of contributions

Seelegent is released under the [FSL-1.1-ALv2](LICENSE) license. By opening a pull request you agree that your
contribution is released under the same terms.
