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
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// kiAccent ist die Akzentfarbe des KI-Support-Panels (identisch zur globalen
// Primärfarbe in winTheme.Color) - dunkles Rot statt Fyne-Blau.
var kiAccent = color.NRGBA{R: 0xB0, G: 0x1E, B: 0x2C, A: 255}
var kiAccentSoft = color.NRGBA{R: 0xF6, G: 0xDF, B: 0xE1, A: 255}

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
	// (config.JarvisSearchQuery, Feld "Prompt für Suchen"). Wird der KI-
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
				var lbl *widget.Label
				switch {
				case err != nil:
					lbl = widget.NewLabel(T("Fehler: ") + err.Error())
				case sum == "":
					lbl = widget.NewLabel(T("(keine Zusammenfassung erhalten)"))
				default:
					lbl = widget.NewLabel(sum)
				}
				lbl.Wrapping = fyne.TextWrapWord
				holder.Add(lbl)
				holder.Refresh()
			})
		}()
	})
	btn.Importance = widget.HighImportance // rot mit weißer Schrift (Theme-Primärfarbe)
	return btn, holder
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
	resultsScroll := container.NewVScroll(resultsBox)
	resultsBg := canvas.NewRectangle(color.White)
	resultsArea := container.NewStack(resultsBg, resultsScroll)

	// clearResults leert die Ergebnis-/Ticketliste und zeigt wieder den
	// Platzhalter. Wird u.a. aufgerufen, wenn der Inhalt des CRM-Felds manuell
	// geaendert wird (die bisherige Ticketliste gehoert dann zu einer anderen
	// CRM und ist ungueltig). Muss im Fyne-Main-Thread laufen.
	clearResults := func() {
		resultsBox.RemoveAll()
		resultsBox.Add(placeholderLbl)
		resultsBox.Refresh()
	}

	// renderResults befuellt die Ergebnisliste. hideRanking blendet das Score-/
	// Ranking-Badge aus (fuer die Ansicht "Tickets zu einer CRM", wo eine
	// Relevanz-Prozentzahl keinen Sinn ergibt).
	renderResults := func(query string, res *jarvisQueryResponse, duration time.Duration, hideRanking bool) {
		resultsBox.RemoveAll()

		status := widget.NewLabel(fmt.Sprintf(T("Ergebnis für „%s“ (%d Treffer · %d ms)"), query, len(res.Blocks), duration.Milliseconds()))

		// Volltext-Filter der bereits geladenen Treffer (rein clientseitig, KEINE
		// neue Server-Anfrage und keine Verbindung zum Suchtext/Prompt). Jede
		// Treffer-Karte wird mit ihrem durchsuchbaren Text (kleingeschrieben)
		// gesammelt; beim Tippen werden nicht passende Karten ausgeblendet, ein
		// leeres Feld zeigt wieder alle. Die KI-Zusammenfassung bleibt als
		// Gesamtueberblick immer sichtbar.
		type filterCard struct {
			obj  fyne.CanvasObject
			text string
		}
		var cards []filterCard
		filterEntry := widget.NewEntry()
		filterEntry.SetPlaceHolder(T("Treffer filtern …"))
		filterEntry.OnChanged = func(q string) {
			ql := strings.ToLower(strings.TrimSpace(q))
			for _, c := range cards {
				if ql == "" || strings.Contains(c.text, ql) {
					c.obj.Show()
				} else {
					c.obj.Hide()
				}
			}
			resultsBox.Refresh()
		}

		// Filterfeld nur bei vorhandenen Treffern; rechtsbuendig neben dem Status
		// mit fester Breite (Border-rechts wuerde dem Entry sonst nur seine
		// schmale Minimalbreite geben).
		if len(res.Blocks) > 0 {
			filterWrap := container.NewGridWrap(fyne.NewSize(220, filterEntry.MinSize().Height), filterEntry)
			resultsBox.Add(container.NewBorder(nil, nil, nil, filterWrap, status))
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
			body := widget.NewLabel("")
			body.Wrapping = fyne.TextWrapWord

			cardBox := container.NewVBox(header, body)
			// Standardmaessig auf 10 Zeilen kuerzen; laengerer Text wird per
			// "mehr"/"weniger" ein-/ausgeklappt.
			short, truncated := collapseText(summary, 10, 700)
			if truncated {
				expanded := false
				toggle := widget.NewButton(T("mehr"), nil)
				toggle.Importance = widget.LowImportance
				apply := func() {
					if expanded {
						body.SetText(summary)
						toggle.SetText(T("weniger"))
					} else {
						body.SetText(short + " …")
						toggle.SetText(T("mehr"))
					}
				}
				toggle.OnTapped = func() {
					expanded = !expanded
					apply()
				}
				apply()
				cardBox.Add(container.NewHBox(toggle)) // Toggle linksbuendig
			} else {
				body.SetText(summary)
			}
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
			if !hideRanking {
				pills = append(pills, kiPill(fmt.Sprintf("%d%%", b.Score), kiAccent, color.White))
			}

			// Rechts im Header: Badges (falls vorhanden) und - in der "Tickets zu
			// einer CRM"-Liste (hideRanking) - der Button "KI-Zusammenfassung"
			// (rot/weiß). Der zugehoerige Inhalt (summaryHolder) wird unten angehaengt.
			right := append([]fyne.CanvasObject{}, pills...)
			var summaryHolder *fyne.Container
			if hideRanking && isTicket {
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
			// Bewusst Label statt RichTextFromMarkdown: der Snippet-Text vom Server
			// ist freier Fliesstext, kein verlaesslich sauberes Markdown - einzelne
			// Zeichenfolgen wurden faelschlich als Ueberschrift interpretiert und
			// dadurch riesig dargestellt. Label ist sicher, aber ohne Link-Klick.
			snippet := widget.NewLabel(b.Summary)
			snippet.Wrapping = fyne.TextWrapWord

			cardContent := container.NewVBox(headerRow, snippet)
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
			cards = append(cards, filterCard{
				obj:  card,
				text: strings.ToLower(strings.Join([]string{title, b.Summary, b.Key, b.Source, b.SourceLabel}, " ")),
			})
			resultsBox.Add(card)
		}

		if len(res.Blocks) == 0 && strings.TrimSpace(res.AISummary) == "" {
			resultsBox.Add(widget.NewLabel(T("Keine Treffer.")))
		}
		resultsBox.Refresh()
	}

	renderError := func(err error) {
		resultsBox.RemoveAll()
		errText := canvas.NewText(T("Fehler: ")+err.Error(), kiAccent)
		errText.TextStyle = fyne.TextStyle{Bold: true}
		resultsBox.Add(errText)
		resultsBox.Refresh()
	}

	progress := widget.NewProgressBarInfinite()
	progress.Hide()

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

	advanced := container.NewVBox(
		widget.NewSeparator(),
		container.New(&compactFormLayout{},
			trLabel("Jira-Limit:"), jiraLimitEntry,
			trLabel("Summary-Zeilen:"), summaryLinesEntry,
		),
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
					renderResults(text, res, duration, false)
				})
			}()
		})
	}

	searchBtn = trButton("Suchen", runSearch)
	searchBtn.Importance = widget.HighImportance
	queryEntry.OnSubmitted = func(string) { runSearch() }

	searchRow := container.NewBorder(nil, nil, nil, searchBtn, queryEntry)

	searchCardBox := container.NewVBox(
		searchRow,
		container.NewHBox(jiraCheck, openOnlyCheck, confluenceCheck, ragCheck),
		aiCheck,
		advancedToggle,
		advanced,
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

	panel := container.NewBorder(top, nil, nil, nil, resultsArea)

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
	// crmFallback=true: bei LEEREM erkannten Text nach allen OFFENEN Tickets zur
	// gefundenen CRM suchen (Button + Webhook-Trigger), statt abzubrechen. Der
	// zyklische Intervall-Scan (crmFallback=false) ueberspringt leeren Text still.
	searchMatchingTickets := func(recognizedText string, trigger *widget.Button, auto bool, crmFallback bool) {
		// Ticketsuche nur, wenn im CRM Feld eine gültige CRM steht
		// (per Webhook gesetzt ODER manuell eingetippt, s. webhook.go/hasCRM).
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
		// hideRanking: bei "Tickets zu einer CRM" (alle offenen Tickets) ergibt eine
		// Relevanz-Prozentzahl keinen Sinn -> Ranking-Badge ausblenden.
		hideRanking := false
		if text == "" {
			// Kein erkannter Text: entweder still überspringen (zyklischer Scan)
			// oder – bei Button/Webhook – alle OFFENEN Tickets zur CRM suchen.
			if !crmFallback {
				return
			}
			displayQuery = "offene Tickets zu " + crm
			hideRanking = true
			req = jarvisQueryRequest{
				Text:         crm,
				OpenOnly:     true, // jira_open (schließt jira_all aus)
				JiraLimit:    jiraLimit,
				SummaryLines: summaryLines,
				Lang:         lang,
			}
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
				res, err := jarvisQuery(req)
				duration := time.Since(start)
				fyne.Do(func() {
					progress.Hide()
					if trigger != nil {
						trigger.Enable()
					}
					if auto {
						autoScanBusy.Store(false)
					}
					if err != nil {
						renderError(err)
						return
					}
					renderResults(displayQuery, res, duration, hideRanking)
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
		debugPreviewAndConfirm(win, "Anfrage: Passende Tickets (/api/support/query)", jarvisRequestPreview(req), run)
	}

	return panel, searchMatchingTickets, clearResults
}
