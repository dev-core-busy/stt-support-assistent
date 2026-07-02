//go:build windows
// +build windows

package main

import (
	"os/exec"
	"syscall"
)

func setSilent(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = 0x08000000 // CREATE_NO_WINDOW
}

// openPath oeffnet eine lokale Datei (oder URL) mit der Standardanwendung des
// Systems. Auf Windows via "cmd /c start" (leerer erster Parameter = Fenster-
// titel, damit Pfade mit Leerzeichen nicht als Titel fehlinterpretiert werden).
func openPath(path string) error {
	cmd := exec.Command("cmd", "/c", "start", "", path)
	setSilent(cmd)
	return cmd.Start()
}
