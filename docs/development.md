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

Release builds: `make cross` builds every release target (windows, linux, darwin × amd64, arm64) into `dist/cross/`;
`make snapshot` builds the release archives and `checksums.txt` into `dist/` with the pinned GoReleaser, and `make
release-check` validates `.goreleaser.yaml`. How releases, the installers and `seelegent update` work:
[release.md](release.md).

`go build ./...` also works on a fresh clone: `web/dist/.gitkeep` keeps the embedded directory present, and the
binary then serves no dashboard. Version, commit and date come from `-ldflags -X
github.com/NovaSeele/Seelegent/internal/buildinfo.{version,commit,date}`; without them the binary reports `dev`
and the VCS data that Go embeds.

## Test and lint

| What             | make                                                    | Without make                                                                                                                                       |
| ---------------- | ------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go tests         | `make test` (`GOTESTFLAGS=-race` for the race detector) | `go test -p 4 ./...`                                                                                                                               |
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

Worker trees (cancel, timeout and the worker guard) are a bar, not a sandbox: a worker can still have a process
started outside its tree by a service of the same user (Windows: WMI `Win32_Process.Create`, the Task Scheduler or
out-of-process COM; Linux: `systemd-run --user`, at, cron; macOS: launchd). Such processes escape cancel and are
seen as the lead. See the `platform` package documentation.

### Light mode

A development machine usually runs Seelegent's workers too, and one froze when several `go test ./...` and lint runs
went at full width together. The commands here therefore run narrow by default:

- `make test` runs `go test -p $(TEST_P)` with `TEST_P ?= 4` (packages tested at once); `make lint` runs
  golangci-lint with `--concurrency $(LINT_J)` (`LINT_J ?= 4`) and `GOMEMLIMIT=2GiB`, and `.golangci.yml` sets
  `run.concurrency: 4` for a plain `golangci-lint run`. On an idle machine raise them: `make test TEST_P=12`.
