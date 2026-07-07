package main

// vad.go — energiebasierte Sprachsegmentierung (VAD) und Audio-Hilfsfunktionen
// der Mitschrift.
//
// Ersetzt das fruehere starre 4-Sekunden-Fenster: das schnitt hart an der
// Byte-Grenze mitten im Wort (alle 4 s eine potenzielle Fehlstelle), Whisper
// verlor den Satzkontext ueber die Grenze, und kurze Aeusserungen gingen im
// RMS-Mittel eines fast stillen Fensters unter. Hier endet ein Segment
// stattdessen an einer SPRECHPAUSE:
//   - ein Chunk gilt als Sprache, wenn seine Spitzenamplitude (aufs ROH-Signal
//     normalisiert, d.h. durch den Kanal-Gain geteilt) die Schwelle reisst;
//   - fuehrende Stille wird nicht angesammelt (nur vadPreRoll als Vorlauf);
//   - >= vadPauseCut zusammenhaengende Stille beendet das Segment, dessen
//     abschliessende Stille bis auf vadTrailKeep abgeschnitten wird;
//   - Sprachfetzen unter vadMinSpeech (Atmer, Knackser) werden verworfen;
//   - nach vadMaxSeg wird notfalls mitten im Redefluss geschnitten.
// Whisper erhaelt so ganze Aeusserungen -> deutlich bessere Erkennung, weniger
// Halluzinationen auf Stille und schnellere Anzeige kurzer Saetze.

import (
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
)

const (
	vadBytesPerSecond = 16000 * 2 // PCM16 mono 16 kHz

	vadPreRoll   = vadBytesPerSecond * 3 / 10   // 300 ms Vorlauf vor dem ersten Sprach-Chunk
	vadPauseCut  = vadBytesPerSecond * 45 / 100 // 450 ms Stille beenden das Segment
	vadTrailKeep = vadBytesPerSecond / 5        // 200 ms Stille am Segmentende stehen lassen
	vadMinSpeech = vadBytesPerSecond * 3 / 10   // < 300 ms Sprache insgesamt: verwerfen
	vadMaxSeg    = vadBytesPerSecond * 15       // Notschnitt nach 15 s Dauersprechen

	// Notschnitt-Verfeinerung: statt hart am Puffer-Ende wird innerhalb der
	// letzten vadCutSearchWin die energiearmste Stelle gesucht (20-ms-Frames)
	// und dort geschnitten - so zerteilt auch der Notschnitt kein Wort mehr.
	// Der Rest hinter der Schnittstelle bleibt als Anfang des naechsten
	// Segments erhalten. Der Remote-Streamer (remote_stt.go) kann nicht
	// rueckwirkend schneiden (Audio ist schon gesendet) und nutzt dasselbe
	// Fenster als NACHFRIST bis zum ersten leisen Chunk.
	vadCutSearchWin = vadBytesPerSecond * 2  // ~2 s Suchfenster am Puffer-Ende
	vadCutFrame     = vadBytesPerSecond / 50 // 20-ms-Frames fuer die Energiesuche

	// vadChunkSpeechThresh: Spitzenamplitude (roh, nach Gain-Division), ab der
	// ein ~10-ms-Chunk als Sprache zaehlt (~1 % Vollausschlag).
	vadChunkSpeechThresh = 300.0
)

// quietestCutPoint liefert die Byte-Position (gerade, PCM16) der
// energiearmsten Stelle innerhalb der letzten vadCutSearchWin von buf:
// Mitte des 20-ms-Frames mit der kleinsten Energie. Rueckfall: Puffer-Ende.
func quietestCutPoint(buf []byte) int {
	start := len(buf) - vadCutSearchWin
	if start < 0 {
		start = 0
	}
	start -= start % 2
	bestPos, bestEnergy := len(buf), math.MaxFloat64
	for p := start; p+vadCutFrame <= len(buf); p += vadCutFrame {
		var sumSq float64
		for i := p; i+1 < p+vadCutFrame; i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(buf[i : i+2])))
			sumSq += v * v
		}
		if sumSq < bestEnergy {
			bestEnergy, bestPos = sumSq, p+vadCutFrame/2
		}
	}
	bestPos -= bestPos % 2
	if bestPos <= 0 || bestPos > len(buf) {
		return len(buf)
	}
	return bestPos
}

