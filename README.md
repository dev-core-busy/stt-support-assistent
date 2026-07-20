# stt-support-assistent

**Version 0.8.14**

Portable Speech-to-Text-Anwendung (Windows) mit lokaler KI-gestützter
Textkorrektur/-analyse und angebundenem KI-Support (Jarvis). Geschrieben in Go
mit Fyne-GUI. Die App ist eigenständig: Binaries und Modelle werden beim ersten
Start automatisch heruntergeladen; danach läuft sie voll offline.

## Funktionsüberblick

- **Zwei STT-Pipelines**
  - **Whisper + Gemma** (Standard): whisper.cpp für die Spracherkennung →
    llama.cpp (Gemma 4) zur Grammatik-/Zeichensetzungskorrektur an Satzgrenzen.
  - **Gemma Native**: llama.cpp multimodal transkribiert Audio direkt über das
    Gemma-4-E2B-Modell.
- **Analyse** wahlweise über lokales Gemma 4, Google Gemini Flash oder Remote-Ollama/vLLM.
- **KI-Support (Jarvis)**: Suche über RAG (Wissensdatenbank) / Jira / Confluence
  mit optionaler KI-Gesamtzusammenfassung; Quellen inkl. klickbarem Link,
  Wissens-Dateien werden mit API-Key geladen (siehe `jarvis_api.md`).
- **Suche passende Tickets**: findet per Knopfdruck zum erkannten Text passende
  Jira-Tickets (Prompt-Vorlage in den Einstellungen hinterlegbar).
- **Auto-Update über GitHub**: prüft beim Start still das neueste Release; ist
  eine neuere Version verfügbar, wird sie nach Rückfrage heruntergeladen,
  installiert und die App automatisch neu gestartet. Die aktuelle Version steht
  im Fenstertitel in Klammern. Release-Erstellung über `release.sh`.
- **Rufnummern-Übergabe (Webhook)**: eingehender HTTP-Webhook (Einstellungen →
  „Rufnummern Übergabe“, Standard-Port 5555, lauscht auf 0.0.0.0). Ein externer
  Trigger übergibt per GET (`?number=…`) oder POST (JSON `{"number":"…"}`) die
  Rufnummer eines Anrufers; die App sucht damit in Jira und trägt den Issue-Key
  des besten Treffers ins Feld „erkannter Kunde“ ein.

## Build

### Linux (nativ)
```bash
go build -o stt-app .
```

### Windows (Cross-Compile von Linux)
```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -ldflags "-H=windowsgui" -o stt-app.exe .
```

Der erste Start benötigt Internet, um ~2 GB Modelle und Binaries zu laden.
Danach ist die App vollständig offline lauffähig.

## Weitere Dokumentation

- `CLAUDE.md` — Architektur- und Quellcode-Überblick
- `PROJEKT_INFO.md` — ausführliche Projektdokumentation
- `jarvis_api.md` — REST-API des internen Jarvis-Support-Assistenten
- `DP-SwyxAgent_STT_WebSocket_Protocol.md` — Protokoll des Remote-Whisper-Servers

## Hinweise

- `config.json`, `libs/`, `models/` und Build-Artefakte sind bewusst per
  `.gitignore` ausgenommen (Secrets bzw. große Laufzeitdaten).
