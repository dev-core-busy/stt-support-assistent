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

Umgesetzt am 2026-07-06:

- **Lokaler whisper-server statt CLI-Spawn** (`server_manager.go`): einmal
  beim Start hochgezogen (Port 8082), Segmente per HTTP `POST /inference`
  (multipart-WAV direkt aus dem RAM via `createWavData`) — kein Disk-I/O,
  kein Modell-Reload pro Segment. Kontext-Priming über das `prompt`-Feld.
  `whisper-cli` bleibt als Rückfall (Warmup/Fehler). Das Binary-Zip enthielt
  `whisper-server.exe` bereits.
- **Modell-Upgrade**: `ggml-large-v3-turbo-q5_0` (~550 MB) wird beim Start
  geladen und bevorzugt genutzt (`localWhisperModelPath`); `whisper-base`
  bleibt Rückfall, solange der Download fehlt/läuft.
- **Notschnitt-Verfeinerung**: `vadSegmenter` schneidet bei Dauersprechen an
  der energieärmsten Stelle der letzten ~2 s (`quietestCutPoint`, 20-ms-
  Frames); der Rest hinter dem Schnitt bleibt als Anfang des nächsten
  Segments erhalten. Der `remoteStreamer` kann nicht rückwirkend schneiden
  (Audio bereits gestreamt) und wartet stattdessen bis zu 2 s Nachfrist auf
  den ersten leisen Chunk.

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

## 5. Schlagwort-Ticketsuche, Schritt 2: echte Abfrage gegen Jarvis + Kundenverwaltung

**Ist (seit 2026-07-06):** "Suche passende Tickets" (STT-Tab) extrahiert
zusätzlich 2–3 Schlagworte aus der Mitschrift (Analyse-LLM,
`extractTicketKeywords` in `main.go`) und zeigt sie in einem Info-Popup —
reine Vorschau, die Schlagworte werden noch NICHT für die Suche verwendet.

**Soll:** Die extrahierten Schlagworte an die Jarvis-API und die
Kundenverwaltungs-API übergeben, damit passende Tickets gesucht und in der
gemeinsamen Ergebnisliste angezeigt werden. Voraussetzung: BEIDE APIs müssen
dafür erst angepasst werden —
- Jarvis (`/api/support/query`): Schlagwort-/Stichwortsuche serverseitig
  unterstützen (heute geht nur Freitext/RAG-Score).
- Kundenverwaltung: es gibt bislang GAR KEINEN Text-/Schlagwort-Suchendpunkt
  (nur `getByNumber` → `getEvents` je Adresse).
Danach in `searchMatchingTickets` die Schlagworte statt/zusätzlich zum
Mitschrift-Volltext senden und die Treffer beider Quellen wie bei der
Anruf-Ansicht mischen.

