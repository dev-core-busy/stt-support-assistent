//go:build windows
// +build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"
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

// singleInstanceMutex haelt den benannten Mutex bis zum Prozessende offen
// (bewusst nie geschlossen).
var singleInstanceMutex uintptr

const errorAlreadyExists = 183 // winerror.h: ERROR_ALREADY_EXISTS

// ensureSingleInstance verhindert den Mehrfachstart der App: ein benannter
// Windows-Mutex (je Anmelde-Sitzung) existiert, solange die erste Instanz
// laeuft - Windows raeumt ihn beim Prozessende automatisch ab (auch nach
// einem Absturz, keine Lock-Datei-Leichen). false = es laeuft bereits eine
// Instanz; der Aufrufer beendet sich dann (s. main).
func ensureSingleInstance() bool {
	name, _ := syscall.UTF16PtrFromString("stt-support-assistent-single-instance")
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return true // Mutex-Anlage fehlgeschlagen: Start lieber nicht blockieren
	}
	singleInstanceMutex = h
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		return false
	}
	return true
}

// notifyAlreadyRunning zeigt den Mehrfachstart-Hinweis nativ per MessageBox -
// die Fyne-App existiert zu diesem Zeitpunkt noch nicht.
func notifyAlreadyRunning() {
	title, _ := syscall.UTF16PtrFromString("SpeechToText und Support Assistent")
	text, _ := syscall.UTF16PtrFromString("Das Programm läuft bereits (Mehrfachstart ist deaktiviert).")
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x40) // MB_OK | MB_ICONINFORMATION
}
