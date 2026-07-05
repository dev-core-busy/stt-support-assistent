package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const logFile = "log.txt"

func InitLogger() {
	// Alte Logdatei löschen
	os.Remove(logFile)
	Log("--- App gestartet ---")
}

func Log(message string) {
	if !atLogging.Load() {
		return
	}

	// Pfade relativ zur App machen (exeDir ausblenden)
	if exeDir != "" {
		message = strings.ReplaceAll(message, exeDir, "")
	}

	// Format: jjjjmmtt - hh:mm -
	timestamp := time.Now().Format("20060102 - 15:04 - ")
	entry := timestamp + message + "\n"

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Fehler beim Schreiben in Logdatei: %v\n", err)
		return
	}
	defer f.Close()

	f.WriteString(entry)
}
