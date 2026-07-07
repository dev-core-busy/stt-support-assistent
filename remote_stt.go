package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// wsMsg ist das JSON-Nachrichtenformat des DP-SwyxAgent STT WebSocket-Protokolls.
type wsMsg struct {
	Type           string `json:"type"`
	SessionId      string `json:"sessionId"`
	Language       string `json:"language,omitempty"`
	SampleRate     int    `json:"sampleRate,omitempty"`
	Channels       int    `json:"channels,omitempty"`
	Format         string `json:"format,omitempty"`
	Sequence       int    `json:"sequence,omitempty"`
	PcmBase64      string `json:"pcmBase64,omitempty"`
	EndOfUtterance bool   `json:"endOfUtterance,omitempty"`
	Text           string `json:"text,omitempty"`
	FullText       string `json:"fullText,omitempty"`
	Message        string `json:"message,omitempty"`
}

// remoteSession hält eine stehende WebSocket-Sitzung pro Sprecher. Eine Reader-
// Goroutine zeigt eintreffende final-Ergebnisse direkt im Transkript an
// (entkoppelt vom Senden – das Protokoll antwortet asynchron).
type remoteSession struct {
	conn      *websocket.Conn
	sessionId string
	speaker   string
	seq       int
	mu        sync.Mutex
}

var (
	remoteSessions   = map[string]*remoteSession{}
	remoteSessionsMu sync.Mutex
)

func remoteWhisperURL() string {
	u := strings.TrimSpace(config.RemoteWhisperUrl)
	if u == "" {
		u = "ws://191.100.130.61:8090/ws/stt"
	}
	return u
}

// getRemoteSession liefert die (ggf. neu aufgebaute) Session eines Sprechers.
func getRemoteSession(speaker string) (*remoteSession, error) {
	remoteSessionsMu.Lock()
	defer remoteSessionsMu.Unlock()
	if s, ok := remoteSessions[speaker]; ok && s.conn != nil {
		return s, nil
	}
	url := remoteWhisperURL()
	conn, err := websocket.Dial(url, "", "http://localhost/")
	if err != nil {
		return nil, fmt.Errorf("Verbindung zu %s fehlgeschlagen: %v", url, err)
	}
	sid := "stt-app-" + speaker
	s := &remoteSession{conn: conn, sessionId: sid, speaker: speaker}
	if err := websocket.JSON.Send(conn, wsMsg{
		Type: "start", SessionId: sid, Language: "de",
		SampleRate: 16000, Channels: 1, Format: "pcm_s16le",
	}); err != nil {
		conn.Close()
		return nil, err
	}
	go s.readLoop()
	remoteSessions[speaker] = s
	Log("Remote-STT-Session gestartet: " + sid)
	return s, nil
}

// readLoop liest Server-Nachrichten und zeigt final-Texte an.
func (s *remoteSession) readLoop() {
	for {
		var m wsMsg
		if err := websocket.JSON.Receive(s.conn, &m); err != nil {
			return // Verbindung geschlossen
		}
		switch m.Type {
		case "final":
			if text := strings.TrimSpace(m.Text); text != "" {
				updateSTTTail(text) // Kontext-Puffer (nutzt derzeit nur der lokale Whisper)
				if atHasPostProc.Load() {
					appendPendingRaw(s.speaker, text)
				} else {
					appendSpeakerSegment(s.speaker, "", text)
				}
			}
		case "partial":
			// Laut Spec vorgesehen (kumulativer Zwischenstand der laufenden
			// Äußerung), vom Server aktuell kaum genutzt. Eine Live-Anzeige
			// bräuchte Replace-Semantik im Transkript (das pendingRaw-Modell
			// kann nur anhängen) - bewusst ignoriert, s. TODO.md.
		case "error":
			Log("Remote-STT-Fehler: " + m.Message)
		}
	}
}

// send überträgt Audio (PCM16 mono 16k) als Base64-Chunk; eou markiert das
// Äußerungsende - erst dann transkribiert der Server seinen Utterance-Puffer
// (s. DP-SwyxAgent_STT_WebSocket_Protocol.md).
func (s *remoteSession) send(audio []byte, eou bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return websocket.JSON.Send(s.conn, wsMsg{
		Type:           "audio",
		SessionId:      s.sessionId,
		Sequence:       s.seq,
		PcmBase64:      base64.StdEncoding.EncodeToString(audio),
		EndOfUtterance: eou,
	})
}

