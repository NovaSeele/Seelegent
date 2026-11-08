package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// OldPIDFile is the old Node version's pid file in the data folder. This version never writes or reads it (it uses
// run/daemon.port and run/daemon.lock), and nothing removes it: doctor names it so the user can (final verification
// N5).
const OldPIDFile = "daemon.pid"

// oldPIDFile checks for the old version's daemon.pid (ok is false when there is none). Doctor deletes nothing: a pid
// file whose process is gone is only clutter, and while the old daemon still runs the old version's own stop needs
// it. A pid of a running process is not proof of the old daemon either (pids are reused), so the running processes
// tell which it is when they can be listed.
func oldPIDFile(ctx context.Context, home string, list func(context.Context) ([]platform.Process, error)) (Check,
	bool) {
	c := Check{Name: "old_pid_file", Status: Warn}
	path := filepath.Join(home, OldPIDFile)
	data, _, err := fsutil.ReadRegular(path, 64)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return c, false
	case err != nil:
		c.Message = fmt.Sprintf("the old Node version's %s cannot be read: %v; this version does not use it", path, err)
		c.Fix = "delete it once no old Seelegent daemon runs; doctor deletes nothing"
		return c, true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		c.Message = fmt.Sprintf("%s of the old Node version holds no pid; this version does not use it", path)
		c.Fix = "delete it; doctor deletes nothing"
		return c, true
	}
	if !platform.ProcessAlive(pid) {
		c.Message = fmt.Sprintf("%s of the old Node version names process %d, which is not running; this version "+
			"does not use it", path, pid)
		c.Fix = "delete it; doctor deletes nothing"
		return c, true
	}
	if list != nil {
		if procs, err := list(ctx); err == nil {
			for _, p := range procs {
				if old, _ := oldNodeArgs(p.Args); old && p.PID == pid {
					c.Message = fmt.Sprintf("the old Node daemon named in %s still runs (pid %d)", path, pid)
					c.Fix = "stop it with the old version's seelegent stop (or end the process), then delete the file"
					return c, true
				}
			}
			c.Message = fmt.Sprintf("%s of the old Node version names process %d, which runs but is not the old "+
				"daemon (the pid was reused); this version does not use the file", path, pid)
			c.Fix = "delete it; doctor deletes nothing"
			return c, true
		}
	}
	c.Message = fmt.Sprintf("%s of the old Node version names process %d, which is running; this version does not "+
		"use the file", path, pid)
	c.Fix = "if that is not the old Seelegent daemon, delete the file; otherwise stop the old daemon first"
	return c, true
}
