# Kundenverwaltung REST API – Dokumentation

Server betrieben durch die Java-Swing-Applikation `kundenverwaltung.jar`.  
Eingebetteter HTTP-Server (`com.sun.net.httpserver.HttpServer`) mit optionalem HTTPS.

## Basis

| Property | Wert |
|---|---|
| **Default-Port** | `8080` (HTTP) oder `8443` (HTTPS) |
| **Standard-URL** | `http://<IP>:8080` bzw. `https://<IP>:8443` |
| **Authentifizierung** | `X-API-Key`-Header ODER `Authorization: Bearer <key>`-Header ODER Query-Parameter `api_key=<key>` |
| **Response-Charset** | UTF-8 |
| **CORS** | `Access-Control-Allow-Origin: *` auf allen Responses |
| **SSL-Zertifikat** | `kundenverwaltung.jks` (JKS, Passwort `2simple4u`), CN=`Kundenverwaltung, O=Nexus AG, L=Otterberg, C=DE` |
| **Konfiguration** | Über das `SetupPanel` → Tab `REST Server` in der GUI (Port, Start-Typ `REST_SERVER_START_TYPE`, API-Key) |
| **Konfigurations-Klasse** | `Properties.getRestServerApiKey()`, `Properties.getRestServerPort()` |

### Authentifizierung

Falls ein API-Key konfiguriert ist, prüft der Server **drei** Stellen (in dieser Reihenfolge):

1. Request-Header `X-API-Key`
2. Request-Header `Authorization: Bearer <key>`
3. Query-Parameter `api_key=<key>`

> Der `/help`-Endpunkt erfordert **keine** Authentifizierung.  
> Der `/externalPhoneCall`-Endpunkt hat einen leeren API-Key (`validApiKey = ""`), erfordert also **keine** Authentifizierung.

### Fehlercodes

| HTTP-Status | Bedeutung |
|---|---|
| `200 OK` | Erfolg |
| `400 Bad Request` | Falscher Content-Type, ungültiges JSON, unbekannter Endpoint |
| `405 Method Not Allowed` | Falsche HTTP-Methode |
| `401 Unauthorized` | Fehlender oder ungültiger API-Key |
| `500 Internal Server Error` | Interne Exception |

---

## Endpunkte

### 1. `GET /help`

**Ohne Authentifizierung.** Gibt eine HTML-Seite mit einer Übersicht aller verfügbaren Endpunkte und einem JSON-Beispiel zurück.

#### Anfrage

```bash
curl http://localhost:8080/help
```

#### Antwort

`Content-Type: text/html; charset=UTF-8`

Siehe `HelpHandler.java:27` – generiert eine HTML-Seite mit Links zu allen Endpunkten und einem JSON-Beispiel.

#### Beispiel

```bash
curl http://localhost:8080/help
```

---

### 2. `POST /va/ad/getByNumber`

**Voice-Assistant-Adresse.** Sucht Kundendatensätze nach Telefonnummer. Wird typischerweise von einem Voice-Assistant-Dienst (Retell AI, Vapi) aufgerufen, um beim Eingangsanruf die anrufende Nummer mit der Datenbank abzugleichen.

| Eigenschaft | Wert |
|---|---|
| **Methode** | `POST` |
| **Content-Type** | `application/json` (Pflicht) |
| **Authentifizierung** | Ja (falls API-Key konfiguriert) |

#### Anfrage – Body

JSON-Objekt, das die anrufende Telefonnummer enthält. Der Server versucht drei Pfade (absteigende Priorität):

1. `args.call_inbound.from_number` (Retell AI)
2. `call_inbound.from_number` (Vapi)
3. `from_number` (Fallback)

##### Beispiel – Retell AI Inbound Call Webhook

```bash
curl -X POST http://localhost:8080/va/ad/getByNumber \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dein-api-key" \
  -d '{
    "event": "call_inbound",
    "call_inbound": {
      "agent_id": "agent_12345",
      "from_number": "+4963013890015",
      "to_number": "+12137771235"
    }
  }'
```

##### Beispiel – Vapi Stil

```bash
curl -X POST http://localhost:8080/va/ad/getByNumber \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer dein-api-key" \
  -d '{
    "event": "call_inbound",
    "from_number": "+491701234567"
  }'
```

##### Beispiel – Minimal (direktes from_number)

```bash
curl -X POST http://localhost:8080/va/ad/getByNumber \
  -H "Content-Type: application/json" \
  -d '{
    "from_number": "+491701234567"
  }'
```

#### Antwort – Treffer

`Content-Type: application/json; charset=UTF-8`

