package main

import "fmt"

// Die drei langen Hilfe-Popups (hinter den "?"-Buttons in den Einstellungen).
// Wegen ihrer Länge und – beim Webhook – eingebetteter Laufzeitwerte (Port/
// Pfad/IP) hier als dedizierte, sprachabhängige Funktionen statt über die
// enOverrides-Map. Sie werden bei jedem Klick neu erzeugt und lesen currentLang
// direkt; ein Sprachwechsel wirkt daher automatisch beim nächsten Öffnen.
// Rückgabe je: (Titel, Text).

func helpLevelMeter() (string, string) {
	if currentLang == "en" {
		return "Level meter & gain",
			"The bar shows the channel's current volume (level).\n\n" +
				"🟢 Green mark (80 %): target level – loud speech should reach here.\n\n" +
				"🟠 Orange mark: highest level recently reached (peak). It decays slowly after pauses and jumps back up at new, louder passages.\n\n" +
				"How to set the gain (slider):\n" +
				"• Orange stays clearly LEFT of the green → increase gain (too quiet).\n" +
				"• Orange sticks all the way RIGHT at the end → reduce gain (clipping/distortion).\n" +
				"• Optimal: at loud speech, orange lands roughly on the green mark."
	}
	return "Pegelanzeige & Aussteuerung",
		"Der Balken zeigt die aktuelle Lautstärke (Pegel) des Kanals.\n\n" +
			"🟢 Grüner Strich (80 %): Ziel-Aussteuerung – hierhin sollte laute Sprache reichen.\n\n" +
			"🟠 Oranger Strich: höchster zuletzt erreichter Pegel (Spitzenwert). Er klingt nach Sprechpausen langsam ab und steigt bei neuen, lauteren Stellen sofort wieder.\n\n" +
			"So stellst du den Gain (Schieberegler) ein:\n" +
			"• Oranger Strich bleibt deutlich LINKS vom grünen → Gain erhöhen (zu leise).\n" +
			"• Oranger Strich klebt ganz RECHTS am Anschlag → Gain senken (Übersteuerung/Verzerrung).\n" +
			"• Optimal: bei lauter Sprache landet Orange etwa auf dem grünen Strich."
}

func helpRecognition() (string, string) {
	if currentLang == "en" {
		return "Speech recognition & post-processing",
			"Recognition:\n" +
				"• Whisper local: local whisper-cli (CPU).\n" +
				"• Remote GPU: GPU Whisper server via the WebSocket URL.\n\n" +
				"Post-processing: improves the recognized text.\n" +
				"• none: raw text\n" +
				"• Gemma 4 E2B / 12B: local correction (12B more accurate but slow on CPU)\n" +
				"• remote LLM: correction via the remote backend selected below"
	}
	return "Spracherkennung & Nachbearbeitung",
		"Erkennung:\n" +
			"• Whisper lokal: lokaler whisper-cli (CPU).\n" +
			"• Remote GPU: GPU-Whisper-Server über die WebSocket-URL.\n\n" +
			"Nachbearbeitung: Verbessert den erkannten Text.\n" +
			"• ohne: roher Text\n" +
			"• Gemma 4 E2B / 12B: lokale Korrektur (12B genauer, aber langsam auf CPU)\n" +
			"• remote LLM: Korrektur über das unten gewählte Remote-Backend"
}