// remoteSendChunk sendet Audio (ggf. mit Äußerungsende-Markierung) an den
// Remote-Whisper-Server; Ergebnisse kommen asynchron über readLoop. Schlägt
// das Senden fehl (typisch: die stehende Verbindung wurde nach einer
// Gesprächspause vom serverseitigen Idle-Cleanup oder NAT/Proxy gekappt),
// wird EINMAL neu verbunden und DERSELBE Chunk erneut gesendet - vorher ging
// er verloren und erst der nächste baute die Verbindung wieder auf.
func remoteSendChunk(audio []byte, speaker string, eou bool) {
	for attempt := 1; attempt <= 2; attempt++ {
		s, err := getRemoteSession(speaker)
		if err != nil {
			Log("Remote-STT: " + err.Error())
			return
		}
		if err := s.send(audio, eou); err == nil {
			return
		} else {
			Log(fmt.Sprintf("Remote-STT senden fehlgeschlagen (Versuch %d/2): %v", attempt, err))
			closeRemoteSession(speaker)
		}
	}
}

// remoteTranscribe sendet ein KOMPLETTES Segment als eigene Äußerung.
// Rückfall-Pfad: der Normalbetrieb streamt kontinuierlich über remoteStreamer
// (s.u.); hierher kommt nur noch ein Segment des lokalen VAD-Segmentierers,
// wenn während laufender Aufnahme von lokal auf remote umgeschaltet wird.
func remoteTranscribe(audio []byte, speaker string) {
	remoteSendChunk(audio, speaker, true)
}

// ---------------------------------------------------------------------------
// Streaming-Modus: kontinuierliche Chunks, endOfUtterance an der Sprechpause
// ---------------------------------------------------------------------------

// remoteStreamer streamt den Audiostrom eines Kanals fortlaufend zum
// GPU-Whisper - so, wie es die Protokoll-Spec vorsieht ("audio chunks senden,
// bei Sprachende endOfUtterance=true"). Anders als der frühere Segment-Versand
// (ganzes 4-s-Fenster als EIN Paket, jedes mit endOfUtterance=true) sammelt
// der Server die Äußerung selbst und transkribiert sie mit vollem Kontext,
// sobald die Pause gemeldet wird: bessere Erkennung, und kurze Sätze
// erscheinen sofort statt nach der Fenster-Füllung.
//
// Stille wird NICHT gestreamt (nur der Vorlauf vadPreRoll vor dem
// Sprachbeginn): das hält den Utterance-Puffer des Servers klein und
// verhindert Halluzinationen auf stillen Kanälen. Gesendet wird gebündelt
// (~250 ms je audio-Nachricht) statt je ~10-ms-Callback-Chunk (weniger
// JSON-/Base64-Overhead). Läuft komplett in der Buffer-Goroutine des Kanals.
type remoteStreamer struct {
	speaker string
	gain    func() float64

	pre       []byte // Vorlauf: letzte Stille vor dem nächsten Sprachbeginn
	sendBuf   []byte // Sprach-Audio, das noch nicht gesendet wurde
	inSpeech  bool
	silentRun int // zusammenhängende Stille seit dem letzten Sprach-Chunk
	utterLen  int // bereits gesendete Bytes der laufenden Äußerung
}

// remoteChunkBytes: Bündelgröße je audio-Nachricht (~250 ms PCM ≈ 8 KB).
const remoteChunkBytes = vadBytesPerSecond / 4

