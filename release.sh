#!/usr/bin/env bash
# release.sh — Cross-kompiliert die Windows-.exe und veroeffentlicht sie als
# GitHub-Release-Asset, damit der eingebaute Auto-Updater sie findet.
#
# Auf dem DEBIAN-BUILD-RECHNER ausfuehren (mingw + gh CLI erforderlich):
#     ./release.sh
#
# Die Versionsnummer wird NICHT als Argument uebergeben, sondern aus der
# Konstante AppVersion in updater.go gelesen — sie ist die einzige Quelle der
# Wahrheit. Ablauf vor dem Release also:
#   1) AppVersion in updater.go hochzaehlen (z.B. "0.7.0" -> "0.8.0")
#   2) README.md-Version spiegeln (optional)
#   3) Aenderungen committen  (das Release-Tag zeigt auf den aktuellen HEAD)
#   4) ./release.sh
#
# Das Skript baut stt-app.exe und legt das Release "v<AppVersion>" an (bzw.
# haengt das Asset an ein bereits existierendes Release an, --clobber).

set -euo pipefail
cd "$(dirname "$0")"

REPO="dev-core-busy/stt-support-assistent"

# --- 1) Version aus updater.go lesen ---------------------------------------
VERSION="$(grep -oP 'AppVersion\s*=\s*"\K[^"]+' updater.go || true)"
if [[ -z "$VERSION" ]]; then
    echo "FEHLER: AppVersion konnte nicht aus updater.go gelesen werden." >&2
    exit 1
fi
TAG="v$VERSION"
echo "== Release $TAG =="

# --- 2) Voraussetzungen pruefen --------------------------------------------
command -v gh >/dev/null 2>&1 || { echo "FEHLER: 'gh' (GitHub CLI) nicht gefunden." >&2; exit 1; }
command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1 || { echo "FEHLER: mingw-Cross-Compiler nicht gefunden." >&2; exit 1; }

# Warnen, wenn das Tag bereits existiert und auf einen anderen Commit zeigt.
if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
    echo "Hinweis: Git-Tag $TAG existiert bereits (Asset wird via --clobber ersetzt)."
fi

# --- 3) Cross-Compile -------------------------------------------------------
echo "Baue stt-app.exe (Windows/amd64) ..."
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
    go build -ldflags "-H=windowsgui" -o stt-app.exe .
echo "  -> $(du -h stt-app.exe | cut -f1)  stt-app.exe"

# --- 4) Release anlegen oder Asset aktualisieren ---------------------------
if gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
    echo "Release $TAG existiert — lade stt-app.exe hoch (--clobber) ..."
    gh release upload "$TAG" stt-app.exe --repo "$REPO" --clobber
else
    echo "Erstelle Release $TAG und haenge stt-app.exe an ..."
    gh release create "$TAG" stt-app.exe \
        --repo "$REPO" \
        --title "$TAG" \
        --notes "Automatisches Release $TAG. stt-app.exe als Asset fuer den integrierten Auto-Updater."
fi

echo ""
echo "OK — $TAG veroeffentlicht. Bestehende Installationen aktualisieren sich beim naechsten Start."
