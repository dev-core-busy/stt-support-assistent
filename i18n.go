package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// ============================================================================
// i18n – App-weite Sprachumschaltung (DE/EN), live ohne Neustart.
//
// Ansatz "Deutsch als Schlüssel": Deutsch ist die Basissprache. Im Code steht
// weiterhin der deutsche Originaltext; T(de) liefert bei aktivem Englisch die
// Übersetzung aus enOverrides, sonst (oder fehlender Übersetzung) den deutschen
// Text selbst. So braucht es keine künstlichen String-Keys und der Code bleibt
// lesbar.
//
// Fyne-Widgets aktualisieren ihren Text nicht von selbst. Darum registriert
// jedes übersetzbare Element über onLangChange eine kleine Aktualisierungs-
// Closure; setLanguage ruft bei jedem Wechsel alle Closures auf (Live-Update).
// Widget-Instanzen bleiben dabei stabil – kein UI-Neuaufbau, kein Verlust von
// Laufzeit-State (Aufnahme, Meter-Referenzen, Audio-Goroutinen).
//
// Die App-Sprache ist config.JarvisLang ("de"/"en") – bislang die Sprache der
// Jarvis-Suche, jetzt zugleich die UI-Sprache (ein Toggle steuert beides). Der
// JSON-Key bleibt "jarvisLang" aus Kompatibilität. Siehe Memory
// [[i18n-todo-de-en-toggle]].
// ============================================================================

// currentLang ist die aktive UI-Sprache ("de"/"en"). Wird beim Start aus
// config.JarvisLang initialisiert (initLang).
var currentLang = "de"

// langChangeCallbacks: Aktualisierungs-Closures aller übersetzbaren Widgets.
// Nur im Fyne-Main-Thread anfassen (Widget-Erstellung und setLanguage laufen
// dort), daher ohne Mutex.
var langChangeCallbacks []func()

// T übersetzt einen deutschen UI-Text in die aktive Sprache. Bei "de" oder
// fehlender Übersetzung wird der deutsche Text unverändert zurückgegeben (er
// ist die Basissprache). Formatstrings mit %-Platzhaltern funktionieren, wenn
// die englische Variante dieselben Platzhalter enthält.
func T(de string) string {
	if currentLang == "en" {
		if en, ok := enOverrides[de]; ok && en != "" {
			return en
		}
	}
	return de
}

// onLangChange registriert eine Closure, die bei jedem Sprachwechsel läuft
// (typisch: Widget-Text neu setzen). Im Fyne-Main-Thread aufrufen.
func onLangChange(cb func()) {
	langChangeCallbacks = append(langChangeCallbacks, cb)
}

// initLang setzt die Startsprache aus der Config (einmalig beim App-Start,
// bevor die UI gebaut wird). Kein Callback-Aufruf – es existiert noch nichts.
func initLang() {
	if config.JarvisLang == "en" {
		currentLang = "en"
	} else {
		currentLang = "de"
		config.JarvisLang = "de" // leeren/ungültigen Wert normalisieren
	}
}

// setLanguage wechselt die aktive Sprache und aktualisiert live alle
// registrierten Widgets. Muss im Fyne-Main-Thread laufen.
func setLanguage(lang string) {
	if lang != "de" && lang != "en" || lang == currentLang {
		return
	}
	currentLang = lang
	config.JarvisLang = lang
	SaveConfig()
	for _, cb := range langChangeCallbacks {
		cb()
	}
}

// ---------------------------------------------------------------------------
// Helfer: erzeugen ein Widget mit übersetztem Text und registrieren zugleich
// dessen Live-Aktualisierung. Für Widgets, die dauerhaft in der UI stehen.
// ---------------------------------------------------------------------------

func trLabel(de string) *widget.Label {
	l := widget.NewLabel(T(de))
	onLangChange(func() { l.SetText(T(de)) })
	return l
}

func trLabelStyle(de string, align fyne.TextAlign, style fyne.TextStyle) *widget.Label {
	l := widget.NewLabelWithStyle(T(de), align, style)
	onLangChange(func() { l.SetText(T(de)) })
	return l
}

func trButton(de string, tapped func()) *widget.Button {
	b := widget.NewButton(T(de), tapped)
	onLangChange(func() { b.SetText(T(de)) })
	return b
}

func trButtonIcon(de string, icon fyne.Resource, tapped func()) *widget.Button {
	b := widget.NewButtonWithIcon(T(de), icon, tapped)
	onLangChange(func() { b.SetText(T(de)) })
	return b
}

func trCheck(de string, changed func(bool)) *widget.Check {
	c := widget.NewCheck(T(de), changed)
	onLangChange(func() {
		c.Text = T(de)
		c.Refresh()
	})
	return c
}

// trPlaceholder setzt einen übersetzten Platzhalter auf ein bestehendes Entry
// und hält ihn bei Sprachwechsel aktuell.
func trPlaceholder(e *widget.Entry, de string) {
	e.SetPlaceHolder(T(de))
	onLangChange(func() { e.SetPlaceHolder(T(de)) })
}

// ---------------------------------------------------------------------------
// tappable – transparenter Klick-Layer über beliebigem Inhalt (z.B. für die
// DE/EN-Segment-Pille, die aus canvas-Objekten besteht und selbst nicht
// klickbar ist).
// ---------------------------------------------------------------------------

type tappable struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
}

func newTappable(content fyne.CanvasObject, onTap func()) *tappable {
	t := &tappable{content: content, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappable) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

func (t *tappable) Tapped(_ *fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

// bindText registriert eine beliebige Setter-Closure für den Sprachwechsel und
// ruft sie einmal sofort auf. Für Fälle ohne passenden Helfer (z.B. canvas.Text
// oder Widgets mit Sonderlogik): bindText(func(s string){ t.Text = s; t.Refresh() }, "Text").
func bindText(set func(string), de string) {
	set(T(de))
	onLangChange(func() { set(T(de)) })
}
