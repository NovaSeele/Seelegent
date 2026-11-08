// Package bench is Seelegent's resource benchmark (M19). It builds the seelegent binary and its companion launcher
// seelegent-launch, starts a daemon on a temp SEELEGENT_HOME with fake agy accounts (quota readers on), and samples
// the daemon's whole process tree while it idles or runs a load of fake workers. The code is behind the `bench`
// build tag, so `go test ./...` never runs it:
//
//	make bench                                   # idle, 10 minutes
//	make bench BENCH_ARGS="-scenario load -duration 5m"
//	go test -tags bench -c -o bin/seelegent-bench ./scripts/bench && bin/seelegent-bench -h
//
// The test binary is also the fake agy CLI (internal/testutil/fakecli) the daemon's workers and quota readers run.
// Results (JSON, a markdown table and the pprof profiles) go to -out, by default %TEMP%/seelegent-bench/<time>.
// See docs/development.md, "Benchmark".
package bench
