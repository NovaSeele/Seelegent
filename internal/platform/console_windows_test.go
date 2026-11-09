package platform

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// runConsoleRole runs the Windows-only console helper roles:
//   - console: writes consoleState (whether the process has a console, and its window) to the file named last;
//   - console-children: does what console does, then starts three console helpers, with HideWindow, as a
//     StartTree root and with HideWindowShared, which write their state to the files named last with ".hidden",
//     ".tree" and ".shared" appended, and waits for them (it is started without a console: the case where every
//     hidden start must give its child a hidden console of its own);
//   - console-guard MODE OUT: a stand-in for the daemon, started with a hidden console of its own. It sets its
//     code page to 437, then starts user-command-style children (StartTreeWith, sharing its console when MODE is
//     "shared") that run `chcp 65001` and send CTRL_BREAK to group 0 and to their own process id, and writes
//     to OUT the code page it is left with and which of the events reached it as an interrupt;
//   - ctrl-break TARGET: sends CTRL_BREAK_EVENT to process group 0 (TARGET "0") or to its own process id.
func runConsoleRole(role string) bool {
	out := os.Args[len(os.Args)-1]
	switch role {
	case "console":
		if err := os.WriteFile(out, []byte(consoleState()), 0o600); err != nil {
			os.Exit(3)
		}
		return true
	case "console-children":
		consoleChildren(out)
		return true
	case "console-guard":
		consoleGuard(os.Args[len(os.Args)-2], out)
		return true
	case "ctrl-break":
		group := uint32(0)
		if out != "0" {
			group = uint32(os.Getpid())
		}
		// Ignored here, so this process outlives its own event and exits normally.
		signal.Ignore(os.Interrupt)
		if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, group); err != nil {
			os.Exit(3)
		}
		time.Sleep(200 * time.Millisecond)
		return true
	}
	return false
}

func consoleChildren(out string) {
	if err := os.WriteFile(out, []byte(consoleState()), 0o600); err != nil {
		os.Exit(3)
	}
	env := func(cmd *exec.Cmd) *exec.Cmd {
		cmd.Env = append(os.Environ(), roleEnv+"=console")
		return cmd
	}
	hidden := env(exec.Command(selfExe(), out+".hidden")) //nolint:noctx // a test helper's own child
	HideWindow(hidden)
	if err := hidden.Run(); err != nil {
		os.Exit(4)
	}
	tree, err := StartTree(env(exec.Command(selfExe(), out+".tree"))) //nolint:noctx // a test helper's own child
	if err != nil {
		os.Exit(5)
	}
	werr := tree.Wait()
	_ = tree.Close()
	if werr != nil {
		os.Exit(6)
	}
	shared := env(exec.Command(selfExe(), out+".shared")) //nolint:noctx // a test helper's own child
	HideWindowShared(shared)
	if err := shared.Run(); err != nil {
		os.Exit(7)
	}
}

func consoleGuard(mode, out string) {
	opt := TreeOptions{ShareConsole: mode == "shared"}
	if windows.SetConsoleCP(437) != nil || windows.SetConsoleOutputCP(437) != nil {
		os.Exit(3)
	}
	interrupts := make(chan os.Signal, 8)
	signal.Notify(interrupts, os.Interrupt)
	run := func(cmd *exec.Cmd) {
		tree, err := StartTreeWith(cmd, opt)
		if err != nil {
			os.Exit(4)
		}
		_ = tree.Wait() // a child that a ctrl event ended still counts
		_ = tree.Close()
	}
	// reached waits for an interrupt: up to 5 s where one is expected (a slow machine still sees it), 1 s where
	// none is (each one is a second of waiting).
	reached := func(expected bool) bool {
		wait := time.Second
		if expected {
			wait = 5 * time.Second
		}
		select {
		case <-interrupts:
			for len(interrupts) > 0 {
				<-interrupts
			}
			return true
		case <-time.After(wait):
			return false
		}
	}
	run(exec.Command("cmd.exe", "/d", "/c", "chcp 65001 >nul")) //nolint:noctx // a test helper's own child
	in, inErr := windows.GetConsoleCP()
	outCP, outErr := windows.GetConsoleOutputCP()
	if inErr != nil || outErr != nil {
		os.Exit(5)
	}
	breaker := func(target string) *exec.Cmd {
		cmd := exec.Command(selfExe(), target) //nolint:noctx // a test helper's own child
		cmd.Env = append(os.Environ(), roleEnv+"=ctrl-break")
		return cmd
	}
	run(breaker("0"))
	group0 := reached(opt.ShareConsole)
	run(breaker("self"))
	self := reached(false)
	result := fmt.Sprintf("cp %d/%d group0 %v self %v", in, outCP, group0, self)
	if err := os.WriteFile(out, []byte(result), 0o600); err != nil {
		os.Exit(6)
	}
}

