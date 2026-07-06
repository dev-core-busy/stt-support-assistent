package main

// Anbindung an den internen Jarvis-Support-Assistenten (REST-API, siehe
// Doku unter /support-api am Jarvis-Host). Bildet die rechte Fensterhaelfte "KI-Support" im STT-Tab:
// Suche ueber RAG/Jira Tickets/Confluence mit optionaler KI-Zusammenfassung
// sowie Zusammenfassung einzelner Jira-Tickets.
//
// Layout/Optik orientieren sich an design.png: eine Such-Karte (Eingabe +
// roter "Suchen"-Button, Filter-Checkboxen, aufklappbare erweiterte
// Einstellungen), darunter eine scrollende Ergebnisliste aus Karten (KI-
// Gesamtzusammenfassung zuerst, dann nummerierte Treffer mit Quelle-/Score-
// Badges). Bewusst KEINE Tabelle/kein Splitter fuer die Ergebnisse (das
// vorherige Table-Layout ueberlappte sich) und keine grauen Kartenhintergruende
// - nur duenne Rahmen auf weissem Grund, Akzentfarbe Rot (siehe winTheme).
import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// kiAccent ist die Akzentfarbe des KI-Support-Panels (identisch zur globalen
// Primärfarbe in winTheme.Color) - dunkles Rot statt Fyne-Blau.
var kiAccent = color.NRGBA{R: 0xB0, G: 0x1E, B: 0x2C, A: 255}
var kiAccentSoft = color.NRGBA{R: 0xF6, G: 0xDF, B: 0xE1, A: 255}

// refreshIBSCheck aktiviert/deaktiviert die Checkbox "IBS Tickets" im
// KI-Support-Panel je nachdem, ob URL und API-Key der Kundenverwaltungs-API
// (config.IBS) hinterlegt sind. Wird beim Bau des Panels gesetzt und von den
// Einstellungs-Feldern in main.go bei jeder Änderung aufgerufen (nil-Guard dort).
var refreshIBSCheck func()

// showIBSTickets/clearIBSTickets bespielen den IBS-Ticketbereich oberhalb der
// Ergebnisliste (Rufnummern-Webhook -> IBS-Kundenverwaltung, s. ibs_client.go).
// Gesetzt beim Bau des Panels (buildKISupportPanel); Aufrufer laufen im
// Fyne-Main-Thread (fyne.Do) und pruefen auf nil.
var (
	showIBSTickets  func(header string, tickets []ibsTicket, errMsg string)
	clearIBSTickets func()
	// resetCallView leert die komplette Anruf-Ansicht SOFORT (Status-Zeile
	// "Jira: n Treffer ...", Ticket-Karten beider Quellen, KI-Caches).
	// Aufruf beim Eingang eines NEUEN Anrufs (webhook.go): die alte Liste
	// gehoert zum vorherigen Anrufer und stuende sonst noch bis zum Eintreffen
	// der neuen Ergebnisse (bis zu 120 s) in der Ansicht.
	resetCallView func()
	// showCallWorking zeigt in der (geleerten) Ergebnisliste einen
	// "Ich arbeite"-Indikator (Text + Endlos-Balken), bis die naechsten
	// Ergebnisse ihn ersetzen (renderResults/renderError). Aufruf beim
	// tatsaechlichen Start der Anruf-Abfragen (webhook.go) und nach der
	// CRM-Auswahl. Bewusst OHNE den Anruf-Zustand anzutasten: bereits
	// eingetroffene IBS-Ergebnisse des AKTUELLEN Anrufs bleiben erhalten.
	showCallWorking func()
)

// jarvisHTTPClient: eigener Client mit deaktivierter TLS-Pruefung, da der
// interne Jarvis-Server laut Doku (/support-api) ein selbstsigniertes Zertifikat
// verwendet (curl-Beispiele dort nutzen "-k"). Timeout grosszuegig, da eine
// Suche mit KI-Zusammenfassung serverseitig RAG+Jira+LLM-Aufruf durchlaeuft.
func jarvisHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

type jarvisQueryRequest struct {
	Text         string `json:"text"`
	RAG          bool   `json:"rag"`
	Jira         bool   `json:"jira_all"`
	Confluence   bool   `json:"confluence"`
	AI           bool   `json:"ai"`
	OpenOnly     bool   `json:"jira_open"`
	JiraLimit    int    `json:"jira_limit,omitempty"`
	SummaryLines int    `json:"summary_lines,omitempty"`
	Lang         string `json:"lang,omitempty"`
	// Prompt ist die in "Einstellungen" hinterlegte Anweisung an die LLM
	// (config.JarvisSearchQuery, Feld "Prompt für KI-Zusammenfassung"). Wird der KI-
	// Gesamtzusammenfassung vorangestellt. Getrennt vom Suchtext (Text) - beides
	// sind bewusst unabhaengige Dinge. omitempty: ohne hinterlegten Prompt wird
	// das Feld gar nicht erst gesendet.
	Prompt string `json:"prompt,omitempty"`
}

