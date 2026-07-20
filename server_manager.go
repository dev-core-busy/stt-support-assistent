package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// llamaInstance ist ein lokal laufender llama-server für genau ein Modell.
type llamaInstance struct {
	symbol     string // "e2b" / "12b"
	port       string // fester Port je Modell
	cmd        *exec.Cmd
	ready      atomic.Bool  // antwortet /health mit 200?
	busy       atomic.Int32 // laufende Anfragen
	restarting atomic.Bool  // wird gerade gestoppt/gestartet -> keine neuen Anfragen
}

// localServers: feste Modell→Port-Zuordnung. e2b und 12b können parallel laufen.
var localServers = map[string]*llamaInstance{
	"e2b": {symbol: "e2b", port: "8080"},
	"12b": {symbol: "12b", port: "8081"},
}
var serversMu sync.Mutex // serialisiert Start/Stop

func instanceFor(symbol string) *llamaInstance { return localServers[symbol] }

func (inst *llamaInstance) baseURL() string { return "http://127.0.0.1:" + inst.port }

func (inst *llamaInstance) running() bool {
	return inst != nil && inst.cmd != nil && inst.cmd.Process != nil
}

// instHealth fragt /health der Instanz ab.
func instHealth(inst *llamaInstance) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(inst.baseURL() + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// refreshServerHealth aktualisiert das ready-Flag aller Instanzen (für die Pills).
func refreshServerHealth() {
	for _, inst := range localServers {
		if inst.running() {
			inst.ready.Store(instHealth(inst))
		} else {
			inst.ready.Store(false)
		}
	}
}

func pipeToLog(r io.Reader, tag string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if t := scanner.Text(); strings.TrimSpace(t) != "" {
			Log(tag + ": " + t)
		}
	}
}

// startInstance startet den llama-server für inst (Modell muss heruntergeladen sein).
func startInstance(inst *llamaInstance) error {
	bin := filepath.Join(exeDir, "libs", "llama-server")
	if runtime.GOOS == "windows" {
		bin = filepath.Join(exeDir, "libs", "llama-server.exe")
	}
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		return fmt.Errorf("llama-server nicht gefunden")
	}
	m := findLocalModel(modelFileForSymbol(inst.symbol))
	if m == nil {
		return fmt.Errorf("unbekanntes Modell-Symbol: %s", inst.symbol)
	}
	modelPath := filepath.Join(exeDir, "models", m.File)
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("Modell-Datei fehlt: %s", m.File)
	}

	threads := runtime.NumCPU() / 2
	if threads < 1 {
		threads = 1
	}
	args := []string{
		"-m", modelPath,
		"-ngl", "99",
		"-c", "8192",
		"-b", "2048",
		"-ub", "2048",
		"--port", inst.port,
		"--host", "127.0.0.1",
		"-t", fmt.Sprintf("%d", threads),
	}
	// Bewusst KEIN --mmproj: die lokalen Modelle werden nur für Text (Nachbearbeitung/
	// Analyse) genutzt. Der Multimodal-Projektor ist überflüssig und manche (z.B.
	// der 12B-mmproj mit Projektor-Typ "gemma4uv") werden von dieser llama.cpp-
	// Version nicht unterstützt und würden den Server-Start zum Absturz bringen.

	cmd := exec.Command(bin, args...)
	setSilent(cmd)
	cmd.Dir = filepath.Join(exeDir, "libs")
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Start fehlgeschlagen: %v", err)
	}
	inst.cmd = cmd
	tag := "LLAMA[" + inst.symbol + "]"
	Log(fmt.Sprintf("Llama-Server '%s' gestartet (Port %s)", inst.symbol, inst.port))
	go pipeToLog(stdoutPipe, tag)
	go pipeToLog(stderrPipe, tag+"-ERR")

	// Warmup-Health-Check
	go func() {
		for i := 0; i < 60; i++ {
			if instHealth(inst) {
				inst.ready.Store(true)
				Log(fmt.Sprintf("Llama-Server '%s' ist bereit.", inst.symbol))
				return
			}
			time.Sleep(2 * time.Second)
		}
		Log(fmt.Sprintf("WARNUNG: Llama-Server '%s' Health-Timeout.", inst.symbol))
	}()
	return nil
}