func helpHeadset() (string, string) {
	if currentLang == "en" {
		return "Help: Headset mode (fix a muted line)",
			"If your communication software (e.g. Teams/Zoom) goes silent when this app starts,\n" +
				"you must disable Windows' exclusive control so that\n" +
				"this app and Teams can access the headset at the same time.\n\n" +
				"Open the sound settings via the button below, " +
				"scroll down to “Advanced”,\nclick “More sound settings”.\n\n" +
				"🔊 For your headset (playback)\n" +
				"1. “Playback” tab\n" +
				"2. Double-click your headset\n" +
				"3. “Advanced” tab\n" +
				"4. Uncheck:\n" +
				"   o “Allow applications to take exclusive control of this device”\n" +
				"   o “Give exclusive mode applications priority”\n" +
				"5. Apply\n" +
				"________________________________________\n" +
				"🎤 For your microphone (recording)\n" +
				"6. “Recording” tab\n" +
				"7. Double-click your microphone\n" +
				"8. “Advanced” tab\n" +
				"9. Uncheck the same two boxes\n" +
				"10. Apply"
	}
	return "Hilfe: Headset-Betrieb (Stumme Leitung fixen)",
		"Wenn deine Kommunikations-Software (z.B. Teams/Zoom) beim Start der App verstummt,\n" +
			"musst du die Exklusiv-Rechte von Windows deaktivieren, damit \n" +
			"diese App und Teams gleichzeitig auf das Headset zugreifen können.\n\n" +
			"Öffne die Soundeinstellungen durch Klick auf den Button unten, " +
			"scrolle nach unten zu „Erweitert“,\nklicke auf „Weitere Soundeinstellungen“.\n\n" +
			"🔊 Für dein Headset (Wiedergabe)\n" +
			"1. Tab „Wiedergabe“\n" +
			"2. Doppelklick auf dein Headset\n" +
			"3. Tab „Erweitert“\n" +
			"4. Entferne die Haken bei:\n" +
			"   o „Anwendungen haben alleinige Kontrolle über das Gerät“\n" +
			"   o „Anwendungen im exklusiven Modus haben Priorität“\n" +
			"5. Übernehmen\n" +
			"________________________________________\n" +
			"🎤 Für dein Mikrofon (Aufnahme)\n" +
			"6. Tab „Aufnahme“\n" +
			"7. Doppelklick auf dein Mikrofon\n" +
			"8. Tab „Erweitert“\n" +
			"9. Die gleichen beiden Haken entfernen\n" +
			"10. Übernehmen"
}

func helpWebhook() (string, string) {
	port := effectiveWebhookPort()
	path := effectiveWebhookPath()
	ip := getLocalIPHint()
	if currentLang == "en" {
		return "Phone number handover (webhook)",
			"An external trigger (e.g. the phone system) hands the caller's number to this app on an incoming call. It is used to search Jira and write the issue key of the best match into the CRM field.\n\n" +
				fmt.Sprintf("The server listens on ALL network addresses:\n    http://<machine-IP>:%d%s\n\n", port, path) +
				"Handing over the number – two ways:\n" +
				"• GET:   append the number as a query parameter.\n" +
				"• POST:  JSON body {\"number\":\"...\"} (Content-Type application/json) or as a form field.\n\n" +
				"Accepted parameter/field names (case-sensitive):\n" +
				"    number, num, nummer, phone, tel, telefon, caller, callerid, rufnummer\n\n" +
				"A leading \"+\" (e.g. +49...) is preserved.\n\n" +
				"Example (GET):\n" +
				fmt.Sprintf("    http://%s:%d%s?nummer=+492056261551\n\n", ip, port, path) +
				"Example (POST):\n" +
				fmt.Sprintf("    curl -X POST -H \"Content-Type: application/json\" \\\n         -d '{\"nummer\":\"+492056261551\"}' \\\n         http://%s:%d%s\n\n", ip, port, path) +
				"Changes to port/path take effect with \"Save settings now\"."
	}
	return "Rufnummern-Übergabe (Webhook)",
		"Ein externer Trigger (z.B. die Telefonanlage) übergibt beim eingehenden Anruf die Rufnummer an diese App. Damit wird in Jira gesucht und der Issue-Key des besten Treffers ins CRM Feld eingetragen.\n\n" +
			fmt.Sprintf("Der Server lauscht auf ALLEN Netzwerk-Adressen:\n    http://<Rechner-IP>:%d%s\n\n", port, path) +
			"Rufnummer übergeben – zwei Wege:\n" +
			"• GET:   Rufnummer als Query-Parameter anhängen.\n" +
			"• POST:  JSON-Body {\"number\":\"...\"} (Content-Type application/json) oder als Formularfeld.\n\n" +
			"Akzeptierte Parameter-/Feldnamen (Groß-/Kleinschreibung beachten):\n" +
			"    number, num, nummer, phone, tel, telefon, caller, callerid, rufnummer\n\n" +
			"Ein führendes \"+\" (z.B. +49...) bleibt erhalten.\n\n" +
			"Beispiel (GET):\n" +
			fmt.Sprintf("    http://%s:%d%s?nummer=+492056261551\n\n", ip, port, path) +
			"Beispiel (POST):\n" +
			fmt.Sprintf("    curl -X POST -H \"Content-Type: application/json\" \\\n         -d '{\"nummer\":\"+492056261551\"}' \\\n         http://%s:%d%s\n\n", ip, port, path) +
			"Änderungen an Port/Pfad werden mit \"Einstellungen jetzt speichern\" übernommen."
}
