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
make build          # pnpm install + build web/dist, then go build into bin/seelegent and bin/seelegent-launch
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

### The companion launcher

Every task's worker is started by a detached launcher process (`internal/launch`) that lives for the whole turn.
The daemon starts it as `seelegent-launch __launch <dir>` when the companion executable `seelegent-launch[.exe]`
(`cmd/seelegent-launch`) sits next to its own executable and is of the same build, and as `seelegent __launch <dir>`
otherwise; both run `launch.Main`, so there is one launcher code path ([release.md](release.md#the-companion-launcher)
says when the companion is used). The companion exists to save memory: it is about 12 MB private on Windows instead
of about 51 MB, because it links no crypto package (any of them brings in the 32 MiB entropy buffer of
`crypto/internal/fips140/drbg`, which Windows charges as commit).

The companion's second role is the MCP relay: `seelegent-launch mcp` (`internal/relay`) copies a lead host's MCP
messages to a session that runs inside the daemon (`POST /api/v1/mcp/relay`), so a host window costs a companion
process instead of a full `seelegent mcp` ([mcp.md](mcp.md#the-mcp-relay)). `seelegent setup` registers it only for a
companion of the same build, so development builds from a modified tree keep `seelegent mcp`; to try the relay,
commit, build both with the same flags, and run `seelegent-launch mcp` by hand (it runs `seelegent mcp` from its
folder when it cannot open a session, and says why on stderr).

Rules that keep it so:

- Nothing `cmd/seelegent-launch` imports may import `crypto/...`: that is `internal/launch`, `internal/relay` and what
  they import (today `platform`, `fsutil` and `mcpwire`). The relay therefore speaks raw HTTP/1.1 over `net` for its
  upgrade and re-implements the few pieces of discovery it needs; `config`, `client`, `api` and `ops` link crypto. `go test ./cmd/seelegent-launch` (`TestLinksNoCrypto`) runs `go list -deps` for
  Windows, Linux and macOS and fails naming each crypto package found (`go list -deps -f '{{.ImportPath}}
  {{.Imports}}'` then shows who imports it). Hashing that needs crypto lives outside that set: project keys (SHA-1)
  are in `internal/projectkey`, not `fsutil`; Windows tree names use `math/rand/v2` (unique, not secret).
- Build the companion with the same command and flags as `seelegent` (`make build`, `scripts/build.ps1`, the bench
  and GoReleaser do): the daemon compares their build information and falls back when anything differs, with one
  line in `logs/daemon.log` saying why. It also falls back for a build from a modified tree (uncommitted changes),
  whose source the build information cannot tell apart: commit before a bench that should measure the companion.
- Development runs (`go run ./cmd/seelegent daemon`, tests, fakecli) have no companion next to the executable and
  use `seelegent __launch`; that is expected and logged once at info level.

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
- `make lint` also vets the bench (`go vet -tags bench ./scripts/bench/`, then again with `GOOS=windows` and
  `GOOS=darwin` so every OS's files compile), and `.golangci.yml` sets `run.build-tags: [bench]`, so the linter
  checks it too (CI does both). `go test -tags bench -run Test ./scripts/bench` runs the report's unit tests (made-up
  numbers, no daemon); without `-run` the test binary runs the bench itself.
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

`scripts/bench` (build tag `bench`) measures what Seelegent costs the machine. It builds `seelegent` and the
companion launcher `seelegent-launch` next to it (with `-bin`, the daemon uses a companion of the same build next to
that binary when there is one; a build from a modified tree runs launches as `seelegent __launch`), starts a daemon on a free port picked for the run (`-port`), with `--pprof-addr` on another (`-pprof-port`) and a temp
`SEELEGENT_HOME` and user home, adds 6 fake agy accounts with their quota readers on, runs 20 short tasks so the
lists are not empty, and then:

- `-scenario idle` (default, `-duration 22m`, at least two quota cycles): nothing happens; this is the daemon
  left running all day. The result splits the idle CPU into the floor (between quota cycles), the CPU per quota
  cycle and per quota-reader spawn, and the window's average, and lists the start of every quota read and gh run.
  The usage recorder's hourly snapshot and prune (the first of its 5-minute ticks after each hour, predicted from
  the daemon's start) is kept out of the floor and reported as CPU per tick.
- `-scenario idle-sse`: the same with one SSE client connected, a dashboard tab left open.
- `-scenario load` (`-duration 5m`): 8 tasks (`-tasks`) whose fake workers stream `-rate 100` lines a second
  together, 3 SSE clients, and a list and a detail request every 250 ms (p50/p95/p99 recorded). The bench daemon's
  config has no cap over all accounts and enough turns per account for every task to run at once: the default 2,
  or more when `-tasks` does not fit (`-tasks 16` on the 6 accounts gets 3 per account).

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
the daemon's log (the load scenario adds `heap-before-load.pprof` and `heap-after-load.pprof`), and
`memstats-end.json`: the daemon's full `runtime.MemStats` (`/debug/vars` without `light=1`) read at the end of every
scenario, in the load scenario after the retained-heap reading. The result's "Go sys breakdown (end)" shows where
Go's `Sys` goes: `HeapSys` (with `HeapInuse` and `HeapReleased`, the part returned to the OS) and what lies outside
the heap, `StackSys` (goroutine stacks), `MSpanSys`, `MCacheSys`, `GCSys` (GC metadata), `BuckHashSys` (profiling)
and `OtherSys`. Compare allocation profiles with `go tool pprof -top -sample_index=alloc_space -base
allocs-start.pprof allocs-end.pprof`. The budget is judged on private bytes (commit), not the working set, which
jumps with what the OS keeps resident: under 0.1% of one core and under 100 MB private when idle; under load a
peak under 80 MB plus 2.5 MB per task running at once (100 MB for the default 8 tasks), since each running attempt
holds its own state; daemon CPU under 8% of one core plus 0.25% per task (10% for 8 tasks: the stream lines are
shared by the tasks, so the base carries the events, the SSE clients and the pollers, and the per-task part what
one attempt adds by itself, measured at about 0.03%); an event delay (worker line to SSE frame) under 1 s at the
95th percentile; growth under load not linear in the events; a private-bytes slope under 10 MB/h and a
live-heap slope under 5 MB/h over the second half of the window, so a leak shows once the start-up growth is over.
It is printed and flagged, never a failure.

Under load the two slopes are shown as information only: the 8 tasks run through the whole window, and what each
running attempt holds (the parser's state, the launcher's output buffers) grows with the events it has seen, so a
rising slope there does not tell a leak from work in progress. The load scenario checks for leaks with the **live
heap retained after load** instead. Just before the load tasks start (after the warmup) the bench forces a garbage
collection in the daemon and reads its live heap; after the window it cancels the load tasks that still run, waits
until every one ended and the SSE clients and pollers stopped, waits 5 s more, and reads the live heap the same way.
Each reading fetches `/debug/pprof/heap?gc=1` twice on the pprof port (every fetch runs a full GC first; the second
also clears what `sync.Pool` keeps for one more cycle), saves the second profile (`heap-before-load.pprof`,
`heap-after-load.pprof`), and then reads `/gc/heap/live` (the bytes the last GC marked live) from
`/debug/vars?light=1`. The same answer carries the SSE replay ring (`daemon.sseRing`: frames, encoded bytes and the
heap they hold), which keeps the last frames for 10 minutes and so still holds the load's frames at the end
reading; that is not a leak. The result shows the start, the end and the raw delta in MB, the ring at both
readings, and the delta without the ring, which is the budgeted value: at most 3 MB. A daemon that does not report
its ring (an older build) is judged on the raw delta, with a note.
`go tool pprof -top -sample_index=inuse_space -base heap-before-load.pprof heap-after-load.pprof` shows what
stayed. The idle scenarios keep the slopes as budgets, except over a window under 15 minutes: a few
minutes extrapolated to an hour give false results, so a shorter window shows them as information only
(window too short), with the raw numbers.

Every result carries the machine profile (OS, CPUs, RAM and commit, free memory and commit over the window, system
threads, the three other programs that used the most CPU), so runs on different machines or on a busy one can be
told apart.

On Windows a CPU profile counts goroutines blocked in a system call as CPU: a launcher or the daemon waiting in
`ReadFile` on a pipe shows up as `os.(*File).Read`, `bytes.Buffer.ReadFrom` or `io.Copy` with large sample counts
while the process is idle. Do not chase those; take the cost from the CPU seconds of the process counters (the
result's tables), and read Windows profiles for the code that does run (for example the thread walk in
`platform.resumeThreads`). Run it at low priority and on an otherwise quiet machine; the latency numbers depend on
what else runs. The CPU seconds of one build vary by about a point of one core between runs, and on a hybrid CPU
(performance and efficiency cores) a run whose daemon lands on efficiency cores can cost half as much again:
compare builds over two runs each, alternating them.

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
  `docs/…`, `ci/…`) that the lead reviews.
- `main` on GitHub takes changes only through pull requests. A ruleset with no bypass blocks direct pushes,
  force pushes and deletion, and requires the six CI jobs to pass: go on ubuntu, windows and macos, lint,
  release, and web. The flow is: push the branch, open a PR, wait for green CI, then merge. Tags can still be
  pushed.
- Merge pull requests only like this:
  `gh pr merge N --merge --author-email 92350459+NovaSeele@users.noreply.github.com`. Do not use rebase, squash,
  auto-merge or the web button. Without `--author-email`, GitHub writes the merging account's default commit
  email into the commits it makes:
  - a merge commit or a squash commit gets it as the author;
  - a rebase merge gets it as the committer of every rebased commit.
  This happened even with "Keep my email addresses private" turned on. A merge commit made with
  `--author-email` has that author, and GitHub's own `noreply@github.com` as committer. The branch's commits
  keep their own identities.
- After every merge, check `git log origin/main --format='%ae | %ce' | sort -u`. The only allowed addresses are
  that noreply address and GitHub's own `noreply@github.com`. If any other address shows up, stop merging and
  report it.
- Conventional commit messages, for example `feat(task): add the wait endpoint` or `fix(pool): …`. Keep commits
  small; each one builds.
- Every commit message ends with a blank line and
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Stage the paths you changed explicitly and check `git status` before committing. Nothing is pushed until the
  lead asks the user, and nothing ever goes to `main` directly.
- The GitHub Wiki has a single page, `Home`, which mirrors `docs/usage.md`. In that copy, relative links point at
  `main` on github.com and images point at `raw.githubusercontent.com`. Whenever a change to `docs/usage.md`
  reaches `main`, regenerate the page. Commit it in a clone of `Seelegent.wiki.git` with the noreply identity,
  then push. Never edit the Wiki on the web, because those commits carry the account's default email.
