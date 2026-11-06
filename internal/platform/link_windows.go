//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// mklinkTimeout bounds the cmd.exe call that creates a junction.
const mklinkTimeout = 10 * time.Second

func linkDir(src, dst string) error {
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	// Symbolic links need developer mode or an elevated token; junctions do not.
	if strings.ContainsAny(src+dst, `"%`) {
		return fmt.Errorf("%w: unsupported characters in path", errNoLink)
	}
	ctx, cancel := context.WithTimeout(context.Background(), mklinkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe")
	HideWindow(cmd)
	sysProcAttr(cmd).CmdLine = `cmd.exe /d /s /c "mklink /J ` + quoted(dst) + " " + quoted(src) + `"`
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: mklink /J: %w: %s", errNoLink, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func linkFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	if err := os.Symlink(src, dst); err != nil {
		return fmt.Errorf("%w: %w", errNoLink, err)
	}
	return nil
}
