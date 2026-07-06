//go:build !windows

package main

import (
	"fyne.io/fyne/v2"
)

func saveWindowPosition(w fyne.Window) {
	// Nicht implementiert auf Nicht-Windows-Systemen
}

func restoreWindowPosition(w fyne.Window) {
	// Nicht implementiert auf Nicht-Windows-Systemen
}

func setWindowSquare(w fyne.Window, square bool) {
	// Fensterrahmen-Ecken sind nur unter Windows (DWM) anpassbar.
}

// ensureSingleInstance/notifyAlreadyRunning: die Mehrfachstart-Sperre ist nur
// unter Windows umgesetzt (Ziel-Plattform, benannter Mutex in sys_windows.go);
// Nicht-Windows-Builds sind reine Entwicklungsumgebungen.
func ensureSingleInstance() bool { return true }

func notifyAlreadyRunning() {}
