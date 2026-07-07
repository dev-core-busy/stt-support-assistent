package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	whisperModelURL = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin"

	// Modell-Upgrade (TODO.md Punkt 1): large-v3-turbo quantisiert (q5_0,
	// ~550 MB) - deutlich besseres Deutsch als whisper-base, nahezu Echtzeit
	// auf moderner CPU. whisper-base bleibt als Rueckfall installiert (und
	// aktiv, solange der turbo-Download noch fehlt/laeuft).
	whisperTurboModelURL  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo-q5_0.bin"
	whisperTurboModelFile = "whisper-large-v3-turbo-q5_0.bin"

	// Final, manuell verifizierte Direktlinks (Stand 15. April 2026)
	whisperWinURL = "https://github.com/ggml-org/whisper.cpp/releases/download/v1.8.4/whisper-bin-x64.zip"
	llamaWinURL   = "https://github.com/ggml-org/llama.cpp/releases/download/b9145/llama-b9145-bin-win-cpu-x64.zip"
)

// localWhisperModelPath liefert das Modell fuer die lokale Erkennung:
// bevorzugt large-v3-turbo (q5_0), Rueckfall whisper-base (z.B. solange der
// turbo-Download noch nicht abgeschlossen ist).
func localWhisperModelPath() string {
	turbo := filepath.Join(exeDir, "models", whisperTurboModelFile)
	if _, err := os.Stat(turbo); err == nil {
		return turbo
	}
	return filepath.Join(exeDir, "models", "whisper-base.bin")
}

// localModel beschreibt ein lokal nutzbares Gemma-Modell samt Auto-Download-Quelle
// und (optionalem) Multimodal-Projektor für die Gemma-Native-STT.
type localModel struct {
	File       string // lokaler GGUF-Dateiname (entspricht config.LocalGemmaModel)
	Label      string // Kurzbeschreibung (Tooltip/Doku)
	URL        string // Download-URL des Modells
	MmprojFile string // lokaler mmproj-Dateiname ("" = kein multimodaler Projektor)
	MmprojURL  string // Download-URL des mmproj
}

// knownLocalModels: auto-ladbare lokale Modelle. Erscheinen im Dropdown
// 'Lokales Modell' und werden bei Auswahl bei Bedarf heruntergeladen.
var knownLocalModels = []localModel{
	{
		File:       "gemma-4-e2b-it.gguf",
		Label:      "Gemma 4 E2B (Q4 – klein & schnell)",
		URL:        "https://huggingface.co/bartowski/google_gemma-4-E2B-it-GGUF/resolve/main/google_gemma-4-E2B-it-Q4_K_M.gguf",
		MmprojFile: "mmproj-gemma-4.gguf",
		MmprojURL:  "https://huggingface.co/bartowski/google_gemma-4-E2B-it-GGUF/resolve/main/mmproj-google_gemma-4-E2B-it-bf16.gguf",
	},
	{
		File:       "gemma-4-12b-it-q8.gguf",
		Label:      "Gemma 4 12B (Q8 – groß & genau, ~13 GB)",
		URL:        "https://huggingface.co/bartowski/gemma-4-12B-it-GGUF/resolve/main/gemma-4-12B-it-Q8_0.gguf",
		MmprojFile: "mmproj-gemma-4-12b.gguf",
		MmprojURL:  "https://huggingface.co/bartowski/gemma-4-12B-it-GGUF/resolve/main/mmproj-gemma-4-12B-it-bf16.gguf",
	},
}

func findLocalModel(file string) *localModel {
	for i := range knownLocalModels {
		if knownLocalModels[i].File == file {
			return &knownLocalModels[i]
		}
	}
	return nil
}

// modelFileForSymbol mappt die UI-Symbole der Pulldowns auf den lokalen Dateinamen.
func modelFileForSymbol(sym string) string {
	switch sym {
	case "e2b":
		return "gemma-4-e2b-it.gguf"
	case "12b":
		return "gemma-4-12b-it-q8.gguf"
	}
	return ""
}

// Label↔Symbol-Mapping für die Pulldowns (Nachbearbeitung/Analyse).
func modelLabelFromSymbol(sym string) string {
	switch sym {
	case "none":
		return "ohne"
	case "e2b":
		return "Gemma 4 E2B"
	case "12b":
		return "Gemma 4 12B"
	case "remote":
		return "remote LLM"
	}
	return "Gemma 4 E2B"
}

func modelSymbolFromLabel(label string) string {
	switch label {
	case "ohne":
		return "none"
	case "Gemma 4 E2B":
		return "e2b"
	case "Gemma 4 12B":
		return "12b"
	case "remote LLM":
		return "remote"
	}
	return "e2b"
}

// localModelExists prüft, ob die GGUF-Datei eines lokalen Modells bereits in
// ./models/ liegt. Leerer Dateiname (z.B. "none"/"remote") gilt als "nicht
// lokal" – dort ist ohnehin kein Download nötig.
func localModelExists(file string) bool {
	if file == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(exeDir, "models", file))
	return err == nil
}

// ensureLocalModel lädt das angegebene Modell herunter, falls noch nicht vorhanden.
// Der Multimodal-Projektor wird NICHT geladen (nur Text-Nutzung – siehe startInstance).
// Unbekannte Dateinamen werden übersprungen.
func ensureLocalModel(file string, progress func(string, float64)) error {
	m := findLocalModel(file)
	if m == nil {
		progress(fmt.Sprintf("WARNUNG: '%s' ist unbekannt – bitte manuell in ./models/ ablegen.", file), 1.0)
		return nil
	}
	modelPath := filepath.Join(exeDir, "models", m.File)
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		if err := downloadFile(m.URL, modelPath, func(p float64) {
			progress(fmt.Sprintf("Lade %s … %.0f%%", m.File, p*100), p)
		}); err != nil {
			return err
		}
	} else {
		progress(fmt.Sprintf("%s (Lokal)", m.File), 1.0)
	}
	return nil
}

