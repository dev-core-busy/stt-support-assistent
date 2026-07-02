package main

import (
	"bufio"
	"fmt"
	"io"
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

func (inst *llamaInstance) running() bool { return inst != nil && inst.cmd != nil && inst.cmd.Process != nil }

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
			needed[sym] = true
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
}

func stopAllServers() {
	serversMu.Lock()
	defer serversMu.Unlock()
	for _, inst := range localServers {
		stopInstance(inst)
	}
}
