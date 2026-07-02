package main

// webhook.go — Rufnummern-Übergabe per eingehendem HTTP-Webhook.
//
// Ein externer Trigger (z.B. eine Telefonanlage) ruft beim eingehenden Anruf
// eine URL dieser App auf und übergibt die Rufnummer des Anrufers. Die App
// sucht mit der Rufnummer in Jira (über die bestehende Jarvis-Jira-Suche) und
// trägt den Issue-Key des Top-Treffers ins Feld "erkannter Kunde"
// (customerEntry) ein.
//
// Der Server lauscht auf 0.0.0.0:<Port><Pfad> (Port/Pfad in den Einstellungen,
// Abschnitt "Rufnummern Übergabe"; Portvorgabe 5555, Pfadvorgabe "/rufnummer").
// Rufnummer wird sowohl per GET-Query (?number=...) als auch per POST
// (JSON {"number":"..."} oder Formularfeld) akzeptiert.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
)

var (
	webhookMu     sync.Mutex
	webhookServer *http.Server
)

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

	go handleIncomingCaller(number)
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

// handleIncomingCaller sucht mit der Rufnummer in Jira und trägt den Issue-Key
// des Top-Treffers ins Feld "erkannter Kunde" ein. Läuft in einer Goroutine.
func handleIncomingCaller(number string) {
	// Sofort die Rohnummer anzeigen ("wird gesucht"-Zustand), damit der Agent
	// den Anrufer sieht, während die Jira-Suche läuft.
	setCustomerField(number)

	jiraLimit := config.JarvisJiraLimit
	if jiraLimit <= 0 {
		jiraLimit = 10
	}
	lang := config.JarvisLang
	if lang == "" {
		lang = "de"
	}

	// Reine Jira-Suche: keine RAG/Confluence, kein AI/LLM und bewusst KEIN
	// Einstellungs-Prompt — die Rufnummer ist der alleinige Suchtext.
	res, err := jarvisQuery(jarvisQueryRequest{
		Text:      number,
		Jira:      true,
		JiraLimit: jiraLimit,
		Lang:      lang,
	})
	if err != nil {
		Log("Rufnummern-Webhook: Jira-Suche fehlgeschlagen: " + err.Error())
		return
	}

	key := topJiraKey(res)
	if key == "" {
		Log(fmt.Sprintf("Rufnummern-Webhook: kein passendes Jira-Ticket zu %q gefunden", number))
		return
	}
	Log(fmt.Sprintf("Rufnummern-Webhook: Rufnummer %q -> Jira %s", number, key))
	setCustomerField(key)
}

// topJiraKey liefert den Issue-Key des besten Jira-Treffers (die Blocks kommen
// bereits nach Score sortiert vom Server).
func topJiraKey(res *jarvisQueryResponse) string {
	if res == nil {
		return ""
	}
	for _, b := range res.Blocks {
		if strings.EqualFold(b.Source, "jira") && strings.TrimSpace(b.Key) != "" {
			return strings.TrimSpace(b.Key)
		}
	}
	for _, b := range res.Blocks {
		if strings.TrimSpace(b.Key) != "" {
			return strings.TrimSpace(b.Key)
		}
	}
	return ""
}

// setCustomerField setzt den Text des Felds "erkannter Kunde" thread-sicher
// über den Fyne-Main-Loop.
func setCustomerField(text string) {
	if customerEntry == nil {
		return
	}
	fyne.Do(func() { customerEntry.SetText(text) })
}
