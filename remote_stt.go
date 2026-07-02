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
				if atHasPostProc.Load() {
					appendPendingRaw(s.speaker, text)
				} else {
					appendSpeakerSegment(s.speaker, "", text)
				}
			}
		case "error":
			Log("Remote-STT-Fehler: " + m.Message)
		}
	}
}

// sendAudio überträgt ein Audio-Segment (PCM16 mono 16k) als Base64; jedes Segment
// wird als Äußerungsende markiert, sodass der Server es transkribiert.
func (s *remoteSession) sendAudio(audio []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return websocket.JSON.Send(s.conn, wsMsg{
		Type:           "audio",
		SessionId:      s.sessionId,
		Sequence:       s.seq,
		PcmBase64:      base64.StdEncoding.EncodeToString(audio),
		EndOfUtterance: true,
	})
}

// remoteTranscribe sendet ein Segment an den Remote-Whisper-Server (Ergebnis kommt
// asynchron über readLoop). Wird aus den Audio-Buffer-Goroutinen aufgerufen.
func remoteTranscribe(audio []byte, speaker string) {
	s, err := getRemoteSession(speaker)
	if err != nil {
		Log("Remote-STT: " + err.Error())
		return
	}
	if err := s.sendAudio(audio); err != nil {
		Log("Remote-STT senden fehlgeschlagen: " + err.Error())
		closeRemoteSession(speaker)
	}
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