func EnsureDependencies(progress func(string, float64)) error {
	os.MkdirAll(filepath.Join(exeDir, "models"), os.ModePerm)
	os.MkdirAll(filepath.Join(exeDir, "libs"), os.ModePerm)

	// 1. Whisper Modelle: base (kleiner Rueckfall) + large-v3-turbo q5_0
	// (Standard fuer die lokale Erkennung, s. localWhisperModelPath).
	whisperPath := filepath.Join(exeDir, "models", "whisper-base.bin")
	if _, err := os.Stat(whisperPath); os.IsNotExist(err) {
		if err := downloadFile(whisperModelURL, whisperPath, func(p float64) {
			progress("Lade Whisper-Modell...", p)
		}); err != nil {
			return err
		}
	} else {
		progress("Whisper-Modell (Lokal)", 1.0)
	}
	turboPath := filepath.Join(exeDir, "models", whisperTurboModelFile)
	if _, err := os.Stat(turboPath); os.IsNotExist(err) {
		if err := downloadFile(whisperTurboModelURL, turboPath, func(p float64) {
			progress(fmt.Sprintf("Lade Whisper large-v3-turbo … %.0f%%", p*100), p)
		}); err != nil {
			// Nicht fatal: whisper-base ist vorhanden, die Erkennung laeuft
			// damit weiter; naechster App-Start versucht den Download erneut.
			Log("Whisper-turbo-Download fehlgeschlagen: " + err.Error())
			progress("Whisper turbo-Download fehlgeschlagen - nutze base", 1.0)
		}
	} else {
		progress("Whisper-Modell turbo (Lokal)", 1.0)
	}

	// 2. Lokale Gemma-Modelle werden bewusst NICHT mehr beim Start geladen
	//    (früher: pauschaler Download der in config gewählten Modelle). Statt
	//    dessen "download on demand": erst wenn ein lokales Modell in den
	//    Einstellungen gewählt wird und noch fehlt, wird es nach Rückfrage
	//    geladen (siehe selectLocalModel in main.go). Erst danach lässt sich die
	//    Auswahl speichern, d.h. eine gespeicherte Auswahl impliziert ein lokal
	//    vorhandenes Modell.

	// 3. Binaries für Windows. whisper-server.exe steckt im selben Zip wie
	// whisper-cli.exe (extractFromZip entpackt ALLE .exe/.dll) - der eigene
	// ensureBinary-Aufruf greift daher nur bei Alt-Installationen, denen die
	// Datei noch fehlt.
	if runtime.GOOS == "windows" {
		if err := ensureBinary(whisperWinURL, "whisper-cli.exe", progress); err != nil {
			return err
		}
		if err := ensureBinary(whisperWinURL, "whisper-server.exe", progress); err != nil {
			return err
		}
		if err := ensureBinary(llamaWinURL, "llama-cli.exe", progress); err != nil {
			return err
		}
	}

	return nil
}

func ensureBinary(url, fileName string, progress func(string, float64)) error {
	targetPath := filepath.Join(exeDir, "libs", fileName)
	if _, err := os.Stat(targetPath); err == nil {
		progress(fileName+" (Lokal)", 1.0)
		return nil
	}

	tempZip := filepath.Join(exeDir, fileName+".zip")
	if err := downloadFile(url, tempZip, func(p float64) {
		progress("Lade "+fileName+"...", p)
	}); err != nil {
		return err
	}
	defer os.Remove(tempZip)

	progress("Entpacke "+fileName+"...", 0.5)
	return extractFromZip(tempZip)
}

func downloadFile(url, dest string, progress func(float64)) error {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Antigravity-STT-App/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Download fehlgeschlagen (HTTP %d): %s", resp.StatusCode, url)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	// Progress Tracking
	size := resp.ContentLength
	var downloaded int64

	buffer := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			out.Write(buffer[:n])
			downloaded += int64(n)
			if size > 0 && progress != nil {
				progress(float64(downloaded) / float64(size))
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func extractFromZip(zipFile string) error {
	r, err := zip.OpenReader(zipFile)
	if err != nil {
		return fmt.Errorf("ZIP konnte nicht geöffnet werden (%v). Datei ist evtl. korrupt.", err)
	}
	defer r.Close()

	for _, f := range r.File {
		// Wir extrahieren alle .exe und .dll Dateien
		isExe := strings.HasSuffix(strings.ToLower(f.Name), ".exe")
		isDll := strings.HasSuffix(strings.ToLower(f.Name), ".dll")

		if isExe || isDll {
			rc, err := f.Open()
			if err != nil {
				return err
			}

			// Wir flachen die Struktur ab (alles direkt nach libs/)
			baseName := filepath.Base(f.Name)
			destPath := filepath.Join(exeDir, "libs", baseName)

			out, err := os.Create(destPath)
			if err != nil {
				rc.Close()
				return err
			}

			_, err = io.Copy(out, rc)
			out.Close()
			rc.Close()

			if err != nil {
				return err
			}
			fmt.Printf("   -> Entpackt: %s\n", baseName)
		}
	}
	return nil
}
