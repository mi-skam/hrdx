//go:build windows

package holder

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the holder into its own process group and detaches it
// from the TUI's console, so it survives the console window closing.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
}
