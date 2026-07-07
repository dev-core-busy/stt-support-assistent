# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Portable Speech-to-Text (STT) application with local AI-powered text correction and analysis. Built in Go with a Fyne GUI, it uses a hybrid AI pipeline: **OpenAI Whisper** for speech recognition and **Google Gemma 4** for intelligent text correction/formatting. The app is fully self-contained — it downloads all binaries and models on first launch.

Two STT pipelines are available:
- **Whisper + Gemma** (default): whisper.cpp for STT → llama.cpp (Gemma 4) for grammar correction at sentence boundaries
- **Gemma Native**: llama.cpp multimodal directly transcribes audio via the Gemma 4 E2B model

Additional analysis modes: local Gemma 4, Google Gemini Flash, or remote Ollama.

## Build Commands

### Linux (native)
```bash
go build -o stt-app .
```

### Windows (cross-compile from Linux)
```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -ldflags "-H=windowsgui" -o stt-app.exe .
```

### Run
```bash
./stt-app        # Linux
./stt-app.exe    # Windows
```

First launch requires internet to download ~2GB of models and binaries. Subsequent runs are fully offline.

## Source Architecture

| File | Purpose |
|------|---------|
| `main.go` | GUI (Fyne), audio capture (malgo), STT pipeline orchestration, config UI, analysis logic |
| `setup_manager.go` | Self-contained dependency manager: downloads Whisper/Gemma models and binary zips on first run |
| `server_manager.go` | Lifecycle of the embedded llama-server process (start, health-check, stop, log piping) |
| `logger.go` | File-based logger writing to `log.txt` with timestamp format |
| `sys_windows.go` | Windows-specific: CREATE_NO_WINDOW for silent processes, native window positioning via user32.dll, rich clipboard |
| `sys_linux.go` | Stub: no silent-mode flag, no native window positioning |
| `main_others.go` | Stub: `saveWindowPosition`/`restoreWindowPosition` no-ops on non-Windows |

## Runtime Structure

```
./
├── libs/          # whisper-cli, llama-server, llama-cli, DLLs (populated at runtime)
├── models/        # whisper-base.bin, gemma-4 E2B GGUF, mmproj, MTP drafter (populated at runtime)
├── config.json    # Persisted AppConfig (JSON)
├── log.txt        # Application log
└── stt-app/.exe   # Built binary
```

## Key Design Points

- **Config**: `AppConfig` struct (line 277 of main.go) persisted as JSON in the exe directory. Migration from Fyne preferences exists (`LoadConfig`). Window position/gain/pipeline settings all configurable via UI.
- **Audio**: 16kHz mono S16 capture via malgo. Two audio paths: mic capture (agent) and Windows loopback (caller/teams audio). Per-channel digital gain with soft-clip limiter (`applyGainSoftClip` in vad.go).
- **Silence detection** (`detectSilence` in main.go): amplitude-based threshold (gain-normalized) triggers paragraph breaks / LLM correction flush at configurable pause intervals.
- **Audio processing**: energy-based VAD segmentation (`vad.go`): segments are cut at speech pauses (450ms silence; min 300ms speech, max 15s — the forced cut picks the lowest-energy point of the last ~2s via `quietestCutPoint`, carrying the remainder into the next segment; leading/trailing silence trimmed) → in-RAM WAV → local whisper-server (`POST /inference`, port 8082, `prompt` field for context priming; `whisper-cli` spawn as fallback) or remote WebSocket. Local model: `ggml-large-v3-turbo-q5_0` preferred, `whisper-base` fallback (`localWhisperModelPath`). Remaining buffer is flushed when recording stops. Open follow-ups documented in TODO.md.
- **Gemma Native pipeline**: Uses llama-server's OpenAI-compatible `/v1/chat/completions` endpoint with multimodal audio input (base64 WAV + text). Expects Gemma 4 E2B model with mmproj.
- **Llama server**: Runs on 127.0.0.1:8080, started as background process on app init, health-checked with 60s timeout, killed on app close.
- **Analysis**: Triggered manually; routes to local Gemma, Gemini Flash, or Ollama based on `config.AnalysisMode`. Output shown in a separate Fyne window with markdown formatting.
- **Build tags**: `sys_windows.go` uses `//go:build windows`, `sys_linux.go` and `main_others.go` use `//go:build !windows`. All in `package main`.
- **Vosk dependency**: `go.mod` has `replace github.com/alphacep/vosk-api/go => ./vosk-go` but Vosk is not used in the current codebase (the project migrated to Whisper + Gemma). The `vosk-go` local directory is absent.
- **`generate_workflow.py`**: A Python/PIL script for generating the app's workflow diagram GIF — not part of the app itself.
- **Platform**: Primarily targets Windows x64 but compiles and runs on Linux. Linux builds lack silent mode, native window positioning, and loopback audio capture.
