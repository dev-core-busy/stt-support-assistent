//go:build !windows
// +build !windows

package main

import (
	"os/exec"
)

func setSilent(cmd *exec.Cmd) {
	// Auf Nicht-Windows Systemen (wie Linux) ist kein spezielles Flag
	// für die Ausführung im Hintergrund nötig/möglich auf diese Weise.
}

// openPath oeffnet eine lokale Datei (oder URL) mit der Standardanwendung des
// Systems. Auf Linux via xdg-open.
func openPath(path string) error {
	cmd := exec.Command("xdg-open", path)
	setSilent(cmd)
	return cmd.Start()
}
