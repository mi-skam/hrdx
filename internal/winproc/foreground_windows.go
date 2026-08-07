//go:build windows

// Package winproc contains the Windows process-tree approximation used by
// both local and holder-owned terminals.
package winproc

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ForegroundName approximates a ConPTY foreground process lookup. Windows has
// no equivalent of a Unix foreground process group, so the best information
// available here is the process tree rooted at the session command.
func ForegroundName(rootPID int) string {
	if rootPID == 0 {
		return ""
	}
	byParent, err := snapshotProcessesByParent()
	if err != nil {
		return ""
	}
	return foregroundName(byParent, uint32(rootPID), processCreationTime)
}

type process struct {
	pid  uint32
	name string
}

// foregroundName follows the newest branch, but returns the first substantive
// process on that branch instead of its deepest descendant. The first process
// is the command the user launched; deeper processes are commonly helpers
// (language servers, search tools, sandboxes, and subprocess shells) and must
// not hide a still-running agent. Shell launchers are transparent so commands
// started through cmd /c, PowerShell, or a nested shell still resolve to the
// actual executable.
func foregroundName(byParent map[uint32][]process, rootPID uint32, created func(uint32) uint64) string {
	pid := rootPID
	for depth := 0; depth < 32; depth++ { // guard against a cyclic snapshot
		child, ok := youngestChild(byParent, pid, created)
		if !ok {
			return ""
		}
		if !isTransparentLauncher(child.name) {
			return child.name
		}
		pid = child.pid
	}
	return ""
}

// isTransparentLauncher identifies processes which commonly exist only to
// start another command. These are deliberately limited to shells and console
// plumbing: treating general runtimes such as node or python as transparent
// would make a top-level custom harness indistinguishable from one of its
// helper descendants without access to its command line.
func isTransparentLauncher(name string) bool {
	switch name {
	case "cmd", "command", "powershell", "pwsh",
		"sh", "bash", "dash", "zsh", "fish", "ksh", "tcsh", "nu",
		"conhost", "openconsole", "winpty-agent":
		return true
	default:
		return false
	}
}

// snapshotProcessesByParent lists every running process's pid and normalized
// executable name, keyed by parent pid, in one syscall-cheap pass.
func snapshotProcessesByParent() (map[uint32][]process, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	byParent := map[uint32][]process{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	for {
		proc := process{pid: entry.ProcessID, name: exeBaseName(windows.UTF16ToString(entry.ExeFile[:]))}
		byParent[entry.ParentProcessID] = append(byParent[entry.ParentProcessID], proc)
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return byParent, nil
}

// youngestChild returns the most recently started direct child of pid. A
// single child skips the extra process-time syscalls, the common case.
func youngestChild(byParent map[uint32][]process, pid uint32, created func(uint32) uint64) (process, bool) {
	children := byParent[pid]
	switch len(children) {
	case 0:
		return process{}, false
	case 1:
		return children[0], true
	}
	best, bestTime := children[0], created(children[0].pid)
	for _, candidate := range children[1:] {
		if candidateTime := created(candidate.pid); candidateTime > bestTime {
			best, bestTime = candidate, candidateTime
		}
	}
	return best, true
}

// processCreationTime returns pid's creation time as a sortable value, or 0
// on failure (which makes an inaccessible process sort as oldest).
func processCreationTime(pid uint32) uint64 {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(handle)
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exit, &kernel, &user); err != nil {
		return 0
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
}

// exeBaseName normalizes Toolhelp names to the lowercase bare command names
// used by hrdx on Unix.
func exeBaseName(name string) string {
	if idx := strings.LastIndexAny(name, `\/`); idx >= 0 {
		name = name[idx+1:]
	}
	for _, ext := range []string{".exe", ".com", ".bat", ".cmd"} {
		if len(name) > len(ext) && strings.EqualFold(name[len(name)-len(ext):], ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	return strings.ToLower(name)
}
