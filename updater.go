package main

// updater.go — Selbst-Aktualisierung ueber GitHub-Releases.
//
// Beim Start prueft die App still im Hintergrund, ob im GitHub-Repo eine neuere
// Release-Version vorliegt (Tag "vX.Y.Z"). Ist das der Fall, wird nach einer
// Rueckfrage die neue stt-app.exe heruntergeladen, die laufende Datei per
// Umbenenn-Trick ersetzt und die App automatisch neu gestartet.
//
// AppVersion ist die EINZIGE Quelle der Wahrheit fuer die Versionsnummer: sie
// steht im Fenstertitel (updateWindowTitle) und dient hier als Vergleichsbasis.
// Das Release-Skript (release.sh) liest denselben Wert und taggt das Release
// als "v<AppVersion>", damit die ausgelieferte Binary immer die passende
// Version meldet.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// AppVersion — aktuelle Programmversion (ohne "v"-Praefix). Vor jedem Release
// hochzaehlen (und in README.md spiegeln), danach release.sh ausfuehren.
const AppVersion = "0.8.1"

// githubRepo — "Besitzer/Repo" fuer die GitHub-Releases-API.
const githubRepo = "dev-core-busy/stt-support-assistent"

// ghRelease bildet die relevanten Felder der GitHub-Releases-API ab.
type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// runUpdateCheck laeuft als Goroutine beim Start. Er prueft nur unter Windows,
// da das Release-Asset eine Windows-.exe ist; unter Linux (Build-Rechner) ist
// Selbst-Update sinnlos und wird uebersprungen.
func runUpdateCheck(win fyne.Window) {
	// Reste eines vorherigen Updates aufraeumen (die alte, umbenannte Binary).
	cleanupOldBinary()

	if runtime.GOOS != "windows" {
		return
	}

	// Start nicht mit dem Netzwerk-Call und einem sofortigen Dialog stoeren,
	// waehrend ggf. noch Dependencies geladen werden.
	time.Sleep(4 * time.Second)

	rel, err := fetchLatestRelease()
	if err != nil {
		Log("Update-Check fehlgeschlagen: " + err.Error())
		return
	}
	if !isNewerVersion(rel.TagName, AppVersion) {
		Log(fmt.Sprintf("Update-Check: bereits aktuell (v%s, neuestes Release %s)", AppVersion, rel.TagName))
		return
	}
	asset := pickExeAsset(rel)
	if asset == "" {
		Log("Update: neues Release " + rel.TagName + " hat kein .exe-Asset")
		return
	}

	Log(fmt.Sprintf("Update verfuegbar: v%s -> %s", AppVersion, rel.TagName))
	fyne.Do(func() {
		msg := fmt.Sprintf(
			"Eine neue Version ist verfuegbar:\n\n    Installiert:  v%s\n    Verfuegbar:   %s\n\nJetzt herunterladen, installieren und die App neu starten?",
			AppVersion, rel.TagName)
		showConfirm("Update verfuegbar", msg, func(ok bool) {
			if !ok {
				return
			}
			startDownloadAndRestart(win, asset, rel.TagName)
		}, win)
	})
}

// fetchLatestRelease holt das neueste (nicht-Draft-)Release ueber die GitHub-API.
func fetchLatestRelease() (*ghRelease, error) {
	url := "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "stt-support-assistent-updater") // GitHub verlangt einen User-Agent

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub-API: %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// pickExeAsset waehlt bevorzugt das Asset "stt-app.exe", sonst das erste .exe.
func pickExeAsset(rel *ghRelease) string {
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, "stt-app.exe") {
			return a.URL
		}
	}
	for _, a := range rel.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
			return a.URL
		}
	}
	return ""
}

// isNewerVersion liefert true, wenn latest (z.B. "v0.8.0") groesser als current
// ("0.7.0") ist. Vergleich rein numerisch ueber major.minor.patch.
func isNewerVersion(latest, current string) bool {
	l := parseVersion(latest)
	c := parseVersion(current)
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseVersion zerlegt "v1.2.3" / "1.2.3-rc1" in [3]int{1,2,3}. Fehlende oder
// nicht-numerische Teile werden als 0 gewertet.
func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 { // Pre-Release-/Build-Suffix abschneiden
		v = v[:i]
	}
	var out [3]int
	for i, s := range strings.Split(v, ".") {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		out[i] = n
	}
	return out
}

// startDownloadAndRestart zeigt einen nicht abbrechbaren Fortschrittsdialog und
// fuehrt das Update im Hintergrund aus. applyUpdate kehrt im Erfolgsfall nicht
// zurueck (os.Exit nach Neustart); nur im Fehlerfall wird der Dialog ersetzt.
func startDownloadAndRestart(win fyne.Window, url, tag string) {
	prog := widget.NewProgressBarInfinite()
	content := container.NewVBox(
		widget.NewLabel("Lade "+tag+" herunter und installiere...\nDie App startet anschliessend automatisch neu."),
		prog,
	)
	d := dialog.NewCustomWithoutButtons("Update wird installiert", content, win)
	d.Show()

	go func() {
		if err := applyUpdate(url); err != nil {
			Log("Update fehlgeschlagen: " + err.Error())
			fyne.Do(func() {
				d.Hide()
				showErr(fmt.Errorf("Update fehlgeschlagen:\n%v", err), win)
			})
		}
	}()
}

// applyUpdate laedt die neue Binary herunter, ersetzt die laufende Datei per
// Umbenenn-Trick (unter Windows laesst sich die laufende .exe umbenennen, aber
// nicht ueberschreiben) und startet die App neu. Im Erfolgsfall beendet die
// Funktion den Prozess (os.Exit) und kehrt nicht zurueck.
func applyUpdate(url string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, e := filepath.EvalSymlinks(exePath); e == nil {
		exePath = resolved
	}

	newPath := exePath + ".new"
	oldPath := exePath + ".old"

	// 1) Herunterladen in eine Nebendatei (downloadFile aus setup_manager.go).
	if err := downloadFile(url, newPath, nil); err != nil {
		os.Remove(newPath)
		return err
	}
	_ = os.Chmod(newPath, 0o755) // fuer Nicht-Windows-Faelle

	// 2) Laufende exe zur Seite raeumen, neue an ihren Platz.
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("laufende Datei konnte nicht umbenannt werden: %w", err)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath) // Rollback
		return fmt.Errorf("neue Datei konnte nicht aktiviert werden: %w", err)
	}

	// 3) Sauber herunterfahren (Server + Config), neuen Prozess starten, Ende.
	stopAllServers()
	SaveConfig()

	cmd := exec.Command(exePath)
	cmd.Dir = filepath.Dir(exePath)
	setSilent(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Neustart fehlgeschlagen: %w", err)
	}
	Log("Update installiert, starte neu -> " + exePath)
	os.Exit(0)
	return nil
}

// cleanupOldBinary entfernt die beim letzten Update umbenannte alte Datei. Der
// erste Versuch direkt nach dem Neustart kann fehlschlagen, wenn der alte
// Prozess die Datei noch haelt — dann greift der naechste Start.
func cleanupOldBinary() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, e := filepath.EvalSymlinks(exePath); e == nil {
		exePath = resolved
	}
	_ = os.Remove(exePath + ".old")
	_ = os.Remove(exePath + ".new") // evtl. abgebrochener Download
}