```json
{
  "endpoint": "getByNumber",
  "timestamp": "04.07.2026 14:32:15 (Mi.)",
  "search_phone_number": "+4963013890015",
  "address": [
    {
      "name": "Bender",
      "phone-normal": "+496301123456",
      "phone-mobile": "+491701234567",
      "full-address": "Herr\nBender, Andreas\nD  "
    }
  ]
}
```

| Feld | Typ | Beschreibung |
|---|---|---|
| `endpoint` | String | Immer `"getByNumber"` |
| `timestamp` | String | Server-Zeitstempel `DD.MM.YYYY HH:MM:SS (Wochentag)` |
| `search_phone_number` | String | Die gesuchte Telefonnummer |
| `address[]` | Array | Gefundene Adressdatensätze |
| `address[].address_id` | String | Datenbank ID |
| `address[].name` | String | Nachname |
| `address[].phone-normal` | String | Festnetznummer |
| `address[].phone-mobile` | String | Mobilnummer |
| `address[].full-address` | String | Vollständige Adresse (Anrede, Name, Land-Kürzel) |

#### Antwort – Kein Treffer

```json
{
  "endpoint": "getByNumber",
  "timestamp": "04.07.2026 14:32:15 (Mi.)",
  "search_phone_number": "+491709999999",
  "name": "nothing found (unknown)"
}
```

#### Antwort – Keine Telefonnummer im Body

```json
{
  "endpoint": "getByNumber",
  "timestamp": "04.07.2026 14:32:15 (Mi.)",
  "search_phone_number": "not found"
}
```

---

### 3. `POST /va/ev/getOpenEvents`

**Voice-Assistant-Tickets.** Gibt **offene** (unfertige) Events/Tickets für eine Adresse zurück.

| Eigenschaft | Wert |
|---|---|
| **Methode** | `POST` |
| **Content-Type** | `application/json` (Pflicht) |
| **Authentifizierung** | Ja (falls API-Key konfiguriert) |
| **Filter** | Nur `state != ENDED` (unfertige Events) |

#### Anfrage – Body

Das JSON muss eine `address_id` enthalten:

```bash
curl -X POST http://localhost:8080/va/ev/getOpenEvents \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dein-api-key" \
  -d '{
    "event": "getOpenEvents",
    "request": {
      "address_id": "28530"
    }
  }'
```

#### Antwort – Treffer

```json
{
  "endpoint": "getOpenEvents",
  "timestamp": "04.07.2026 14:35:22 (Mi.)",
  "address_id": "28530",
  "event": [
    {
      "id": 1234,
      "creation_time": "03.07.2026 10:15:00",
      "state_type": "0",
      "state": "NEU",
      "dispatch user": "r.meister",
      "text": "Rechnung für März nicht erhalten"
    },
    {
      "id": 1235,
      "creation_time": "04.07.2026 08:00:00",
      "state_type": "60",
      "state": "Befunde abwarten",
      "dispatch user": "",
      "text": "Software-Update angefragt"
    }
  ]
}
```

| Feld | Typ | Beschreibung |
|---|---|---|
| `endpoint` | String | Immer `"getOpenEvents"` |
| `timestamp` | String | Server-Zeitstempel |
| `address_id` | String | Die angefragte Adresse-ID |
| `event[]` | Array | Liste der offenen Events |
| `event[].id` | Integer | Lokale Event-ID (`localId`) |
| `event[].creation_time` | String | Erstellungszeit |
| `event[].state_type` | Integer | Status-Txpe aus `EVENT_STATE`-Enum |
| `event[].state` | String | Status-Name aus `EVENT_STATE`-Enum |
| `event[].dispatch user` | String | Zugewiesener Benutzer (Loginname) |
| `event[].text` | String | Event-Beschreibungstext |

#### Antwort – Kein Treffer

```json
{
  "endpoint": "getOpenEvents",
  "timestamp": "04.07.2026 14:35:22 (Mi.)",
  "address_id": "28530",
  "address_id": "nothing found (unknown)"
}
```

#### Antwort – Fehlende address_id

```json
{
  "endpoint": "getOpenEvents",
  "timestamp": "04.07.2026 14:35:22 (Mi.)",
  "request.address_id": "not found"
}
```

---

### 4. `POST /va/ev/getEvents`

**Voice-Assistant-Tickets (alle).** Gibt **alle** Events/Tickets (offene + abgeschlossene) für eine Adresse zurück.

| Eigenschaft | Wert |
|---|---|
| **Methode** | `POST` |
| **Content-Type** | `application/json` (Pflicht) |
| **Authentifizierung** | Ja (falls API-Key konfiguriert) |
| **Filter** | Keine – alle Events |

#### Anfrage – Body

Gleich wie `/getOpenEvents`, nur anderes Endpoint:

