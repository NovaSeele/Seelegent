package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

// The test binary doubles as the launcher and as a fake worker CLI, so the end-to-end tests run the real launcher
// and a real process tree without any real CLI or account:
//
//   - "<exe> __launch <dir>" is the launcher (internal/launch.Run), as the daemon runs it;
//   - with FAKECLI_SCRIPT set, the binary is the fake CLI: whatever its arguments, it records them (and its standard
//     input and selected environment variables) to FAKECLI_RECORD, then replays the script;
//   - with FAKECLI_SLEEPER set, it is a grandchild that writes its pid and sleeps (to prove cancel kills the tree).
//
// A script is a recorded stream: every line goes to standard output, except directive lines:
//
//	#!stderr <text>   write text to standard error
//	#!sleep <ms>      pause
//	#!exit <code>     exit now with code
//	#!hang            sleep for five minutes (until canceled)
//	#!spawn           start a sleeper grandchild that writes its pid to FAKECLI_PIDFILE
//	#!echo-env <NAME> print "NAME=<value>" (a non-JSON line)
func TestMain(m *testing.M) {
	switch {
	case len(os.Args) > 2 && os.Args[1] == "__launch":
		if err := launch.Run(context.Background(), os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case os.Getenv("FAKECLI_SLEEPER") != "":
		if f := os.Getenv("FAKECLI_PIDFILE"); f != "" {
			_ = os.WriteFile(f, []byte(strconv.Itoa(os.Getpid())), 0o600)
		}
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	case os.Getenv("FAKECLI_SCRIPT") != "":
		os.Exit(runFakeCLI())
	}
	os.Exit(m.Run())
}

// fakeRecord is what the fake CLI saw.
type fakeRecord struct {
	Args  []string          `json:"args"`
	Stdin string            `json:"stdin"`
	Env   map[string]string `json:"env"`
	Dir   string            `json:"dir"`
}

func runFakeCLI() int {
	stdin, _ := io.ReadAll(os.Stdin)
	if path := os.Getenv("FAKECLI_RECORD"); path != "" {
		rec := fakeRecord{Args: os.Args[1:], Stdin: string(stdin), Env: map[string]string{}}
		rec.Dir, _ = os.Getwd()
		for _, n := range strings.Split(os.Getenv("FAKECLI_ENV"), ",") {
			if v, ok := os.LookupEnv(n); ok && n != "" {
				rec.Env[n] = v
			}
		}
		data, _ := json.Marshal(rec)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 99
		}
	}
	f, err := os.Open(os.Getenv("FAKECLI_SCRIPT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 98
	}
	defer func() { _ = f.Close() }()
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "#!") {
			_, _ = out.WriteString(line + "\n")
			_ = out.Flush()
			continue
		}
		op, arg, _ := strings.Cut(strings.TrimPrefix(line, "#!"), " ")
		switch op {
		case "stderr":
			fmt.Fprintln(os.Stderr, arg)
		case "sleep":
			ms, _ := strconv.Atoi(arg)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "exit":
			code, _ := strconv.Atoi(arg)
			_ = out.Flush()
			return code
		case "hang":
			_ = out.Flush()
			time.Sleep(5 * time.Minute)
		case "spawn":
			exe, _ := os.Executable()
			cmd := exec.Command(exe) //nolint:noctx // the test binary itself, as a sleeper
			cmd.Env = append(withoutVar(os.Environ(), "FAKECLI_SCRIPT"), "FAKECLI_SLEEPER=1")
			if err := cmd.Start(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 97
			}
		case "echo-env":
			_, _ = out.WriteString(arg + "=" + os.Getenv(arg) + "\n")
			_ = out.Flush()
		}
	}
	return 0
}

func withoutVar(env []string, name string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return out
}
