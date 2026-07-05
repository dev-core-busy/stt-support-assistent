package main

// ibs_client.go — Anbindung der IBS-Kundenverwaltungs-API ("IBS Tickets").
//
// API laut REST_API_DOKUMENTATION_Kundenverwaltung.md (Projekt-Root; Server ist
// kundenverwaltung.jar, Basis-URL + API-Key in den Einstellungen, Felder
// "URL Kundenverwaltung API" / "API-Key Kundenverwaltung"):
//
//   POST /va/ad/getByNumber        {"from_number": "<nr>"}  -> address[] (name, full-address, ...)
//   POST /va/ev/getEvents          {"event":"getEvents","request":{"address_id":"<id>"}}
//                                  -> event[] (id, creation_time, state, "dispatch user", text)
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
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
)

// ibsTicket ist die anzeigefertige Form eines IBS-Events fuer den IBS-Bereich
// der Ergebnisliste (Felder gemaess event[]-Schema der Doku).
type ibsTicket struct {
	Key     string // event.id (lokale Event-ID)
	Status  string // event.state (EVENT_STATE-Enum, z.B. NEU, IN_BEARBEITUNG, ENDED)
	Created string // event.creation_time
	User    string // event."dispatch user" (zugewiesener Benutzer)
	Text    string // event.text (Beschreibung)
	Open    bool   // state != ENDED (Filter des Radios "offen"/"alle")
}

// ibsConfigured meldet, ob URL und API-Key der Kundenverwaltung hinterlegt
// sind (Voraussetzung der Checkbox "IBS Tickets" und Sichtbarkeits-Schalter
// der Kundenverwaltungs-Bedienelemente, s. refreshIBSCheck).
func ibsConfigured() bool {
	return strings.TrimSpace(config.IBS.Url) != "" && strings.TrimSpace(config.IBS.ApiKey) != ""
}

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
// address[]-Array (Top-Level "name": "nothing found (unknown)") -> addrID "".
//
// ACHTUNG Doku-Luecke: das dokumentierte address[]-Schema (name, phone-normal,
// phone-mobile, full-address) enthaelt KEIN ID-Feld, getEvents verlangt aber
// eine address_id. Die ID wird daher tolerant gesucht (address_id/id/...);
// fehlt sie tatsaechlich, gibt es eine klare Fehlermeldung + Rohantwort im Log.
func ibsAddressLookup(number string) (addrID, addrLabel, raw string, err error) {
	v, raw, err := ibsPostJSON("/va/ad/getByNumber", map[string]string{"from_number": number})
	if err != nil {
		return "", "", raw, err
	}

	// address[] ist das Treffer-Array; fehlt es (oder ist leer), gab es keinen
	// Treffer. Mehrere Adressen zur Nummer: erste nehmen (geloggt).
	addresses, _ := ibsFindValue(v, "address", "addresses", "adresse").([]interface{})
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
			Key:     ibsFieldString(m, "id", "eventid", "localid", "nr", "number"),
			Status:  ibsFieldString(m, "state", "status"),
			Created: ibsFieldString(m, "creationtime", "created", "createdat", "date"),
			User:    ibsFieldString(m, "dispatchuser", "user", "assignee"),
			Text:    ibsFieldString(m, "text", "description", "beschreibung", "note", "message"),
		}
		t.Open = !strings.EqualFold(strings.TrimSpace(t.Status), "ENDED")
		if t.Key == "" && t.Text == "" {
			if j, err := json.Marshal(m); err == nil {
				t.Text = string(j)
			}
		}
		out = append(out, t)
	}
	return out
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
