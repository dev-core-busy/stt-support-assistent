# Übergabe / Rechnerwechsel — STT-App (live_SST)

> Zweck: Den **Claude-Code-Entwicklungsrechner** von Windows 11 auf **Debian 13**
> umziehen und dort **nahtlos** weiterentwickeln. Das **Entwicklungsziel bleibt die
> Windows-App** (`stt-app.exe`) — sie wird auf Debian per **Cross-Compile** gebaut,
> NICHT auf Linux portiert.
> Stand: 29.06.2026. Letzter erfolgreicher Build: `stt-app.exe`, 29.06.2026 07:35.
> Sprache der Zusammenarbeit: **ausschließlich Deutsch.**

---

## 0. Schnellstart auf dem neuen Debian-Rechner

1. **Projektordner kopieren:** gesamtes Verzeichnis an einen Linux-Pfad legen,
   z. B. `~/live_SST`. Pfad frei wählbar (Restore-Skript + Memory-Slug passen sich an).
   - `libs/` und `models/` **nicht** mitkopieren: Das sind die zur Laufzeit von der
     App heruntergeladenen Windows-Binaries/Modelle (~2 GB) — sie gehören zur
     laufenden App auf dem **Windows-Testrechner**, nicht zur Entwicklung. Zum Bauen
     werden sie nicht gebraucht.
   - `config.json` ist Laufzeit-Zustand der App (Windows) — für die Entwicklung
     optional. Enthält die Remote-Server-URLs/Gain/Theme.
2. **Cross-Compile-Toolchain installieren** (siehe §2, `apt`-Block).
3. **Memory (Wissen) wiederherstellen:** Snapshot liegt im Projekt unter
   `.claude-memory/`. Einmalig ausführen:
   ```bash
   bash restore-memory.sh
   ```
   Das Skript bildet den Ziel-Slug aus dem absoluten Projektpfad (Claude-Konvention:
   Nicht-alphanumerisch → `-`; z. B. `/home/me/live_SST` ⇒ `-home-me-live-SST`) und
   kopiert die Memory-`.md`-Dateien nach `~/.claude/projects/<slug>/memory/`.
   Volltext der Memory steht zusätzlich in §8 (Fallback).
4. **Build testen** (siehe §2): Cross-Compile muss `stt-app.exe` mit Exit 0 erzeugen.

> **An Claude auf dem neuen Rechner:** Führe als ERSTES Schritt 3 aus (Memory aus
> `.claude-memory/` wiederherstellen — `bash restore-memory.sh` ODER die `.md`-Dateien
> direkt in dein Memory-Verzeichnis schreiben, das dein System-Prompt unter „Memory"
> nennt — das ist der zuverlässige Weg, falls der Slug abweicht). Lies danach diese
> Datei zu Ende und mach bei §7 weiter. **Wichtig:** Die App ist und bleibt eine
> Windows-App; auf Debian wird nur **cross-kompiliert**, nicht ausgeführt.

---

## 1. Was ist das Projekt?

Portable **Speech-to-Text-Windows-App** in **Go + Fyne** (GUI). Nimmt Mikrofon
(Agent) und Windows-Loopback (Anrufer/Teams) parallel auf (malgo, 16 kHz mono
S16LE), transkribiert und kann den Text per LLM nachbearbeiten und analysieren.

**Dreigeteilte Pipeline** (vom Nutzer so gewünscht, bereits umgesetzt):
1. **Erkennung** — Checkbox „Whisper lokal". An = lokaler `whisper-cli`.
   Aus = **Remote Whisper GPU** (WebSocket, `remote_stt.go`).
2. **Nachbearbeitung** — Pulldown `[ohne] [Gemma 4 E2B] [Gemma 4 12B] [remote LLM]`,
   korrigiert Whisper-Text an Satz-/Sprechpausengrenzen. (`config.PostProcModel`)
3. **Analyse** (manuell, Button) — Pulldown `[Gemma 4 E2B] [Gemma 4 12B] [remote LLM]`.
   (`config.AnalysisModel`)