// chunkPeak liefert die Spitzenamplitude eines PCM16-Blocks.
func chunkPeak(chunk []byte) float64 {
	var max int16
	for i := 0; i+1 < len(chunk); i += 2 {
		v := int16(binary.LittleEndian.Uint16(chunk[i : i+2]))
		if v < 0 {
			v = -v
		}
		if v > max {
			max = v
		}
	}
	return float64(max)
}

// vadSegmenter sammelt die Audio-Chunks eines Kanals und schneidet an
// Sprechpausen. Laeuft komplett in der Buffer-Goroutine des Kanals
// (keine Synchronisation noetig).
type vadSegmenter struct {
	speaker string
	gain    func() float64       // aktueller Kanal-Gain (Chunks liegen bereits verstaerkt vor)
	emit    func([]byte, string) // fertiges Segment -> processSegment

	buf        []byte
	speechLen  int // Bytes, die als Sprache eingestuft wurden
	silentTail int // zusammenhaengende Stille am Ende von buf
	sawSpeech  bool
}

// feed verarbeitet einen Audio-Chunk (~10 ms aus dem Capture-Callback).
func (s *vadSegmenter) feed(chunk []byte) {
	g := s.gain()
	if g < 1 {
		g = 1
	}
	speech := chunkPeak(chunk)/g > vadChunkSpeechThresh

	s.buf = append(s.buf, chunk...)
	if speech {
		s.sawSpeech = true
		s.speechLen += len(chunk)
		s.silentTail = 0
	} else {
		s.silentTail += len(chunk)
	}

	if !s.sawSpeech {
		// Fuehrende Stille nicht ansammeln - nur den Vorlauf behalten, damit
		// der Wortanfang beim ersten Sprach-Chunk nicht abgeschnitten ist.
		if len(s.buf) > vadPreRoll {
			s.buf = s.buf[len(s.buf)-vadPreRoll:]
		}
		return
	}
	if s.silentTail >= vadPauseCut {
		s.cut()
	} else if len(s.buf) >= vadMaxSeg {
		// Notschnitt mitten im Redefluss: an der leisesten Stelle der
		// letzten ~2 s trennen statt hart am Puffer-Ende (kein zerteiltes
		// Wort); der Rest laeuft als naechstes Segment weiter.
		s.cutAtQuietest()
	}
}

// cut schliesst das aktuelle Segment ab (abschliessende Stille bis auf
// vadTrailKeep entfernen) und uebergibt es emit; zu wenig Sprache -> verwerfen.
func (s *vadSegmenter) cut() {
	end := len(s.buf) - s.silentTail + vadTrailKeep
	if end > len(s.buf) {
		end = len(s.buf)
	}
	if s.speechLen >= vadMinSpeech && end > 0 {
		seg := make([]byte, end)
		copy(seg, s.buf[:end])
		s.emit(seg, s.speaker)
	}
	s.buf = nil
	s.speechLen = 0
	s.silentTail = 0
	s.sawSpeech = false
}

// cutAtQuietest: Notschnitt bei Dauersprechen - emittiert den Puffer bis zur
// energiearmsten Stelle der letzten ~2 s (quietestCutPoint); der Rest dahinter
// bleibt als Anfang des naechsten Segments im Puffer (wir stehen mitten in
// der Sprache, daher sawSpeech=true; speechLen des Rests ist eine Naeherung).
func (s *vadSegmenter) cutAtQuietest() {
	pos := quietestCutPoint(s.buf)
	if s.speechLen >= vadMinSpeech && pos > 0 {
		seg := make([]byte, pos)
		copy(seg, s.buf[:pos])
		s.emit(seg, s.speaker)
	}
	rest := make([]byte, len(s.buf)-pos)
	copy(rest, s.buf[pos:])
	s.buf = rest
	s.speechLen = len(rest)
	s.silentTail = 0
	s.sawSpeech = true
}

