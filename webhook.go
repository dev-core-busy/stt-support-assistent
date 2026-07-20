package main

// webhook.go — Rufnummern-Übergabe per eingehendem HTTP-Webhook.
//
// Ein externer Trigger (z.B. eine Telefonanlage) ruft beim eingehenden Anruf
// eine URL dieser App auf und übergibt die Rufnummer des Anrufers. Die App
// sucht mit der Rufnummer in Jira (über die bestehende Jarvis-Jira-Suche) und
// trägt den Issue-Key des Top-Treffers ins CRM Feld
// (customerField, reines Anzeige-Label) ein.
//
// Der Server lauscht auf 0.0.0.0:<Port><Pfad> (Port/Pfad in den Einstellungen,
// Abschnitt "Rufnummern Übergabe"; Portvorgabe 5555, Pfadvorgabe "/rufnummer").
// Rufnummer wird sowohl per GET-Query (?number=...) als auch per POST
// (JSON {"number":"..."} oder Formularfeld) akzeptiert.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var (
	webhookMu     sync.Mutex
	webhookServer *http.Server

	// currentCRM spiegelt die aktuell im CRM Feld stehende, gültige
	// CRM-Nummer (Issue-Key). Wird über setCustomerField gepflegt (das Feld ist
	// ein reines Anzeige-Label, Webhook/Wiederhol-Button sind die einzigen
	// Schreibquellen). Leer, wenn im Feld
	// keine gültige CRM steht (z.B. "nicht gefunden" oder leer). Die Ticketsuche
	// (manuell + Auto-Scan) läuft nur, wenn hier eine CRM steht (s. hasCRM).
	// Mutex-geschützt, da aus dem UI-Thread gesetzt und aus dem Auto-Scan gelesen.
	crmMu      sync.Mutex
	currentCRM string

	// lastCallerNumber ist die zuletzt empfangene Rufnummer. Der Wiederhol-Button
	// (STT-Tab) startet damit die CRM-Abfrage erneut. Mutex-geschützt, da aus dem
	// Webhook-Goroutine gesetzt und aus dem UI-Thread gelesen.
	callerNumMu      sync.Mutex
	lastCallerNumber string
)

