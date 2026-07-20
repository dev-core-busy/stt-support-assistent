package main

// ibs_client.go — Anbindung der IBS-Kundenverwaltungs-API ("IBS Tickets").
//
// API laut REST_API_DOKUMENTATION_Kundenverwaltung.md (Projekt-Root; Server ist
// kundenverwaltung.jar, Basis-URL + API-Key in den Einstellungen, Felder
// "URL Kundenverwaltung API" / "API-Key Kundenverwaltung"):
//
//   POST /va/ad/getByNumber        {"from_number": "<nr>"}  -> addresses[] (address_id, name, ...)
//   POST /va/ev/getEvents          {"event":"getEvents","request":{"address_id":"<id>"}}
//                                  -> event[] (id, creation_time, modification_time,
//                                     state, state_type, "dispatch user", text);
//                                     state_type < 80 = offen, >= 80 = geschlossen
//                                     (API-Update 2026-07-05)
//
// Optimierungen gegenueber der ersten (spezifikationslosen) Fassung:
//   - Adresse kommt aus getByNumber (externalPhoneCall liefert keine Daten -
//     nur GUI-Seiteneffekt in der Kundenverwaltung - und wird auf Wunsch gar
//     nicht mehr aufgerufen).
//   - getOpenEvents entfaellt: getEvents liefert state je Event, "offen" ist
//     laut Doku state != ENDED - das Radio "offen"/"alle" der Anruf-Ansicht
//     filtert rein clientseitig (eine Server-Anfrage gespart).
//   - Auth: X-API-Key ODER Bearer (Server prueft beide) - wir senden beide.
//
// Die Parser bleiben bewusst TOLERANT (Schluesselvarianten, s. ibsFieldString):
// die Doku-Beispiele koennten vom echten Server abweichen (vgl. Jarvis-Doku).
// Insbesondere listet die Doku im getByNumber-Ergebnis KEIN ID-Feld - die fuer
// getEvents noetige address_id wird daher tolerant gesucht und ihr Fehlen klar
// gemeldet. Rohantworten landen im Log und im Debug-Popup.
//
// Flow bei eingehendem Anruf (webhook.go, ZUSAETZLICH zur Jira-CRM-Suche,
// gated durch Checkbox "IBS Tickets"): Rufnummer -> getByNumber -> address_id
// -> getEvents -> Anzeige im IBS-Bereich der Ergebnisliste (showIBSTickets,
// jarvis_client.go).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
)

// ibsTicket ist die anzeigefertige Form eines IBS-Events fuer den IBS-Bereich
// der Ergebnisliste (Felder gemaess event[]-Schema der Doku).
type ibsTicket struct {
	Key      string // event.id (lokale Event-ID)
	Status   string // event.state (EVENT_STATE-Enum, z.B. NEU, IN_BEARBEITUNG, ENDED)
	Created  string // event.creation_time
	Modified string // event.modification_time (letzter Zugriff)
	User     string // event."dispatch user" (zugewiesener Benutzer)
	Text     string // event.text (Beschreibung)
	// Open steuert das Radio "offen"/"alle": state_type < 80 = offen,
	// >= 80 = geschlossen; Rueckfall ohne state_type: state != ENDED.
	Open bool
}

// ibsConfigured meldet, ob URL und API-Key der Kundenverwaltung hinterlegt
// sind (Voraussetzung der Checkbox "IBS Tickets" und Sichtbarkeits-Schalter
// der Kundenverwaltungs-Bedienelemente, s. refreshIBSCheck).
func ibsConfigured() bool {
	return strings.TrimSpace(config.IBS.Url) != "" && strings.TrimSpace(config.IBS.ApiKey) != ""
}

// currentIBSAddrID haelt die address_id des zuletzt per Rufnummern-Webhook
// gefundenen Anrufers fest (von performIBSLookup gesetzt, beim naechsten Anruf
// zurueckgesetzt, s. webhook.go). Die Schlagwort-Suche (getMatchingEvents)
// braucht diese ID - sie steht nur im Anzeige-Label ("Kundenv. ID"), das aber
// "-" statt der Roh-ID zeigt.
var currentIBSAddrID string

func ibsBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(config.IBS.Url), "/")
}

// ibsDo fuehrt einen Request aus und liefert den Body als String. Der Server
// akzeptiert laut Doku X-API-Key ODER Bearer - wir senden beide. TLS-Pruefung
// wie beim Jarvis-Client deaktiviert (selbstsigniertes JKS-Zertifikat).
func ibsDo(req *http.Request) (string, error) {
	apiKey := strings.TrimSpace(config.IBS.ApiKey)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := jarvisHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf(T("IBS-Server nicht erreichbar (%s): %v"), ibsBaseURL(), err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return string(body), fmt.Errorf(T("IBS-Server meldet HTTP %d"), resp.StatusCode)
	}
	return string(body), nil
}

// ibsPostJSON POSTet ein JSON-Objekt (Content-Type application/json ist laut
// Doku Pflicht) und liefert die dekodierte Antwort samt Rohtext.
func ibsPostJSON(path string, body interface{}) (interface{}, string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequest("POST", ibsBaseURL()+path, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := ibsDo(req)
	if err != nil {
		Log("IBS " + path + ": " + err.Error())
		return nil, raw, err
	}
	var v interface{}
	if uerr := json.Unmarshal([]byte(raw), &v); uerr != nil {
		err = fmt.Errorf(T("IBS: Antwort ist kein gültiges JSON: %v"), uerr)
		Log("IBS " + path + ": " + err.Error() + " | Rohantwort: " + raw)
		return nil, raw, err
	}
	return v, raw, nil
}

// ibsNormKey normalisiert einen JSON-Schluessel fuer den toleranten Vergleich:
// kleingeschrieben, ohne '_', '-' und Leerzeichen. So matchen address_id,
// addressId und "dispatch user" die Suchschluessel "addressid"/"dispatchuser".
func ibsNormKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	k = strings.ReplaceAll(k, "-", "")
	return strings.ReplaceAll(k, " ", "")
}

// ibsFindValue durchsucht dekodiertes JSON in Breitensuche nach dem ersten
// Wert, dessen (normalisierter) Schluessel einem der keys entspricht; die
// Reihenfolge der keys ist die Praeferenz innerhalb eines Objekts.
// Breitensuche, damit flache Treffer tief verschachtelte schlagen.
func ibsFindValue(v interface{}, keys ...string) interface{} {
	queue := []interface{}{v}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		switch t := cur.(type) {
		case map[string]interface{}:
			norm := make(map[string]interface{}, len(t))
			for k, val := range t {
				norm[ibsNormKey(k)] = val
			}
			for _, k := range keys {
				if val, ok := norm[k]; ok && val != nil {
					return val
				}
			}
			for _, val := range t {
				queue = append(queue, val)
			}
		case []interface{}:
			queue = append(queue, t...)
		}
	}
	return nil
}

// ibsScalarString formatiert einen JSON-Skalar als Anzeige-String
// (JSON-Zahlen kommen als float64 an; die Event-ID 1234 soll "1234" bleiben).
// Nicht-Skalare ergeben "".
func ibsScalarString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s)
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	}
	return ""
}

// ibsFieldString liefert den ersten Skalar-Wert eines Objekts zu einem der
// (normalisierten) keys - flach, ohne Rekursion (Event-/Adressfelder liegen
// direkt im Objekt).
func ibsFieldString(m map[string]interface{}, keys ...string) string {
	norm := make(map[string]interface{}, len(m))
	for k, val := range m {
		norm[ibsNormKey(k)] = val
	}
	for _, k := range keys {
		if val, ok := norm[k]; ok {
			if s := ibsScalarString(val); s != "" {
				return s
			}
		}
	}
	return ""
}

