//go:build windows

package term

import (
	"github.com/aymanbagabas/go-pty"
	"github.com/patriceckhart/hrdx/internal/winproc"
)

func foregroundName(_ pty.Pty, rootPID int) string {
	return winproc.ForegroundName(rootPID)
}
