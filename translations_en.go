package main

// enOverrides: englische Übersetzung je deutschem UI-Text (Basissprache DE).
// Schlüssel = exakter deutscher Originaltext im Code. Fehlt ein Eintrag, zeigt
// die App den deutschen Text (unschädlicher Fallback). Formatstrings müssen in
// beiden Sprachen dieselben %-Platzhalter in gleicher Reihenfolge enthalten.
//
// Sprachneutrale Bezeichner sind bewusst NICHT enthalten (Eigennamen wie
// "Ollama", "vLLM", "Google Flash", Modellnamen wie "Gemma 4 E2B" sowie
// Options-Werte von Radios/Selects, die zugleich als Logik-Schlüssel dienen).
var enOverrides = map[string]string{
	// --- Statuszeile / Aufnahme ---
	"Initialisiere...":                    "Initializing...",
	"Bereit":                              "Ready",
	"Höre zu...":                          "Listening...",
	"Analysiere...":                       "Analyzing...",
	"Engine: Wartet...":                   "Engine: Waiting...",
	"Fehler bei Dependencies!":            "Dependency error!",
	"Mitschrift":                          "Transcript",
	"Mitschrift stoppen":                  "Stop transcript",
	"Bereite Modell vor …":                "Preparing model …",
	"Gesprochener Text erscheint hier...": "Spoken text appears here...",
	"Inhalt leeren":                       "Clear content",
	"In Zwischenablage kopieren":          "Copy to clipboard",
	"Kein Text zum Kopieren vorhanden.":   "No text to copy.",
	"Mitschrift wurde in die Zwischenablage kopiert.": "Transcript copied to clipboard.",
	"Analysieren": "Analyze",

	// --- Tab-Titel ---
	"KI-Support (Jarvis)":  "AI Support (Jarvis)",
	"System-Einstellungen": "System Settings",

	// --- Einstellungen: Abschnitte / Labels ---
	"Spracherkennung und Analyse":              "Speech recognition and analysis",
	"Betriebsmodus":                            "Operating mode",
	"Audio-Geräte":                             "Audio devices",
	"Mikrofon:":                                "Microphone:",
	"Lautsprecher:":                            "Speaker:",
	"Transkribierung über:":                    "Transcription via:",
	"Remote-Whisper-URL:":                      "Remote Whisper URL:",
	"Nachbearbeitung:":                         "Post-processing:",
	"Analyse (manuell, mit Prompt):":           "Analysis (manual, with prompt):",
	"LLM Prompt zur Analyse:":                  "LLM prompt for analysis:",
	"KI-Analyse Prompt (z.B. Fasse zusammen)":  "AI analysis prompt (e.g. summarize)",
	"Analyse-Vorgabe:":                         "Analysis preset:",
	"(keine Vorgabe)":                          "(no preset)",
	"Beschreibung, z. B. \"Zusammenfassung\"":  "Description, e.g. \"Summary\"",
	"Vorgabe speichern":                        "Save preset",
	"Bitte zuerst eine Beschreibung eingeben.": "Please enter a description first.",
	"Sprache:":                                 "Language:",
	"Einstellungen jetzt speichern":            "Save settings now",
	"Soundeinstellungen ändern":                "Change sound settings",
	"remote LLM konfigurieren":                 "Configure remote LLM",

	// --- Einstellungen: Ticketsuche / Webhook / CRM ---
	"Automatische Ticketsuche":       "Automatic ticket search",
	"aktiviert":                      "enabled",
	"Anrufer automatisch suchen":     "Search caller automatically",
	"Mitschrift bei Anruf starten":   "Start transcript on call",
	"Prompt für passende Tickets:":   "Prompt for matching tickets:",
	"Prompt für KI-Zusammenfassung:": "Prompt for AI summary:",
	"Rufnummern Übergabe":            "Phone number handover",
	"Webhook aktiv:":                 "Webhook active:",
	"Webhook-URL (Pfad):":            "Webhook URL (path):",

	"Mehrere CRM-Treffer": "Multiple CRM matches",
	"Debug-Modus (Anfrage vor Versand anzeigen: Suchen/Analysieren)": "Debug mode (show request before sending: search/analyze)",

	// --- Einstellungen: Remote-Backends ---
	"Server-URL:": "Server URL:",
	"API-Key:":    "API key:",
	"API-Key":     "API key",
	"API Key":     "API key",
	"API key:":    "API key:",
	"Modelname:":  "Model name:",
	"Modell-Name": "Model name",
	"URL:":        "URL:",
	"Port:":       "Port:",

	// --- Einstellungen: System / Pfade ---
	"System-Logging aktivieren":                   "Enable system logging",
	"System-Logging aktiviert":                    "System logging enabled",
	"Automatisch bei Windows-Anmeldung starten":   "Start automatically at Windows sign-in",
	"Autostart konnte nicht geändert werden:\n%v": "Could not change autostart:\n%v",
	"Speicherpfade":                               "Storage paths",
	"Modelle: ./models/":                          "Models: ./models/",
	"Binaries: ./libs/":                           "Binaries: ./libs/",

	// --- Modell-Auswahl / Server ---
	"Suche Modelle...":      "Searching models...",
	"Modell herunterladen?": "Download model?",
	"Modell wird geladen":   "Downloading model",
	"Im Hintergrund weiter": "Continue in background",

	// --- Schlagwort-Ticketsuche (Schritt 1, Vorschau) ---
	"Extrahiere aus dem folgenden Support-Gespräch 2 bis 3 Schlagworte, die das Anliegen am besten beschreiben. Antworte NUR mit den Schlagworten, durch Kommas getrennt, ohne weitere Erklärung.": "Extract 2 to 3 keywords from the following support conversation that best describe the issue. Reply ONLY with the keywords, comma-separated, without any explanation.",
	"Schlagworte zur Ticketsuche": "Keywords for ticket search",
	"Extrahierte Schlagworte:":    "Extracted keywords:",
	"Hinweis: Die Jarvis-API muss noch angepasst werden, damit mit diesen Schlagworten auch dort passende Tickets gesucht werden können. Die Kundenverwaltung (getMatchingEvents) wird bereits abgefragt, sofern ein Anruf eine Kundenv.-ID geliefert hat.": "Note: The Jarvis API still needs to be adapted so that matching tickets can also be searched there using these keywords. Customer management (getMatchingEvents) is already queried, provided a call supplied a customer-mgmt ID.",
	"Treffer zu: ": "Matches for: ",
	"Bitte mindestens eine Quelle auswählen (Jira/Confluence/Wissen oder IBS Tickets).":       "Please select at least one source (Jira/Confluence/Knowledge or IBS tickets).",
	"Kundenverwaltung-Suche: Jarvis-Server-URL ist nicht konfiguriert (siehe Einstellungen).": "Customer management search: Jarvis server URL is not configured (see settings).",
	"Kundenverwaltung nicht erreichbar (%s): %v":                                              "Customer management not reachable (%s): %v",
	"Kundenverwaltung meldet HTTP %d":                                                         "Customer management returned HTTP %d",
	"Kundenverwaltung: Antwort ist kein gültiges JSON: %v":                                    "Customer management: response is not valid JSON: %v",
	"Kundenverwaltung": "Customer management",
	"Kundenverwaltung: keine Kundennummer bekannt (erst nach einem Anruf mit Kundenverwaltungs-Treffer verfügbar).": "Customer management: no customer number known (only available after a call with a customer-management match).",
	"Schlagworte konnten nicht ermittelt werden: ":                                                                  "Keywords could not be determined: ",
	"(keine Antwort erhalten)": "(no response received)",
	"Das Textfenster ist leer – es werden alle Tickets zur gefundenen CRM geladen.\nSchlagworte können erst extrahiert werden, wenn eine Mitschrift vorliegt.": "The text window is empty – loading all tickets for the found CRM instead.\nKeywords can only be extracted once a transcript is available.",
	"Das Textfenster ist leer – es können keine Schlagworte extrahiert werden.":                                                                                "The text window is empty – no keywords can be extracted.",

	// --- KI-Support-Panel (Jarvis) ---
	"Suche passende Tickets":         "Search matching tickets",
	"Suche nach passenden Tickets":   "Search for matching tickets",
	"Noch keine Suche durchgeführt.": "No search performed yet.",
	"Keine Treffer.":                 "No results.",
	"Treffer filtern …":              "Filter results …",
	"mehr":                           "more",
	"weniger":                        "less",
	"Suchen":                         "Search",
	"Portal Suche":                   "Portal search",
	"Tickets zur CRM":                "Tickets for CRM",
	"Jira Tickets":                   "Jira tickets",
	"offene Jira Tickets":            "Open Jira tickets",
	"Wissen":                         "Knowledge",
	"IBS Tickets":                    "IBS tickets",
	"(ohne Titel)":                   "(no title)",
	"IBS: keine Adresse zur Rufnummer gefunden.":             "IBS: no address found for this phone number.",
	"IBS: Antwort von getByNumber enthält keine address_id.": "IBS: getByNumber response contains no address_id.",
	"offen":             "open",
	"alle":              "all",
	"Sortierung:":       "Sort by:",
	"unsortiert":        "unsorted",
	"erstellt":          "created",
	"geändert":          "changed",
	"Erstellt: ":        "Created: ",
	"Letzter Zugriff: ": "Last access: ",
	"Einen Moment – die Tickets werden neu sortiert …": "One moment – re-sorting the tickets …",
	"Kundenv.":                          "Cust. mgmt",
	"Kundenv. ID":                       "Cust. mgmt ID",
	"Kundenverwaltung-Tickets:":         "Customer mgmt tickets:",
	"Kundenverwaltung-Schlagwortsuche:": "Customer mgmt keyword search:",
	"Kundenverwaltung: ":                "Customer management: ",
	"Jira: %d Treffer":                  "Jira: %d hits",
	"Kundenv.: %d Tickets zu %s":        "Cust. mgmt: %d tickets for %s",
	"Kundenv.: %d Tickets":              "Cust. mgmt: %d tickets",
	"Kundenv.: Fehler":                  "Cust. mgmt: error",
	"Fasse das folgende Support-Ticket in höchstens %d Zeilen zusammen. Antworte nur mit der Zusammenfassung.": "Summarize the following support ticket in at most %d lines. Reply with the summary only.",
	"IBS: Antwort ist kein gültiges JSON: %v":      "IBS: response is not valid JSON: %v",
	"IBS-Server nicht erreichbar (%s): %v":         "IBS server unreachable (%s): %v",
	"IBS-Server meldet HTTP %d":                    "IBS server returned HTTP %d",
	"URL Kundenverwaltung API:":                    "Customer management API URL:",
	"API-Key Kundenverwaltung:":                    "Customer management API key:",
	"KI-Gesamtzusammenfassung":                     "AI overall summary",
	"KI-GESAMTZUSAMMENFASSUNG":                     "AI OVERALL SUMMARY",
	"KI-Zusammenfassung":                           "AI summary",
	"KI-Zusammenfassung wird geladen …":            "Loading AI summary …",
	"(keine Zusammenfassung erhalten)":             "(no summary received)",
	"Quelle:":                                      "Source:",
	"Fehler: ":                                     "Error: ",
	"Jira-Limit:":                                  "Jira limit:",
	"Summary-Zeilen:":                              "Summary lines:",
	"Suchtext, z. B. \"Drucker im 2. OG offline\"": "Search text, e.g. \"Printer on 2nd floor offline\"",
	"Ergebnis für „%s“ (%d Treffer · %d ms)":       "Result for “%s” (%d hits · %d ms)",
	"Bitte einen Suchtext eingeben.":               "Please enter a search text.",
	"Im CRM Feld steht keine gültige CRM-Nummer. Die Ticketsuche ist erst mit einer CRM möglich.": "The CRM field does not contain a valid CRM number. Ticket search requires a CRM first.",
	"Quelle konnte nicht geladen werden: %v":                                                      "Could not load source: %v",
	"Datei konnte nicht geöffnet werden: %v":                                                      "Could not open file: %v",
	"Jarvis-Server-URL und/oder API-Key sind nicht konfiguriert (siehe Einstellungen).":           "Jarvis server URL and/or API key are not configured (see Settings).",
	"Jarvis-Server nicht erreichbar (%s): %v":                                                     "Jarvis server unreachable (%s): %v",
	"Antwort nicht im erwarteten Format (HTTP %d): %v":                                            "Response not in expected format (HTTP %d): %v",

	// --- Dialoge: Titel / Buttons ---
	"Fehler":           "Error",
	"Erfolg":           "Success",
	"OK":               "OK",
	"Ja":               "Yes",
	"Nein":             "No",
	"Senden":           "Send",
	"Abbrechen":        "Cancel",
	"Auswählen":        "Select",
	"Herunterladen":    "Download",
	"Modell auswählen": "Select model",

	// --- Betriebsmodus-Radio (interne Werte, hier nur Anzeige-Labels) ---
	"Standard-Betrieb": "Standard mode",
	"Headset-Betrieb":  "Headset mode",

	// --- Collapsible-Section-Titel ---
	"Erweiterte Einstellungen": "Advanced settings",

	// --- Transkribierung-Radio (Whisper lokal / Remote GPU) ---
	"Whisper lokal": "Whisper local",

	// --- Design (Theme-Radio; interne Werte, hier nur Anzeige-Labels) ---
	"Design":                          "Design",
	"Hell (klassisch)":                "Light (classic)",
	"Hell (modern)":                   "Light (modern)",
	"Dunkel (modern)":                 "Dark (modern)",
	"ohne":                            "none",
	"Erkennung: Whisper lokal":        "Recognition: Whisper local",
	"Erkennung: Remote Whisper (GPU)": "Recognition: Remote Whisper (GPU)",
	"Nachbearb.: ":                    "Post-proc.: ",
	"Analyse: ":                       "Analysis: ",
	"Engine: GPU beschleunigt":        "Engine: GPU accelerated",
	"Engine: CPU (AVX2/512)":          "Engine: CPU (AVX2/512)",

	// --- Tabs / diverse ---
	"Einstellungen":        "Settings",
	"Satzpause: %.1fs":     "Sentence pause: %.1fs",
	"Scan-Intervall: %d s": "Scan interval: %d s",
	"%d Modelle erkannt:":  "%d models detected:",

	// --- Meldungen (Analyse / Export / Vorgaben / Modelle) ---
	"Vorgabe löschen": "Delete preset",
	"Diese Vorgabe ist nicht in der Liste gespeichert.": "This preset is not saved in the list.",
	"Analyse-Vorgabe wirklich löschen?":                 "Really delete this analysis preset?",
	"Zu wenig Text für eine Analyse vorhanden.":         "Not enough text available for an analysis.",
	"Keine Rufnummer": "No phone number",
	"Es liegt noch keine Rufnummer eines Anrufs vor, die wiederholt werden könnte.": "There is no call phone number yet that could be repeated.",
	"Kein Text zum Speichern vorhanden.":                                            "No text available to save.",
	"Export erfolgreich":                                                            "Export successful",
	"Text gespeichert unter:":                                                       "Text saved to:",
	"Server unter %s erreichbar, meldet aber keine Modelle.":                        "Server at %s is reachable but reports no models.",
	"Alle Einstellungen wurden dauerhaft gespeichert.":                              "All settings have been saved permanently.",
	"Das lokale Modell „%s“ ist noch nicht vorhanden.\n" +
		"Es muss einmalig heruntergeladen werden (mehrere GB), bevor die\n" +
		"Auswahl aktiv wird.\n\nJetzt herunterladen?": "The local model “%s” is not available yet.\n" +
		"It must be downloaded once (several GB) before the\n" +
		"selection becomes active.\n\nDownload now?",

	// --- Auto-Update (updater.go) ---
	"Update verfuegbar":       "Update available",
	"Update wird installiert": "Installing update",
	"Eine neue Version ist verfuegbar:\n\n    Installiert:  v%s\n    Verfuegbar:   %s\n\nJetzt herunterladen, installieren und die App neu starten?": "A new version is available:\n\n    Installed:  v%s\n    Available:  %s\n\nDownload, install and restart the app now?",
	"Lade %s herunter und installiere...\nDie App startet anschliessend automatisch neu.":                                                            "Downloading %s and installing...\nThe app will restart automatically afterwards.",
	"Update fehlgeschlagen:\n%v": "Update failed:\n%v",

	// --- Rufnummern-Webhook (webhook.go) ---
	"Anruf von: ":         "Call from: ",
	"CRM-Kunde auswählen": "Select CRM customer",
	"Übernehmen":          "Apply",
	"Zu Rufnummer %s wurden %d CRM-Kunden gefunden.\nEinen Kunden anklicken und „Übernehmen“ – oder per Doppelklick direkt wählen:": "For phone number %s, %d CRM customers were found.\nClick a customer and “Apply” – or double-click to select directly:",
}
