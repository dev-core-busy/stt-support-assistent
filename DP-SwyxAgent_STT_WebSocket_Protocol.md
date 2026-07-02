# DP-SwyxAgent STT WebSocket Protocol

## Endpoint

```text
ws://<server>:8090/ws/stt
```

Beispiel:

```text
ws://191.100.130.61:8090/ws/stt
```

---

## Format

- Transport: **WebSocket**
- Nachrichtenformat: **JSON**
- Audioformat: **PCM signed 16-bit little endian**
- Sample Rate: **16000 Hz**
- Channels: **1 / Mono**
- Audioübertragung: **Base64-kodiert im JSON-Feld `pcmBase64`**

---

## Grundprinzip

Das Protokoll ist ein leichtgewichtiges **JSON-over-WebSocket-Protokoll** für Speech-to-Text.

Der Client:

1. öffnet eine WebSocket-Verbindung,
2. startet eine STT-Session,
3. sendet fortlaufend Audio-Chunks,
4. markiert optional das Ende einer Äußerung,
5. empfängt erkannte Texte,
6. beendet die Session mit `stop`.

---

## Empfohlener Ablauf

```text
WebSocket verbinden
→ start senden
→ ready empfangen
→ audio chunks senden
→ bei Sprachende endOfUtterance=true senden
→ final empfangen
→ optional flush senden
→ stop senden
→ stopped empfangen
→ WebSocket schließen
```

---

## Session-Namen

Empfohlen ist eine eindeutige Session-ID pro Sprecher/Audioquelle.

Beispiel pro Call:

```text
call-<callId>-agent
call-<callId>-customer
```

Konkretes Beispiel:

```text
call-67987-agent
call-67987-customer
```

---

## Nachrichten

## 1. Session starten

### Client → Server

```json
{
  "type": "start",
  "sessionId": "call-123-agent",
  "language": "de",
  "sampleRate": 16000,
  "channels": 1,
  "format": "pcm_s16le"
}
```

### Server → Client

```json
{
  "type": "ready",
  "sessionId": "call-123-agent"
}
```

### Bedeutung

- `type`: Nachrichtentyp, hier `start`
- `sessionId`: eindeutige Session-ID
- `language`: Sprache, z. B. `de`
- `sampleRate`: aktuell `16000`
- `channels`: aktuell `1`
- `format`: aktuell `pcm_s16le`

---

## 2. Audio-Chunk senden

### Client → Server

```json
{
  "type": "audio",
  "sessionId": "call-123-agent",
  "sequence": 1,
  "pcmBase64": "<base64-pcm-data>",
  "endOfUtterance": false
}
```

Wenn eine Äußerung endet:

```json
{
  "type": "audio",
  "sessionId": "call-123-agent",
  "sequence": 42,
  "pcmBase64": "<base64-pcm-data>",
  "endOfUtterance": true
}
```

### Bedeutung

- `sequence`: fortlaufende Chunk-Nummer
- `pcmBase64`: Base64-kodierte PCM16-Mono-16k-Audiodaten
- `endOfUtterance`: `true`, wenn das Ende einer Äußerung erkannt wurde

---

## 3. Finales STT-Ergebnis

### Server → Client

```json
{
  "type": "final",
  "sessionId": "call-123-agent",
  "sequence": 42,
  "text": "Guten Tag, womit kann ich helfen?",
  "fullText": "Guten Tag, womit kann ich helfen?"
}
```

### Bedeutung

- `type`: Ergebnis ist final für das verarbeitete Segment
- `text`: erkannter Text des aktuellen Segments
- `fullText`: vollständiger Text für den aktuellen Kontext bzw. das Segment

---

## 4. Partial-Ergebnis

Partial-Ergebnisse sind im Protokoll vorgesehen.

### Server → Client

```json
{
  "type": "partial",
  "sessionId": "call-123-agent",
  "sequence": 41,
  "text": "Guten Tag",
  "fullText": "Guten Tag"
}
```

### Hinweis

In der aktuellen Server-Implementierung werden primär `final`-Nachrichten verwendet. `partial` ist protokollseitig vorgesehen und kann für späteres Live-Partial-Streaming genutzt werden.

---

## 5. Flush

### Client → Server

```json
{
  "type": "flush",
  "sessionId": "call-123-agent"
}
```

### Bedeutung

Der Server soll den aktuell gepufferten Audioinhalt verarbeiten und, sofern Text erkannt wurde, ein Ergebnis zurückgeben.

---

## 6. Session stoppen

### Client → Server

```json
{
  "type": "stop",
  "sessionId": "call-123-agent"
}
```

### Server → Client

```json
{
  "type": "stopped",
  "sessionId": "call-123-agent"
}
```

### Bedeutung

Die Session wird serverseitig beendet und aus den aktiven Sessions entfernt.

---

## 7. Fehler

### Server → Client

```json
{
  "type": "error",
  "sessionId": "call-123-agent",
  "message": "Session nicht bekannt. Erst start senden."
}
```

---

## Session-Lebenszyklus

Eine Session wird geschlossen durch:

- explizites `stop` vom Client,
- Trennung der WebSocket-Verbindung,
- serverseitigen Idle-Cleanup nach konfigurierter Inaktivitätszeit.

---

## Aktueller Streaming-Status

Das Protokoll überträgt Audio kontinuierlich als Stream von Audio-Chunks.

Die aktuelle Server-Implementierung transkribiert segmentweise, typischerweise bei:

- `endOfUtterance = true`,
- `flush`,
- `stop`.

Damit ist es aktuell:

```text
Live-Audio-Streaming mit segmentweiser STT-Auswertung
```

Nicht gemeint ist aktuell:

```text
permanentes tokenweises STT-Streaming
```

---

## Kurzbeschreibung

```text
Das DP-SwyxAgent STT WebSocket Protocol ist ein JSON-over-WebSocket-Protokoll.
Der Client startet eine Session, sendet PCM16-Mono-16k-Audiochunks als Base64 und erhält erkannte Texte als final- oder optional partial-Nachrichten zurück.
Die Session wird mit stop beendet oder serverseitig bei Verbindungsabbruch/Idle-Timeout aufgeräumt.
```