// consoleState is "none" for a process without a console, else "console" and its window handle (0: hidden).
func consoleState() string {
	if !hasConsole() {
		return "none"
	}
	win, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	return fmt.Sprintf("console %#x", win)
}

// TestHiddenStartsHaveNoConsoleWindow: no way Seelegent starts a hidden process (HideWindow, HideWindowShared,
// the detached daemon and launcher, a process tree) opens a console window, so no window flashes when a worker
// or a git call starts (old L14 and L19). HideWindow and StartTree give the child a hidden console of its own;
// HideWindowShared and StartTreeWith ShareConsole (fixed helpers) share this process's console when it has one,
// in a process group of their own, and fall back to a hidden console of their own when it has none; the launcher
// (StartDetachedNoConsole) gets no console.
func TestHiddenStartsHaveNoConsoleWindow(t *testing.T) {
	dir := t.TempDir()
	state := func(name string) string {
		t.Helper()
		mark, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(mark)
	}
	check := func(name string, cmd *exec.Cmd, wait func() error, want uint32, wantState string) {
		t.Helper()
		attr := cmd.SysProcAttr
		if attr == nil || !attr.HideWindow || attr.CreationFlags&want != want {
			t.Errorf("%s: SysProcAttr %+v, want HideWindow and flags %#x", name, attr, want)
		}
		if err := wait(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := state(name); got != wantState {
			t.Errorf("%s: console %q, want %q", name, got, wantState)
		}
	}
	const own = "console 0x0"
	inherited, sharedFlag := consoleState(), uint32(windows.CREATE_NEW_PROCESS_GROUP)
	t.Logf("this process: %s", inherited)
	if inherited == "none" {
		inherited, sharedFlag = own, windows.CREATE_NO_WINDOW
	}

	hidden := helper("console", filepath.Join(dir, "hidden"))
	HideWindow(hidden)
	if err := hidden.Start(); err != nil {
		t.Fatal(err)
	}
	check("hidden", hidden, hidden.Wait, windows.CREATE_NO_WINDOW, own)

	shared := helper("console", filepath.Join(dir, "shared"))
	HideWindowShared(shared)
	if err := shared.Start(); err != nil {
		t.Fatal(err)
	}
	check("shared", shared, shared.Wait, sharedFlag, inherited)

	detached, err := StartDetached(func() *exec.Cmd { return helper("console", filepath.Join(dir, "detached")) })
	if err != nil {
		t.Fatal(err)
	}
	check("detached", detached, detached.Wait, windows.CREATE_NO_WINDOW|windows.CREATE_NEW_PROCESS_GROUP, own)

	cmd := helper("console", filepath.Join(dir, "tree"))
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tree.Close() })
	check("tree", cmd, tree.Wait, windows.CREATE_NO_WINDOW, own)

	cmd = helper("console", filepath.Join(dir, "tree-shared"))
	sharedTree, err := StartTreeWith(cmd, TreeOptions{ShareConsole: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sharedTree.Close() })
	check("tree-shared", cmd, sharedTree.Wait, sharedFlag, inherited)

	// The launcher has no console, and what it starts hidden gets a hidden console of its own.
	launcher, err := StartDetachedNoConsole(func() *exec.Cmd {
		return helper("console-children", filepath.Join(dir, "children"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if attr := launcher.SysProcAttr; attr.CreationFlags&windows.CREATE_NO_WINDOW != 0 {
		t.Errorf("no console: flags %#x, want no CREATE_NO_WINDOW", attr.CreationFlags)
	}
	check("children", launcher, launcher.Wait, windows.DETACHED_PROCESS|windows.CREATE_NEW_PROCESS_GROUP, "none")
	for _, name := range []string{"children.hidden", "children.tree", "children.shared"} {
		if got := state(name); got != own {
			t.Errorf("%s: console %q, want a hidden console of its own", name, got)
		}
	}
}

// TestUserCommandsCannotReachTheDaemonConsole (E1-N1): a command line from a user, a repository or a worker
// (gate commands, hooks, prechecks, worker trees: StartTree's default) runs in a console of its own, so neither
// `chcp 65001` nor a CTRL_BREAK broadcast from it reaches the daemon. The "shared" run shows the probe works:
// a child sharing the console does change the code page and does deliver a broadcast; its own process group
// still keeps an event aimed at its own process id from the daemon. The daemon is a helper process with a hidden
// console of its own, so nothing here touches the test's console.
func TestUserCommandsCannotReachTheDaemonConsole(t *testing.T) {
	for mode, want := range map[string]string{
		"own":    "cp 437/437 group0 false self false",
		"shared": "cp 65001/65001 group0 true self false",
	} {
		t.Run(mode, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "guard")
			guard := helper("console-guard", mode, out)
			HideWindow(guard)
			if err := guard.Run(); err != nil {
				t.Fatalf("guard: %v", err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(got)) != want {
				t.Errorf("%s: got %q, want %q", mode, got, want)
			}
		})
	}
}