func stopInstance(inst *llamaInstance) {
	inst.ready.Store(false)
	if inst.cmd != nil && inst.cmd.Process != nil {
		Log("Beende Llama-Server '" + inst.symbol + "'...")
		if runtime.GOOS == "windows" {
			inst.cmd.Process.Kill()
		} else {
			inst.cmd.Process.Signal(os.Interrupt)
		}
	}
	inst.cmd = nil
}

// ensureLocalServers startet die von Nachbearbeitung/Analyse referenzierten lokalen
// Modelle (bedarfsgesteuert) und stoppt nicht mehr benötigte. Modelle müssen
// vorher heruntergeladen sein (sonst Logfehler, Server startet nicht).
func ensureLocalServers() {
	serversMu.Lock()
	defer serversMu.Unlock()

	needed := map[string]bool{}
	for _, sym := range []string{config.PostProcModel, config.AnalysisModel} {
		if sym == "e2b" || sym == "12b" {
			// "download on demand": einen lokalen Server nur dann starten, wenn das
			// Modell auch wirklich vorhanden ist. So verursacht eine (Default-)Auswahl,
			// deren Modell noch nie geladen wurde, beim Start keinen Fehlversuch; das
			// Modell wird erst nach bewusster Auswahl+Download in den Einstellungen
			// bereitgestellt (siehe selectLocalModel in main.go).
			if localModelExists(modelFileForSymbol(sym)) {
				needed[sym] = true
			}
		}
	}
	for sym, inst := range localServers {
		if needed[sym] && !inst.running() {
			if err := startInstance(inst); err != nil {
				Log(fmt.Sprintf("Konnte Server '%s' nicht starten: %v", sym, err))
			}
		} else if !needed[sym] && inst.running() {
			stopInstance(inst)
		}
	}
	// Der lokale whisper-server gehoert mit zu den lokalen Servern: bei jedem
	// Abgleich mitziehen (Start/Stop/Modellwechsel; eigener Mutex).
	ensureWhisperServer()
}

func stopAllServers() {
	serversMu.Lock()
	defer serversMu.Unlock()
	for _, inst := range localServers {
		stopInstance(inst)
	}
	stopWhisperServer()
}

// ---------------------------------------------------------------------------
// Lokaler whisper-server (TODO.md Punkt 1): einmal beim Start hochziehen,
// Modell bleibt im RAM - statt pro Segment whisper-cli zu spawnen (0,5-2 s
// Modell-Ladezeit JEDES Mal). Segmente kommen per HTTP POST /inference als
// multipart-WAV direkt aus dem RAM (createWavData) - kein Disk-I/O.
// whisper-cli bleibt als Rueckfall, solange der Server (noch) nicht bereit
// ist (Warmup) oder die Anfrage scheitert (s. processSegment).
// ---------------------------------------------------------------------------

// whisperSrv: der eine lokale whisper-server (Port neben den llama-Ports).
var whisperSrv = struct {
	port  string
	cmd   *exec.Cmd
	ready atomic.Bool
	model string // Pfad des geladenen Modells (Neustart bei Wechsel)
}{port: "8082"}

// whisperSrvMu serialisiert Start/Stop des whisper-servers (eigener Mutex,
// NICHT serversMu: ensureWhisperServer wird aus ensureLocalServers heraus
// aufgerufen, das serversMu bereits haelt).
var whisperSrvMu sync.Mutex

func whisperSrvBaseURL() string { return "http://127.0.0.1:" + whisperSrv.port }