// flush erzwingt einen Schnitt (Aufnahme-Stopp): der Restpuffer wird noch
// transkribiert statt verworfen - vorher fehlten so bis zu 4 s vom Ende
// des Gespraechs im Transkript.
func (s *vadSegmenter) flush() {
	if s.sawSpeech {
		s.cut()
		return
	}
	s.buf = nil
	s.silentTail = 0
}

// ---------------------------------------------------------------------------
// Pegelanzeige: gedrosselte UI-Updates
// ---------------------------------------------------------------------------

// Vorher lief fyne.Do bei JEDEM ~10-ms-Audio-Callback (~100 UI-Aufrufe/s je
// Kanal). Jetzt hoechstens alle meterUpdateNs; zwischenzeitliche Spitzen
// gehen nicht verloren (hoechster Pegel seit dem letzten Update wird
// akkumuliert und dann angezeigt).
const meterUpdateNs = int64(66 * time.Millisecond)

var (
	agentMeterLastNs  atomic.Int64
	agentMeterPeak    atomic.Uint64 // float64-Bits
	callerMeterLastNs atomic.Int64
	callerMeterPeak   atomic.Uint64
)

func updateMeterThrottled(lastNs *atomic.Int64, peakBits *atomic.Uint64, level float64, apply func(float64)) {
	for {
		old := peakBits.Load()
		if level <= math.Float64frombits(old) {
			break
		}
		if peakBits.CompareAndSwap(old, math.Float64bits(level)) {
			break
		}
	}
	now := time.Now().UnixNano()
	last := lastNs.Load()
	if now-last < meterUpdateNs || !lastNs.CompareAndSwap(last, now) {
		return
	}
	lv := math.Float64frombits(peakBits.Swap(0))
	fyne.Do(func() { apply(lv) })
}

// ---------------------------------------------------------------------------
// Digitale Verstaerkung mit weichem Limiter
// ---------------------------------------------------------------------------

// applyGainSoftClip verstaerkt PCM16 in-place. Spitzen oberhalb des Knies
// werden weich komprimiert statt hart gekappt - hartes Clipping erzeugt
// Obertoene/Verzerrung, die die Whisper-Erkennung verschlechtern.
func applyGainSoftClip(p []byte, gain float64) {
	if gain <= 1 {
		return
	}
	const knee = 28000.0
	for i := 0; i+1 < len(p); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(p[i:i+2]))) * gain
		av := v
		if av < 0 {
			av = -av
		}
		if av > knee {
			av = knee + (av-knee)*0.25
			if av > 32767 {
				av = 32767
			}
			if v < 0 {
				v = -av
			} else {
				v = av
			}
		}
		binary.LittleEndian.PutUint16(p[i:i+2], uint16(int16(v)))
	}
}

// ---------------------------------------------------------------------------
// Kontext-Priming: letzter erkannter Text als initial prompt fuer Whisper
// ---------------------------------------------------------------------------

// sttTail haelt die letzten ~200 Zeichen erkannten Textes (ohne Sprecher-
// Labels). Der lokale whisper-cli bekommt sie als --prompt mit: konsistente
// Schreibweisen und Eigennamen (Kunden-/Produktnamen) ueber Segmentgrenzen
// hinweg, weniger Fehler am Segmentanfang. Bei Aufnahme-Start geleert.
var (
	sttTailMu sync.Mutex
	sttTail   string
)

func updateSTTTail(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	sttTailMu.Lock()
	defer sttTailMu.Unlock()
	joined := strings.TrimSpace(sttTail + " " + text)
	if r := []rune(joined); len(r) > 200 {
		joined = string(r[len(r)-200:])
	}
	sttTail = joined
}

func getSTTTail() string {
	sttTailMu.Lock()
	defer sttTailMu.Unlock()
	return sttTail
}

func resetSTTTail() {
	sttTailMu.Lock()
	defer sttTailMu.Unlock()
	sttTail = ""
}
