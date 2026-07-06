//go:build windows

package main

// autostart_windows.go — "Als Dienst" starten: Autostart bei der Windows-Anmeldung.
//
// Ein echter Windows-Dienst (Session 0) kann keine GUI anzeigen und hat keinen
// Zugriff auf Mikrofon/Zwischenablage des angemeldeten Benutzers — beides
// braucht diese App. Stattdessen wird der klassische Run-Key des Benutzers
// (HKCU\Software\Microsoft\Windows\CurrentVersion\Run) genutzt: die App startet
// damit automatisch bei jeder Anmeldung, ohne Adminrechte und pro Benutzer.
//
// Die Registry ist die einzige Quelle der Wahrheit (kein config.json-Feld):
// so kann der Eintrag nicht mit einem extern gesetzten/geloeschten Autostart
// auseinanderlaufen. Ein vorhandener Eintrag wird beim App-Start mit dem
// aktuellen exe-Pfad neu geschrieben (SetChecked in main.go loest setAutostart
// aus), falls die exe verschoben wurde — das Selbst-Update ersetzt sie an Ort
// und Stelle, aendert den Pfad also nicht.

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const (
	autostartRunKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	autostartValueName = "STT-Support-Assistent"
)

func autostartSupported() bool { return true }

// autostartExePath liefert den aufgeloesten Pfad der laufenden exe.
func autostartExePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, e := filepath.EvalSymlinks(p); e == nil {
		p = resolved
	}
	return p, nil
}

// autostartEnabled prueft, ob der Run-Key-Eintrag existiert.
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autostartValueName)
	return err == nil
}

// setAutostart legt den Run-Key-Eintrag an (Pfad in Anfuehrungszeichen, wegen
// Leerzeichen im Pfad) bzw. entfernt ihn.
func setAutostart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if !enable {
		if err := k.DeleteValue(autostartValueName); err != nil && err != registry.ErrNotExist {
			return err
		}
		Log("Autostart deaktiviert (Run-Key entfernt)")
		return nil
	}

	exe, err := autostartExePath()
	if err != nil {
		return err
	}
	if err := k.SetStringValue(autostartValueName, `"`+exe+`"`); err != nil {
		return err
	}
	Log("Autostart aktiviert: " + exe)
	return nil
}