```bash
curl -X POST http://localhost:8080/va/ev/getEvents \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dein-api-key" \
  -d '{
    "event": "getEvents",
    "request": {
      "address_id": "28530"
    }
  }'
```

#### Antwort – Struktur

Identisch zu `/getOpenEvents` (siehe oben), nur dass das `event[]`-Array auch abgeschlossene Events (`state == "ENDED"`) enthält.

---

### 5. `GET /externalPhoneCall/tel=<nummer>`

**Externer Telefonanruf.** Löst einen simulierten eingehenden Anruf aus der Rufnummer `<nummer>` aus. Die Rufnummer wird per **GET**-Parameter über die URL übergeben. Intern wird die Nummer an das `TsipPanel` weitergeleitet, das die Nummer nach einer Adresse sucht und das gefundene Ergebnis als Tab-Titel anzeigt.

| Eigenschaft | Wert |
|---|---|
| **Methode** | `GET` (einzige erlaubte Methode) |
| **Authentifizierung** | **Nein** (leerer API-Key) |
| **Parameter** | `tel=<telefonnummer>` im URL-Pfad |

#### Anfrage

```bash
curl "http://localhost:8080/externalPhoneCall/tel=+4963013890015"
```

#### Antwort – immer erfolgreich

```json
{
  "status": "success",
  "message": "Phone number processed"
}
```

#### Seiteneffekt

Der Server gibt die Nummer an `TsipPanel.tSIPServerCallback(phoneNumber)` weiter, das:

1. **Interne Kurznummern (3-stellig):** Zeigt einen Direkt-Namen (`ABE`, `CSE`, `ALA`, `frei`, `unbekannt`) und setzt den Tab-Titel des `TSIP`-Tabs.
2. **Unbekannte Nummern (≤ 5 Zeichen):** Zeigt `unbekannt: <nummer>` als Tab-Titel und protokolliert den Anruf.
3. **Vollständige Nummern (> 5 Zeichen):** Sucht nach einer passenden Adresse in der Datenbank (`addressFactory.getMatchingPhoneNumber`). Wenn gefunden, zeigt er Name und Nummer an, aktualisiert den Tab-Titel und protokolliert den Anruf.

#### Beispiele

```bash
# Interne Nummer → Tab-Titel "ABE (100)"
curl "http://localhost:8080/externalPhoneCall/tel=100"

# Unbekannte Nummer → Tab-Titel "unbekannt: +49170"
curl "http://localhost:8080/externalPhoneCall/tel=+49170"

# Vollständige Nummer → Adresse suchen, Tab-Titel = gefundener Name
curl "http://localhost:8080/externalPhoneCall/tel=+4963013890015"
```

---

## Zusammenfassung aller Endpunkte

| Methode | Endpoint | Auth? | Body | Beschreibung |
|---|---|---|---|---|
| `GET` | `/help` | Nein | — | HTML-Hilfe mit allen Endpunkten |
| `POST` | `/va/ad/getByNumber` | Ja | JSON (`from_number`) | Adresse nach Telefonnummer suchen |
| `POST` | `/va/ev/getOpenEvents` | Ja | JSON (`address_id`) | Offene Events/Tickets abrufen |
| `POST` | `/va/ev/getEvents` | Ja | JSON (`address_id`) | Alle Events/Tickets abrufen |
| `GET` | `/externalPhoneCall/tel=<nr>` | Nein | — | Simulierter Anruf auslösen |

## Code-Referenzen

| Klasse | Datei | Rolle |
|---|---|---|
| `RestServer` | `rest/RestServer.java` | Server-Initialisierung, Auth-Prüfung, Response-Helfer |
| `CallHandler` | `rest/CallHandler.java` | `/externalPhoneCall` – leitet an TsipPanel weiter |
| `AddressHandler` | `rest/AddressHandler.java` | `/va/ad/getByNumber` – Adresssuche per Telefon |
| `EventHandler` | `rest/EventHandler.java` | `/va/ev/getOpenEvents`, `/va/ev/getEvents` – Ticket-Abfrage |
| `HelpHandler` | `rest/HelpHandler.java` | `/help` – generiert HTML-Hilfe |
| `KeyStoreManager` | `rest/KeyStoreManager.java` | SSL-Zertifikat-Verwaltung (JKS, RSA 2048, 825 Tage) |
| `ENDPOINT` | `rest/ENDPOINT.java` | Enum aller Endpoint-Namen |
| `ADDRESS_PARAMETER_NAMES` | `rest/ADDRESS_PARAMETER_NAMES.java` | Enum für GET-Parameter-Namen |
| `Properties` | `application/Properties.java` | Liest/schreibt API-Key und Port |
| `RestServerPanel` | `application/RestServerPanel.java` | GUI zum Konfigurieren des REST-Servers |
