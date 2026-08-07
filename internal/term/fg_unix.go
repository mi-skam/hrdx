//go:build !windows

package term

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/unix"
)

// foregroundName resolves the foreground process group of the PTY's slave
// side; the group leader is the running command.
func foregroundName(p pty.Pty, _ int) string {
	up, ok := p.(pty.UnixPty)
	if !ok {
		return ""
	}
	name := ""
	_ = up.Control(func(fd uintptr) {
		if pgid, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil && pgid > 0 {
			name = processName(pgid)
		}
	})
	return name
}

// processName resolves a pid to its command name. Linux reads /proc, other
// unixes shell out to ps; both paths return the bare name without path.
func processName(pid int) string {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		return strings.TrimSpace(string(data))
	}
	output, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(output))
	// Login shells report as "-zsh"; strip the marker before basing.
	return strings.TrimPrefix(filepath.Base(name), "-")
}