// feed verarbeitet einen Audio-Chunk (~10 ms) aus dem Capture-Callback.
// Schwellen/Zeitkonstanten identisch zum lokalen vadSegmenter (vad.go).
func (r *remoteStreamer) feed(chunk []byte) {
	g := r.gain()
	if g < 1 {
		g = 1
	}
	speech := chunkPeak(chunk)/g > vadChunkSpeechThresh

	if !r.inSpeech {
		if !speech {
			r.pre = append(r.pre, chunk...)
			if len(r.pre) > vadPreRoll {
				r.pre = r.pre[len(r.pre)-vadPreRoll:]
			}
			return
		}
		// Sprachbeginn: Vorlauf + aktuellen Chunk in die Äußerung übernehmen.
		r.inSpeech = true
		r.silentRun = 0
		r.utterLen = 0
		r.sendBuf = append(r.sendBuf, r.pre...)
		r.pre = nil
	} else if speech {
		r.silentRun = 0
	} else {
		r.silentRun += len(chunk)
	}
	r.sendBuf = append(r.sendBuf, chunk...)

	if r.silentRun >= vadPauseCut {
		// Sprechpause: überzählige Stille am Ende nicht mitsenden (bis auf
		// vadTrailKeep), dann Äußerungsende melden -> Server transkribiert.
		drop := r.silentRun - vadTrailKeep
		if drop > len(r.sendBuf) {
			drop = len(r.sendBuf)
		}
		if drop > 0 {
			r.sendBuf = r.sendBuf[:len(r.sendBuf)-drop]
		}
		r.endUtterance()
		return
	}
	// Dauersprechen ohne Pause: Notschnitt, sonst puffert der Server
	// unbegrenzt und der Text erschiene erst am Gesprächsende. Verfeinert:
	// nicht mehr HART beim Erreichen von vadMaxSeg schneiden (zerteilte
	// Woerter), sondern innerhalb einer Nachfrist (vadCutSearchWin, ~2 s)
	// auf den ersten LEISEN Chunk warten und dort trennen. Rueckwirkend wie
	// beim lokalen vadSegmenter (quietestCutPoint) geht hier nicht - das
	// Audio ist bereits zum Server gestreamt. Erst nach Ablauf der Nachfrist
	// wird notfalls doch mitten im Redefluss geschnitten.
	if total := r.utterLen + len(r.sendBuf); total >= vadMaxSeg+vadCutSearchWin ||
		(total >= vadMaxSeg && !speech) {
		r.endUtterance()
		return
	}
	if len(r.sendBuf) >= remoteChunkBytes {
		r.push(false)
	}
}

// push sendet den gepufferten Abschnitt (eou = Äußerungsende).
func (r *remoteStreamer) push(eou bool) {
	if len(r.sendBuf) == 0 && !eou {
		return
	}
	remoteSendChunk(r.sendBuf, r.speaker, eou)
	r.utterLen += len(r.sendBuf)
	r.sendBuf = nil
}

func (r *remoteStreamer) endUtterance() {
	r.push(true)
	r.inSpeech = false
	r.silentRun = 0
	r.utterLen = 0
}

// flush schließt eine laufende Äußerung ab (Aufnahme-Stopp): ohne das fehlte
// der letzte Satz, weil kein weiterer Chunk mehr die Pause melden würde.
func (r *remoteStreamer) flush() {
	if r.inSpeech {
		r.endUtterance()
	}
	r.pre = nil
}

func closeRemoteSession(speaker string) {
	remoteSessionsMu.Lock()
	defer remoteSessionsMu.Unlock()
	if s, ok := remoteSessions[speaker]; ok {
		if s.conn != nil {
			websocket.JSON.Send(s.conn, wsMsg{Type: "stop", SessionId: s.sessionId})
			s.conn.Close()
		}
		delete(remoteSessions, speaker)
	}
}

func closeAllRemoteSessions() {
	remoteSessionsMu.Lock()
	defer remoteSessionsMu.Unlock()
	for sp, s := range remoteSessions {
		if s.conn != nil {
			websocket.JSON.Send(s.conn, wsMsg{Type: "stop", SessionId: s.sessionId})
			s.conn.Close()
		}
		delete(remoteSessions, sp)
	}
}

// remoteWhisperHealth prüft, ob der Remote-Whisper-Server erreichbar ist
// (HTTP /health, abgeleitet aus der WebSocket-URL). Blockierend bis 2s.
func remoteWhisperHealth() bool {
	h := remoteWhisperURL()
	h = strings.Replace(h, "wss://", "https://", 1)
	h = strings.Replace(h, "ws://", "http://", 1)
	if i := strings.Index(h, "/ws/"); i != -1 {
		h = h[:i]
	}
	h = strings.TrimRight(h, "/") + "/health"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(h)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