// crmKeyPattern erkennt einen Jira-Issue-Key / eine CRM-Nummer wie "CRM-10550"
// oder "SUP-1234": Buchstabe, dann Buchstaben/Ziffern, "-", Ziffern.
var crmKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-\d+$`)

// validCRM liefert die getrimmte CRM, wenn text wie eine CRM/Issue-Key aussieht,
// sonst "" (z.B. für "nicht gefunden", leere Eingabe oder eine reine Rufnummer).
func validCRM(text string) string {
	t := strings.TrimSpace(text)
	if crmKeyPattern.MatchString(t) {
		return t
	}
	return ""
}

// setCurrentCRM / getCurrentCRM / hasCRM kapseln den CRM-Status thread-sicher.
func setCurrentCRM(s string) {
	crmMu.Lock()
	currentCRM = strings.TrimSpace(s)
	crmMu.Unlock()
}

func getCurrentCRM() string {
	crmMu.Lock()
	defer crmMu.Unlock()
	return currentCRM
}

// hasCRM meldet, ob im CRM Feld aktuell eine gültige CRM steht.
func hasCRM() bool {
	return getCurrentCRM() != ""
}

// numberParamKeys sind die akzeptierten Parameter-/JSON-Feldnamen für die
// Rufnummer (verschiedene Telefonanlagen benennen das Feld unterschiedlich).
var numberParamKeys = []string{"number", "num", "nummer", "phone", "tel", "telefon", "caller", "callerid", "rufnummer"}

// effectiveWebhookPort liefert den konfigurierten Port, sonst die Vorgabe 5555.
func effectiveWebhookPort() int {
	p := config.WebhookPort
	if p <= 0 || p > 65535 {
		return 5555
	}
	return p
}

// effectiveWebhookPath liefert den konfigurierten URL-Pfad (mit führendem "/"),
// sonst die Vorgabe "/rufnummer".
func effectiveWebhookPath() string {
	p := strings.TrimSpace(config.WebhookPath)
	if p == "" {
		p = "/rufnummer"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// getLocalIPHint ermittelt die primäre LAN-IPv4 des Rechners (für kopierbare
// Beispiel-URLs im Einstellungen-Hilfetext). Der UDP-"Dial" sendet keine Pakete,
// sondern ermittelt nur die Quell-Adresse der Standard-Route. Ohne Route (kein
// Netz) folgt der Platzhalter "<Rechner-IP>".
func getLocalIPHint() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
			return addr.IP.String()
		}
	}
	return "<Rechner-IP>"
}

// startWebhookServer startet den Listener, sofern in den Einstellungen aktiviert
// und noch keiner läuft. Idempotent (mehrfacher Aufruf schadet nicht).
func startWebhookServer() {
	webhookMu.Lock()
	defer webhookMu.Unlock()

	if webhookServer != nil {
		return // läuft bereits
	}
	if !config.WebhookEnabled {
		return
	}

	port := effectiveWebhookPort()
	path := effectiveWebhookPath()

	mux := http.NewServeMux()
	mux.HandleFunc(path, webhookHandler)

	srv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	webhookServer = srv
	Log(fmt.Sprintf("Rufnummern-Webhook lauscht auf http://0.0.0.0:%d%s", port, path))

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Log(fmt.Sprintf("Rufnummern-Webhook: Serverfehler (Port %d belegt?): %v", port, err))
			webhookMu.Lock()
			if webhookServer == srv {
				webhookServer = nil
			}
			webhookMu.Unlock()
		}
	}()
}

// stopWebhookServer fährt einen laufenden Listener sauber herunter.
func stopWebhookServer() {
	webhookMu.Lock()
	srv := webhookServer
	webhookServer = nil
	webhookMu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	Log("Rufnummern-Webhook gestoppt")
}

// restartWebhookServer übernimmt geänderte Einstellungen (Aktiv/Port/Pfad).
func restartWebhookServer() {
	stopWebhookServer()
	startWebhookServer()
}

// webhookHandler nimmt die Rufnummer entgegen, bestätigt sofort (die Jira-Suche
// kann bis zu 120 s dauern) und stößt die CRM-Ermittlung asynchron an.
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	number := extractNumber(r)
	w.Header().Set("Content-Type", "application/json")
	if number == "" {
		Log("Rufnummern-Webhook: Aufruf ohne Rufnummer von " + r.RemoteAddr)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"ok":false,"error":"keine Rufnummer uebergeben (Parameter 'number')"}`)
		return
	}
	Log(fmt.Sprintf("Rufnummern-Webhook: Rufnummer %q von %s empfangen", number, r.RemoteAddr))

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"ok":true,"number":%q}`, number)

	// Bei eingehendem Anruf optional automatisch die Mitschrift starten. Bewusst
	// hier (echter Webhook-Aufruf), nicht in handleIncomingCaller – der
	// Wiederhol-Button nutzt handleIncomingCaller und soll keine Aufnahme starten.
	maybeAutoStartRecording()

	go handleIncomingCaller(number, true)
}

// maybeAutoStartRecording startet bei eingehendem Anruf automatisch die
// Mitschrift, sofern in den Einstellungen aktiviert (config.AutoRecordOnCall)
// und noch keine Aufnahme läuft. toggleRecording manipuliert UI-Elemente und
// läuft daher im Fyne-Main-Loop; der isRecording-Check wird dort erneut
// ausgeführt (Schutz gegen parallele Aufrufe).
func maybeAutoStartRecording() {
	if !config.AutoRecordOnCall || isRecording.Load() {
		return
	}
	Log("Rufnummern-Webhook: Auto-Start der Mitschrift bei Anruf")
	fyne.Do(func() {
		if !isRecording.Load() {
			toggleRecording()
		}
	})
}

// extractNumber liest die Rufnummer aus Query-Parametern (GET & POST), aus einem
// JSON-Body oder aus Formularfeldern.
func extractNumber(r *http.Request) string {
	// 1) Query-Parameter (funktioniert für GET und POST). Bewusst NICHT
	//    r.URL.Query(), da Go dort '+' als Leerzeichen dekodiert und damit das
	//    führende '+' einer E.164-Nummer (+49...) verschluckt würde.
	for _, k := range numberParamKeys {
		if v := strings.TrimSpace(queryValuePreservingPlus(r.URL.RawQuery, k)); v != "" {
			return v
		}
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		return ""
	}

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body map[string]interface{}
		if json.Unmarshal(data, &body) == nil {
			for _, k := range numberParamKeys {
				if v, ok := body[k]; ok {
					if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" && s != "<nil>" {
						return s
					}
				}
			}
		}
		return ""
	}

	// 2) Formular-Body (application/x-www-form-urlencoded / multipart).
	if err := r.ParseForm(); err == nil {
		for _, k := range numberParamKeys {
			if v := strings.TrimSpace(r.PostFormValue(k)); v != "" {
				return v
			}
		}
	}
	return ""
}

// queryValuePreservingPlus liest einen Query-Parameter aus rawQuery, ohne ein
// literales '+' in ein Leerzeichen zu wandeln. Perzent-Sequenzen (%2B, %20 …)
// werden korrekt dekodiert (url.PathUnescape lässt '+' unverändert). So bleibt
// eine E.164-Rufnummer wie "+491701234567" erhalten.
func queryValuePreservingPlus(rawQuery, key string) string {
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, err := url.QueryUnescape(kv[0])
		if err != nil || k != key {
			continue
		}
		if v, err := url.PathUnescape(kv[1]); err == nil {
			return v
		}
		return kv[1]
	}
	return ""
}

// handleIncomingCaller verarbeitet einen eingehenden Anruf: zeigt die Rufnummer
// im Label an und ermittelt die CRM über den Jarvis-Endpoint. Läuft in einer
// Goroutine. Im Debug-Modus wird die Anfrage vor dem Versand als Popup gezeigt
// (debugPreviewAndConfirm).
//
// auto=true: automatischer Trigger (echter Webhook-Anruf) - die CRM-Suche läuft
// nur, wenn "Anrufer automatisch suchen" aktiviert ist (config.AutoSearchCaller);
// sonst wird ausschließlich die Rufnummer angezeigt. auto=false: manueller
// Trigger (Wiederhol-Button) - sucht immer.
func handleIncomingCaller(number string, auto bool) {
	// Rufnummer im Label anzeigen. Der CRM-Status folgt dem Feldinhalt
	// (setCustomerField) und wird durch das Ergebnis unten aktualisiert.
	setCallerNumber(number)

	// Die komplette Anruf-Ansicht SOFORT leeren (Status-Zeile "Jira: n
	// Treffer ...", Ticket-Karten beider Quellen): ihr Inhalt gehoert zum
	// VORHERIGEN Anrufer und stuende sonst bis zum Eintreffen der neuen
	// Ergebnisse (bis zu 120 s) in der Liste. Bewusst unabhaengig davon, ob
	// die Abfragen unten laufen - sonst bliebe bei deaktivierter Auto-Suche/
	// Checkbox ein veralteter Stand stehen.
	fyne.Do(func() {
		if resetCallView != nil {
			resetCallView()
		}
	})
	// Kundenv.-ID des vorherigen Anrufers zuruecksetzen (performIBSLookup
	// setzt gleich die neue, sofern die IBS-Abfrage unten laeuft). Auch den
	// Merker fuer die Schlagwort-Suche (getMatchingEvents) leeren.
	currentIBSAddrID = ""
	setIBSAddressField("-")

	if auto && !config.AutoSearchCaller {
		Log(fmt.Sprintf("Rufnummern-Webhook: Rufnummer %q angezeigt, Auto-Suche deaktiviert", number))
		return
	}

	// Im Debug-Modus die Anfrage als Popup zeigen (Senden/Abbrechen), sonst
	// sofort ausführen. debugPreviewAndConfirm regelt beides selbst. Sobald
	// die Abfrage TATSAECHLICH startet (nicht bei Abbruch im Debug-Popup),
	// zeigt die Ergebnisliste den "Ich arbeite"-Indikator - aber nur, wenn
	// danach auch eine Jira-TICKETLISTE geladen wird (s. wantJiraCallTickets):
	// die reine CRM-Ermittlung fuellt nur das CRM Feld, ohne Ticketliste
	// bliebe der Balken sonst endlos stehen.
	fyne.Do(func() {
		debugPreviewAndConfirm(mainWin, "Rufnummern-Webhook: CRM-Abfrage", jarvisPhonePreview(number), func() {
			if wantJiraCallTickets() && showCallWorking != nil {
				showCallWorking()
			}
			go performCallerJiraLookup(number)
		})
	})

	// IBS-Kundenverwaltung: laeuft ZUSAETZLICH zur Jira-CRM-Suche, wenn die
	// Checkbox "IBS Tickets" aktiv ist (nur aktivierbar, wenn URL + API-Key
	// hinterlegt sind). Flow: Rufnummer -> Adresse -> alle Events; Anzeige in
	// der gemeinsamen Anruf-Ticketliste (s. ibs_client.go).
	if config.JarvisIBS && ibsConfigured() {
		fyne.Do(func() {
			debugPreviewAndConfirm(mainWin, "Rufnummern-Webhook: IBS-Abfrage", ibsRequestPreview(number), func() {
				if showCallWorking != nil {
					showCallWorking()
				}
				go performIBSLookup(number)
			})
		})
	}
}

// performCallerJiraLookup fragt über GET /api/jira/phonenumber die CRM zur
// Rufnummer ab und trägt sie ins CRM Feld ein, sonst
// "nicht gefunden". setCustomerField aktualisiert das Feld und
// pflegt currentCRM (Freigabe der Ticketsuche).
func performCallerJiraLookup(number string) {
	res, raw, err := jarvisPhoneLookup(number)

	// Bei aktivem Debug-Modus die Serverantwort (bzw. den Fehler) als Popup zeigen.
	payload := prettyJSON(raw)
	if err != nil {
		if payload == "" {
			payload = "Fehler: " + err.Error()
		} else {
			payload = "Fehler: " + err.Error() + "\n\nRohantwort:\n" + payload
		}
	}
	fyne.Do(func() { showDebugResponse("Rufnummern-Webhook: Antwort", payload) })

	if err != nil {
		Log("Rufnummern-Webhook: CRM-Abfrage fehlgeschlagen: " + err.Error())
		setCurrentCRM("")
		setCustomerField("-")
		return
	}

	matches := distinctCRMMatches(res)
	// Kandidatenanzahl immer protokollieren (unabhängig vom Debug-Modus), damit
	// im log.txt nachvollziehbar ist, wie viele CRM-Treffer der Server lieferte.
	Log(fmt.Sprintf("Rufnummern-Webhook: Rufnummer %q -> %d CRM-Kandidat(en) (total=%d)", number, len(matches), res.Total))
	if len(matches) == 0 {
		Log(fmt.Sprintf("Rufnummern-Webhook: keine CRM zu %q gefunden (total=%d)", number, res.Total))
		setCurrentCRM("")
		setCustomerField("-")
		return
	}

	crm := matches[0].Key
	if len(matches) > 1 {
		if config.CallerTakeFirstMatch {
			// Einstellung "ersten Treffer nehmen": ohne Popup den ersten übernehmen.
			Log(fmt.Sprintf("Rufnummern-Webhook: %d CRM-Kandidaten zu %q -> erster genommen (%s)", len(matches), number, crm))
		} else {
			// Mehrere CRM-Kunden zur Rufnummer: den richtigen per Popup auswählen
			// lassen. askCRMSelection blockiert bis zur Auswahl (Ergebnis kommt per
			// Kanal aus dem Fyne-Main-Loop zurück); "" bedeutet Abbruch. Bewusst hier
			// in der Goroutine warten, damit applyCRM darunter wieder aus Goroutine-
			// Kontext läuft und fyne.Do gefahrlos nutzen kann.
			Log(fmt.Sprintf("Rufnummern-Webhook: %d CRM-Kandidaten zu %q -> Auswahl", len(matches), number))
			crm = askCRMSelection(number, matches)
			if crm == "" {
				Log(fmt.Sprintf("Rufnummern-Webhook: CRM-Auswahl abgebrochen (Rufnummer %q)", number))
				setCurrentCRM("")
				setCustomerField("-")
				return
			}
		}
	}
	applyCRM(number, crm)
}

// distinctCRMMatches liefert die eindeutigen, gültigen CRM-Treffer aus der
// Server-Antwort in Reihenfolge ihres ersten Auftretens. Primärquelle ist
// matches[] (die CRM-Nummer steht im Feld key, dazu name+type); ist matches
// leer, dient das Top-Level-Feld crm als Rückfall (nur wenn found=true).
func distinctCRMMatches(res *jiraPhoneResponse) []jiraPhoneMatch {
	seen := map[string]bool{}
	var out []jiraPhoneMatch
	for _, m := range res.Matches {
		c := validCRM(m.Key)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		m.Key = c // getrimmt
		out = append(out, m)
	}
	if len(out) == 0 {
		if c := validCRM(res.CRM); c != "" && res.Found {
			out = append(out, jiraPhoneMatch{Key: c})
		}
	}
	return out
}

// crmMatchLabel baut den Anzeigetext eines CRM-Treffers: "CRM-xxxx — Name (Typ)".
func crmMatchLabel(m jiraPhoneMatch) string {
	label := m.Key
	if name := strings.TrimSpace(m.Name); name != "" {
		label = fmt.Sprintf("%s — %s", m.Key, name)
	}
	if typ := strings.TrimSpace(m.Type); typ != "" {
		label = fmt.Sprintf("%s  (%s)", label, typ)
	}
	return label
}

// orgSymbolRes / personSymbolRes sind die fest eingebetteten Typ-Symbole des
// CRM-Auswahl-Popups (s. assets.go). Einmal gebaut, im Popup wiederverwendet.
var (
	orgSymbolRes    = fyne.NewStaticResource("organisation.png", orgSymbolPNG)
	personSymbolRes = fyne.NewStaticResource("person.png", personSymbolPNG)
)

// crmTypeIcon liefert das (eingebettete) Symbol zu einem Treffer-Typ:
// Organisation bzw. Person; für alles andere ein neutraler Platzhalter
// (theme.QuestionIcon).
func crmTypeIcon(typ string) fyne.Resource {
	t := strings.ToLower(strings.TrimSpace(typ))
	switch {
	case strings.Contains(t, "organisation"):
		return orgSymbolRes
	case strings.Contains(t, "person"):
		return personSymbolRes
	default:
		return theme.QuestionIcon()
	}
}

// crmRow ist eine anklickbare Zeile im CRM-Auswahl-Popup: Typ-Symbol + Text.
// Einzelklick wählt die Zeile aus (Hervorhebung), Doppelklick übernimmt sie
// direkt. Das Symbol sitzt in einer festen 28×28-Zelle (GridWrap) mit FillMode
// Contain und wird dadurch NICHT verzerrt/gestreckt.
type crmRow struct {
	widget.BaseWidget
	idx      int
	bg       *canvas.Rectangle
	content  fyne.CanvasObject
	onSelect func(int)
	onChoose func(int)
}

func newCRMRow(idx int, icon fyne.Resource, text string, onSelect, onChoose func(int)) *crmRow {
	img := canvas.NewImageFromResource(icon)
	img.FillMode = canvas.ImageFillContain // Seitenverhältnis wahren, nicht strecken
	iconCell := container.NewGridWrap(fyne.NewSize(28, 28), img)
	lbl := widget.NewLabel(text)
	lbl.Wrapping = fyne.TextWrapWord

	r := &crmRow{
		idx:      idx,
		bg:       canvas.NewRectangle(color.Transparent),
		content:  container.NewBorder(nil, nil, iconCell, nil, lbl),
		onSelect: onSelect,
		onChoose: onChoose,
	}
	r.ExtendBaseWidget(r)
	return r
}

func (r *crmRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(r.bg, container.NewPadded(r.content)))
}

func (r *crmRow) Tapped(*fyne.PointEvent) {
	if r.onSelect != nil {
		r.onSelect(r.idx)
	}
}

func (r *crmRow) DoubleTapped(*fyne.PointEvent) {
	if r.onChoose != nil {
		r.onChoose(r.idx)
	}
}

func (r *crmRow) setSelected(sel bool) {
	if sel {
		r.bg.FillColor = kiAccentSoft
	} else {
		r.bg.FillColor = color.Transparent
	}
	r.bg.Refresh()
}

// askCRMSelection zeigt bei mehreren CRM-Kandidaten ein Auswahl-Popup und
// liefert die gewählte CRM-Nummer (key) zurück (leer bei Abbruch). Wird aus
// einer Goroutine aufgerufen und blockiert bis zur Auswahl: die UI läuft im
// Fyne-Main-Loop (fyne.Do), das Ergebnis kommt über einen gepufferten Kanal
// zurück. Je Eintrag: Typ-Symbol (Organisation/Person/neutral), CRM-Nummer,
// Name und Typ. Einzelklick wählt aus, Doppelklick übernimmt sofort.
func askCRMSelection(number string, matches []jiraPhoneMatch) string {
	ch := make(chan string, 1)
	var once sync.Once
	send := func(s string) { once.Do(func() { ch <- s }) } // genau ein Ergebnis

	fyne.Do(func() {
		selected := 0
		var rows []*crmRow
		var dlg dialog.Dialog

		selectRow := func(idx int) {
			selected = idx
			for i, r := range rows {
				r.setSelected(i == idx)
			}
		}
		choose := func(idx int) {
			if idx < 0 || idx >= len(matches) {
				return
			}
			send(matches[idx].Key) // erst Ergebnis liefern...
			if dlg != nil {
				dlg.Hide() // ...dann schließen (ein evtl. Cancel-Callback wird durch once ignoriert)
			}
		}

		listBox := container.NewVBox()
		for i, m := range matches {
			row := newCRMRow(i, crmTypeIcon(m.Type), crmMatchLabel(m), selectRow, choose)
			rows = append(rows, row)
			listBox.Add(row)
		}
		selectRow(0) // Vorauswahl: erster Treffer

		info := widget.NewLabel(fmt.Sprintf(T("Zu Rufnummer %s wurden %d CRM-Kunden gefunden.\nEinen Kunden anklicken und „Übernehmen“ – oder per Doppelklick direkt wählen:"), number, len(matches)))
		info.Wrapping = fyne.TextWrapWord

		scroll := container.NewVScroll(listBox)
		scroll.SetMinSize(fyne.NewSize(520, 300))
		content := container.NewBorder(info, nil, nil, nil, scroll)

		dlg = dialog.NewCustomConfirm(T("CRM-Kunde auswählen"), T("Übernehmen"), T("Abbrechen"), content, func(ok bool) {
			if !ok || selected < 0 || selected >= len(matches) {
				send("")
				return
			}
			send(matches[selected].Key)
		}, mainWin)
		dlg.Show()
	})
	return <-ch
}

// wantJiraCallTickets meldet, ob beim Anruf die Jira-TICKETLISTE geladen
// werden soll: nur wenn in der Such-Karte auch eine Jira-Quelle angehakt ist
// ("Jira Tickets" oder "offene Jira Tickets"). Ist dort z.B. nur "IBS
// Tickets" aktiv, wird beim Anruf KEINE Jira-Ticketsuche ausgefuehrt - die
// CRM zur Rufnummer wird aber weiterhin ermittelt und im CRM Feld angezeigt.
func wantJiraCallTickets() bool {
	return config.JarvisJira || config.JarvisOpenOnly
}

// applyCRM übernimmt die (ggf. ausgewählte) CRM: zeigt sie im CRM Feld
// (setCustomerField pflegt dabei auch den CRM-Status) und stößt automatisch
// die Ticketsuche an – mit erkanntem Text, oder (bei leerem Textfenster) alle
// Tickets zur CRM (nicht-offene anfangs per Checkbox ausgeblendet). Aus
// Goroutine-Kontext aufrufen (nutzt fyne.Do). Die Suche ist NACH
// setCustomerField eingereiht, der CRM-Status steht also bereits, wenn sie
// läuft (fyne.Do arbeitet die Closures in Reihenfolge ab).
func applyCRM(number, crm string) {
	Log(fmt.Sprintf("Rufnummern-Webhook: Rufnummer %q -> CRM %s", number, crm))
	setCustomerField(crm)
	if !wantJiraCallTickets() {
		Log("Rufnummern-Webhook: Jira-Ticketsuche übersprungen (keine Jira-Quelle in der Suche angehakt)")
		return
	}
	fyne.Do(func() {
		// Nach der CRM-Auswahl kann die Ticketsuche dauern: "Ich arbeite"
		// zeigen. showCallWorking laesst bereits angezeigte Ergebnisse des
		// AKTUELLEN Anrufs (z.B. die schnellere IBS-Quelle) stehen.
		if showCallWorking != nil {
			showCallWorking()
		}
		if searchMatchingTickets != nil && outputArea != nil {
			searchMatchingTickets(outputArea.Text, nil, true, true)
		}
	})
}

// setCustomerField setzt den Text des CRM Felds thread-sicher über den
// Fyne-Main-Loop und pflegt den daraus abgeleiteten CRM-Status (currentCRM,
// Freigabe der Ticketsuche). Das erledigte frueher das OnChanged des
// Textfelds; seit der Umstellung auf ein reines Anzeige-Label ist diese
// Funktion die einzige Schreibquelle. Neuer Feldinhalt -> die bisherige
// Ticketliste gehoert zu einer anderen CRM und wird geleert; beim
// Webhook-Treffer folgt unmittelbar eine neue Suche, die sie wieder fuellt.
func setCustomerField(text string) {
	if customerField == nil {
		return
	}
	fyne.Do(func() {
		// Unveraenderter Wert: nichts tun. Das alte Textfeld-OnChanged feuerte
		// nur bei echter Aenderung - ohne diesen Guard wuerde z.B. ein
		// Wiederhol-Lookup mit derselben CRM die angezeigte Ticketliste
		// grundlos leeren und die ausgedehnte Ansicht zuklappen.
		if customerField.Text == text {
			return
		}
		customerField.SetText(text)
		setCurrentCRM(validCRM(text))
		if clearTicketResults != nil {
			clearTicketResults()
		}
	})
}

// prettyJSON gibt s eingerückt zurück, wenn es gültiges JSON ist, sonst s
// unverändert (bzw. "" bleibt "").
func prettyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return s
	}
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(s), "", "  ") == nil {
		return buf.String()
	}
	return s
}

// setCallerNumber zeigt die empfangene Rufnummer im Label zwischen Feld und
// Start-Button an (thread-sicher über den Fyne-Main-Loop).
func setCallerNumber(number string) {
	callerNumMu.Lock()
	lastCallerNumber = number
	callerNumMu.Unlock()
	if callerNumberLabel == nil {
		return
	}
	fyne.Do(func() { callerNumberLabel.SetText(T("Anruf von: ") + number) })
}

// getLastCallerNumber liefert die zuletzt empfangene Rufnummer (für den
// Wiederhol-Button); leer, wenn noch kein Anruf einging.
func getLastCallerNumber() string {
	callerNumMu.Lock()
	defer callerNumMu.Unlock()
	return lastCallerNumber
}