// ibsAddressLookup sucht die Adresse zur Rufnummer via POST /va/ad/getByNumber
// (Body laut Doku: {"from_number": "<nr>"}). Liefert die address_id fuer
// getEvents sowie einen Anzeigenamen. Kein Treffer meldet der Server ohne
// Treffer-Array (Top-Level "name": "nothing found (unknown)") -> addrID "".
// Seit dem API-Update heisst das Treffer-Array addresses[] und jeder Eintrag
// traegt seine address_id direkt; aeltere Namen bleiben als Rueckfall.
func ibsAddressLookup(number string) (addrID, addrLabel, raw string, err error) {
	v, raw, err := ibsPostJSON("/va/ad/getByNumber", map[string]string{"from_number": number})
	if err != nil {
		return "", "", raw, err
	}

	// addresses[] ist das Treffer-Array; fehlt es (oder ist leer), gab es
	// keinen Treffer. Mehrere Adressen zur Nummer: erste nehmen (geloggt).
	addresses, _ := ibsFindValue(v, "addresses", "address", "adresse").([]interface{})
	if len(addresses) == 0 {
		return "", "", raw, nil
	}
	if len(addresses) > 1 {
		Log(fmt.Sprintf("IBS getByNumber: %d Adressen zu %q - erste genommen", len(addresses), number))
	}
	first, _ := addresses[0].(map[string]interface{})
	if first == nil {
		return "", "", raw, nil
	}

	addrLabel = ibsFieldString(first, "name", "fulladdress", "displayname", "label")
	if full := ibsFieldString(first, "fulladdress"); full != "" && addrLabel != full {
		// full-address ist mehrzeilig (Anrede\nName, Vorname\nLand) - fuer die
		// Kopfzeile reicht die kompakteste Form als Ergaenzung zum Namen.
		if flat := strings.Join(strings.Fields(strings.ReplaceAll(full, "\n", " ")), " "); flat != "" && !strings.EqualFold(flat, addrLabel) {
			if addrLabel != "" {
				addrLabel += " — " + flat
			} else {
				addrLabel = flat
			}
		}
	}

	if idVal := ibsFindValue(first, "addressid", "adressid", "adrid", "id", "addressnr", "adressnr"); idVal != nil {
		addrID = ibsScalarString(idVal)
	}
	if addrID == "" {
		// Rueckfall: ID ausserhalb des address[]-Eintrags suchen.
		if idVal := ibsFindValue(v, "addressid", "adressid", "adrid"); idVal != nil {
			addrID = ibsScalarString(idVal)
		}
	}
	if addrID == "" {
		err = fmt.Errorf(T("IBS: Antwort von getByNumber enthält keine address_id."))
		Log("IBS getByNumber: " + err.Error() + " | Rohantwort: " + raw)
	}
	return addrID, addrLabel, raw, err
}

// ibsFetchEvents holt ALLE Events zur Adresse via POST /va/ev/getEvents
// (Body laut Doku: {"event":"getEvents","request":{"address_id":"<id>"}}).
// getOpenEvents wird nicht benoetigt: state != ENDED markiert offene Events,
// das Radio "offen"/"alle" der Anruf-Ansicht filtert clientseitig.
func ibsFetchEvents(addrID string) ([]interface{}, string, error) {
	body := map[string]interface{}{
		"event":   "getEvents",
		"request": map[string]string{"address_id": addrID},
	}
	v, raw, err := ibsPostJSON("/va/ev/getEvents", body)
	if err != nil {
		return nil, raw, err
	}
	events, _ := ibsFindValue(v, "event", "events", "tickets", "items", "data", "result").([]interface{})
	if events == nil {
		// Kein event[]-Array: bei Treffern immer vorhanden, fehlt bei "kein
		// Treffer"/fehlender address_id -> als 0 Events werten, Rohantwort loggen.
		Log("IBS getEvents: kein event[]-Array in der Antwort | Rohantwort: " + raw)
	}
	return events, raw, nil
}

