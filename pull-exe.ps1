# pull-exe.ps1  -  Holt die frisch gebaute stt-app.exe vom Debian-Build-Rechner
# ==========================================================================
# Auf dem WINDOWS-TESTRECHNER ausfuehren. Kopiert per SCP (OpenSSH, in
# Windows 10/11 eingebaut) die auf Debian cross-kompilierte stt-app.exe
# hierher und beendet vorher eine ggf. laufende App (sonst Dateisperre).
#
# Aufruf (in PowerShell, im Zielordner):
#     .\pull-exe.ps1
# Optional Parameter ueberschreiben:
#     .\pull-exe.ps1 -RemoteHost 191.100.144.16 -RemoteUser bender
# --------------------------------------------------------------------------

param(
    # Debian-Build-Rechner. Default = kabelgebundene LAN-IP (eno1).
    # Alternativen: 191.100.144.16 (WLAN) oder 100.98.115.116 (Tailscale).
    [string]$RemoteHost = "191.100.144.15",

    # SSH-Benutzer auf dem Debian-Rechner (Projekt gehoert 'bender').
    [string]$RemoteUser = "bender",

    # Vollstaendiger Pfad der exe auf dem Debian-Rechner.
    [string]$RemotePath = "/home/bender/ai/projekte/live_SST/stt-app.exe",

    # Zielordner auf Windows. Default = Ordner, in dem dieses Skript liegt.
    [string]$LocalDir   = $PSScriptRoot,

    # SSH-Private-Key fuer die Anmeldung (Key-basiert, kein Passwort).
    [string]$IdentityFile = "C:\Users\bender\.ssh\id_rsa",

    # Wenn gesetzt: laufende App NICHT beenden (nur kopieren).
    [switch]$NoKill
)

$ErrorActionPreference = "Stop"
if ([string]::IsNullOrWhiteSpace($LocalDir)) { $LocalDir = (Get-Location).Path }
$LocalPath = Join-Path $LocalDir "stt-app.exe"

Write-Host "== stt-app.exe holen ==" -ForegroundColor Cyan
Write-Host "  Quelle : ${RemoteUser}@${RemoteHost}:${RemotePath}"
Write-Host "  Ziel   : $LocalPath"
Write-Host ""

# 1) Laufende App beenden (Datei ist sonst gesperrt und kann nicht ueberschrieben werden)
if (-not $NoKill) {
    $procs = Get-Process -Name "stt-app" -ErrorAction SilentlyContinue
    if ($procs) {
        Write-Host "Beende laufende stt-app ($($procs.Count) Prozess(e)) ..." -ForegroundColor Yellow
        $procs | Stop-Process -Force
        Start-Sleep -Milliseconds 800   # Dateihandle freigeben lassen
    }
}

# 2) OpenSSH-Client vorhanden?
$scp = Get-Command scp -ErrorAction SilentlyContinue
if (-not $scp) {
    throw "scp nicht gefunden. OpenSSH-Client installieren: Einstellungen > Apps > Optionale Features > 'OpenSSH-Client'."
}

# 3) SSH-Key vorhanden?
if (-not (Test-Path -LiteralPath $IdentityFile)) {
    throw "SSH-Key nicht gefunden: $IdentityFile"
}

# 3b) Zugriffsrechte des privaten Schluessels korrigieren. Windows-OpenSSH lehnt
#     einen Key STILLSCHWEIGEND ab, wenn weitere Benutzer/Gruppen (z.B. "Users",
#     "Authenticated Users") Zugriff darauf haben, und faellt dann ohne Fehler auf
#     eine Passwortabfrage zurueck - genau das Symptom "...password:" statt Login.
#     Setzt die Vererbung zurueck und erlaubt nur dem aktuellen Benutzer Lesezugriff.
Write-Host "Pruefe Zugriffsrechte des SSH-Keys ..." -ForegroundColor Cyan
icacls "$IdentityFile" /inheritance:r | Out-Null
icacls "$IdentityFile" /grant:r "${env:USERNAME}:(R)" | Out-Null

# 4) Kopieren per SCP mit Key (-i). Kein Passwort noetig.
#    -o StrictHostKeyChecking=accept-new: Host-Key beim ERSTEN Kontakt automatisch
#    akzeptieren und in known_hosts merken (keine Ja/Nein-Eingabe noetig), spaetere
#    Verbindungen werden trotzdem gegen den gemerkten Key geprueft.
#    -o IdentitiesOnly=yes: erzwingt genau diesen Key statt evtl. andere Keys aus
#    einem laufenden ssh-agent zu probieren.
Write-Host "Kopiere per SCP (Key: $IdentityFile) ..." -ForegroundColor Cyan
& scp -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes -i "$IdentityFile" "${RemoteUser}@${RemoteHost}:${RemotePath}" "$LocalPath"
if ($LASTEXITCODE -ne 0) {
    throw "SCP fehlgeschlagen (Exit $LASTEXITCODE). Host/Benutzer/Key/SSH-Zugang pruefen."
}

# 5) Ergebnis melden
$fi = Get-Item $LocalPath
Write-Host ""
Write-Host "OK - kopiert:" -ForegroundColor Green
Write-Host ("  {0}  ({1:N0} Bytes, {2})" -f $fi.FullName, $fi.Length, $fi.LastWriteTime)
Write-Host ""
Write-Host "Start mit:  .\stt-app.exe" -ForegroundColor Cyan
Write-Host "(Erster Start laedt libs/ + models/ nach, benoetigt Internet.)"
