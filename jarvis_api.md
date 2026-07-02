
Der Support-Assistent ist auch ohne Web-Oberfläche per REST erreichbar – z. B. aus Ticketsystemen, Skripten oder anderen Anwendungen. Basis-URL ist dein Jarvis-Host (https://DEIN-JARVIS-HOST). Alle Bodies sind JSON (Content-Type: application/json).

Authentifizierung
Angemeldeter Benutzer (Browser/Sitzung): Header Authorization: Bearer <token>
Externe Anwendung: Header X-API-Key: <API-Key> – API-Keys werden in den Einstellungen verwaltet. /query, /summarize und /knowledge/file_raw akzeptieren API-Keys (per Test bestätigt: file_raw liefert mit gültigem X-API-Key HTTP 200, ohne Auth HTTP 401); die übrigen Endpunkte erfordern ein Benutzer-Token.
Endpunkte
Methode	Pfad	Zweck	Auth
POST	/api/support/query	Suche über RAG + Jira + Confluence, optional KI-Zusammenfassung	Token oder API-Key
POST	/api/support/summarize	KI-Zusammenfassung eines einzelnen Jira-Tickets	Token oder API-Key
GET	/api/support/status	Aktive Quellen + Maxima (Zeilen, Ticketanzahl)	Token
GET/POST	/api/support/instructions	Persönliche Anweisungen lesen/speichern (Markdown)	Token
GET/DELETE	/api/support/history	Suchverlauf des Benutzers lesen/löschen	Token
GET	/api/knowledge/file_raw?path=…	Rohdatei einer WISSEN-Quelle abrufen (z. B. PDF); path ist URL-kodiert	Token oder API-Key
Beispiel 1 – Suche mit KI-Zusammenfassung
Durchsucht Wissensdatenbank und (nur offene) Jira-Tickets, holt bis zu 10 Tickets und lässt eine KI-Gesamtzusammenfassung erstellen.

curl -sk https://DEIN-JARVIS-HOST/api/support/query \
  -H "X-API-Key: DEIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
        "text": "Drucker im 2. OG offline",
        "rag": true,
        "jira_all": true,
        "confluence": false,
        "ai": true,
        "jira_open": true,
        "jira_limit": 10,
        "summary_lines": 5,
        "lang": "de"
      }'
Antwort (gekürzt):

{
  "ok": true,
  "query": "Drucker im 2. OG offline",
  "ai_summary": "Mögliche Ursache ist ein Netzwerk-Timeout des Druckers …",
  "jira_total": 7,
  "blocks": [
    { "source": "JIRA", "source_label": "Jira", "key": "SUP-1234", "title": "SUP-1234",
      "score": 92, "summary": "Drucker reagiert nicht …", "link": "https://jira.example/browse/SUP-1234" },
    { "source": "WISSEN", "source_label": "dc-Pathos technische Referenz",
      "title": "dc-Pathos technische Referenz", "score": 83, "summary": "Schritt 1: …",
      "link": "/api/knowledge/file_raw?path=%2Fmnt%2Fjarvis-kb%2Fshare_1%2Fdcpathos%2Fdc-Pathos%20technische%20Referenz.pdf",
      "doc": "/mnt/jarvis-kb/share_1/dcpathos/dc-Pathos technische Referenz.pdf",
      "doc_name": "dc-Pathos technische Referenz.pdf" }
  ]
}
blocks[].source ist WISSEN, JIRA oder CONFLUENCE; source_label ist ein Anzeige-Label für die Quelle; link ist die URL zur Quelle; score ist die Relevanz in %. Felder: text (Pflicht), rag/jira_all/confluence/ai/jira_open (bool), jira_limit (1…Maximum), summary_lines, lang (de/en), prompt (optional).

prompt (optional): Eine zusätzliche Anweisung an die LLM, die der KI-Gesamtzusammenfassung vorangestellt wird (getrennt vom Suchtext text). Wird per Test bestätigt auch mit X-API-Key wirksam ausgewertet (Beispiel: prompt "Antworte nur mit dem Wort PROMPTWIRKT" → ai_summary = "PROMPTWIRKT"). Leer/weggelassen = Standardverhalten.

Zusätzliche Block-Felder bei WISSEN-Treffern (nicht in jedem Block vorhanden):
- doc – serverseitiger Dateipfad der Quelle (z. B. /mnt/jarvis-kb/…/datei.pdf)
- doc_name – reiner Dateiname der Quelle (z. B. datei.pdf)

Hinweise zu blocks[].link (wichtig, weicht vom vereinfachten Beispiel oben ab):
- Der Link ist bei WISSEN-Treffern häufig RELATIV (beginnt mit „/“, z. B. /api/knowledge/file_raw?path=…) und hat dann kein http/https-Schema. Solche Links müssen gegen die Basis-URL des Jarvis-Hosts aufgelöst werden (Basis-URL + Link), bevor sie abgerufen werden können.
- Manche Links sind absolut (z. B. Confluence-Seiten, externe Webseiten) und werden unverändert verwendet.
- Server-eigene Links – insbesondere /api/knowledge/file_raw – erfordern zwingend die Authentifizierung (Header X-API-Key bzw. Bearer-Token). Ein Abruf ohne Auth liefert HTTP 401. Diese Dateien lassen sich daher NICHT als reiner Browser-Link öffnen, sondern müssen per HTTP-GET mit gesetztem Auth-Header geladen werden.

Beispiel – Wissensdatei mit API-Key laden (liefert die Rohdatei, z. B. application/pdf):

curl -sk https://DEIN-JARVIS-HOST/api/knowledge/file_raw?path=%2Fmnt%2Fjarvis-kb%2F… \
  -H "X-API-Key: DEIN_API_KEY" \
  -o quelle.pdf

Beispiel 2 – Einzelnes Ticket zusammenfassen
Lädt ein konkretes Jira-Ticket (Beschreibung + Kommentare) und fasst es in wenigen Sätzen zusammen.

curl -sk https://DEIN-JARVIS-HOST/api/support/summarize \
  -H "X-API-Key: DEIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{ "source": "JIRA", "key": "SUP-1234", "lang": "de" }'
Antwort:

{
  "ok": true,
  "key": "SUP-1234",
  "summary": "Das Ticket beschreibt einen Druckerausfall im 2. OG …"
}
Hinweise: -k akzeptiert das selbstsignierte Zertifikat (für Tests). Fehler liefern { "ok": false, "error": "…" } mit Status 401 (Auth), 403 (Skill inaktiv) oder 400 (ungültige Eingabe).