// ensureWhisperServer startet/stoppt den lokalen whisper-server passend zur
// Konfiguration: laufen soll er nur bei lokaler Erkennung (config.WhisperLocal)
// und vorhandenem Binary+Modell. Ein Modellwechsel (turbo-Download inzwischen
// fertig) loest einen Neustart aus. Aufruf beim App-Start (ensureLocalServers)
// und beim Umschalten des Erkennungs-Radios.
func ensureWhisperServer() {
	whisperSrvMu.Lock()
	defer whisperSrvMu.Unlock()

	bin := filepath.Join(exeDir, "libs", "whisper-server")
	if runtime.GOOS == "windows" {
		bin = filepath.Join(exeDir, "libs", "whisper-server.exe")
	}
	modelPath := localWhisperModelPath()
	want := config.WhisperLocal
	if _, err := os.Stat(bin); err != nil {
		if want {
			Log("whisper-server nicht gefunden - Erkennung nutzt weiter whisper-cli")
		}
		want = false
	}
	if _, err := os.Stat(modelPath); err != nil {
		want = false
	}

	running := whisperSrv.cmd != nil && whisperSrv.cmd.Process != nil
	if running && (!want || whisperSrv.model != modelPath) {
		stopWhisperServerLocked()
		running = false
	}
	if !want || running {
		return
	}

	// Threads: fast alle Kerne (statt der Haelfte wie bei llama) - die
	// Transkription muss SCHNELLER als Echtzeit laufen, sonst staut sich
	// die Segment-Warteschlange (s. runVADLoop) und Segmente gehen verloren.
	// Sie laeuft zudem nur in kurzen Schueben je Segment.
	threads := runtime.NumCPU() - 2
	if threads < 2 {
		threads = 2
	}
	args := []string{
		"-m", modelPath,
		"--host", "127.0.0.1",
		"--port", whisperSrv.port,
		"-l", "de",
		"-t", fmt.Sprintf("%d", threads),
	}
	cmd := exec.Command(bin, args...)
	setSilent(cmd)
	cmd.Dir = filepath.Join(exeDir, "libs")
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		Log("whisper-server Start fehlgeschlagen: " + err.Error())
		return
	}
	whisperSrv.cmd = cmd
	whisperSrv.model = modelPath
	Log(fmt.Sprintf("whisper-server gestartet (Port %s, Modell %s, %d Threads)", whisperSrv.port, filepath.Base(modelPath), threads))
	// whisper-server schreibt ALLE Diagnose-Ausgaben (Init, system_info,
	// "processing ...") auf stderr - das sind KEINE Fehler, daher derselbe
	// neutrale Log-Tag wie fuer stdout (das fruehere "-ERR" las sich wie
	// eine Warnungsflut).
	go pipeToLog(stdoutPipe, "WHISPER-SRV")
	go pipeToLog(stderrPipe, "WHISPER-SRV")

	// Warmup: GET /health (in whisper.cpp v1.8.4 vorhanden) antwortet 200,
	// sobald der Server laeuft und das Modell geladen ist.
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		for i := 0; i < 60; i++ {
			if resp, err := client.Get(whisperSrvBaseURL() + "/health"); err == nil {
				ok := resp.StatusCode == http.StatusOK
				resp.Body.Close()
				if ok {
					whisperSrv.ready.Store(true)
					Log("whisper-server ist bereit.")
					return
				}
			}
			time.Sleep(2 * time.Second)
		}
		Log("WARNUNG: whisper-server Warmup-Timeout.")
	}()
}

func stopWhisperServer() {
	whisperSrvMu.Lock()
	defer whisperSrvMu.Unlock()
	stopWhisperServerLocked()
}

func stopWhisperServerLocked() {
	whisperSrv.ready.Store(false)
	if whisperSrv.cmd != nil && whisperSrv.cmd.Process != nil {
		Log("Beende whisper-server...")
		if runtime.GOOS == "windows" {
			whisperSrv.cmd.Process.Kill()
		} else {
			whisperSrv.cmd.Process.Signal(os.Interrupt)
		}
	}
	whisperSrv.cmd = nil
}

// whisperServerTranscribe schickt ein PCM16-Segment als WAV (aus dem RAM,
// createWavData) an den lokalen whisper-server: POST /inference, multipart.
// Kontext-Priming laeuft ueber das Formularfeld "prompt" (Pendant zum
// --prompt des CLI). ok=false, wenn der Server nicht bereit ist oder die
// Anfrage scheitert - dann uebernimmt der whisper-cli-Rueckfall.
func whisperServerTranscribe(audio []byte, prompt string) (string, bool) {
	if !whisperSrv.ready.Load() {
		return "", false
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "segment.wav")
	if err != nil {
		return "", false
	}
	fw.Write(createWavData(audio))
	mw.WriteField("response_format", "json")
	mw.WriteField("language", "de")
	mw.WriteField("temperature", "0.0")
	if prompt != "" {
		mw.WriteField("prompt", prompt)
	}
	mw.Close()

	req, err := http.NewRequest("POST", whisperSrvBaseURL()+"/inference", &body)
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		Log("whisper-server /inference: " + err.Error())
		return "", false
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		Log(fmt.Sprintf("whisper-server /inference: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data))))
		return "", false
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		Log("whisper-server /inference: Antwort nicht lesbar: " + err.Error())
		return "", false
	}
	return strings.TrimSpace(out.Text), true
}
