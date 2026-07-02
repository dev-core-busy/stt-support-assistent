# Projektdokumentation: Portable High-Accuracy STT-App

## Übersicht
Diese Anwendung ist eine leistungsstarke, portable Windows-Lösung für Speech-to-Text (STT), die lokale KI-Modelle nutzt, um höchste Datenschutzstandards und Präzision zu gewährleisten. Sie kombiniert **OpenAI Whisper** für die Spracherkennung mit **Google Gemma 4** für die intelligente Textverarbeitung und Analyse. Alle benötigten Komponenten werden beim ersten Start automatisch heruntergeladen; danach läuft die App vollständig offline.

---

## Kernfunktionen

### 1. Zwei wählbare STT-Pipelines
*   **Whisper + Gemma (Standard):** Das `whisper-base`-Modell wandelt Sprache latenzarm und ressourcenschonend in Text um. Optimiert für Echtzeit-Erkennung auf Standard-Hardware (CPU).
*   **Gemma Native:** Das multimodale **Gemma 4 E2B**-Modell (mit `mmproj`-Projektor) transkribiert das Audio direkt über `llama.cpp`. Technologisch wegweisend, aber deutlich hardware-hungriger (GPU empfohlen).

> Die Pipeline ist zur Laufzeit in den Einstellungen umschaltbar.

### 2. Flexible KI-Analyse (manuell ausgelöst)
Der erkannte Text kann per Klick analysiert/zusammengefasst werden. Es stehen vier Backends zur Wahl:
*   **Gemma 4 (lokal):** Nutzt den bereits laufenden lokalen `llama-server` – schnell, da das Modell nicht neu geladen wird.
*   **Google Gemini Flash:** Cloud-Analyse über die Google-API (API-Key erforderlich).
*   **Ollama:** Anbindung an einen entfernten Ollama-Server.
*   **vLLM:** Anbindung an einen OpenAI-kompatiblen vLLM-Server.

### 3. Audio-Verarbeitung & Betriebsmodi
*   **Zwei Audioquellen:** Mikrofon ("Agent") und – unter Windows – Loopback-Aufnahme des Lautsprecher-/Gesprächstons ("Anrufer", z. B. Teams/Zoom).
*   **Betriebsmodi:** *Standard-Betrieb* (nur Mikrofon) und *Headset-Betrieb* (Mikrofon + Gegenstelle, inkl. Hilfe zum Deaktivieren des Windows-Exklusivmodus).
*   **Digitale Verstärkung:** Pro Kanal getrennt einstellbarer Gain (1×–20×) mit Clipping-Schutz.
*   **Sprechpausen-Erkennung:** Amplituden-basierte Stille-Erkennung erzeugt nach einer einstellbaren Pause automatisch Absatzumbrüche im Transkript.

### 4. Moderne Benutzeroberfläche (GUI)
*   **Fyne-Oberfläche** im Windows-Stil mit eigenen, kompakten Layouts.
*   **Echtzeit-Feedback:** Live-Pegelanzeige je Kanal, Status ("Initialisiere…", "Höre zu…"), Hardware-Erkennung (CPU mit AVX2/512 bzw. GPU-Beschleunigung) und Fortschrittsbalken für Modell-Downloads.
*   **Theme-Switching:** Windows-Standard, Dunkel, Hell und automatische System-Anpassung.
*   **Fensterposition:** Größe und Position werden unter Windows gespeichert und wiederhergestellt.
*   **Rich-Text-Export:** Analyse-Ergebnisse lassen sich formatiert (HTML) in die Zwischenablage kopieren.

### 5. 100 % Portabilität ("Silent Engine")
*   **Autonome Abhängigkeiten:** Beim ersten Start werden alle Binaries (`.exe`), Bibliotheken (`.dll`) und Modelle automatisch heruntergeladen und entpackt.
*   **Invisible Engines:** Hintergrundprozesse (`whisper-cli.exe`, `llama-server.exe`) laufen im "Silent Mode" ohne störende Konsolenfenster.
*   **Persistente Konfiguration:** Alle Einstellungen werden in `config.json` im Programmverzeichnis gespeichert (mit Migration aus älteren Fyne-Preferences).

---

## Technische Architektur

| Komponente | Technologie | Details |
| :--- | :--- | :--- |
| **Sprache** | Go (Golang) | Hauptlogik & Orchestrierung |
| **GUI-Framework** | Fyne.io (v2) | Cross-Plattform-UI mit Custom Widgets |
| **Audio-Capture** | malgo (MiniAudio) | 16 kHz, S16 Mono; Mikrofon + Windows-Loopback |
| **STT-Engine** | whisper.cpp | Lokale Inferenz via CLI (`whisper-cli`) |
| **LLM-Engine** | llama.cpp | Persistenter `llama-server` (OpenAI-kompatibel) auf `127.0.0.1:8080` |
| **Modell (Erkennung)** | Whisper Base | OpenAI (GGML-Format) |
| **Modell (Korrektur/Native)** | Gemma 4 E2B | Google (GGUF via bartowski), inkl. `mmproj` für Native-Pipeline |

---

## Dateistruktur und Module

| Datei | Zweck |
| :--- | :--- |
| `main.go` | Herzstück: GUI, Audio-Capture & -Pipeline, Konfiguration, Analyse-Logik |
| `setup_manager.go` | Lädt Modelle und Binär-ZIPs beim ersten Start herunter (mit Fortschritt) |
| `server_manager.go` | Lebenszyklus des `llama-server`-Prozesses (Start, Health-Check, Stop, Log-Piping) |
| `logger.go` | Datei-basiertes Logging nach `log.txt` |
| `sys_windows.go` / `sys_linux.go` | Plattform-Schalter für den Silent-Mode (Build-Tags) |
| `main_windows.go` | Windows-spezifisch: Fensterpositionierung (user32.dll), Rich-Clipboard |
| `main_others.go` | Nicht-Windows-Stubs für die Fensterpositionierung |
| `libs/` | Zielverzeichnis für Engines und DLLs (zur Laufzeit befüllt) |
| `models/` | Speicherort der KI-Modelle (zur Laufzeit befüllt, ca. 2 GB) |

---

## Installation & Build

### Windows (nativ)
Voraussetzungen: Go-Toolchain **und** ein C-Compiler (mingw-w64 / GCC) im `PATH`, da CGO (malgo/Fyne) benötigt wird.
```powershell
$env:CGO_ENABLED = "1"
go build -ldflags "-H=windowsgui" -o stt-app.exe .
```

### Windows (Cross-Compile von Linux)
```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -ldflags "-H=windowsgui" -o stt-app.exe .
```

### Linux (nativ)
```bash
go build -o stt-app .
```

> [!IMPORTANT]
> Beim ersten Start benötigt die App eine Internetverbindung, um die ca. 2 GB an KI-Komponenten einmalig herunterzuladen. Danach ist sie vollständig offline-fähig.

> [!NOTE]
> Linux-Builds unterstützen keinen Silent-Mode, keine native Fensterpositionierung und keine Loopback-Audioaufnahme – diese Funktionen sind Windows-spezifisch.