// kvBuzzwordPath ist der Kundenverwaltungs-Schlagwort-Endpunkt. Er liegt im
// Jarvis-/api/-Namespace (wie /api/support/query) - also auf dem JARVIS-Host
// (config.Jarvis.Url), NICHT auf der kundenverwaltung.jar (die /va/... nutzt).
const kvBuzzwordPath = "/api/kundenverwaltung/tickets-by-buzzwords"

// kvBaseURL liefert die Basis-URL des Buzzword-Endpunkts (Jarvis-Host).
func kvBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(config.Jarvis.Url), "/")
}

// kvBuzzwordURL baut die GET-URL inkl. Query-Parameter (buzzwords, limit und -
// nur bei nicht-leerer Kundennummer - address_id). Ein Ort fuer Aufruf und
// Debug-Vorschau, damit beide garantiert identisch sind.
func kvBuzzwordURL(addrID, buzzwords string, limit int) string {
	q := url.Values{}
	q.Set("buzzwords", buzzwords)
	q.Set("limit", strconv.Itoa(limit))
	if strings.TrimSpace(addrID) != "" {
		q.Set("address_id", addrID)
	}
	return kvBaseURL() + kvBuzzwordPath + "?" + q.Encode()
}

// ibsFetchMatchingEvents sucht die zu Schlagworten passenden Kundenverwaltungs-
// Tickets via GET {Jarvis.Url}/api/kundenverwaltung/tickets-by-buzzwords
// ?buzzwords=...&limit=...&address_id=... . Der Endpunkt ist ein FastAPI-
// Gateway (Antwort-Stil {"detail": ...}); es akzeptiert KEIN POST (HTTP 405),
// die Suche laeuft als GET mit Query-Parametern. Das Gateway reicht die Werte
// intern an die Java-kundenverwaltung.jar weiter (dort per
// getStringByPath("request.address_id"/"request.limit"/"request.buzzwords")).
// address_id wird nur bei nicht-leerer Kundennummer mitgeschickt (leer => der
// Endpunkt sucht global). buzzwords ist die (komma-/leerzeichengetrennte)
// Schlagwortliste.
//
// Authentifizierung: HTTP Basic mit dem Windows-Domaenen-Login
// (config.KvUser im Format "domaene\benutzer", config.KvPassword) - dieser
// Endpunkt verlangt Basic-Auth statt des Jarvis-API-Keys. Zugangsdaten stehen
// ausschliesslich in config.json (gitignored), nie im Quellcode (Repo public).
// Die Antwort wird tolerant geparst (Treffer-Array unter diversen Schluesseln),
// Rohtext geht ins Log und ins Debug-Popup.
func ibsFetchMatchingEvents(addrID, buzzwords string, limit int) ([]interface{}, string, error) {
	base := kvBaseURL()
	if base == "" {
		return nil, "", fmt.Errorf(T("Kundenverwaltung-Suche: Jarvis-Server-URL ist nicht konfiguriert (siehe Einstellungen)."))
	}
	req, err := http.NewRequest("GET", kvBuzzwordURL(addrID, buzzwords, limit), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	if u := strings.TrimSpace(config.KvUser); u != "" {
		req.SetBasicAuth(u, config.KvPassword)
	} else {
		Log("Kundenverwaltung-Suche: kein Basic-Auth-Benutzer gesetzt (config.KvUser) - Server lehnt die Anfrage vermutlich mit 401 ab")
	}

	resp, err := jarvisHTTPClient().Do(req)
	if err != nil {
		e := fmt.Errorf(T("Kundenverwaltung nicht erreichbar (%s): %v"), base, err)
		Log("Kundenverwaltung " + kvBuzzwordPath + ": " + e.Error())
		return nil, "", e
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	rawS := string(raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := fmt.Errorf(T("Kundenverwaltung meldet HTTP %d"), resp.StatusCode)
		Log("Kundenverwaltung " + kvBuzzwordPath + ": " + e.Error() + " | Rohantwort: " + rawS)
		return nil, rawS, e
	}
	var v interface{}
	if uerr := json.Unmarshal(raw, &v); uerr != nil {
		e := fmt.Errorf(T("Kundenverwaltung: Antwort ist kein gültiges JSON: %v"), uerr)
		Log("Kundenverwaltung " + kvBuzzwordPath + ": " + e.Error() + " | Rohantwort: " + rawS)
		return nil, rawS, e
	}
	events, _ := ibsFindValue(v, "event", "events", "tickets", "items", "data", "result", "matches").([]interface{})
	if events == nil {
		Log("Kundenverwaltung tickets-by-buzzwords: kein Treffer-Array in der Antwort | Rohantwort: " + rawS)
	}
	return events, rawS, nil
}

// ibsEventTickets mappt die rohen Events auf die Anzeigeform (Feldnamen laut
// Doku, tolerante Varianten als Rueckfall). Offen = state != ENDED (so
// filtert auch der Server bei getOpenEvents). Ohne erkennbaren Inhalt landet
// das kompakte JSON des Events im Text, damit IMMER etwas Sichtbares ankommt.
func ibsEventTickets(events []interface{}) []ibsTicket {
	var out []ibsTicket
	for _, e := range events {
		m, ok := e.(map[string]interface{})
		if !ok {
			if s := ibsScalarString(e); s != "" {
				out = append(out, ibsTicket{Text: s, Open: true})
			}
			continue
		}
		t := ibsTicket{
			Key:      ibsFieldString(m, "id", "eventid", "localid", "nr", "number"),
			Status:   ibsFieldString(m, "state", "status"),
			Created:  ibsFieldString(m, "creationtime", "created", "createdat", "date"),
			Modified: ibsFieldString(m, "modificationtime", "modified", "modifiedat", "lastaccess", "lastaccesstime", "updated", "updatedat"),
			User:     ibsFieldString(m, "dispatchuser", "user", "assignee"),
			Text:     ibsFieldString(m, "text", "description", "beschreibung", "note", "message"),
		}
		// Offen/geschlossen: massgeblich ist state_type (< 80 offen, >= 80
		// geschlossen; Zuordnung s. ibsStateNames/event_states.txt). Fehlt das
		// Feld oder ist es nicht numerisch, entscheidet der Status-NAME.
		// Geloeschte Events (state_type 99 bzw. "gelöscht") werden komplett
		// uebersprungen und tauchen nirgends auf.
		stateType, hasStateType := 0, false
		if st := ibsFieldString(m, "statetype", "statetyp"); st != "" {
			if n, ok := ibsLeadingNumber(st); ok {
				stateType, hasStateType = int(n), true
			} else {
				Log("IBS: state_type nicht numerisch (" + st + ") - Status-Name entscheidet")
			}
		}
		if (hasStateType && stateType == ibsStateDeleted) || ibsDeletedByName(t.Status) {
			continue
		}
		if hasStateType {
			t.Open = stateType < 80
			// Status-Pill: einheitlicher Anzeige-Name laut Enum-Tabelle;
			// unbekannte Typen behalten den Server-Text.
			if name, ok := ibsStateNames[stateType]; ok {
				t.Status = name
			}
		} else {
			t.Open = !ibsClosedByName(t.Status)
		}
		if t.Key == "" && t.Text == "" {
			if j, err := json.Marshal(m); err == nil {
				t.Text = string(j)
			}
		}
		out = append(out, t)
	}
	return out
}

// ibsStateNames: Anzeige-Name je state_type laut EVENT_STATE-Enum der
// Kundenverwaltung (Quelle: event_states.txt im Projekt-Root). Die Status-
// Pill zeigt bevorzugt diesen Namen; unbekannte Typen behalten den vom
// Server gelieferten state-Text. state_type 99 ("gelöscht") wird NIE
// angezeigt - solche Events fliegen komplett aus der Liste (Nutzer-Vorgabe).
var ibsStateNames = map[int]string{
	0:  "offen",
	5:  "Angebot erstellen",
	10: "Angebot erstellt",
	21: "DFÜ Fragen telefonisch klären",
	22: "DFÜ Fragen telefonisch geklärt",
	30: "account erstellen",
	34: "Installationstermin vereinbaren",
	35: "installieren",
	36: "installiert",
	45: "Benutzeranmeldung abwarten",
	60: "Befunde abwarten",
	65: "Erstsupport durchführen",
	80: "Info",
	81: "Sprechzeiten",
	90: "abgeschlossen",
	91: "abgeschlossen (berechnet)",
	99: "gelöscht",
}

const ibsStateDeleted = 99

// ibsDeletedByName erkennt geloeschte Events am Status-Namen (Rueckfall,
// wenn state_type fehlt/unlesbar ist).
func ibsDeletedByName(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return strings.Contains(s, "gelöscht") || strings.Contains(s, "geloescht") || strings.Contains(s, "deleted")
}

// ibsClosedByName meldet, ob ein Status-Name ein geschlossenes Event
// beschreibt (Rueckfall, wenn state_type fehlt/unlesbar ist).
func ibsClosedByName(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	for _, m := range []string{"abgeschlossen", "geschlossen", "ended", "closed", "erledigt", "storniert", "abgebrochen"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// ibsLeadingNumber liest eine Zahl aus s - komplett ("91") oder als fuehrende
// Ziffernfolge ("91 (abgeschlossen)"), tolerant gegen angehaengten Text.
func ibsLeadingNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n, true
	}
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9') {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:end], 64)
	return n, err == nil
}

// ibsRequestPreview ist der Text des Debug-Popups vor dem Versand (analog
// jarvisPhonePreview in jarvis_client.go).
func ibsRequestPreview(number string) string {
	base := ibsBaseURL()
	return fmt.Sprintf(
		"POST %s/va/ad/getByNumber  {\"from_number\":%q}\n"+
			"POST %s/va/ev/getEvents  {\"event\":\"getEvents\",\"request\":{\"address_id\":\"<aus Schritt 1>\"}}",
		base, number, base)
}

// ibsDebugPayload baut den Inhalt des Debug-Antwort-Popups (analog
// performCallerJiraLookup in webhook.go).
func ibsDebugPayload(raw string, err error) string {
	payload := prettyJSON(raw)
	if err == nil {
		return payload
	}
	if payload == "" {
		return "Fehler: " + err.Error()
	}
	return "Fehler: " + err.Error() + "\n\nRohantwort:\n" + payload
}

// setIBSAddressField zeigt die gefundene address_id im STT-Tab neben dem
// CRM-Wert ("Kundenv. ID"); "-" wenn (noch) keine gefunden. Thread-sicher via
// fyne.Do. Der zugehoerige Block (ibsIDBox, main.go) ist nur sichtbar, wenn
// URL + API-Key der Kundenverwaltung hinterlegt sind (refreshIBSCheck).
func setIBSAddressField(id string) {
	if ibsAddressField == nil {
		return
	}
	if strings.TrimSpace(id) == "" {
		id = "-"
	}
	fyne.Do(func() { ibsAddressField.SetText(id) })
}

// performIBSLookup ist der komplette IBS-Flow zu einem eingehenden Anruf:
// Rufnummer -> getByNumber (Adresse) -> getEvents (alle Tickets) -> Anzeige in
// der Anruf-Ticketliste. Laeuft in einer Goroutine (Aufruf s.
// handleIncomingCaller in webhook.go); UI-Zugriffe via fyne.Do.
func performIBSLookup(number string) {
	// label ist der Anzeigename der gefundenen Adresse (Rueckfall: Rufnummer);
	// die Anruf-Ansicht (jarvis_client.go) baut daraus die Status-Zeile.
	render := func(label string, tickets []ibsTicket, errMsg string) {
		fyne.Do(func() {
			if showIBSTickets != nil {
				showIBSTickets(label, tickets, errMsg)
			}
		})
	}

	addrID, addrLabel, raw, err := ibsAddressLookup(number)
	fyne.Do(func() { showDebugResponse("IBS: Antwort getByNumber", ibsDebugPayload(raw, err)) })
	currentIBSAddrID = addrID  // fuer die spaetere Schlagwort-Suche (getMatchingEvents)
	setIBSAddressField(addrID) // "Kundenv. ID" im STT-Tab ("-" wenn leer)
	if err != nil {
		render(number, nil, err.Error())
		return
	}
	if addrID == "" {
		Log(fmt.Sprintf("IBS: keine Adresse zu Rufnummer %q gefunden", number))
		render(number, nil, T("IBS: keine Adresse zur Rufnummer gefunden."))
		return
	}
	who := addrLabel
	if who == "" {
		who = number
	}
	Log(fmt.Sprintf("IBS: Rufnummer %q -> Adresse %s (%q)", number, addrID, addrLabel))

	events, rawEv, err := ibsFetchEvents(addrID)
	fyne.Do(func() { showDebugResponse("IBS: Antwort getEvents", ibsDebugPayload(rawEv, err)) })
	if err != nil {
		render(who, nil, err.Error())
		return
	}
	tickets := ibsEventTickets(events)
	open := 0
	for _, t := range tickets {
		if t.Open {
			open++
		}
	}
	Log(fmt.Sprintf("IBS: %d Ticket(s) zu Adresse %s, davon %d offen", len(tickets), addrID, open))
	render(who, tickets, "")
}

// performIBSBuzzwordSearch ist Schritt 2 der Schlagwort-Ticketsuche fuer die
// Kundenverwaltung: es sucht mit den Schlagworten (buzzwords) die passenden
// Tickets via POST /api/kundenverwaltung/tickets-by-buzzwords und zeigt sie in
// der Ticketliste. addrID grenzt die Suche auf einen Kunden ein; ist addrID
// leer (z.B. manuelle Portal-Suche ohne aktiven Anruf), sucht der Endpunkt
// GLOBAL ueber alle Kunden. Voraussetzung: Checkbox "IBS Tickets" aktiv
// (config.JarvisIBS). Laeuft in einer Goroutine; UI-Zugriffe via fyne.Do.
func performIBSBuzzwordSearch(addrID, buzzwords string) {
	buzzwords = strings.TrimSpace(buzzwords)
	addrID = strings.TrimSpace(addrID)
	if !config.JarvisIBS {
		return
	}
	if buzzwords == "" {
		Log("IBS Schlagwort-Suche übersprungen: keine Schlagworte")
		return
	}

	limit := config.JarvisIBSSearchLimit
	if limit <= 0 {
		limit = 30
	}

	render := func(label string, tickets []ibsTicket, errMsg string) {
		fyne.Do(func() {
			if showIBSTickets != nil {
				showIBSTickets(label, tickets, errMsg)
			}
		})
	}

	scope := addrID
	if scope == "" {
		scope = "(global)"
	}
	Log(fmt.Sprintf("IBS Schlagwort-Suche: Kunde %s, limit %d, Schlagworte %q", scope, limit, buzzwords))
	events, raw, err := ibsFetchMatchingEvents(addrID, buzzwords, limit)
	// Debug-Popup zeigt Request UND Rohantwort - so ist bei "keine Treffer"
	// sofort sichtbar, was gesendet und was geantwortet wurde.
	reqPreview := "GET " + kvBuzzwordURL(addrID, buzzwords, limit)
	fyne.Do(func() {
		showDebugResponse("Kundenverwaltung: tickets-by-buzzwords", reqPreview+"\n\n--- Antwort ---\n"+ibsDebugPayload(raw, err))
	})
	label := T("Treffer zu: ") + buzzwords
	if err != nil {
		render(label, nil, err.Error())
		return
	}
	tickets := ibsEventTickets(events)
	Log(fmt.Sprintf("IBS Schlagwort-Suche: %d Treffer (Kunde %s)", len(tickets), scope))
	render(label, tickets, "")
}