type jarvisBlock struct {
	Source      string `json:"source"`
	SourceLabel string `json:"source_label"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Score       int    `json:"score"`
	Summary     string `json:"summary"`
	Link        string `json:"link"`
	// Bei WISSEN-Treffern zusaetzlich gefuellt: doc ist der (serverseitige)
	// Dateipfad der Quelle, doc_name der reine Dateiname. Der eigentliche
	// Quell-Link steht in Link - haeufig RELATIV (z.B.
	// "/api/knowledge/file_raw?path=...", also ohne http-Schema); solche Links
	// muessen gegen die Jarvis-Basis-URL aufgeloest werden (s. resolveJarvisLink).
	Doc     string `json:"doc"`
	DocName string `json:"doc_name"`
}

type jarvisQueryResponse struct {
	OK        bool          `json:"ok"`
	Error     string        `json:"error"`
	Query     string        `json:"query"`
	AISummary string        `json:"ai_summary"`
	JiraTotal int           `json:"jira_total"`
	Blocks    []jarvisBlock `json:"blocks"`
}

// jarvisPost fuehrt einen POST mit JSON-Body gegen den Jarvis-Server aus und
// dekodiert die Antwort in out. HTTP-Statuscodes 401/403/400 liefern laut
// Doku (/support-api) ebenfalls JSON ({"ok": false, "error": "..."}) - werden also
// hier bewusst nicht als Transport-Fehler behandelt, sondern durchgereicht.
func jarvisPost(path string, reqBody interface{}, out interface{}) error {
	baseURL := strings.TrimRight(strings.TrimSpace(config.Jarvis.Url), "/")
	apiKey := strings.TrimSpace(config.Jarvis.ApiKey)
	if baseURL == "" || apiKey == "" {
		err := fmt.Errorf(T("Jarvis-Server-URL und/oder API-Key sind nicht konfiguriert (siehe Einstellungen)."))
		Log("Jarvis " + path + ": " + err.Error())
		return err
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		Log("Jarvis " + path + ": Request konnte nicht codiert werden: " + err.Error())
		return err
	}
	req, err := http.NewRequest("POST", baseURL+path, bytes.NewReader(payload))
	if err != nil {
		Log("Jarvis " + path + ": Request konnte nicht gebaut werden: " + err.Error())
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := jarvisHTTPClient().Do(req)
	if err != nil {
		wrapped := fmt.Errorf(T("Jarvis-Server nicht erreichbar (%s): %v"), baseURL, err)
		Log("Jarvis " + path + ": " + wrapped.Error())
		return wrapped
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, out); err != nil {
		wrapped := fmt.Errorf(T("Antwort nicht im erwarteten Format (HTTP %d): %v"), resp.StatusCode, err)
		Log("Jarvis " + path + ": " + wrapped.Error() + " | Rohantwort: " + string(body))
		return wrapped
	}
	return nil
}

func jarvisQuery(req jarvisQueryRequest) (*jarvisQueryResponse, error) {
	var res jarvisQueryResponse
	if err := jarvisPost("/api/support/query", req, &res); err != nil {
		return nil, err
	}
	if !res.OK {
		Log("Jarvis /api/support/query: Server meldet Fehler: " + res.Error)
		return nil, fmt.Errorf("%s", res.Error)
	}
	return &res, nil
}

// jarvisSummarizeRequest ist der Body (Mode A: einzelnes Jira-Ticket) für
// POST /api/support/summarize.
type jarvisSummarizeRequest struct {
	Key    string `json:"key"`
	Source string `json:"source"`
	Lines  int    `json:"lines,omitempty"`
	Lang   string `json:"lang,omitempty"`
}

// jarvisSummarizeResponse ist die Antwort für Mode A (einzelnes Ticket).
type jarvisSummarizeResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	JiraBase string `json:"jira_base"`
}

// jarvisSummarizeTicket holt die KI-Zusammenfassung eines einzelnen Jira-Tickets
// (POST /api/support/summarize, Mode A). Rückgabe: Zusammenfassungstext.
func jarvisSummarizeTicket(key string) (string, error) {
	lang := config.JarvisLang
	if lang == "" {
		lang = "de"
	}
	req := jarvisSummarizeRequest{
		Key:    key,
		Source: "JIRA",
		Lines:  config.JarvisSummaryLines, // 0 -> omitempty -> Serverdefault
		Lang:   lang,
	}
	var res jarvisSummarizeResponse
	if err := jarvisPost("/api/support/summarize", req, &res); err != nil {
		return "", err
	}
	if !res.OK {
		Log("Jarvis /api/support/summarize: Server meldet Fehler: " + res.Error)
		return "", fmt.Errorf("%s", res.Error)
	}
	return strings.TrimSpace(res.Summary), nil
}

// jarvisRequestPreview formatiert eine Anfrage lesbar fuer den Debug-Modus
// (siehe debugPreviewAndConfirm in main.go) - Server-URL, Endpunkt und der
// JSON-Body, der tatsaechlich verschickt wuerde.
func jarvisRequestPreview(req jarvisQueryRequest) string {
	body, _ := json.MarshalIndent(req, "", "  ")
	baseURL := strings.TrimRight(strings.TrimSpace(config.Jarvis.Url), "/")
	return fmt.Sprintf("POST %s/api/support/query\n\n%s", baseURL, string(body))
}

// jiraPhoneResponse ist die Antwort von GET /api/jira/phonenumber?phone=...
// (Rufnummern-Webhook, s. webhook.go). Der Server ermittelt zur Rufnummer die
// passende CRM-Nummer.
// jiraPhoneMatch ist ein einzelner CRM-Treffer der Rufnummern-Suche. key ist die
// CRM-Nummer (CRM-xxxxx), name der Kunden-/Entitätsname, type die Kategorie
// (z.B. "Organisationen", "Organisationen Produktgruppen", "Personen").
type jiraPhoneMatch struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type jiraPhoneResponse struct {
	OK      bool             `json:"ok"`
	Error   string           `json:"error"`
	Phone   string           `json:"phone"`
	CRM     string           `json:"crm"`
	Found   bool             `json:"found"`
	Matches []jiraPhoneMatch `json:"matches"`
	Total   int              `json:"total"`
	// JQL/IQL: die serverseitig ausgeführte Abfrage (Feldname je nach Backend
	// "jql" oder "iql"). Variant: die tatsächlich gesuchte, normalisierte
	// Nummern-Variante. Nur für Debug/Nachvollziehbarkeit, nicht logikrelevant.
	JQL     string `json:"jql"`
	IQL     string `json:"iql"`
	Variant string `json:"variant"`
}

// jarvisPhoneLookup ruft GET /api/jira/phonenumber?phone=<phone> auf und liefert
// die vom Server ermittelte CRM. Auth/Client wie bei jarvisPost (X-API-Key,
// TLS-Skip für das selbstsignierte Zertifikat).
// Rückgabe: geparste Antwort, Roh-Body (für die Debug-Anzeige) und Fehler.
func jarvisPhoneLookup(phone string) (*jiraPhoneResponse, string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.Jarvis.Url), "/")
	apiKey := strings.TrimSpace(config.Jarvis.ApiKey)
	if baseURL == "" || apiKey == "" {
		err := fmt.Errorf(T("Jarvis-Server-URL und/oder API-Key sind nicht konfiguriert (siehe Einstellungen)."))
		Log("Jarvis /api/jira/phonenumber: " + err.Error())
		return nil, "", err
	}

	reqURL := baseURL + "/api/jira/phonenumber?phone=" + url.QueryEscape(phone)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := jarvisHTTPClient().Do(req)
	if err != nil {
		wrapped := fmt.Errorf(T("Jarvis-Server nicht erreichbar (%s): %v"), baseURL, err)
		Log("Jarvis /api/jira/phonenumber: " + wrapped.Error())
		return nil, "", wrapped
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	raw := string(body)
	var out jiraPhoneResponse
	if err := json.Unmarshal(body, &out); err != nil {
		wrapped := fmt.Errorf(T("Antwort nicht im erwarteten Format (HTTP %d): %v"), resp.StatusCode, err)
		Log("Jarvis /api/jira/phonenumber: " + wrapped.Error() + " | Rohantwort: " + raw)
		return nil, raw, wrapped
	}
	if !out.OK {
		Log("Jarvis /api/jira/phonenumber: Server meldet Fehler: " + out.Error)
		return nil, raw, fmt.Errorf("%s", out.Error)
	}
	return &out, raw, nil
}

// jarvisPhonePreview formatiert die CRM-Abfrage lesbar für den Debug-Modus.
func jarvisPhonePreview(phone string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(config.Jarvis.Url), "/")
	return fmt.Sprintf("GET %s/api/jira/phonenumber?phone=%s", baseURL, url.QueryEscape(phone))
}

// kiCard umrahmt content mit einem duennen, abgerundeten Rand auf weissem/
// transparentem Grund - bewusst KEIN grauer Flaechenhintergrund.
func kiCard(content fyne.CanvasObject) fyne.CanvasObject {
	border := canvas.NewRectangle(color.Transparent)
	border.StrokeColor = color.NRGBA{R: 0, G: 0, B: 0, A: 40}
	border.StrokeWidth = 1
	border.CornerRadius = 8
	return container.NewStack(border, container.NewPadded(content))
}

// kiPill zeichnet ein kleines, abgerundetes Badge (z.B. Quelle oder Score-%).
func kiPill(text string, bg, fg color.Color) fyne.CanvasObject {
	rect := canvas.NewRectangle(bg)
	rect.CornerRadius = 9
	label := canvas.NewText(text, fg)
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.TextSize = 11
	return container.NewStack(rect, container.NewPadded(container.NewCenter(label)))
}

// jarvisTitleWidget zeigt den Treffer-Titel als klickbaren Link (oeffnet im
// Standardbrowser), wenn die API einen "link" mitliefert - sonst als normaler
// fetter Text (kein Link vorhanden/ungueltige URL).
//
// Der Titel wird IMMER vollstaendig angezeigt (Wrapping statt Kuerzung) und
// bricht bei Bedarf in mehrere Zeilen um. Bewusst KEIN Hover-Tooltip mehr:
// ein widget.PopUp ist ein Canvas-weites Overlay, und solange es sichtbar ist,
// durchsucht Fyne bei einem Klick NUR das Overlay (FindObjectAtPositionMatching
// in internal/driver/util.go) - der darunterliegende Link bekommt den Klick
// dann nicht. Da MouseMoved das Tooltip bis unmittelbar vor den Klick offen
// hielt, wurden Klicks auf den Link unzuverlaessig verschluckt. Der Titel ist
// durch das Wrapping ohnehin vollstaendig lesbar, ein Tooltip ist unnoetig.
func jarvisTitleWidget(index int, title, link string) fyne.CanvasObject {
	text := fmt.Sprintf("%d. %s", index, title)
	if link != "" {
		if u, err := url.Parse(link); err == nil && u.Scheme != "" {
			h := widget.NewHyperlink(text, u)
			// Wrapping statt Kuerzung: der Titel muss vollstaendig lesbar bleiben,
			// darf aber nicht als eine einzige lange Zeile die Ergebnisliste (und
			// damit den Fenstersteiler sttSplit) in die Breite treiben. Funktioniert
			// nur, weil dieses Widget in seinem Border-Layout als MITTE (nicht als
			// "left") sitzt, s. headerRow - "left"/"right" bekommen in Fyne immer nur
			// ihre eigene Minimalbreite, nie mehr, egal wie viel Platz vorhanden ist.
			h.Wrapping = fyne.TextWrapWord
			return h
		}
	}
	label := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	label.Wrapping = fyne.TextWrapWord // s.o.: vollstaendiger Titel, keine Breitenexplosion
	return label
}

// resolveJarvisLink macht aus einem Block-Link eine oeffenbare absolute URL.
// Der Jarvis-Server liefert Quell-Links bei WISSEN-Treffern haeufig RELATIV
// (z.B. "/api/knowledge/file_raw?path=..." ohne Schema); diese werden gegen die
// konfigurierte Jarvis-Basis-URL (config.Jarvis.Url) aufgeloest. Absolute Links
// (http/https, z.B. Confluence) bleiben unveraendert. Rueckgabe nil, wenn kein
// verwertbarer Link vorliegt.
func resolveJarvisLink(link string) *url.URL {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil
	}
	u, err := url.Parse(link)
	if err != nil {
		return nil
	}
	if u.Scheme != "" {
		return u // bereits absolut
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.Jarvis.Url), "/"))
	if err != nil || base.Scheme == "" {
		return nil // ohne gueltige Basis-URL laesst sich ein relativer Link nicht oeffnen
	}
	return base.ResolveReference(u)
}

// jarvisIsServerURL prueft, ob u auf den Jarvis-Server selbst zeigt (gleicher
// Host wie config.Jarvis.Url). Solche Links - v.a. "/api/knowledge/file_raw" -
// sind ohne den API-Key (Header X-API-Key) nicht abrufbar; ein normaler
// Browser-Klick wuerde an der Authentifizierung scheitern. Externe Links
// (Confluence o.ae., anderer Host) werden dagegen ganz normal im Browser
// geoeffnet.
func jarvisIsServerURL(u *url.URL) bool {
	base, err := url.Parse(strings.TrimSpace(config.Jarvis.Url))
	if err != nil || base.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, base.Host)
}

// sanitizeFilename macht aus einem (Server-)Dateinamen einen sicheren, lokalen
// Basisnamen (ohne Pfadanteile, ohne fuer Windows unzulaessige Zeichen).
func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(strings.ReplaceAll(name, "\\", "/")))
	name = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '|', '?', '*':
			return '_'
		}
		if r < 0x20 {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		return ""
	}
	return name
}

// downloadJarvisFile laedt eine Server-Quelle (z.B. file_raw-PDF) MIT API-Key
// herunter, speichert sie in einem Temp-Verzeichnis und liefert den lokalen
// Pfad zurueck. Nutzt denselben HTTP-Client wie die Suche (selbstsigniertes
// Zertifikat, s. jarvisHTTPClient).
func downloadJarvisFile(u *url.URL, suggestedName string) (string, error) {
	apiKey := strings.TrimSpace(config.Jarvis.ApiKey)
	if apiKey == "" {
		return "", fmt.Errorf("kein API-Key konfiguriert (siehe Einstellungen)")
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := jarvisHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	name := sanitizeFilename(suggestedName)
	if name == "" {
		name = "jarvis-quelle"
	}
	dir := filepath.Join(os.TempDir(), "jarvis-quellen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return dst, nil
}

// jarvisSourceRow baut die eigene "Quelle:"-Zeile, die UNTERHALB jedes
// Treffer-Blocks angezeigt wird - insbesondere fuer WISSEN-Treffer, deren
// Quelle sonst gar nicht sichtbar/klickbar waere (relative Links wurden zuvor
// verworfen). Als Anzeigetext dient der Dateiname (doc_name) bzw. das
// Quelle-Label.
//
// Klick-Verhalten: Zeigt der Link auf den Jarvis-Server selbst (file_raw &
// Co.), wird die Datei MIT API-Key heruntergeladen und mit der System-
// Standardanwendung geoeffnet (ein Browser-Klick wuerde an der Authentifizierung
// scheitern). Externe Links (Confluence o.ae.) oeffnen normal im Browser.
// Rueckgabe nil, wenn der Block keinen verwertbaren Link hat.
func jarvisSourceRow(b jarvisBlock, win fyne.Window) fyne.CanvasObject {
	u := resolveJarvisLink(b.Link)
	if u == nil {
		return nil
	}
	name := strings.TrimSpace(b.DocName)
	if name == "" {
		name = strings.TrimSpace(b.SourceLabel)
	}
	if name == "" {
		name = u.String()
	}
	link := widget.NewHyperlink(name, u)
	// Wrapping + Border-Mitte (nicht "left"), damit lange Dateinamen/URLs
	// vollstaendig umbrechen statt rechts abgeschnitten zu werden.
	link.Wrapping = fyne.TextWrapWord

	if jarvisIsServerURL(u) {
		dlName := strings.TrimSpace(b.DocName)
		if dlName == "" {
			dlName = name
		}
		link.OnTapped = func() {
			Log("Jarvis-Quelle wird mit API-Key geladen: " + u.String())
			go func() {
				path, err := downloadJarvisFile(u, dlName)
				fyne.Do(func() {
					if err != nil {
						showErr(fmt.Errorf(T("Quelle konnte nicht geladen werden: %v"), err), win)
						return
					}
					if err := openPath(path); err != nil {
						showErr(fmt.Errorf(T("Datei konnte nicht geöffnet werden: %v"), err), win)
					}
				})
			}()
		}
	}

	prefix := widget.NewLabelWithStyle(T("Quelle:"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return container.NewBorder(nil, nil, prefix, nil, link)
}

// collapseText kuerzt s auf hoechstens maxLines Zeilen bzw. maxChars Zeichen
// (rune-sicher, Schnitt an Wortgrenze). Rueckgabe: gekuerzter Text und ob
// ueberhaupt gekuerzt wurde (nur dann wird ein "mehr"/"weniger"-Umschalter
// benoetigt). Dient dem Einklappen der KI-Zusammenfassung.
func collapseText(s string, maxLines, maxChars int) (string, bool) {
	truncated := false
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	out := strings.Join(lines, "\n")
	if runes := []rune(out); len(runes) > maxChars {
		cut := string(runes[:maxChars])
		if idx := strings.LastIndexAny(cut, " \n"); idx > 0 {
			cut = cut[:idx]
		}
		out = strings.TrimRight(cut, " \n")
		truncated = true
	}
	return out, truncated
}

// Einheitliche Kuerzungsgrenze fuer ALLE Karteninhalte (Jira-/Wissen-/IBS-
// Karten, KI-Zusammenfassungen): laengere Texte starten eingeklappt und
// werden per "mehr"/"weniger" umgeschaltet.
const (
	cardCollapseLines = 6
	cardCollapseChars = 500
)

// collapsibleLabel liefert einen umbruchfaehigen Label-Text, der ab
// cardCollapseLines/Chars eingeklappt startet und einen "mehr"/"weniger"-
// Umschalter darunter bekommt; kurze Texte bleiben ein schlichtes Label.
// Fuer Inhalte OHNE Filter-Markierung (KI-Zusammenfassungen) - die Treffer-
// Karten selbst brauchen die RichText-/Highlight-Variante in renderResults.
// Bewusst Label statt RichTextFromMarkdown: LLM-Text ist kein verlaesslich
// sauberes Markdown (s. Kommentar bei der KI-Gesamtzusammenfassung).
func collapsibleLabel(s string) fyne.CanvasObject {
	lbl := widget.NewLabel("")
	lbl.Wrapping = fyne.TextWrapWord
	short, truncated := collapseText(s, cardCollapseLines, cardCollapseChars)
	if !truncated {
		lbl.SetText(s)
		return lbl
	}
	expanded := false
	toggle := widget.NewButton(T("mehr"), nil)
	toggle.Importance = widget.LowImportance
	apply := func() {
		if expanded {
			lbl.SetText(s)
			toggle.SetText(T("weniger"))
		} else {
			lbl.SetText(short + " …")
			toggle.SetText(T("mehr"))
		}
	}
	toggle.OnTapped = func() {
		expanded = !expanded
		apply()
	}
	apply()
	return container.NewVBox(lbl, container.NewHBox(toggle)) // Toggle linksbuendig
}

// ticketSummaryControls baut die Bedienelemente der "KI-Zusammenfassung" eines
// Ticket-Eintrags (nur in der "Tickets zu einer CRM"-Liste): einen Button (oben
// rechts im Eintrag zu platzieren, rot mit weißer Schrift) und einen darunter
// einzublendenden Inhalts-Container. Der Button laedt on demand die Einzelticket-
// Zusammenfassung (POST /api/support/summarize) und blendet sie ein-/aus
// (Toggle). Lade-/Fehlerzustand erscheinen im Inhalts-Container.
func ticketSummaryControls(key string) (*widget.Button, *fyne.Container) {
	holder := container.NewVBox()
	var btn *widget.Button
	btn = widget.NewButton(T("KI-Zusammenfassung"), func() {
		if len(holder.Objects) > 0 { // bereits eingeblendet -> zuklappen
			holder.RemoveAll()
			holder.Refresh()
			return
		}
		holder.Add(widget.NewLabel(T("KI-Zusammenfassung wird geladen …")))
		holder.Refresh()
		btn.Disable()
		go func() {
			sum, err := jarvisSummarizeTicket(key)
			fyne.Do(func() {
				btn.Enable()
				holder.RemoveAll()
				text := sum
				switch {
				case err != nil:
					text = T("Fehler: ") + err.Error()
				case sum == "":
					text = T("(keine Zusammenfassung erhalten)")
				}
				// Lange Zusammenfassungen eingeklappt mit "mehr"/"weniger".
				holder.Add(collapsibleLabel(text))
				holder.Refresh()
			})
		}()
	})
	btn.Importance = widget.HighImportance // rot mit weißer Schrift (Theme-Primärfarbe)
	return btn, holder
}

// ibsSummaryCache/ibsSummaryExpanded halten die KI-Zusammenfassungen der
// IBS-Karten ueber Neu-Renderings der Anruf-Ticketliste hinweg. Die Liste
// wird komplett neu aufgebaut, sobald das zweite Teil-Ergebnis (Jira/IBS)
// eintrifft, das Anzeige-Limit wechselt oder der zyklische Ticket-Scan
// rendert - OHNE Cache ersetzte das eine gerade geladene (oder eben
// eingetroffene) Zusammenfassung durch eine frische, zugeklappte Karte:
// "Antwort kommt, wird aber nicht angezeigt". Schluessel ist die Event-ID;
// geleert wird beim naechsten Anruf bzw. beim Schliessen der Ansicht
// (clearIBSTickets/closeCallView).
var (
	ibsSummaryCache    = map[string]string{}
	ibsSummaryExpanded = map[string]bool{}
)

func clearIBSSummaryCache() {
	ibsSummaryCache = map[string]string{}
	ibsSummaryExpanded = map[string]bool{}
}

// ibsSummaryControls: das Pendant zu ticketSummaryControls fuer eine
// Kundenverwaltungs-Karte (IBS) der Anruf-Ticketliste. Der Jarvis-Summarize-
// Endpunkt arbeitet Jira-Key-basiert und kennt IBS-Events nicht - die
// Zusammenfassung laeuft daher ueber das in den Einstellungen gewaehlte
// Analyse-LLM (runAnalysisLogic: lokal e2b/12b oder remote) mit dem
// Event-Text als Eingabe. Anders als bei Jira ERSETZT die Zusammenfassung
// den Tickettext in der Karte (bodySlot zeigt entweder textBody oder die
// Zusammenfassung); der zweite Klick stellt den Text wieder her.
// Zeilenvorgabe aus config.JarvisSummaryLines. Eine bereits geladene,
// aufgeklappte Zusammenfassung erscheint nach einem Neu-Rendern der Liste
// sofort wieder (Cache s.o.).
func ibsSummaryControls(tk ibsTicket, textBody fyne.CanvasObject, bodySlot *fyne.Container) *widget.Button {
	cacheKey := tk.Key
	if cacheKey == "" {
		// Ohne Event-ID kein stabiler Schluessel: Text-Laenge+Erstellzeit als Behelf.
		cacheKey = fmt.Sprintf("%s|%d", tk.Created, len(tk.Text))
	}
	showSummary := func(s string) {
		if s == "" {
			s = T("(keine Zusammenfassung erhalten)")
		}
		// Lange Zusammenfassungen eingeklappt mit "mehr"/"weniger".
		bodySlot.Objects = []fyne.CanvasObject{collapsibleLabel(s)}
		bodySlot.Refresh()
	}
	restoreText := func() {
		bodySlot.Objects = []fyne.CanvasObject{textBody}
		bodySlot.Refresh()
	}
	// Beim (Neu-)Aufbau der Karte eine zuvor aufgeklappte Zusammenfassung
	// direkt wieder anstelle des Textes zeigen.
	if ibsSummaryExpanded[cacheKey] {
		if sum, ok := ibsSummaryCache[cacheKey]; ok {
			showSummary(sum)
		} else {
			ibsSummaryExpanded[cacheKey] = false // Laden wurde vom Neu-Rendern unterbrochen
		}
	}
	var btn *widget.Button
	btn = widget.NewButton(T("KI-Zusammenfassung"), func() {
		if ibsSummaryExpanded[cacheKey] { // Zusammenfassung sichtbar -> Text zurueck
			ibsSummaryExpanded[cacheKey] = false
			restoreText()
			return
		}
		ibsSummaryExpanded[cacheKey] = true
		if sum, ok := ibsSummaryCache[cacheKey]; ok {
			showSummary(sum)
			return
		}
		loading := widget.NewLabel(T("KI-Zusammenfassung wird geladen …"))
		bodySlot.Objects = []fyne.CanvasObject{loading}
		bodySlot.Refresh()
		btn.Disable()
		lines := config.JarvisSummaryLines
		if lines <= 0 {
			lines = 5
		}
		prompt := fmt.Sprintf(T("Fasse das folgende Support-Ticket in höchstens %d Zeilen zusammen. Antworte nur mit der Zusammenfassung."), lines)
		// Kontextzeile (ID/Datum/Bearbeiter/Status) + Beschreibungstext als
		// Eingabe fuer die LLM.
		var meta []string
		if tk.Key != "" {
			meta = append(meta, "#"+tk.Key)
		}
		if tk.Created != "" {
			meta = append(meta, tk.Created)
		}
		if tk.User != "" {
			meta = append(meta, tk.User)
		}
		if tk.Status != "" {
			meta = append(meta, tk.Status)
		}
		text := strings.TrimSpace(strings.Join(meta, " · ") + "\n\n" + tk.Text)
		Log(fmt.Sprintf("IBS KI-Zusammenfassung #%s: Anfrage an Analyse-LLM (%s, %d Zeichen Text)", tk.Key, config.AnalysisModel, len(text)))
		go func() {
			sum := strings.TrimSpace(runAnalysisLogic(text, prompt))
			Log(fmt.Sprintf("IBS KI-Zusammenfassung #%s: %d Zeichen erhalten", tk.Key, len(sum)))
			fyne.Do(func() {
				btn.Enable()
				// Fehlertexte ("Fehler: ...") bewusst NICHT cachen, damit ein
				// erneuter Klick einen neuen Versuch startet.
				if sum != "" && !strings.HasPrefix(sum, "Fehler") {
					ibsSummaryCache[cacheKey] = sum
				}
				// runAnalysisLogic liefert Fehler als Text - direkt anzeigen.
				// Wurde inzwischen zurueckgeschaltet, bleibt der Text stehen
				// (der naechste Klick zeigt das gecachte Ergebnis sofort).
				if ibsSummaryExpanded[cacheKey] {
					showSummary(sum)
				}
			})
		}()
	})
	btn.Importance = widget.HighImportance
	return btn
}

// newFilterWrap gibt einem Filter-Entry eine feste Breite fuer die rechte
// Seite eines Border-Layouts (dort bekaeme er sonst nur seine schmale
// Minimalbreite). Eine Stelle fuer beide Filterfelder (Status-Zeile und
// CRM-Kopfzeile), damit die Breite nicht auseinanderlaeuft.
func newFilterWrap(e *widget.Entry) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(220, e.MinSize().Height), e)
}

// highlightedSegments zerlegt text in RichText-Segmente, in denen alle
// Vorkommen von query (case-insensitiv) fett in der Akzentfarbe markiert sind
// - fuer die Treffer-Markierung beim Tippen im Volltext-Filter. Leere query
// oder kein Treffer liefert ein einziges unmarkiertes Segment. Der Vergleich
// laeuft allokationsfrei auf Rune-Basis (die Funktion wird pro Karte und
// Tastendruck gerufen): strings.ToLower kann bei Sonderzeichen die
// BYTE-Laenge aendern, Rune-Indizes von Original und Kleinschreibung bleiben
// dagegen synchron (unicode.ToLower ist 1:1 pro Rune); aendert sich doch die
// Rune-Anzahl, wird sicherheitshalber gar nicht markiert statt falsch
// geschnitten.
func highlightedSegments(text, query string) []widget.RichTextSegment {
	plain := func(s string) widget.RichTextSegment {
		return &widget.TextSegment{Text: s, Style: widget.RichTextStyleInline}
	}
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(q) == 0 {
		return []widget.RichTextSegment{plain(text)}
	}
	r := []rune(text)
	lr := []rune(strings.ToLower(text))
	if len(lr) != len(r) || len(q) > len(r) {
		return []widget.RichTextSegment{plain(text)}
	}
	matchAt := func(i int) bool {
		for j, qr := range q {
			if lr[i+j] != qr {
				return false
			}
		}
		return true
	}
	var segs []widget.RichTextSegment
	start := 0
	for i := 0; i+len(q) <= len(lr); {
		if !matchAt(i) {
			i++
			continue
		}
		if i > start {
			segs = append(segs, plain(string(r[start:i])))
		}
		segs = append(segs, &widget.TextSegment{Text: string(r[i : i+len(q)]), Style: widget.RichTextStyle{
			Inline:    true,
			ColorName: theme.ColorNamePrimary,
			TextStyle: fyne.TextStyle{Bold: true},
		}})
		i += len(q)
		start = i
	}
	if len(segs) == 0 { // kein Treffer im Text (Karte matcht z.B. nur im Titel)
		return []widget.RichTextSegment{plain(text)}
	}
	if start < len(r) {
		segs = append(segs, plain(string(r[start:])))
	}
	return segs
}

// defaultTicketSearchPrompt ist der Standard-Prompt fuer den Button "Suche
// passende Tickets" (STT-Tab). "[Textfenster]" wird vor dem API-Call durch den
// erkannten Text ersetzt. Ueberschreibbar in "Einstellungen"
// (config.JarvisTicketSearchPrompt).
const defaultTicketSearchPrompt = "Suche Tickets mit ähnlichem Inhalt zu [Textfenster]"

// ticketSearchPlaceholder ist der Platzhalter im Ticket-Such-Prompt, der vor
// dem API-Call durch den erkannten Text ersetzt wird.
const ticketSearchPlaceholder = "[Textfenster]"

// buildKISupportPanel liefert den Inhalt der rechten Fensterhaelfte: eine
// Such-Karte oben (Eingabe, roter Suchen-Button, Filter, aufklappbare
// erweiterte Einstellungen, Ticket-Zusammenfassung) und darunter eine
// scrollende Ergebnisliste (KI-Gesamtzusammenfassung + Treffer-Karten mit
// Quelle-/Score-Badge), angelehnt an design.png.
// Server-URL/API-Key werden im Tab "Einstellungen" gepflegt (config.Jarvis).
// Suchtext und Ticketbezug sind reine Sitzungseingaben ohne jede Verbindung
// zu einem Prompt aus "Einstellungen" - Suche/Sucheingabe und LLM-Prompt sind
// unabhaengige Konzepte und duerfen nicht miteinander synchronisiert werden.
//
// Rueckgabe zusaetzlich zum Panel: searchMatchingTickets - wird vom Button
// "Suche passende Tickets" im STT-Tab (linke Haelfte) sowie vom automatischen
// zyklischen Scan aufgerufen und sucht zum uebergebenen (erkannten) Text
// passende Jira-Tickets; das Ergebnis erscheint in derselben Ergebnisliste wie
// eine normale Suche. Parameter auto=true unterdrueckt das Debug-Popup (sonst
// erschiene es bei jedem Zyklus) und ueberspringt einen Zyklus, wenn noch eine
// vorherige Automatik-Suche laeuft (Ueberlappschutz).
func buildKISupportPanel(win fyne.Window) (fyne.CanvasObject, func(recognizedText string, trigger *widget.Button, auto bool, crmFallback bool), func()) {
	// --- Ergebnisbereich (wird nach jeder Suche neu befuellt) ---
	// placeholderLbl ist ein DAUERHAFTES, übersetzbares Label (trLabel registriert
	// einen Sprachwechsel-Callback). Es wird im Platzhalter-Zustand wieder-
	// verwendet statt bei jedem clearResults neu erzeugt - so folgt der Platzhalter
	// auch einem Sprachwechsel, ohne dass zuvor eine Suche laufen musste.
	placeholderLbl := trLabel("Noch keine Suche durchgeführt.")
	resultsBox := container.NewVBox(placeholderLbl)

	// --- Gemeinsame Anruf-Ticketliste: Zustand Jira + Kundenverwaltung ---
	// Jira-CRM-Suche und IBS-Abfrage laufen beim Anruf parallel; beide
	// Ergebnisse landen in EINER Liste (renderResults, crmView). crmLast
	// speichert die Jira-Seite des letzten CRM-Renderns, ibsLast die IBS-Seite
	// - wer spaeter fertig wird, rendert die Liste komplett neu (skipCallReset
	// laesst dabei die Kopfzeilen-Bedienelemente unangetastet).
	// showIBSTickets/clearIBSTickets werden erst NACH renderResults zugewiesen
	// (sie rendern darueber); der Zustand liegt hier, weil renderResults ihn liest.
	type ibsState struct {
		label   string // Anzeigename der gefundenen Adresse (bzw. Rufnummer)
		tickets []ibsTicket
		errMsg  string
	}
	type crmState struct {
		query    string
		res      *jarvisQueryResponse // nil: (noch) keine Jira-Daten, nur IBS
		duration time.Duration
		openKeys map[string]bool
	}
	var ibsLast *ibsState
	var crmLast *crmState

	resultsScroll := container.NewVScroll(resultsBox)
	resultsBg := canvas.NewRectangle(color.White)
	resultsArea := container.NewStack(resultsBg, resultsScroll)

	// setCRMListExpanded blendet fuer die CRM-Ticketliste ("Tickets zur CRM")
	// den kompletten Suchbereich oben aus, sodass die Liste die ganze rechte
	// Haelfte einnimmt; stattdessen erscheint eine Kopfzeile mit ✕-Button zum
	// Schliessen. Wird erst nach dem Bau des Panels zugewiesen (s.u.), daher
	// ueberall nil-geschuetzt aufrufen.
	var setCRMListExpanded func(expanded bool)

	// Kopfzeilen-Bedienelemente der Anruf-/CRM-Ticketliste (Kopfzeile "Tickets
	// zur CRM", s.u. crmHeader): EIN Radio "offen"/"alle" fuer BEIDE Quellen
	// (ersetzt die fruehere Checkbox "offene Tickets"), zwei Quellen-Haekchen
	// "Jira"/"Kundenv." und der Volltext-Filter. Alles wirkt rein clientseitig
	// auf die bereits geladenen Karten. renderResults verdrahtet den Filter bei
	// jedem CRM-Rendern neu; Radio/Haekchen rufen crmApplyFilters auf, das auf
	// das applyFilters des JEWEILS letzten Renderns zeigt. Zustand liegt
	// sprachneutral in openOnlyTickets/showJiraSrc/showIBSSrc (Index/bool statt
	// uebersetztem Anzeigetext).
	// Radio- und Haekchen-Zustand kommt aus der Config und wird bei jeder
	// Aenderung dort gespeichert - er bleibt damit (wie die Such-Checkboxen)
	// ueber Abfragen und App-Neustarts erhalten.
	var crmApplyFilters func()
	openOnlyTickets := config.JarvisCallOpenOnly
	openIdx := 0
	if !openOnlyTickets {
		openIdx = 1
	}
	// applyAndScrollTop: Umschalten von Radio/Quellen-Haekchen zeigt eine
	// inhaltlich andere Liste - danach immer an den Listenanfang springen
	// (sonst bliebe die Scroll-Position mitten im vorherigen Ergebnis stehen).
	applyAndScrollTop := func() {
		if crmApplyFilters != nil {
			crmApplyFilters()
			resultsScroll.ScrollToTop()
		}
	}
	crmOpenRadio := trRadio([]string{"offen", "alle"}, openIdx, func(idx int) {
		openOnlyTickets = idx == 0
		if config.JarvisCallOpenOnly != openOnlyTickets {
			config.JarvisCallOpenOnly = openOnlyTickets
			saveConfigDebounced()
		}
		applyAndScrollTop()
	})
	showJiraSrc, showIBSSrc := config.JarvisCallShowJira, config.JarvisCallShowIBS
	crmJiraCheck := trCheck("Jira", func(b bool) {
		showJiraSrc = b
		if config.JarvisCallShowJira != b {
			config.JarvisCallShowJira = b
			saveConfigDebounced()
		}
		applyAndScrollTop()
	})
	crmIBSCheck := trCheck("Kundenv.", func(b bool) {
		showIBSSrc = b
		if config.JarvisCallShowIBS != b {
			config.JarvisCallShowIBS = b
			saveConfigDebounced()
		}
		applyAndScrollTop()
	})
	crmJiraCheck.SetChecked(showJiraSrc)
	crmIBSCheck.SetChecked(showIBSSrc)
	crmFilterEntry := widget.NewEntry()
	trPlaceholder(crmFilterEntry, "Treffer filtern …")
	// resetCallControls setzt beim Aufbau einer NEUEN Anruf-Ansicht nur den
	// Volltext-Filter zurueck; Radio und Quellen-Haekchen behalten ihren
	// Zustand bewusst (s.o.). Nach-Renderings (zweites Ergebnis eingetroffen,
	// Anzeige-Limit geaendert) ueberspringen auch das via skipCallReset,
	// sonst wuerde eine laufende Nutzer-Filterung verworfen.
	skipCallReset := false
	resetCallControls := func() {
		crmFilterEntry.SetText("")
	}

	// progress: DER Endlos-Balken der Such-Karte - die einzige
	// "Ich arbeite"-Anzeige des Panels. Suchen (runSearch/searchMatchingTickets)
	// zeigen/verbergen ihn selbst; die Anruf-Abfragen zeigen ihn ueber
	// showCallWorking, und das erste eintreffende Ergebnis
	// (renderResults/renderError) bzw. clearResults verbirgt ihn wieder.
	// Bewusst VOR clearResults/showCallWorking deklariert (Referenz).
	progress := widget.NewProgressBarInfinite()
	progress.Hide()

	// renderResults wird vorwaerts deklariert: clearResults (Jira-Seite
	// verwerfen, IBS-Seite ggf. weiterzeigen) und die IBS-Hooks weiter unten
	// rendern darueber.
	var renderResults func(query string, res *jarvisQueryResponse, duration time.Duration, openKeys map[string]bool)

	// clearResults verwirft die Jira-/Suchseite der Ergebnisliste. Wird u.a.
	// aufgerufen, wenn der Inhalt des CRM-Felds wechselt (die bisherige
	// Jira-Ticketliste gehoert dann zu einer anderen CRM und ist ungueltig).
	// Die IBS-Seite gehoert dagegen zum AKTUELLEN Anruf (webhook.go leert sie
	// bei jedem neuen Anruf ueber clearIBSTickets): liegen IBS-Tickets vor,
	// bleibt die Anruf-Ansicht mit nur dieser Quelle offen - z.B. wenn zur
	// Rufnummer zwar eine Kundenverwaltungs-Adresse, aber keine Jira-CRM
	// gefunden wurde. Muss im Fyne-Main-Thread laufen.
	clearResults := func() {
		progress.Hide() // Sackgassen (CRM nicht gefunden/Auswahl abgebrochen) beenden die Arbeits-Anzeige
		crmLast = nil
		if ibsLast != nil {
			renderResults(ibsLast.label, nil, 0, map[string]bool{})
			return
		}
		if setCRMListExpanded != nil {
			setCRMListExpanded(false)
		}
		resultsBox.RemoveAll()
		resultsBox.Add(placeholderLbl)
		resultsBox.Refresh()
	}

	// closeCallView (✕-Button der Kopfzeile): schliesst die Anruf-Ansicht
	// KOMPLETT - auch die IBS-Seite - und stellt die normale Suchansicht
	// ("alles sichtbar") wieder her.
	closeCallView := func() {
		ibsLast = nil
		clearIBSSummaryCache()
		clearResults()
	}
	// Neuer Anruf leert die Ansicht auf demselben Weg wie der ✕-Button.
	resetCallView = closeCallView
	// Kein eigener Balken in der Ergebnisliste (es gaebe sonst ZWEI Anzeigen):
	// die Liste wird geleert und der vorhandene Balken der Such-Karte laeuft,
	// bis das erste Ergebnis eintrifft.
	showCallWorking = func() {
		if setCRMListExpanded != nil {
			setCRMListExpanded(false)
		}
		resultsBox.RemoveAll()
		resultsBox.Refresh()
		progress.Show()
	}

	// renderResults befuellt die Ergebnisliste. openKeys (nur CRM-Ticketliste,
	// sonst nil) ist die Menge der Jira-Keys, die der Server als OFFEN gemeldet
	// hat, und schaltet zugleich die CRM-Ansicht (crmView): Liste auf die
	// komplette rechte Haelfte ausgedehnt, Score-/Ranking-Badge ausgeblendet
	// (Relevanz-Prozent ergibt dort keinen Sinn), Radio "offen"/"alle"
	// (Start: offen) blendet nicht-offene Tickets rein clientseitig aus/ein -
	// KEINE neue Server-Anfrage beim Umschalten.
	renderResults = func(query string, res *jarvisQueryResponse, duration time.Duration, openKeys map[string]bool) {
		progress.Hide() // eintreffendes Ergebnis beendet die Arbeits-Anzeige
		crmView := openKeys != nil
		if crmView {
			// Jira-Seite fuer Nach-Renderings merken (IBS-Ergebnis trifft ein,
			// Anzeige-Limit geaendert, IBS-Seite geleert).
			crmLast = &crmState{query: query, res: res, duration: duration, openKeys: openKeys}
		} else {
			crmLast = nil // normale Suche verlaesst die Anruf-Ansicht
		}
		// res == nil (nur Anruf-Ansicht): keine Jira-Daten - zur Rufnummer gab
		// es (noch) keine CRM, angezeigt werden nur Kundenverwaltungs-Tickets.
		noJira := res == nil
		if noJira {
			res = &jarvisQueryResponse{}
		}
		if setCRMListExpanded != nil {
			setCRMListExpanded(crmView)
		}
		resultsBox.RemoveAll()

		// Status-Zeile: in der Anruf-Ansicht Treffer je Quelle (Jira /
		// Kundenverwaltung samt gefundener Adresse), sonst wie gehabt.
		var status *widget.Label
		if crmView {
			var segs []string
			if !noJira {
				segs = append(segs, fmt.Sprintf(T("Jira: %d Treffer"), len(res.Blocks)))
			}
			if ibsLast != nil {
				switch {
				case ibsLast.errMsg != "":
					segs = append(segs, T("Kundenv.: Fehler"))
				case ibsLast.label != "":
					segs = append(segs, fmt.Sprintf(T("Kundenv.: %d Tickets zu %s"), len(ibsLast.tickets), ibsLast.label))
				default:
					segs = append(segs, fmt.Sprintf(T("Kundenv.: %d Tickets"), len(ibsLast.tickets)))
				}
			}
			status = widget.NewLabel(strings.Join(segs, "  ·  "))
			status.Wrapping = fyne.TextWrapWord
		} else {
			status = widget.NewLabel(fmt.Sprintf(T("Ergebnis für „%s“ (%d Treffer · %d ms)"), query, len(res.Blocks), duration.Milliseconds()))
		}

		// Volltext-Filter der bereits geladenen Treffer (rein clientseitig, KEINE
		// neue Server-Anfrage und keine Verbindung zum Suchtext/Prompt). Jede
		// Treffer-Karte wird mit ihrem durchsuchbaren Text (kleingeschrieben)
		// gesammelt; beim Tippen werden nicht passende Karten ausgeblendet, ein
		// leeres Feld zeigt wieder alle. Die KI-Zusammenfassung bleibt als
		// Gesamtueberblick immer sichtbar.
		type filterCard struct {
			obj  fyne.CanvasObject
			text string
			open bool
			// snippet (RichText der Kartenbeschreibung) + raw (Originaltext):
			// beim Tippen im Volltext-Filter werden die Fundstellen darin
			// markiert. hlQuery ist der Filtertext der zuletzt gebauten
			// Markierung - nur bei Aenderung wird neu gebaut (Checkbox-Umschalten
			// und wieder eingeblendete Karten loesen sonst unnoetige, teure
			// RichText-Rebuilds aus).
			snippet *widget.RichText
			raw     string
			hlQuery string
			// src: "ibs" fuer Kundenverwaltungs-Karten, sonst Jira/Suche -
			// Grundlage der Quellen-Haekchen "Jira"/"Kundenv." (nur Anruf-Ansicht).
			src string
		}
		var cards []filterCard
		// Volltext-Filter: in der CRM-Ansicht kommt der Filtertext aus dem
		// dauerhaften Kopfzeilen-Entry, sonst aus einem lokalen Filterfeld in
		// der Status-Zeile (nur dann wird es ueberhaupt gebaut).
		filterSrc := crmFilterEntry
		var filterEntry *widget.Entry
		if !crmView {
			filterEntry = widget.NewEntry()
			filterEntry.SetPlaceHolder(T("Treffer filtern …"))
			filterSrc = filterEntry
		}
		// applyFilters kombiniert Volltext-Filter, Radio "offen"/"alle" und die
		// Quellen-Haekchen "Jira"/"Kundenv." (Radio + Haekchen nur in der
		// Anruf-Ansicht); alles rein clientseitig auf den geladenen Karten.
		applyFilters := func() {
			ql := strings.ToLower(strings.TrimSpace(filterSrc.Text))
			openOnly := crmView && openOnlyTickets
			for i := range cards {
				c := &cards[i]
				srcVisible := !crmView
				if !srcVisible {
					if c.src == "ibs" {
						srcVisible = showIBSSrc
					} else {
						srcVisible = showJiraSrc
					}
				}
				if srcVisible && (ql == "" || strings.Contains(c.text, ql)) && (!openOnly || c.open) {
					if c.snippet != nil && c.hlQuery != ql {
						c.snippet.Segments = highlightedSegments(c.raw, ql)
						c.snippet.Refresh()
						c.hlQuery = ql
					}
					c.obj.Show()
				} else {
					c.obj.Hide()
				}
			}
			resultsBox.Refresh()
		}

		// Filter-Widgets auf DIESE Kartenliste verdrahten. In der Anruf-Ansicht
		// sind das Radio/Quellen-Haekchen/Filterfeld der Kopfzeile "Tickets zur
		// CRM" (via crmApplyFilters); die Zuweisung ersetzt die Closure des
		// vorherigen Renderns, deren Karten bereits entfernt sind. Die
		// Bedienelemente werden nur beim Aufbau einer NEUEN Anruf-Ansicht
		// zurueckgesetzt - Nach-Renderings (zweites Ergebnis, Limit geaendert)
		// setzen skipCallReset und lassen die Nutzer-Auswahl stehen.
		// applyFilters laeuft am Ende dieses Renderns ohnehin einmal explizit.
		if crmView {
			crmApplyFilters = applyFilters
			crmFilterEntry.OnChanged = func(string) { applyFilters() }
			if !skipCallReset {
				resetCallControls()
			}
			skipCallReset = false
		} else {
			filterEntry.OnChanged = func(string) { applyFilters() }
		}

		// Filterfeld nur bei vorhandenen Treffern, rechtsbuendig neben dem
		// Status. In der CRM-Ticketliste entfaellt es hier - dort sitzen Filter
		// und Checkbox in der Kopfzeile.
		if len(res.Blocks) > 0 && !crmView {
			resultsBox.Add(container.NewBorder(nil, nil, nil, newFilterWrap(filterEntry), status))
		} else {
			resultsBox.Add(status)
		}

		if summary := strings.TrimSpace(res.AISummary); summary != "" {
			header := canvas.NewText(T("KI-GESAMTZUSAMMENFASSUNG"), kiAccent)
			header.TextStyle = fyne.TextStyle{Bold: true}
			header.TextSize = 12
			// Bewusst Label statt RichTextFromMarkdown: die KI-Zusammenfassung ist
			// freier Fliesstext vom Server, kein verlaesslich sauberes Markdown -
			// Ticket-Referenzen wie "[2] [JIRA]" wurden vom Markdown-Parser als
			// (unvollstaendige) Link-Syntax interpretiert und dadurch in viele
			// winzige Text-Segmente zerlegt, die dann einzeln umgebrochen wurden
			// (sichtbar als Spalte aus Einzelbuchstaben-Zeilen). Gleiches Problem
			// wie beim Snippet-Text unten, dort bereits mit Label geloest.
			// Einheitliches Einklappen ab cardCollapseLines Zeilen mit
			// "mehr"/"weniger" (s. collapsibleLabel).
			cardBox := container.NewVBox(header, collapsibleLabel(summary))
			resultsBox.Add(kiCard(cardBox))
		}

		for i, b := range res.Blocks {
			title := b.Title
			if title == "" {
				title = b.Key
			}
			titleWidget := jarvisTitleWidget(i+1, title, b.Link)

			// Ticket-Treffer (JIRA): der Link steckt bereits im Titel-Header, daher
			// KEINE Quelle-Bubble und KEINE "Quelle:"-Zeile. Nicht-Tickets
			// (WISSEN/Confluence) behalten Quelle-Badge und -Zeile.
			isTicket := strings.EqualFold(b.Source, "JIRA")

			var pills []fyne.CanvasObject
			if !isTicket {
				sourceLabel := b.SourceLabel
				if sourceLabel == "" {
					sourceLabel = b.Source
				}
				pills = append(pills, kiPill(strings.ToUpper(sourceLabel), kiAccentSoft, kiAccent))
			}
			if !crmView {
				pills = append(pills, kiPill(fmt.Sprintf("%d%%", b.Score), kiAccent, color.White))
			}

			// Rechts im Header: Badges (falls vorhanden) und - in der "Tickets zu
			// einer CRM"-Liste - der Button "KI-Zusammenfassung"
			// (rot/weiß). Der zugehoerige Inhalt (summaryHolder) wird unten angehaengt.
			right := append([]fyne.CanvasObject{}, pills...)
			var summaryHolder *fyne.Container
			if crmView && isTicket {
				if key := strings.TrimSpace(b.Key); key != "" {
					btn, holder := ticketSummaryControls(key)
					summaryHolder = holder
					right = append(right, btn)
				}
			}

			// titleWidget als Mitte (nicht als "left"!) des Border-Layouts: nur so
			// bekommt es beim Layout die tatsaechlich verfuegbare Breite zugewiesen
			// und kann seinen (vollstaendigen, s. jarvisTitleWidget) Text passend
			// umbrechen. Als "left" wuerde Fyne dem Titel immer nur seine eigene
			// Minimalbreite geben - das hatte zuvor entweder eine endlose Zeile
			// (Fenstersteiler blockiert) oder eine Kuerzung mit "…" zur Folge.
			// Ohne rechte Objekte steht der Titel allein.
			var headerRow fyne.CanvasObject = titleWidget
			if len(right) > 0 {
				headerRow = container.NewBorder(nil, nil, nil, container.NewHBox(right...), titleWidget)
			}
			// Bewusst KEIN RichTextFromMarkdown: der Snippet-Text vom Server ist
			// freier Fliesstext, kein verlaesslich sauberes Markdown - einzelne
			// Zeichenfolgen wurden faelschlich als Ueberschrift interpretiert und
			// dadurch riesig dargestellt. RichText mit direkt gebauten Text-
			// Segmenten (ohne Markdown-Parser) ist genauso sicher wie das fruehere
			// Label und erlaubt zusaetzlich die Markierung der Filter-Treffer.
			//
			// Lange Inhalte starten (wie bei den IBS-Karten) auf
			// cardCollapseLines Zeilen eingeklappt, "mehr"/"weniger" schaltet
			// um; der Volltext-Filter durchsucht immer den KOMPLETTEN Text,
			// markiert aber im jeweils angezeigten Zustand (raw wird beim
			// Umschalten nachgezogen).
			full := b.Summary
			short, truncated := collapseText(full, cardCollapseLines, cardCollapseChars)
			display := full
			if truncated {
				display = short + " …"
			}
			snippet := widget.NewRichText(highlightedSegments(display, "")...)
			snippet.Wrapping = fyne.TextWrapWord

			cardContent := container.NewVBox(headerRow, snippet)
			if truncated {
				idx := len(cards) // Index des unten angehaengten filterCard-Eintrags
				expanded := false
				toggle := widget.NewButton(T("mehr"), nil)
				toggle.Importance = widget.LowImportance
				toggle.OnTapped = func() {
					expanded = !expanded
					disp := short + " …"
					if expanded {
						disp = full
						toggle.SetText(T("weniger"))
					} else {
						toggle.SetText(T("mehr"))
					}
					ql := strings.ToLower(strings.TrimSpace(filterSrc.Text))
					snippet.Segments = highlightedSegments(disp, ql)
					snippet.Refresh()
					cards[idx].raw = disp
					cards[idx].hlQuery = ql
				}
				cardContent.Add(container.NewHBox(toggle)) // Toggle linksbuendig unter dem Text
			}
			// Quelle-Zeile nur fuer Nicht-Tickets (v.a. WISSEN-Treffer, deren oft
			// relative Quell-Links sonst gar nicht sichtbar waeren, s.
			// jarvisSourceRow/resolveJarvisLink).
			if !isTicket {
				if srcRow := jarvisSourceRow(b, win); srcRow != nil {
					cardContent.Add(srcRow)
				}
			}
			// KI-Zusammenfassung erscheint unterhalb (ein-/ausklappbar via Header-Button).
			if summaryHolder != nil {
				cardContent.Add(summaryHolder)
			}
			card := kiCard(cardContent)
			// Durchsuchbarer Text der Karte fuer den Volltext-Filter oben.
			// open: Nicht-Tickets (WISSEN/Confluence) gelten immer als "offen",
			// damit die Checkbox sie nie ausblendet; Tickets nach Server-Meldung.
			cards = append(cards, filterCard{
				obj:     card,
				text:    strings.ToLower(strings.Join([]string{title, b.Summary, b.Key, b.Source, b.SourceLabel}, " ")),
				open:    !crmView || !isTicket || openKeys[b.Key],
				snippet: snippet,
				raw:     display,
			})
			resultsBox.Add(card)
		}

		// Kundenverwaltungs-Tickets (IBS) als Karten in DERSELBEN Liste (nur
		// Anruf-Ansicht). Offene zuerst, dann beendete (innerhalb der Gruppen
		// Server-Reihenfolge); das Anzeige-Limit config.JarvisIBSLimit
		// ("Kundenverwaltung-Tickets" in den erweiterten Einstellungen) deckelt
		// die Kartenzahl - die Status-Zeile oben nennt stets die Gesamtzahl.
		if crmView && ibsLast != nil {
			if ibsLast.errMsg != "" {
				errText := canvas.NewText(T("Kundenverwaltung: ")+ibsLast.errMsg, kiAccent)
				errText.TextStyle = fyne.TextStyle{Bold: true}
				resultsBox.Add(errText)
			}
			ordered := make([]ibsTicket, 0, len(ibsLast.tickets))
			for _, tk := range ibsLast.tickets {
				if tk.Open {
					ordered = append(ordered, tk)
				}
			}
			for _, tk := range ibsLast.tickets {
				if !tk.Open {
					ordered = append(ordered, tk)
				}
			}
			limit := config.JarvisIBSLimit
			if limit <= 0 {
				limit = 10
			}
			if len(ordered) > limit {
				Log(fmt.Sprintf("IBS: Anzeige auf %d von %d Tickets begrenzt", limit, len(ordered)))
				ordered = ordered[:limit]
			}
			statusBg := color.NRGBA{R: 0xEA, G: 0xEA, B: 0xEA, A: 255}
			statusFg := color.NRGBA{R: 0x44, G: 0x44, B: 0x44, A: 255}
			for _, tk := range ordered {
				// Kopfzeile EINZEILIG: links nur die Event-Nummer "#nnnnnn"
				// (Erstellzeit/Bearbeiter erscheinen bewusst nicht; die Nummer
				// wird spaeter ein Link auf die API-Funktion "Event anzeigen" -
				// Endpunkt folgt). Rechts IBS-/Status-Pill und
				// "KI-Zusammenfassung". Darunter - wie bei den Jira-Karten -
				// der Beschreibungstext (bodySlot); die KI-Zusammenfassung
				// ERSETZT ihn beim Klick (zweiter Klick stellt ihn wieder her).
				head := T("(ohne Titel)")
				if tk.Key != "" {
					head = "#" + tk.Key
				}
				titleLbl := widget.NewLabelWithStyle(head, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
				right := []fyne.CanvasObject{kiPill("IBS", kiAccentSoft, kiAccent)}
				if tk.Status != "" {
					right = append(right, kiPill(tk.Status, statusBg, statusFg))
				}
				// Beschreibungstext: lange Inhalte standardmaessig auf 6 Zeilen
				// gekuerzt und per "mehr"/"weniger" auf-/zuklappbar (gleiches
				// Muster wie die KI-Gesamtzusammenfassung). Der Volltext-Filter
				// durchsucht immer den KOMPLETTEN Text, markiert aber im jeweils
				// angezeigten Zustand (raw wird beim Umschalten nachgezogen).
				full := tk.Text
				short, truncated := collapseText(full, cardCollapseLines, cardCollapseChars)
				display := full
				if truncated {
					display = short + " …"
				}
				snippet := widget.NewRichText(highlightedSegments(display, "")...)
				snippet.Wrapping = fyne.TextWrapWord
				// textBody buendelt Text + Toggle: die KI-Zusammenfassung ersetzt
				// beides (bodySlot) und stellt beides wieder her.
				textBody := container.NewVBox(snippet)
				bodySlot := container.NewVBox(textBody)
				if strings.TrimSpace(tk.Text) != "" {
					right = append(right, ibsSummaryControls(tk, textBody, bodySlot))
				}
				headerRow := container.NewBorder(nil, nil, nil, container.NewHBox(right...), titleLbl)
				card := kiCard(container.NewVBox(headerRow, bodySlot))
				cards = append(cards, filterCard{
					obj:     card,
					text:    strings.ToLower(strings.Join([]string{tk.Key, tk.Created, tk.User, tk.Status, tk.Text}, " ")),
					open:    tk.Open,
					snippet: snippet,
					raw:     display,
					src:     "ibs",
				})
				if truncated {
					idx := len(cards) - 1
					expanded := false
					toggle := widget.NewButton(T("mehr"), nil)
					toggle.Importance = widget.LowImportance
					toggle.OnTapped = func() {
						expanded = !expanded
						disp := short + " …"
						if expanded {
							disp = full
							toggle.SetText(T("weniger"))
						} else {
							toggle.SetText(T("mehr"))
						}
						// Anzeige sofort mit dem aktuellen Filtertext markieren und
						// den Karten-Zustand nachziehen, damit die naechste
						// Filter-Eingabe (applyFilters) den Klappzustand behaelt.
						ql := strings.ToLower(strings.TrimSpace(filterSrc.Text))
						snippet.Segments = highlightedSegments(disp, ql)
						snippet.Refresh()
						cards[idx].raw = disp
						cards[idx].hlQuery = ql
					}
					textBody.Add(container.NewHBox(toggle)) // Toggle linksbuendig unter dem Text
				}
				resultsBox.Add(card)
			}
		}

		if len(cards) == 0 && strings.TrimSpace(res.AISummary) == "" {
			resultsBox.Add(widget.NewLabel(T("Keine Treffer.")))
		}
		// Anfangszustand der Filter anwenden (blendet in der CRM-Liste die
		// nicht-offenen Tickets aus); ruft auch resultsBox.Refresh() auf.
		applyFilters()
	}

	// Anzeige-Hooks der IBS-Abfrage (Aufruf aus ibs_client.go/webhook.go via
	// fyne.Do). clearIBSTickets laeuft beim Start jedes NEUEN Anrufs: haengt
	// noch eine Anruf-Ansicht des vorherigen Anrufs offen, verschwinden deren
	// IBS-Karten (die Jira-Seite raeumt setCustomerField/clearResults ab).
	clearIBSTickets = func() {
		clearIBSSummaryCache()
		if ibsLast == nil {
			return
		}
		ibsLast = nil
		if crmLast == nil {
			return
		}
		if crmLast.res == nil {
			// Die Ansicht zeigte NUR IBS-Tickets: ohne sie bleibt nichts -
			// komplett schliessen statt eine leere Liste zu zeigen.
			clearResults()
			return
		}
		skipCallReset = true
		renderResults(crmLast.query, crmLast.res, crmLast.duration, crmLast.openKeys)
	}
	showIBSTickets = func(label string, tickets []ibsTicket, errMsg string) {
		ibsLast = &ibsState{label: label, tickets: tickets, errMsg: errMsg}
		if crmLast != nil {
			// Jira-Seite steht schon: Anruf-Ansicht mit beiden Quellen neu
			// rendern, Bedienelemente unangetastet lassen.
			skipCallReset = true
			renderResults(crmLast.query, crmLast.res, crmLast.duration, crmLast.openKeys)
			return
		}
		// Noch keine Jira-Ticketliste (keine CRM gefunden oder Suche noch
		// unterwegs): Anruf-Ansicht nur mit Kundenverwaltungs-Tickets oeffnen.
		renderResults(label, nil, 0, map[string]bool{})
	}

	renderError := func(err error) {
		progress.Hide()
		resultsBox.RemoveAll()
		errText := canvas.NewText(T("Fehler: ")+err.Error(), kiAccent)
		errText.TextStyle = fyne.TextStyle{Bold: true}
		resultsBox.Add(errText)
		resultsBox.Refresh()
	}

	// --- Such-Karte: Eingabe + Button ---
	// queryEntry ist der Suchtext fuer die Jarvis-Anfrage - eine reine
	// Sitzungseingabe ohne jede Verbindung zu einem in "Einstellungen"
	// hinterlegten Prompt (LLM-Anfragen und Suchtext sind unabhaengige Dinge).
	queryEntry := widget.NewEntry()
	trPlaceholder(queryEntry, "Suchtext, z. B. \"Drucker im 2. OG offline\"")

	// "Jira Tickets" und "offene Jira Tickets" schliessen sich gegenseitig aus
	// (maximal eins von beiden aktiv, oder keins). Beide Checks muessen vor dem
	// initialen SetChecked existieren, da SetChecked den jeweils anderen ueber
	// OnChanged bereits referenziert. Zustand wird in config.json gemerkt.
	var jiraCheck, openOnlyCheck *widget.Check
	jiraCheck = trCheck("Jira Tickets", func(checked bool) {
		if checked {
			openOnlyCheck.SetChecked(false)
		}
		config.JarvisJira = checked
		saveConfigDebounced()
	})
	openOnlyCheck = trCheck("offene Jira Tickets", func(checked bool) {
		if checked {
			jiraCheck.SetChecked(false)
		}
		config.JarvisOpenOnly = checked
		saveConfigDebounced()
	})
	jiraCheck.SetChecked(config.JarvisJira)
	openOnlyCheck.SetChecked(config.JarvisOpenOnly)
	confluenceCheck := trCheck("Confluence", func(b bool) {
		config.JarvisConfluence = b
		saveConfigDebounced()
	})
	confluenceCheck.SetChecked(config.JarvisConfluence)
	ragCheck := trCheck("Wissen", func(b bool) {
		config.JarvisRAG = b
		saveConfigDebounced()
	})
	ragCheck.SetChecked(config.JarvisRAG)
	// "IBS Tickets" (Kundenverwaltungs-API): nur klickbar, wenn in den
	// Einstellungen URL und API-Key der Kundenverwaltung hinterlegt sind.
	// Die eigentliche API-Abfrage wird später hinterlegt. refreshIBSCheck
	// rufen die beiden Einstellungs-Felder (main.go) bei jeder Änderung auf.
	ibsCheck := trCheck("IBS Tickets", func(b bool) {
		config.JarvisIBS = b
		saveConfigDebounced()
	})
	ibsCheck.SetChecked(config.JarvisIBS)
	refreshIBSCheck = func() {
		if strings.TrimSpace(config.IBS.Url) != "" && strings.TrimSpace(config.IBS.ApiKey) != "" {
			ibsCheck.Enable()
			return
		}
		if ibsCheck.Checked {
			ibsCheck.SetChecked(false) // setzt via OnChanged auch config.JarvisIBS zurück
		}
		ibsCheck.Disable()
	}
	refreshIBSCheck()
	aiCheck := trCheck("KI-Gesamtzusammenfassung", func(b bool) {
		config.JarvisAISummary = b
		saveConfigDebounced()
	})
	aiCheck.SetChecked(config.JarvisAISummary)

	// Erweiterte Einstellungen (Jira-Limit/Summary-Zeilen) - aufklappbar.
	// Die Sprache (DE/EN) wird zentral in "Einstellungen" gepflegt (config.JarvisLang).
	jiraLimitEntry := widget.NewEntry()
	jiraLimitEntry.SetText(strconv.Itoa(config.JarvisJiraLimit))
	jiraLimitEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			config.JarvisJiraLimit = n
			saveConfigDebounced()
		}
	}
	summaryLinesEntry := widget.NewEntry()
	summaryLinesEntry.SetText(strconv.Itoa(config.JarvisSummaryLines))
	summaryLinesEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			config.JarvisSummaryLines = n
			saveConfigDebounced()
		}
	}

	// Anzeige-Limit der Kundenverwaltungs-Tickets (IBS-Bereich). Eigene Zeile
	// statt Eintrag im compactFormLayout oben: sie ist nur sichtbar, wenn die
	// Kundenverwaltungs-API konfiguriert ist (s. refreshIBSCheck), und laesst
	// sich als eigener Container sauber ein-/ausblenden.
	ibsLimitEntry := widget.NewEntry()
	ibsLimitEntry.SetText(strconv.Itoa(config.JarvisIBSLimit))
	ibsLimitEntry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			config.JarvisIBSLimit = n
			saveConfigDebounced()
			// Limit wirkt sofort auf eine offene Anruf-Ansicht; die laufende
			// Nutzer-Filterung bleibt stehen (skipCallReset).
			if crmLast != nil {
				skipCallReset = true
				renderResults(crmLast.query, crmLast.res, crmLast.duration, crmLast.openKeys)
			}
		}
	}
	ibsLimitRow := container.New(&compactFormLayout{},
		trLabel("Kundenverwaltung-Tickets:"), ibsLimitEntry,
	)

	advanced := container.NewVBox(
		widget.NewSeparator(),
		container.New(&compactFormLayout{},
			trLabel("Jira-Limit:"), jiraLimitEntry,
			trLabel("Summary-Zeilen:"), summaryLinesEntry,
		),
		ibsLimitRow,
	)

	// Hide()/Show() allein loest kein Neu-Layout des umgebenden VBox aus (Fyne
	// refresht nur das versteckte/gezeigte Objekt selbst) - refreshSearchCard
	// wird unten gesetzt, sobald der VBox existiert, und erzwingt das Neu-Layout.
	var refreshSearchCard func()
	advancedToggle := newCollapsibleSection("Erweiterte Einstellungen", advanced, config.JarvisAdvancedExpanded, func(exp bool) {
		config.JarvisAdvancedExpanded = exp
		saveConfigDebounced()
	}, func() {
		if refreshSearchCard != nil {
			refreshSearchCard()
		}
	})

	var searchBtn *widget.Button
	runSearch := func() {
		text := strings.TrimSpace(queryEntry.Text)
		if text == "" {
			showErr(fmt.Errorf(T("Bitte einen Suchtext eingeben.")), win)
			return
		}
		jiraLimit, _ := strconv.Atoi(strings.TrimSpace(jiraLimitEntry.Text))
		if jiraLimit <= 0 {
			jiraLimit = 10
		}
		summaryLines, _ := strconv.Atoi(strings.TrimSpace(summaryLinesEntry.Text))
		if summaryLines <= 0 {
			summaryLines = 5
		}
		lang := config.JarvisLang
		if lang == "" {
			lang = "de"
		}
		req := jarvisQueryRequest{
			Text:         text,
			RAG:          ragCheck.Checked,
			Jira:         jiraCheck.Checked,
			Confluence:   confluenceCheck.Checked,
			AI:           aiCheck.Checked,
			OpenOnly:     openOnlyCheck.Checked,
			JiraLimit:    jiraLimit,
			SummaryLines: summaryLines,
			Lang:         lang,
			// In "Einstellungen" hinterlegter Prompt (config.JarvisSearchQuery) -
			// unabhaengig vom Suchtext. Erscheint dadurch auch im Debug-Fenster.
			Prompt: strings.TrimSpace(config.JarvisSearchQuery),
		}

		debugPreviewAndConfirm(win, "Anfrage: Suchen (/api/support/query)", jarvisRequestPreview(req), func() {
			searchBtn.Disable()
			progress.Show()
			start := time.Now()

			go func() {
				res, err := jarvisQuery(req)
				duration := time.Since(start)
				fyne.Do(func() {
					progress.Hide()
					searchBtn.Enable()
					if err != nil {
						renderError(err)
						return
					}
					renderResults(text, res, duration, nil)
				})
			}()
		})
	}

	searchBtn = trButton("Suchen", runSearch)
	searchBtn.Importance = widget.HighImportance
	queryEntry.OnSubmitted = func(string) { runSearch() }

	searchRow := container.NewBorder(nil, nil, nil, searchBtn, queryEntry)

	// advancedSection buendelt Toggle + Inhalt der erweiterten Einstellungen als
	// EINE verschiebbare Einheit: sie sitzt normalerweise in der Such-Karte
	// (cardAdvSlot), wandert aber in der ausgedehnten CRM-Ticketliste unter deren
	// Kopfzeile (crmAdvSlot, s.u.), damit Jira-Limit/Summary-Zeilen dort sichtbar
	// und bedienbar bleiben. Ein Widget kann nur EINEN Parent haben, daher wird
	// dieselbe Instanz zwischen den beiden Slots umgehaengt statt dupliziert
	// (zwei Instanzen wuerden sich beim Tippen nicht synchronisieren).
	advancedSection := container.NewVBox(advancedToggle, advanced)
	cardAdvSlot := container.NewVBox(advancedSection)

	searchCardBox := container.NewVBox(
		searchRow,
		container.NewHBox(jiraCheck, openOnlyCheck, confluenceCheck, ragCheck, ibsCheck),
		aiCheck,
		cardAdvSlot,
		progress,
	)
	searchCard := kiCard(searchCardBox)
	refreshSearchCard = func() {
		searchCardBox.Refresh()
		searchCardBox.Resize(searchCardBox.MinSize())
		searchCard.Refresh()
	}

	top := container.NewVBox(
		trLabel("Portal Suche"),
		searchCard,
	)

	// Kopfzeile der ausgedehnten CRM-Ticketliste: Titel links, rechts die
	// Quellen-Haekchen/Radio "offen|alle", das Volltext-Filterfeld (dauerhaft, von
	// renderResults pro Rendern neu verdrahtet, s.o.) und der ✕-Button. Der ✕
	// schliesst die Liste komplett (clearResults klappt ueber setCRMListExpanded
	// auch die Ansicht wieder ein, s.o.), wodurch der Suchbereich
	// ("Portal Suche" + Such-Karte) wieder sichtbar wird.
	// ✕ schliesst die Anruf-Ansicht komplett (beide Quellen, s. closeCallView).
	crmCloseBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), closeCallView)
	crmHeader := container.NewBorder(nil, nil, nil,
		container.NewHBox(crmJiraCheck, crmIBSCheck, crmOpenRadio, newFilterWrap(crmFilterEntry), crmCloseBtn),
		trLabelStyle("Tickets zur CRM", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))

	// Sichtbarkeit der Kundenverwaltungs-Bedienelemente (Quellen-Haekchen
	// "Kundenv." in der Kopfzeile, Limit-Zeile in den erweiterten
	// Einstellungen): nur wenn URL + API-Key hinterlegt sind. refreshIBSCheck
	// (oben bereits fuer die Checkbox "IBS Tickets" zustaendig) wird darum
	// erweitert - die Einstellungs-Felder in main.go rufen es bei jeder
	// Aenderung auf. Hide()/Show() allein loest kein Neu-Layout der Umgebung
	// aus (s.o.), daher Refresh der betroffenen Container.
	baseRefreshIBSCheck := refreshIBSCheck
	refreshIBSCheck = func() {
		baseRefreshIBSCheck()
		if ibsConfigured() {
			crmIBSCheck.Show()
			ibsLimitRow.Show()
			if ibsIDBox != nil {
				ibsIDBox.Show()
			}
		} else {
			crmIBSCheck.Hide()
			ibsLimitRow.Hide()
			if ibsIDBox != nil {
				ibsIDBox.Hide()
			}
		}
		if refreshSearchCard != nil {
			refreshSearchCard()
		}
		crmHeader.Refresh()
		if ibsIDBox != nil {
			ibsIDBox.Refresh()
		}
	}
	refreshIBSCheck()

	// crmAdvSlot nimmt in der ausgedehnten Ansicht die advancedSection auf
	// ("Erweiterte Einstellungen" bleiben so sichtbar und bedienbar).
	crmAdvSlot := container.NewVBox()
	crmTop := container.NewVBox(crmHeader, crmAdvSlot)
	crmTop.Hide()

	// topWrap traegt beide Kopf-Varianten; VBox ueberspringt versteckte Kinder
	// beim Layout, es ist also immer nur eine sichtbar.
	topWrap := container.NewVBox(top, crmTop)
	panel := container.NewBorder(topWrap, nil, nil, nil, resultsArea)

	// refreshHeaderArea stoesst das Neu-Layout des Kopfbereichs an -
	// Hide()/Show()/Umhaengen allein loest kein Neu-Layout der Umgebung aus
	// (s.o. bei refreshSearchCard).
	refreshHeaderArea := func() {
		topWrap.Refresh()
		panel.Refresh()
	}

	setCRMListExpanded = func(expanded bool) {
		if expanded {
			top.Hide()
			// advancedSection unter die CRM-Kopfzeile umhaengen (idempotent:
			// Remove eines Nicht-Kindes ist ein No-op, Add nur wenn leer).
			cardAdvSlot.Remove(advancedSection)
			if len(crmAdvSlot.Objects) == 0 {
				crmAdvSlot.Add(advancedSection)
			}
			crmTop.Show()
		} else {
			crmTop.Hide()
			crmAdvSlot.Remove(advancedSection)
			if len(cardAdvSlot.Objects) == 0 {
				cardAdvSlot.Add(advancedSection)
			}
			top.Show()
		}
		refreshHeaderArea()
	}

	// refreshSearchCard (Ein-/Ausklappen der erweiterten Einstellungen) muss
	// auch die ausgedehnte CRM-Ansicht neu layouten, wenn die advancedSection
	// gerade dort haengt.
	baseRefreshSearchCard := refreshSearchCard
	refreshSearchCard = func() {
		baseRefreshSearchCard()
		refreshHeaderArea()
	}

	// searchMatchingTickets: sucht zum erkannten Text passende Jira-Tickets.
	// Manueller Trigger (Button "Suche passende Tickets") und automatischer Zyklus
	// verwenden EXAKT denselben Anfrage-Body; einziger Unterschied ist der
	// Ausloeser (auto=false: mit Debug-Popup + Button-Sperre; auto=true: ohne
	// Popup, mit Ueberlappschutz).
	//
	// Body: als einzige aktive Quelle "jira_all" (rag/confluence/jira_open/ai aus),
	// dazu jira_limit + summary_lines. Der Prompt aus "Einstellungen"
	// (config.JarvisTicketSearchPrompt, "[Textfenster]" durch den erkannten Text
	// ersetzt) wird im Feld prompt mitgeschickt (und ist im Debug-Fenster sichtbar).
	// Der erkannte Text geht zusaetzlich als Suchtext (text) an die API.
	// auto=true: automatischer Zyklus/Trigger (kein Debug-Popup, Ueberlappschutz).
	// crmFallback=true: bei LEEREM erkannten Text ALLE Tickets zur gefundenen CRM
	// laden (Button + Webhook-Trigger), statt abzubrechen; nicht-offene Tickets
	// sind anfangs per Radio "offen" ausgeblendet. Der zyklische
	// Intervall-Scan (crmFallback=false) ueberspringt leeren Text still.
	searchMatchingTickets := func(recognizedText string, trigger *widget.Button, auto bool, crmFallback bool) {
		// Ticketsuche nur, wenn im CRM Feld eine gültige CRM steht
		// (per Webhook/Wiederhol-Button gesetzt, s. webhook.go/hasCRM).
		// Ohne CRM: Automatik still überspringen, manuell mit Hinweis.
		if !hasCRM() {
			if !auto {
				showErr(fmt.Errorf(T("Im CRM Feld steht keine gültige CRM-Nummer. Die Ticketsuche ist erst mit einer CRM möglich.")), win)
			}
			return
		}
		crm := getCurrentCRM()

		jiraLimit := config.JarvisJiraLimit
		if jiraLimit <= 0 {
			jiraLimit = 10
		}
		summaryLines := config.JarvisSummaryLines
		if summaryLines <= 0 {
			summaryLines = 5
		}
		lang := config.JarvisLang
		if lang == "" {
			lang = "de"
		}

		var req jarvisQueryRequest
		text := strings.TrimSpace(recognizedText)
		displayQuery := text
		// reqAll (nur CRM-Ticketliste, sonst nil): ZWEITE Anfrage mit jira_all.
		// Die Liste laedt ALLE Tickets zur CRM: req (jira_open) liefert die
		// offenen - sie bleiben die verlaessliche "offen"-Quelle, denn die
		// Treffer-Blocks der API tragen KEIN Status-Feld -, reqAll ergaenzt die
		// nicht-offenen. Letztere blendet das Radio "offen"/"alle" im
		// Ergebnis clientseitig aus/ein (s. renderResults/openKeys).
		var reqAll *jarvisQueryRequest
		if text == "" {
			// Kein erkannter Text: entweder still überspringen (zyklischer Scan)
			// oder – bei Button/Webhook – alle Tickets zur CRM laden.
			if !crmFallback {
				return
			}
			displayQuery = "Tickets zu " + crm
			req = jarvisQueryRequest{
				Text:         crm,
				OpenOnly:     true, // jira_open (schließt jira_all aus)
				JiraLimit:    jiraLimit,
				SummaryLines: summaryLines,
				Lang:         lang,
			}
			all := req
			all.OpenOnly = false
			all.Jira = true // jira_all
			reqAll = &all
		} else {
			promptTemplate := strings.TrimSpace(config.JarvisTicketSearchPrompt)
			if promptTemplate == "" {
				promptTemplate = defaultTicketSearchPrompt
			}
			prompt := strings.ReplaceAll(promptTemplate, ticketSearchPlaceholder, text)
			req = jarvisQueryRequest{
				Text:         text,
				RAG:          false,
				Jira:         true, // jira_all - einziger aktiver Quell-Key
				Confluence:   false,
				AI:           false,
				OpenOnly:     false,
				JiraLimit:    jiraLimit,
				SummaryLines: summaryLines,
				Lang:         lang,
				Prompt:       prompt,
			}
		}

		run := func() {
			if trigger != nil {
				trigger.Disable()
			}
			progress.Show()
			start := time.Now()

			go func() {
				// CRM-Ticketliste: jira_open und jira_all PARALLEL laden - die
				// Antworten haengen nicht voneinander ab, sequenziell wuerde sich
				// die Wartezeit addieren.
				var allRes *jarvisQueryResponse
				var allErr error
				var wg sync.WaitGroup
				if reqAll != nil {
					wg.Add(1)
					go func() {
						defer wg.Done()
						allRes, allErr = jarvisQuery(*reqAll)
					}()
				}
				res, err := jarvisQuery(req)
				wg.Wait()
				// Offene Keys aus der jira_open-Antwort merken, dann die
				// nicht-offenen Tickets aus jira_all anhaengen (Duplikate und
				// Nicht-Tickets ueberspringen - WISSEN/Confluence-Blocks kaemen
				// sonst doppelt). Reihenfolge: offene zuerst, dann der Rest.
				// openKeys ist durch das Jira-Limit gedeckelt: offene Tickets
				// jenseits des Limits koennten in der jira_all-Liste als "nicht
				// offen" einsortiert werden - die Standard-Ansicht (nur offene)
				// entspricht aber weiterhin exakt der jira_open-Antwort und
				// verliert dadurch nichts.
				var openKeys map[string]bool
				if err == nil && reqAll != nil {
					openKeys = map[string]bool{}
					for _, b := range res.Blocks {
						if strings.EqualFold(b.Source, "JIRA") && b.Key != "" {
							openKeys[b.Key] = true
						}
					}
					if allErr != nil {
						// Nur die ERGAENZUNG ist fehlgeschlagen: offene Tickets
						// trotzdem anzeigen statt Totalausfall mit Fehlermeldung.
						Log("CRM-Ticketliste: jira_all-Ergänzung fehlgeschlagen: " + allErr.Error())
					} else {
						for _, b := range allRes.Blocks {
							if !strings.EqualFold(b.Source, "JIRA") || (b.Key != "" && openKeys[b.Key]) {
								continue
							}
							res.Blocks = append(res.Blocks, b)
						}
					}
				}
				duration := time.Since(start)
				fyne.Do(func() {
					progress.Hide()
					if trigger != nil {
						trigger.Enable()
					}
					if auto {
						autoScanBusy.Store(false)
					}
					// Veraltetes Ergebnis verwerfen: Hat sich die CRM waehrend der
					// Anfrage geaendert (neuer Webhook-Anruf), gehoeren diese
					// Tickets nicht mehr zum angezeigten CRM Feld - ohne den Guard
					// erschiene die Liste der ALTEN CRM unter dem NEUEN Label.
					if reqAll != nil && getCurrentCRM() != crm {
						return
					}
					if err != nil {
						renderError(err)
						return
					}
					renderResults(displayQuery, res, duration, openKeys)
				})
			}()
		}

		if auto {
			// Ueberlappschutz: laeuft noch eine Automatik-Suche, diesen Zyklus
			// auslassen. Kein Debug-Popup im Automatikbetrieb.
			if !autoScanBusy.CompareAndSwap(false, true) {
				return
			}
			run()
			return
		}
		preview := jarvisRequestPreview(req)
		if reqAll != nil {
			preview += "\n\n--- 2. Anfrage (alle Tickets, jira_all) ---\n\n" + jarvisRequestPreview(*reqAll)
		}
		debugPreviewAndConfirm(win, "Anfrage: Passende Tickets (/api/support/query)", preview, run)
	}

	return panel, searchMatchingTickets, clearResults
}