- Without make: `go test -p 4 ./...`, and `GOMEMLIMIT=2GiB golangci-lint run -j 4 ./...`.
- `-p` caps the packages tested at once, not the tests inside one package: a package's `t.Parallel` tests run up
  to `-parallel` at a time, which defaults to GOMAXPROCS (every CPU). On a busy machine add `-parallel 4`, or set
  `GOMAXPROCS=8` for the whole run (it also caps each test binary's own threads): `GOMAXPROCS=8 go test -p 4
  -parallel 4 ./...`.
- `make lint` also vets the bench (`go vet -tags bench ./scripts/bench/`), and `.golangci.yml` sets
  `run.build-tags: [bench]`, so the linter checks it too (CI does both).
- Never run the tests and the linter at the same time; test only the packages you changed while iterating
  (`go test -p 4 ./internal/task`).
- Run long checks at low priority, so the desktop and the workers stay responsive:
  - Windows (PowerShell): `$p = Start-Process go -ArgumentList 'test','-p','4','./...' -NoNewWindow -PassThru;
    $p.PriorityClass = 'BelowNormal'; $p.WaitForExit()`, or start a shell at low priority with
    `cmd /c start "" /belownormal pwsh` (everything it starts inherits the class). Setting
    `(Get-Process -Id $PID).PriorityClass = 'BelowNormal'` in a script lowers what the script starts afterwards.
  - Linux and macOS: `nice -n 10 go test -p 4 ./...` (and `ionice -c 3` on Linux for disk-heavy runs).
- Low CPU priority does not lower memory use: check free memory before a heavy run (Windows:
  `(Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory/1MB` GB; Linux: `free -g`).

### Profiling the daemon (pprof)

`seelegent daemon --pprof-addr 127.0.0.1:6060` serves `net/http/pprof` and `/debug/vars` on that address, on a
server and a mux of their own (never the API's port, never `http.DefaultServeMux`). The flag is hidden, off by
default and never a setting. It takes loopback addresses only (`127.0.0.1`, `[::1]`, `localhost`; a `localhost`
that resolves to anything else is refused once it listens) and refuses requests whose `Host` header is not
`127.0.0.1`, `localhost`, `[::1]` or the address it listens on, with its port (the API's DNS-rebinding check).
`/debug/vars` is expvar-style JSON: `pid`, `goroutines` and `runtime` (runtime/metrics: heap, live heap, allocations,
GC cycles) always, `memstats` and `cmdline` unless the query has `light=1` (`runtime.ReadMemStats` stops the world, so a
sampler polls `?light=1`). It is a development tool: heap and
goroutine profiles can hold secrets (API keys, prompts, tokens in memory), so never share them or start a daemon
with it on a machine others use.

```sh
SEELEGENT_HOME=$(mktemp -d) go run ./cmd/seelegent daemon --port 47831 --pprof-addr 127.0.0.1:6060
go tool pprof -top http://127.0.0.1:6060/debug/pprof/profile?seconds=30   # CPU
go tool pprof -top -sample_index=alloc_space http://127.0.0.1:6060/debug/pprof/allocs
curl -s 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=1' | head      # goroutines
curl -s http://127.0.0.1:6060/debug/vars                                  # memstats, runtime metrics
curl -s 'http://127.0.0.1:6060/debug/vars?light=1'                        # goroutines, runtime metrics
```

### Benchmark

`scripts/bench` (build tag `bench`) measures what Seelegent costs the machine. It builds `seelegent`, starts a
daemon on a free port picked for the run (`-port`), with `--pprof-addr` on another (`-pprof-port`) and a temp
`SEELEGENT_HOME` and user home, adds 6 fake agy accounts with their quota readers on, runs 20 short tasks so the
lists are not empty, and then:

- `-scenario idle` (default, `-duration 22m`, at least two quota cycles): nothing happens; this is the daemon
  left running all day. The result splits the idle CPU into the floor (between quota cycles), the CPU per quota
  cycle and the window's average, and lists the start of every quota read and gh run.
- `-scenario idle-sse`: the same with one SSE client connected, a dashboard tab left open.
- `-scenario load` (`-duration 5m`): 8 tasks whose fake workers stream `-rate 100` lines a second together, 3 SSE
  clients, and a list and a detail request every 250 ms (p50/p95/p99 recorded).

Every 5 s it samples the daemon's whole process tree (daemon, launchers, workers, quota readers, git and their
consoles): working set, private bytes, CPU seconds and handles per role (Windows: toolhelp snapshot and process
counters; Linux: `/proc`), spawns per hour by name, the daemon's goroutines and heap (`/debug/vars?light=1`, with
memstats once a minute) and the database and WAL sizes. On Windows the daemon also runs in a Job Object of the
bench's, whose accounting gives the exact CPU of the whole tree and every process started, short-lived ones
included. Each sample records the machine too (free memory and commit, processes and threads on the system), and
the result carries a machine profile (OS, logical CPUs, RAM, commit limit): the cost of starting a process tree on
Windows grows with the system's thread count, and the defaults are computed per machine.

The setup is measured on its own: the daemon's and the tree's CPU per account, per seed task and per load task
start, with a CPU profile from the daemon's start (`-setup-profile 90s`, `setup-cpu.pprof`; the warmup waits for
it). A CPU profile is taken after the measured window (`-cpuprofile 60s`, kept out of the CPU numbers), together
with allocation profiles at the start and the end of the window.

Benches can run at the same time: each talks only to the daemon it started. Before it uses the daemon and before
it stops it, it checks that the API port answers with that process's pid and the HomeID of its own
`SEELEGENT_HOME`, and that the pprof port's `/debug/vars` names the same pid; another daemon on the port is an
error, never used or stopped (the bench kills only the process it launched).

The bench is hermetic: the fake agy is the test binary itself (`internal/testutil/fakecli`), which logs each run so
quota reads are counted exactly and answers a quota read instantly (a real agy costs far more); gh is a copy of the
test binary named `gh`, first on the daemon's PATH, answering the access check offline; the daemon gets temp
folders for `HOME`/`USERPROFILE`, `APPDATA`, `LOCALAPPDATA` and `GH_CONFIG_DIR`, and none of the machine's
`GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN` or `GH_HOST`. It never uses this
machine's GitHub sign-in or reaches the network. Its config turns the memory thresholds off, so a busy machine
does not hold its tasks.

```sh
make bench                                            # idle, 10 minutes
make bench BENCH_ARGS="-scenario load -duration 5m"
go test -tags bench -c -o bin/seelegent-bench ./scripts/bench
bin/seelegent-bench -scenario load -baseline <dir>/result.json   # prints the deltas against an earlier run
```

Results go to `-out` (default `<temp>/seelegent-bench/<time>-<scenario>`, outside the repository): `result.json`,
`result.md` (the tables), `setup-cpu.pprof`, `cpu.pprof`, `allocs-start.pprof`, `allocs-end.pprof`, `heap.pprof`, `goroutine.txt` and
the daemon's log. Compare allocation profiles with `go tool pprof -top -sample_index=alloc_space -base
allocs-start.pprof allocs-end.pprof`. The budget is judged on private bytes (commit), not the working set, which
jumps with what the OS keeps resident: under 0.1% of one core and under 100 MB private when idle; growth under
load not linear in the events; a private-bytes slope under 10 MB/h and a live-heap slope under 5 MB/h over the
second half of the window, so a leak shows once the start-up growth is over. It is printed and flagged, never a
failure. Every result carries the machine profile (OS, CPUs, RAM and commit, free memory and commit over the
window, system threads, the three other programs that used the most CPU), so runs on different machines or on a
busy one can be told apart.

On Windows a CPU profile counts goroutines blocked in a system call as CPU: a launcher or the daemon waiting in
`ReadFile` on a pipe shows up as `os.(*File).Read`, `bytes.Buffer.ReadFrom` or `io.Copy` with large sample counts
while the process is idle. Do not chase those; take the cost from the CPU seconds of the process counters (the
result's tables), and read Windows profiles for the code that does run (for example the thread walk in
`platform.resumeThreads`). Run it at low priority and
on an otherwise quiet machine; the latency numbers depend on what else runs.

Hot paths have plain Go benchmarks (stream parsers, launcher line delivery, task repository writes and reads, the
SSE hub and the task bus): `go test -p 4 -run '^$' -bench . ./internal/backend ./internal/launch ./internal/task
./internal/httpapi`.

## Dev ports and the dashboard

- The installed Seelegent daemon uses port 47821 and `~/.seelegent`. Development runs never touch them: run the
  development daemon on **port 47831** with **`SEELEGENT_HOME` set to a temp dir**, for example
  `SEELEGENT_HOME=$(mktemp -d) go run ./cmd/seelegent daemon --port 47831 --dev-origin http://localhost:5173`, or
  `make dev-daemon`. On Windows (PowerShell): `$env:SEELEGENT_HOME = (New-Item -ItemType Directory
  "$env:TEMP\sg-dev-$(Get-Random)").FullName; go run ./cmd/seelegent daemon --port 47831`.
- `seelegent daemon` (old name `serve`) runs in the foreground until Ctrl+C or `seelegent stop` (graceful
  shutdown: open SSE streams end, connections that never sent a request and idle keep-alive ones close at once,
  requests get 5 s and are then cut off with a warning in the log, not an error, the task engine stops).
  `--port` defaults to `SEELEGENT_PORT`, then the settings' `port`. It listens on 127.0.0.1 only, holds
  `run/daemon.lock` for its whole life and records its port and pid in `run/daemon.port`. With a temp home,
  `SEELEGENT_HOME=... SEELEGENT_PORT=47831 seelegent status|stop|doctor` talk to the development daemon; see [cli.md](cli.md).
- `cd web; pnpm dev` starts Vite on http://localhost:5173 with hot reload. It proxies `/api` to
  `http://127.0.0.1:47831`, so the dashboard talks to the development daemon. The proxy keeps the browser's
  `Host` and `Origin` (`localhost:5173`), which the daemon's Host check (421) and same-origin check (403) refuse
  unless it was started with `--dev-origin http://localhost:5173`. That flag accepts only loopback `http` origins;
  never use it outside development.
- The HTTP API is described in [api.md](api.md); the binding contract is `contracts/api-v1.md` in the spec
  workspace.
- `pnpm build` writes `web/dist`; the next `go build` embeds it (package `web`, `web.FS()`).

## Database migrations

Migrations live in `internal/store/migrations/NNNN_name.sql`, numbered without gaps. Never rewrite a migration
that has run anywhere (an installed daemon, a release, a user's data folder): change the schema with a new one.
Only a migration that never left an unmerged branch may still be renamed or changed.

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
- Release: `goreleaser check`, a snapshot build of every archive, and the installers parse (`sh -n`, `dash -n`,
  `shellcheck`, the PowerShell parser).
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
