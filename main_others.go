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

func applyCrispWindowIcon(w fyne.Window) {
	// Natives Titelleisten-Icon (WM_SETICON) gibt es nur unter Windows.
}

func moveWindowNear(child fyne.Window, parent fyne.Window) {
	// Native Fensterpositionierung nur unter Windows.
}

// copyToClipboardRich: die Rich-Text-Zwischenablage (HTML-Format) ist nur
// unter Windows umgesetzt - hier Rueckfall auf reinen Text, damit der
// Linux-Entwicklungs-Build kompiliert.
func copyToClipboardRich(plainText, markdown string) {
	if mainWin != nil {
		mainWin.Clipboard().SetContent(plainText)
	}
}

// ensureSingleInstance/notifyAlreadyRunning: die Mehrfachstart-Sperre ist nur
// unter Windows umgesetzt (Ziel-Plattform, benannter Mutex in sys_windows.go);
// Nicht-Windows-Builds sind reine Entwicklungsumgebungen.
func ensureSingleInstance() bool { return true }

func notifyAlreadyRunning() {}

// Autostart (Windows-Run-Key, autostart_windows.go) existiert nur unter
// Windows; auf anderen Plattformen wird die Checkbox gar nicht angezeigt.
func autostartSupported() bool { return false }

func autostartEnabled() bool { return false }

func setAutostart(enable bool) error { return nil }
