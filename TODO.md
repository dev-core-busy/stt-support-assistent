# TODO — Offene STT-Optimierungen

Stand 2026-07-05, Ergebnis der Mitschrift-Analyse. Bereits umgesetzt
(v0.8.9-dev):

- VAD-Segmentierung statt starrem 4-s-Fenster (`vad.go`) — Schnitt an
  Sprechpausen, kein Zerteilen von Wörtern mehr; Restpuffer-Flush beim
  Aufnahme-Stopp (vorher fehlten bis zu 4 s vom Gesprächsende).
- Remote-GPU-Whisper: echtes Chunk-Streaming laut Protokoll-Spec
  (`remoteStreamer` in `remote_stt.go`): ~250-ms-Chunks fortlaufend,
  `endOfUtterance=true` an der Sprechpause; Stille wird nicht gestreamt;
  Reconnect + Resend bei gekappter Session.
- Sprach-Gate in 250-ms-Subfenstern (kurze Äußerungen wie "Ja." gehen nicht
  mehr im RMS-Mittel unter), Stille-Trimmen vor der Transkription.
- Kontext-Priming: letzte ~200 Zeichen als `--prompt` für den lokalen Whisper.
- Soft-Limiter statt Hard-Clipping beim Digital-Gain; Pegelanzeige auf ~15
  UI-Updates/s gedrosselt; Pausenerkennung gain-unabhängig.

## 1. Lokaler Whisper: whisper-server statt CLI-Spawn + größeres Modell

**Ist:** Pro Segment wird eine WAV auf Platte geschrieben und `whisper-cli`
NEU gestartet — das Modell (whisper-base) wird jedes Mal neu geladen (0,5–2 s
Overhead pro Segment). `whisper-base` ist für Deutsch schwach.

**Soll:**
- `whisper-server` (whisper.cpp) einmal beim App-Start hochziehen — die
  Server-Lifecycle-Infrastruktur existiert bereits (`server_manager.go`,
  llama-server, mehrere Instanzen) und lässt sich spiegeln. Segmente per HTTP
  (`/inference`, multipart-WAV direkt aus dem RAM via `createWavData`) — kein
  Disk-I/O, kein Modell-Reload.
- Modell-Upgrade: `large-v3-turbo` quantisiert (~1,6 GB, deutlich besseres
  Deutsch, nahezu Echtzeit auf moderner CPU) oder `small` (~500 MB) als
  Kompromiss. Download in `setup_manager.go` ergänzen; prüfen, ob das
  Binary-Zip `whisper-server` schon enthält, sonst Download erweitern.
- `prompt`-Feld des Servers nutzen, damit das Kontext-Priming erhalten bleibt.

## 2. Live-Partials im Transkript  *(Protokoll kann es, UI noch nicht)*

Die Spec sieht `partial`-Nachrichten vor (kumulativer Zwischenstand der
laufenden Äußerung); der Server nutzt sie aktuell kaum. `readLoop` ignoriert
sie bewusst: das Transkript-Modell (`pendingRaw`) kann nur ANHÄNGEN — für
Partials braucht es Replace-Semantik (letzten Partial-Text durch den nächsten/
das `final` ersetzen). Lohnt, sobald der Server Partials wirklich sendet:
Text erscheint dann live beim Sprechen statt erst an der Pause.

## 3. Session-Keepalive  *(Klärung mit dem Server-Team)*

Die Spec definiert kein Ping/Keepalive; Sessions sterben durch serverseitigen
Idle-Cleanup. Aktueller Workaround: Reconnect + Resend des fehlgeschlagenen
Chunks (`remoteSendChunk`). Sauberer wäre ein leichtes App-Level-Keepalive
oder definierte Idle-Zeit — mit dem Server-Team klären. Alternativ: Sessions
beim Aufnahme-Stopp aktiv mit `stop` schließen und je Aufnahme neu starten.

## 4. Opus-/Binary-Übertragung — nur falls GPU-Weg über WAN/VPN geht

Base64-JSON ist protokollfest (+33 % Volumen); im LAN irrelevant (32 KB/s
Rohstrom je Kanal). Falls der Remote-Whisper künftig über WAN/VPN erreicht
wird: Binary-Frames oder Opus @ ~24 kbit/s (Faktor ~10) — beides erfordert
eine Protokoll-Erweiterung auf dem Server.

## 5. Notschnitt-Verfeinerung (nice-to-have)

`vadSegmenter`/`remoteStreamer` schneiden nach 15 s Dauersprechen hart
(`vadMaxSeg`). Selten, aber dort kann wieder ein Wort zerteilt werden.
Verbesserung: den energieärmsten Punkt der letzten ~2 s suchen und dort
trennen.