„remote LLM" wird über `config.RemoteBackend` (Google Flash / Ollama / vLLM)
konkretisiert.

---

## 2. Build-Setup auf Debian 13 (Cross-Compile → Windows)

Gebaut wird die **Windows-.exe** von Linux aus. Nötig: Go + der **mingw-w64
Cross-Compiler** (CGO ist Pflicht für malgo + Fyne).

```bash
sudo apt update
sudo apt install -y golang-go gcc-mingw-w64-x86-64
# Falls Debians golang-go zu alt ist (go.mod: go 1.23, toolchain 1.24.4):
# offizielles Go-Tarball von https://go.dev/dl/ nach /usr/local/go entpacken,
# dann: export PATH=$PATH:/usr/local/go/bin
```

**Build-Befehl (Cross-Compile, Windows-Target):**
```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -ldflags "-H=windowsgui" -o stt-app.exe .
```
- `-H=windowsgui` unterdrückt das Konsolenfenster der Windows-App.
- `CC=x86_64-w64-mingw32-gcc` ist der von `gcc-mingw-w64-x86-64` bereitgestellte
  Cross-Compiler.
- Build dauert ~10–60 s (Module gecacht).
- **Verifikation durch Claude:** `go build` (Exit 0) + `go vet` + `gofmt -l *.go`.
  Für `go vet` ggf. ebenfalls `GOOS=windows` setzen, damit die Windows-Dateien
  geprüft werden. Erwartbar sind nur die bekannten `unsafe.Pointer`-Hinweise in
  `main_windows.go`.

### 2a. Testen der App
Die GUI-App läuft **nicht** auf dem Debian-Entwicklungsrechner. Zum Ausführen/Testen
die gebaute `stt-app.exe` auf einen **Windows-Rechner** kopieren und dort starten
(erster Start lädt `libs/`+`models/`). Alternativ ließe sich die .exe unter **Wine**
antesten — für echte Audio-/Loopback-Tests aber nur bedingt aussagekräftig.

### 2b. Nativer Windows-Build (Referenz, alter Rechner)
Auf dem Windows-Quellrechner via **winget**: `GoLang.Go` (1.26.4),
`BrechtSanders.WinLibs.POSIX.UCRT` (16.1.0, mingw für CGO). Dort persistiert der
PowerShell-Shell-State nicht zwischen Tool-Calls → in jedem Call PATH+CGO neu setzen:
```powershell
$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User")
$env:CGO_ENABLED = "1"
go build -ldflags "-H=windowsgui" -o stt-app.exe .
```
Läuft die App gerade (`stt-app.exe` gesperrt) → in `stt-app.new.exe` bauen oder App
vorher beenden.

---

## 3. Dateistruktur (Quellcode)

| Datei | Zeilen | Zweck |
|-------|-------|-------|
| `main.go` | ~2728 | GUI (Fyne), Audio (malgo), Pipeline-Orchestrierung, Config, Analyse, UI-Tabs, Themes |
| `setup_manager.go` | 265 | Selbst-Download von Whisper/Gemma-Modellen + Binär-Zips; `knownLocalModels`-Registry; vLLM/Ollama-Modell-Discovery |
| `server_manager.go` | 181 | Lebenszyklus mehrerer llama-server (`llamaInstance`, `localServers`, `ensureLocalServers`) |
| `remote_stt.go` | 173 | Remote-Whisper-WebSocket-Client (JSON-over-WS, eine Session pro Sprecher) |
| `logger.go` | 40 | Datei-Logger → `log.txt` |
| `main_windows.go` | 215 | Windows: silent process, Fensterposition (user32), eckige Ecken (DWM), Rich-Clipboard |
| `main_others.go` | 18 | Non-Windows-Stubs (no-ops) — nur damit ein `!windows`-Build übersetzt; für die Windows-App irrelevant |
| `sys_windows.go` / `sys_linux.go` | 16/13 | CREATE_NO_WINDOW-Flag bzw. Stub |

