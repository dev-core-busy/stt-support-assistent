@echo off
REM pull-and-run.bat  -  Holt die frische stt-app.exe (pull-exe.ps1) und startet sie.
REM Auf dem WINDOWS-TESTRECHNER per Doppelklick oder aus der Konsole ausfuehren.
setlocal

REM In den Ordner wechseln, in dem dieses Batch-Skript liegt.
cd /d "%~dp0"

echo == pull-exe.ps1 ausfuehren ==
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0pull-exe.ps1"
if errorlevel 1 (
    echo.
    echo FEHLER: pull-exe.ps1 ist fehlgeschlagen. stt-app.exe wird NICHT gestartet.
    pause
    exit /b 1
)

echo.
echo == stt-app.exe starten ==
start "" "%~dp0stt-app.exe"

endlocal