Build-Tags: `sys_windows.go`/`main_windows.go` = `//go:build windows`;
`sys_linux.go`/`main_others.go` = `//go:build !windows`. Alle `package main`.
Beim Cross-Compile mit `GOOS=windows` werden die `windows`-Dateien kompiliert.

**Doku im Repo:**
- `CLAUDE.md` — Projektanweisungen für Claude Code (teils noch alter Stand:
  beschreibt „Gemma Native"/Single-Server — die sind ersetzt, s. §5).
- `PROJEKT_INFO.md` — Projektbeschreibung.
- `DP-SwyxAgent_STT_WebSocket_Protocol.md` — **Protokoll-Spec des Remote-Whisper-Servers** (wichtig für `remote_stt.go`).

**Übergabe-Artefakte (NICHT löschen):** `exchange.md` (diese Datei),
`restore-memory.sh` (Linux) + `restore-memory.ps1` (Windows) für die
Memory-Wiederherstellung, `.claude-memory/` (Memory-Snapshot).

**Aufräum-Hinweis:** `test_llama.log`, `test_llama_out.log`, `session.bat` sind
Wegwerf-/Testdateien. `go.mod` hat noch ein `replace …/vosk-api/go => ./vosk-go`,
obwohl Vosk nicht mehr genutzt wird (Verzeichnis fehlt) — Altlast, stört nicht.

---

## 4. Konfiguration (`config.json`, Struct `AppConfig` in main.go)

Aktueller Live-Stand (vom Windows-Rechner):
- `theme`: „Hell (klassisch)" (Optionen: Hell/Dunkel (modern), Hell (klassisch))
- `whisperLocal`: **false** → nutzt Remote-Whisper-GPU
- `postProcModel`: `remote`, `analysisModel`: `remote`, `remoteBackend`: `vLLM`
- `remoteWhisperUrl`: `ws://191.100.130.61:8090/ws/stt`
- `vllm.url`: `http://191.100.130.61:9081/v1/`, `model`: `Qwen/Qwen3.6-35B-A3B-FP8`
  *(Achtung: die Memory nennt Port 9082 für vLLM — Live-config nutzt 9081. Im UI per Autodiscover prüfen.)*
- `micGain`: 7, `spkGain`: 2 (Jabra Evolve2 30 SE); `pauseThreshold`: 4 s

Symbolwerte: PostProc/Analysis ∈ `none`/`e2b`/`12b`/`remote`. Migration alter
Felder (`SttPipeline`/`AnalysisMode`/`LocalGemmaModel`) via
`migrateLegacyPipelineFields()`/`migrateLegacyBackendFields()`.

---

## 5. Architektur-Kernpunkte (aktueller Code, NICHT der alte CLAUDE.md-Stand)

- **Multi-Server llama:** `server_manager.go` verwaltet **mehrere** llama-server:
  `localServers = {"e2b":{port 8080}, "12b":{port 8081}}`, bedarfsgesteuert über
  `ensureLocalServers()` (startet nur referenzierte Modelle, stoppt ungenutzte).
  `startInstance` läuft **ohne** `--mmproj` (sonst Crash `clip_init: unknown
  projector type: gemma4uv` — 12B nur Text-Nutzung).
- **„Gemma Native" entfällt** (Code entfernt). Lokale Modelle: e2b + 12b
  (`knownLocalModels` in setup_manager.go, mit Download-URLs).
- **Remote-Whisper** (`remote_stt.go`): JSON-over-WebSocket
  (`golang.org/x/net/websocket`), eine stehende Session pro Sprecher,
  Reader-Goroutine zeigt `final`-Texte entkoppelt an. Sendet aktuell **pro
  4-s-Segment** `endOfUtterance=true` (segmentweise, nicht echtes Streaming —
  optionale spätere Optimierung auf utterance-basiert).
  Protokoll: `start`→`ready`, `audio` (`pcmBase64`,`sequence`,`endOfUtterance`),
  Server→`final`/`partial` (`text`/`fullText`), `flush`, `stop`→`stopped`.
- **Atomics für Hot-Path:** `atWhisperLocal`, `atHasPostProc`, `atMicGain` etc.,
  gesetzt in `syncConfigToAtomics`.
- **Silence-Gate gegen Whisper-Halluzinationen:** `hasSpeech(audio, speaker)` —
  RMS-Gate (gain-normalisiert, Schwelle `rms/gain > 200`) am Anfang von
  `processSegment`; verwirft stille Segmente. **Bewusst KEINE Phrasen-Blacklist**
  („Vielen Dank" ist legitimer Text).
- **Sprecher-Absätze:** `writeSpeakerPrefix(speaker, marker, ts)` — neue Zeile bei
  Sprecherwechsel mit `TT.MM.JJJJ - SS:MM:SS [Agent]:`-Präfix; bei
  durchgehendem selben Sprecher kein erneutes Präfix, nur Leerzeichen.
  `nowStamp()` = `time.Now().Format("02.01.2006 - 15:04:05")`.
- **Themes:** `winTheme{dark, classic}`. `applyTheme(a, mode)` mit
  „Hell (modern)"/„Dunkel (modern)"/„Hell (klassisch)" (+ Legacy-Migration).
  Klassisch = eckige Widget-Ecken (Radius 0) + `#F0F0F0` BG + eckige
  Fensterrahmen via DWM (`setWindowSquare`, `DWMWA_WINDOW_CORNER_PREFERENCE`).
- **Pegelanzeige:** `meterMarkerLayout` legt 2 vertikale 2px-Striche über die
  ProgressBar — orange = Ist-Spitze (Peak-Hold, Decay 250 ms × 0.96), grün =
  Zielwert (`targetLevel = 0.8`). `newLevelMeter(bar, markerVal)`. Mic- und
  Spk-Zeile rechtsbündig (transparenter Spacer in Spk-Breite = „?"-Button-Breite).
- **Dialoge linksbündig:** Helfer `showInfo`/`showErr`/`showConfirm` nutzen
  `dialog.ShowCustom(Confirm)` mit linksbündigem Label (`Alignment =
  TextAlignLeading`) statt der zentrierenden `dialog.ShowInformation/Error/Confirm`
  (erledigt, siehe §7).

---

## 6. Externe Server (intern, nexus-ag)

- **Remote-Whisper-STT:** `ws://191.100.130.61:8090/ws/stt`
  (Health `http://191.100.130.61:8090/health`). Dienst „DP-SwyxAgent STT Server"
  v0.1.0 (uvicorn/FastAPI), Modell **faster-whisper-large-v3**, CUDA/float16.
- **vLLM (Analyse-LLM):** selber Host, Port **9081** lt. Live-config
  (`/v1/`, Modell `Qwen/Qwen3.6-35B-A3B-FP8`); Memory notiert 9082 — beim Start
  per Autodiscover-Lupe im UI verifizieren. `vllmBaseURL()` strippt doppeltes
  `/v1` (war ein behobener Bug: `.../v1/v1/models`).

---

## 7. Aktueller Arbeitsstand / offene Aufgabe

**ZULETZT ABGESCHLOSSEN (Build 29.06.2026 07:35, Exit 0):**
> „Die Texte aller Popups sind aktuell zentriert dargestellt. Umstellen auf
> linksbündig."

Umgesetzt in `main.go` (drei Helfer vor `func contains`, alle mit linksbündigem
Label `Alignment = fyne.TextAlignLeading`):
- `showInfo(title, msg, parent)` → ersetzt `dialog.ShowInformation` (5 Aufrufe).
- `showErr(err, parent)` → ersetzt `dialog.ShowError` (Titel „Fehler").
- `showConfirm(title, msg, cb, parent)` → ersetzt `dialog.ShowConfirm` (Ja/Nein
  via `dialog.ShowCustomConfirm`).
- `dialog.ShowCustomConfirm` bei „Modell auswählen" bleibt unverändert (eigener Inhalt).

Verifiziert: keine `dialog.Show(Information|Error|Confirm)`-Treffer mehr; Windows-Build
Exit 0. **Visueller Check am UI steht noch aus** (auf einem Windows-Rechner prüfen).

**Optionale, vom Nutzer noch nicht beauftragte Punkte** (nur bei Bedarf):
- Remote-STT von segmentweise (`endOfUtterance` pro 4-s-Segment) auf
  utterance-/streaming-basiert optimieren.
- Whisper-Halluzinations-Schwelle (`hasSpeech` RMS) feinjustieren.
- Visueller Check der linksbündigen Dialoge.

---

## 8. Claude-Code-Memory (Snapshot + Volltext-Fallback)

**Primär:** Snapshot liegt im Projekt unter `.claude-memory/` und wird per
`restore-memory.sh` (Linux) bzw. `restore-memory.ps1` (Windows) zurückgespielt
(siehe §0 Schritt 3). Der Volltext unten ist nur Fallback bzw. zum Nachlesen.

Ziel-Pfad Linux: `~/.claude/projects/<slug>/memory/` (Slug aus Projektpfad).
Pfad auf dem alten Windows-Rechner:
`%USERPROFILE%\.claude\projects\C---temp-live-SST\memory\`

### MEMORY.md (Index)
```
- [Go-Toolchain (winget)](go-toolchain-missing.md) — Go+mingw via winget da; PATH pro PowerShell-Call neu setzen
- [Remote-Whisper-STT geplant](remote-whisper-stt-geplant.md) — STT-Pipeline gegen internen GPU-Whisper-WS; umgesetzt
- [Debian-Migration](debian-migration.md) — Claude-CLI-Rechner Win11→Debian13; App bleibt Windows, Build per Cross-Compile
```

### go-toolchain-missing.md (Windows-Quellrechner)
```
Go-Toolchain via winget installiert: Go (GoLang.Go, 1.26.4) → C:\Program Files\Go\bin;
GCC/mingw-w64 (BrechtSanders.WinLibs.POSIX.UCRT, 16.1.0) für CGO (malgo/Fyne Pflicht).
Shell-State persistiert NICHT zwischen Tool-Calls → in JEDEM PowerShell-Aufruf zuerst:
  $env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User")
  $env:CGO_ENABLED = "1"
  go build -ldflags "-H=windowsgui" -o stt-app.exe .
```
> Auf Debian gilt stattdessen die Cross-Compile-Toolchain aus §2
> (`apt install golang-go gcc-mingw-w64-x86-64`, `CC=x86_64-w64-mingw32-gcc`).

### remote-whisper-stt-geplant.md
Status **UMGESETZT**. Remote-Whisper-Pipeline gegen `ws://191.100.130.61:8090/ws/stt`
(faster-whisper-large-v3), implementiert in `remote_stt.go`. UI dreigeteilt
(WhisperLocal / PostProcModel / AnalysisModel), Multi-Server llama. Typ `project`.

### debian-migration.md
Claude-CLI-Entwicklungsrechner zieht Win11→Debian13; **die App bleibt Windows**,
Build per Cross-Compile (`GOOS=windows`, mingw). Testen der .exe auf Windows-Rechner.
Memory-Mitnahme via `.claude-memory/` + `restore-memory.sh`. Typ `project`.

---

## 9. Wichtige Verhaltensregeln (aus bisheriger Zusammenarbeit)

- **Nur Deutsch** kommunizieren.
- **Fyne-Thread-Safety:** ALLE UI-Updates über `fyne.Do()`. Eingebettete Widgets
  brauchen `ExtendBaseWidget(self)` im Konstruktor — sonst sind z. B.
  Eingabefelder nicht editierbar (war ein Bug bei `MinSizeEntry`/`MinSizeSelect`).
- Bei Edit-Whitespace-Mismatch: exakte Tabs prüfen (`cat -A` via Bash).
- Builds darf/soll Claude selbst verifizieren — auf Debian per Cross-Compile
  (`GOOS=windows … go build`, Exit 0). Ausführen/GUI-Test nur auf Windows.
- Der Nutzer leitet Entwickler-Rückfragen zum Remote-Server intern weiter.
