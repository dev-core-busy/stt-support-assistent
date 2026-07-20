package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/gen2brain/malgo"
	"image/color"
)

// ========= UI Elements with Tooltips =========
type tooltipButton struct {
	widget.Button
	tip string
}

func (b *tooltipButton) Tooltip() string {
	return b.tip
}

func newTooltipButton(icon fyne.Resource, tapped func(), tip string) *tooltipButton {
	b := &tooltipButton{tip: tip}
	b.Icon = icon
	b.OnTapped = tapped
	b.ExtendBaseWidget(b)
	return b
}

func newTooltipButtonNoTap(icon fyne.Resource, tip string) *tooltipButton {
	b := &tooltipButton{tip: tip}
	b.Icon = icon
	b.ExtendBaseWidget(b)
	return b
}

// ========= MinSizeEntry =========
// Entry mit fester MinBreite von mindestens 200px
type MinSizeEntry struct {
	widget.Entry
	minWidth float32
}

func NewMinSizeEntry(minW float32) *MinSizeEntry {
	e := &MinSizeEntry{minWidth: minW}
	// Pflicht bei eingebettetem widget.Entry: ohne ExtendBaseWidget verdrahtet
	// Fyne Fokus/Tastatur nicht korrekt -> das Feld nimmt keine Eingaben an.
	e.ExtendBaseWidget(e)
	return e
}

func (m *MinSizeEntry) MinSize() fyne.Size {
	s := m.Entry.MinSize()
	if s.Width < m.minWidth {
		s.Width = m.minWidth
	}
	return s
}

func (m *MinSizeEntry) Disable()       { m.Entry.Disable() }
func (m *MinSizeEntry) Enable()        { m.Entry.Enable() }
func (m *MinSizeEntry) Disabled() bool { return m.Entry.Disabled() }

// ========= MinSizeSelect =========
// Dropdown (widget.Select) mit fester Mindestbreite, damit lange Einträge
// (z.B. "Gemma 4 E2B") nicht abgeschnitten werden.
type MinSizeSelect struct {
	widget.Select
	minWidth float32
}

func NewMinSizeSelect(options []string, changed func(string), minW float32) *MinSizeSelect {
	s := &MinSizeSelect{minWidth: minW}
	s.Options = options
	s.OnChanged = changed
	// Pflicht bei eingebettetem widget.Select: ohne ExtendBaseWidget funktioniert
	// das Aufklapp-Menü nicht korrekt.
	s.ExtendBaseWidget(s)
	return s
}

func (m *MinSizeSelect) MinSize() fyne.Size {
	s := m.Select.MinSize()
	if s.Width < m.minWidth {
		s.Width = m.minWidth
	}
	return s
}

// ========= StretchHForm =========
// Layout für Controls: Label-Entry-Paare horizontal nebeneinander (1 Zeile pro Modell)
type stretchHForm struct{}

func (s *stretchHForm) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, maxH float32
	for i := 0; i < len(objects); i += 2 {
		if i+1 >= len(objects) {
			break
		}
		l, e := objects[i], objects[i+1]
		if !l.Visible() && !e.Visible() {
			continue
		}
		lMin := l.MinSize()
		eMin := e.MinSize()
		w += lMin.Width + eMin.Width + 8
		rowH := lMin.Height
		if eMin.Height > rowH {
			rowH = eMin.Height
		}
		if rowH > maxH {
			maxH = rowH
		}
	}
	return fyne.NewSize(w, maxH)
}

func (s *stretchHForm) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	x := float32(0)
	for i := 0; i < len(objects); i += 2 {
		if i+1 >= len(objects) {
			break
		}
		l, e := objects[i], objects[i+1]
		if !l.Visible() && !e.Visible() {
			continue
		}
		lMin := l.MinSize()
		eMin := e.MinSize()
		rowH := lMin.Height
		if eMin.Height > rowH {
			rowH = eMin.Height
		}

		l.Resize(fyne.NewSize(lMin.Width, rowH))
		l.Move(fyne.NewPos(x, 0))
		x += lMin.Width + 4

		e.Resize(fyne.NewSize(eMin.Width, rowH))
		e.Move(fyne.NewPos(x, 0))
		x += eMin.Width + 4
	}
}

// ========= StretchForm (VERTIKAL) =========
// Packt Label-Entry-Paare vertikal untereinander, Entry stretcht auf volle Breite
type stretchForm struct{}

func (s *stretchForm) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var labelW, maxW float32
	var h float32
	for i := 0; i < len(objects); i += 2 {
		if i+1 >= len(objects) {
			break
		}
		l, e := objects[i], objects[i+1]
		if !l.Visible() && !e.Visible() {
			continue
		}
		lMin := l.MinSize()
		eMin := e.MinSize()
		if lMin.Width > labelW {
			labelW = lMin.Width
		}
		rowW := lMin.Width + eMin.Width + 4
		if rowW > maxW {
			maxW = rowW
		}
		rowH := lMin.Height
		if eMin.Height > rowH {
			rowH = eMin.Height
		}
		h += rowH + 4
	}
	return fyne.NewSize(maxW, h)
}

func (s *stretchForm) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for i := 0; i < len(objects); i += 2 {
		if i+1 >= len(objects) {
			break
		}
		l, e := objects[i], objects[i+1]
		if !l.Visible() && !e.Visible() {
			continue
		}
		lMin := l.MinSize()
		eMin := e.MinSize()
		rowH := lMin.Height
		if eMin.Height > rowH {
			rowH = eMin.Height
		}

		l.Resize(fyne.NewSize(lMin.Width, rowH))
		l.Move(fyne.NewPos(0, y))

		e.Resize(fyne.NewSize(size.Width-lMin.Width-4, rowH))
		e.Move(fyne.NewPos(lMin.Width+4, y))

		y += rowH + 4
	}
}

// ========= LLMTableLayout =========
// Festes Koordinatenraster: 4 Zeilen, 3 Spalten à 25% Breite
// objects[0] = Header, dann je 3 Widgets pro Zeile (Label, Radio, Control)
type llmTableLayout struct{}

func (l *llmTableLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	// Höhe konsistent zu Layout() berechnen: Header + eine Höhe pro Zeile
	// (je 3 Objekte: Radio, Label, Control). Vorher wurde jedes Objekt einzeln
	// summiert, was massiven Leerraum unter der Tabelle erzeugte.
	var h float32
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		if i == 0 {
			h += o.MinSize().Height + 8
			continue
		}
		if (i-1)%3 == 2 { // 3. Spalte = Control -> Zeilenhöhe einmal zählen
			h += o.MinSize().Height + 6
		}
	}
	return fyne.NewSize(760, h) // 180(Radio) + 10(gutter) + 100(Label) + 450(Entry) + Puffer
}

func (l *llmTableLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	radioW := float32(180) // Spalte 0: Radio
	labelW := float32(100) // Spalte 1: Label (breit genug für "Modelname:")
	entryW := float32(450) // Spalte 2: Entry (300 * 1.5)
	gutter := float32(10)  // Abstand zwischen Spalten

	y := float32(0)
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		s := o.MinSize()
		if i == 0 {
			o.Resize(fyne.NewSize(size.Width, s.Height))
			o.Move(fyne.NewPos(0, y))
			y += s.Height + 8
			continue
		}
		switch (i - 1) % 3 {
		case 0: // Radio
			o.Resize(fyne.NewSize(radioW, s.Height))
			o.Move(fyne.NewPos(0, y))
		case 1: // Label
			o.Resize(fyne.NewSize(labelW, s.Height))
			o.Move(fyne.NewPos(radioW+gutter, y))
		case 2: // Entry/Label (localModelLabel)
			o.Resize(fyne.NewSize(entryW, s.Height))
			o.Move(fyne.NewPos(radioW+gutter+labelW, y))
		}
		if (i-1)%3 == 2 {
			y += s.Height + 6
		}
	}
}

// ========= Compact Layouts =========
// segmentLayout ordnet Elemente ohne Zwischenabstand gleich breit nebeneinander
// an - fuer den DE/EN-Segment-Umschalter (wirkt wie ein zusammenhaengendes
// Pill-Widget statt zweier getrennter Buttons).
type segmentLayout struct{}

func (s *segmentLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var maxW, maxH float32
	for _, o := range objects {
		m := o.MinSize()
		if m.Width > maxW {
			maxW = m.Width
		}
		if m.Height > maxH {
			maxH = m.Height
		}
	}
	return fyne.NewSize(maxW*float32(len(objects)), maxH)
}

func (s *segmentLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	n := float32(len(objects))
	w := size.Width / n
	for i, o := range objects {
		o.Resize(fyne.NewSize(w, size.Height))
		o.Move(fyne.NewPos(w*float32(i), 0))
	}
}

// alignedFormLayout ordnet Label/Value-Paare mit FESTER Spaltenaufteilung an:
// Value beginnt immer bei alignedFormValueX mit Breite alignedFormValueW -
// exakt die X-Position/Breite der "API key"-Zeile in llmTableLayout
// (radioW 180 + gutter 10 + labelW 100 = 290, entryW 450). So stehen
// Mikrofon/Lautsprecher/Remote-Whisper-URL/Analyse/Jarvis-Felder auf
// derselben Flucht wie das API-Key-Feld ("aufgeraeumtes, symmetrisches Design").
type alignedFormLayout struct{}

const (
	alignedFormValueX = 290
	alignedFormValueW = 450
)

func (a *alignedFormLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var h float32
	for i := 0; i+1 < len(objects); i += 2 {
		if !objects[i].Visible() {
			continue
		}
		rowH := objects[i].MinSize().Height
		if vh := objects[i+1].MinSize().Height; vh > rowH {
			rowH = vh
		}
		h += rowH + 4
	}
	return fyne.NewSize(alignedFormValueX+alignedFormValueW, h)
}

func (a *alignedFormLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for i := 0; i+1 < len(objects); i += 2 {
		label, value := objects[i], objects[i+1]
		if !label.Visible() {
			continue
		}
		rowH := label.MinSize().Height
		if vh := value.MinSize().Height; vh > rowH {
			rowH = vh
		}
		label.Resize(fyne.NewSize(alignedFormValueX-10, rowH))
		label.Move(fyne.NewPos(0, y))
		value.Resize(fyne.NewSize(alignedFormValueW, rowH))
		value.Move(fyne.NewPos(alignedFormValueX, y))
		y += rowH + 4
	}
}

// framedSelect hinterlegt ein Dropdown mit einem leicht grauen Hintergrund,
// damit es sich optisch vom weissen Einstellungen-Hintergrund absetzt (Fynes
// Select-Rahmen allein war zu unauffaellig).
func framedSelect(sel fyne.CanvasObject) fyne.CanvasObject {
	// WICHTIG: sel deckt mit seinem eigenen (undurchsichtigen) Hintergrund die
	// gesamte ihm zugewiesene Flaeche ab. Ein Rahmen/Hintergrund in der GLEICHEN
	// Groesse (z.B. per Stack ohne Inset) wird dadurch komplett uebermalt und
	// bleibt unsichtbar. Deshalb hier: sel per Padded etwas KLEINER als der
	// aussen liegende Rahmen machen, damit der Rahmen im Rand-Bereich sichtbar
	// bleibt (dünne schwarze Linie rundherum).
	border := canvas.NewRectangle(color.Transparent)
	border.StrokeColor = color.Black
	border.StrokeWidth = 1
	return container.NewStack(border, container.NewPadded(sel))
}

// newCollapsibleSection baut einen Klapp-Header im Stil von "Erweiterte
// Einstellungen": Klick auf den Titel zeigt/versteckt content. Fyne loest bei
// Hide()/Show() eines Kindes KEIN Neu-Layout des Elterncontainers aus, daher
// muss refreshParent (Refresh+Resize auf den umschliessenden Container) nach
// jedem Umschalten aufgerufen werden. persist speichert den neuen Zustand
// (z.B. in config.json), initialExpanded ist der beim Start wiederhergestellte
// Zustand.
func newCollapsibleSection(title string, content fyne.CanvasObject, initialExpanded bool, persist func(bool), refreshParent func()) *widget.Button {
	var toggle *widget.Button
	apply := func(exp bool) {
		if exp {
			content.Show()
			toggle.SetIcon(theme.MenuDropUpIcon())
		} else {
			content.Hide()
			toggle.SetIcon(theme.MenuDropDownIcon())
		}
		if refreshParent != nil {
			refreshParent()
		}
	}
	expanded := initialExpanded
	toggle = widget.NewButtonWithIcon(T(title), theme.MenuDropDownIcon(), func() {
		expanded = !expanded
		apply(expanded)
		persist(expanded)
	})
	onLangChange(func() { toggle.SetText(T(title)) }) // Titel bei Sprachwechsel mitziehen
	toggle.Importance = widget.LowImportance
	toggle.Alignment = widget.ButtonAlignLeading
	apply(expanded)
	return toggle
}

// topRightOverlayLayout positioniert genau ein Objekt rechtsbuendig mit festem
// Randabstand, vertikal um yOffset gegenueber der Oberkante verschoben
// (negativ = nach oben). Fuer das Firmenlogo, das ueber der AppTabs-Leiste liegt.
type topRightOverlayLayout struct {
	rightMargin float32
	yOffset     float32
}

func (l *topRightOverlayLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	return objects[0].MinSize()
}

func (l *topRightOverlayLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	o := objects[0]
	s := o.MinSize()
	o.Resize(s)
	o.Move(fyne.NewPos(size.Width-s.Width-l.rightMargin, l.yOffset))
}

type compactVBoxLayout struct{}

func (c *compactVBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		size := o.MinSize()
		if _, ok := o.(*widget.Separator); ok {
			// 1px dünne Linie + je 4px Luft oben/unten (Separator wird sonst auf
			// die zugewiesene Höhe gestreckt und wirkt als dicker Balken).
			h += 9
			if size.Width > w {
				w = size.Width
			}
			continue
		}
		if size.Width > w {
			w = size.Width
		}
		h += size.Height + 1
	}
	return fyne.NewSize(w, h)
}

func (c *compactVBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		if _, ok := o.(*widget.Separator); ok {
			o.Resize(fyne.NewSize(size.Width, 1)) // 1px dünne Trennlinie
			o.Move(fyne.NewPos(0, y+4))           // 4px Luft oben
			y += 9                                // 1px Linie + 8px Luft (4 oben/4 unten)
			continue
		}
		min := o.MinSize()
		o.Resize(fyne.NewSize(size.Width, min.Height))
		o.Move(fyne.NewPos(0, y))
		y += min.Height + 1
	}
}

type compactFormLayout struct{}

func (c *compactFormLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var lw, rw, h float32
	for i := 0; i < len(objects); i += 2 {
		if !objects[i].Visible() {
			continue
		}
		lMin := objects[i].MinSize()
		rMin := objects[i+1].MinSize()
		if lMin.Width > lw {
			lw = lMin.Width
		}
		if rMin.Width > rw {
			rw = rMin.Width
		}

		maxH := lMin.Height
		if rMin.Height > maxH {
			maxH = rMin.Height
		}
		h += maxH + 1
	}
	return fyne.NewSize(lw+rw+4, h)
}

func (c *compactFormLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	var lw float32
	for i := 0; i < len(objects); i += 2 {
		if objects[i].MinSize().Width > lw {
			lw = objects[i].MinSize().Width
		}
	}

	y := float32(0)
	for i := 0; i < len(objects); i += 2 {
		l, r := objects[i], objects[i+1]
		if !l.Visible() {
			continue
		}

		lMin := l.MinSize()
		rMin := r.MinSize()
		maxH := lMin.Height
		if rMin.Height > maxH {
			maxH = rMin.Height
		}

		// Labels rechtsbündig ausrichten für bessere Form-Optik
		l.Resize(fyne.NewSize(lw, maxH))
		l.Move(fyne.NewPos(0, y))

		r.Resize(fyne.NewSize(size.Width-lw-4, maxH))
		r.Move(fyne.NewPos(lw+4, y))

		y += maxH + 1
	}
}

type compactTableLayout struct {
	Cols int
}

func (c *compactTableLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	colWidths := make([]float32, c.Cols)

	// Finde maximale Breite jeder Spalte
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		col := i % c.Cols
		s := o.MinSize()
		if s.Width > colWidths[col] {
			colWidths[col] = s.Width
		}
	}

	for _, cw := range colWidths {
		w += cw
	}
	if c.Cols > 1 {
		w += float32(c.Cols-1) * 4 // 4px Lücke zwischen Spalten
	}

	// Finde Höhe (Summe der maximalen Höhen jeder Zeile)
	for i := 0; i < len(objects); i += c.Cols {
		var rowH float32
		for j := 0; j < c.Cols; j++ {
			if i+j >= len(objects) {
				break
			}
			o := objects[i+j]
			if !o.Visible() {
				continue
			}
			s := o.MinSize()
			if s.Height > rowH {
				rowH = s.Height
			}
		}
		h += rowH + 1 // 1px Zeilenabstand
	}

	return fyne.NewSize(w, h)
}

func (c *compactTableLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	colWidths := make([]float32, c.Cols)
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		col := i % c.Cols
		s := o.MinSize()
		if s.Width > colWidths[col] {
			colWidths[col] = s.Width
		}
	}

	// Wenn Spalte 2 (Index 2) und 4 (Index 4) flexibel sein sollen, verteilen wir den Restplatz
	usedW := float32(0)
	for _, cw := range colWidths {
		usedW += cw
	}
	usedW += float32(c.Cols-1) * 4

	if size.Width > usedW && c.Cols == 5 {
		extra := (size.Width - usedW) / 2
		colWidths[2] += extra
		colWidths[4] += extra
	}

	y := float32(0)
	for i := 0; i < len(objects); i += c.Cols {
		var rowH float32
		for j := 0; j < c.Cols; j++ {
			if i+j >= len(objects) {
				break
			}
			o := objects[i+j]
			if !o.Visible() {
				continue
			}
			if o.MinSize().Height > rowH {
				rowH = o.MinSize().Height
			}
		}

		x := float32(0)
		for j := 0; j < c.Cols; j++ {
			if i+j >= len(objects) {
				break
			}
			o := objects[i+j]
			if o.Visible() {
				// Vertikal zentrieren innerhalb der Zeile
				oH := o.MinSize().Height
				oY := y + (rowH-oH)/2
				o.Resize(fyne.NewSize(colWidths[j], oH))
				o.Move(fyne.NewPos(x, oY))
			}
			x += colWidths[j] + 4
		}
		y += rowH + 1
	}
}

// ========= Pegelanzeige mit Richtwert-Marker =========
// targetLevel ist der empfohlene Aussteuerungspegel (grüner Ziel-Strich).
const targetLevel = 0.8

// meterMarkerLayout legt zwei dünne vertikale Striche über die Pegelanzeige
// (objs[0]): objs[1] = beobachteter Spitzenpegel (orange, *markerVal), objs[2] =
// fester Ziel-Pegel (grün, targetVal). Richtwert für die Aussteuerung / Autoadjust.
type meterMarkerLayout struct {
	markerVal *float64
	targetVal float64
}

func (l *meterMarkerLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	if len(objs) == 0 {
		return fyne.NewSize(0, 0)
	}
	return objs[0].MinSize()
}

func (l *meterMarkerLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if len(objs) < 3 {
		return
	}
	objs[0].Resize(size) // Pegelanzeige füllt die Fläche
	objs[0].Move(fyne.NewPos(0, 0))
	place := func(o fyne.CanvasObject, val float64) {
		v := float32(val)
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		o.Resize(fyne.NewSize(2, size.Height))
		o.Move(fyne.NewPos(v*size.Width-1, 0))
	}
	place(objs[1], *l.markerVal) // Peak-Hold (orange)
	place(objs[2], l.targetVal)  // Ziel (grün)
}

// newLevelMeter kombiniert eine Pegelanzeige mit Peak-Hold- und Ziel-Strich.
func newLevelMeter(bar *widget.ProgressBar, markerVal *float64) *fyne.Container {
	peak := canvas.NewRectangle(color.NRGBA{R: 235, G: 120, B: 0, A: 255})   // orange: Ist-Spitze
	target := canvas.NewRectangle(color.NRGBA{R: 30, G: 170, B: 60, A: 255}) // grün: Ziel
	return container.New(&meterMarkerLayout{markerVal: markerVal, targetVal: targetLevel}, bar, peak, target)
}

// ========= Windows Desktop Theme =========
// winTheme erzwingt einen festen Hell-/Dunkel-Variant (unabhängig vom System) und
// gibt Windows-typische Farben. Im 'classic'-Modus zusätzlich eckige Ecken
// (Radius 0) und klassisches Fenster-Grau – wie eine traditionelle Win32-App.
type winTheme struct {
	dark    bool
	classic bool
}

func (w *winTheme) variantOf() fyne.ThemeVariant {
	if w.dark {
		return theme.VariantDark
	}
	return theme.VariantLight
}

func (w *winTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	v := w.variantOf()
	// App-weite Akzentfarbe: Rot statt des Fyne-Standardblaus (betrifft u.a.
	// Tab-Indikator, fokussierte Rahmen, ProgressBar, markierte Checkboxen,
	// "HighImportance"-Buttons und Hyperlinks - siehe KI-Support-Panel).
	switch name {
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return color.NRGBA{R: 0xB0, G: 0x1E, B: 0x2C, A: 255}
	}
	if v == theme.VariantLight {
		switch name {
		case theme.ColorNameBackground:
			if w.classic {
				return color.NRGBA{R: 240, G: 240, B: 240, A: 255} // klassisches Windows-Grau
			}
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255} // weiß wie moderne Windows-App
		case theme.ColorNameForeground:
			return color.NRGBA{R: 0, G: 0, B: 0, A: 255}
		case theme.ColorNameInputBackground:
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		case theme.ColorNameInputBorder:
			// Sichtbarer Rahmen fuer Eingabefelder/Pulldowns auf weissem Grund
			// (Fynes Standard-Rahmenfarbe war hier kaum erkennbar).
			return color.NRGBA{R: 0x9B, G: 0x9B, B: 0x9B, A: 255}
		}
	} else {
		switch name {
		case theme.ColorNameBackground:
			return color.NRGBA{R: 32, G: 32, B: 32, A: 255}
		case theme.ColorNameForeground:
			return color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
	}
	return theme.DefaultTheme().Color(name, v)
}

// loadSystemFont laedt eine TTF-Datei aus dem Windows-Fontverzeichnis zur
// Laufzeit (kein Embedding/Redistribution - Segoe UI/Consolas sind
// Microsoft-Lizenzware, nur auf Windows-Systemen selbst vorhanden). Liefert
// nil, wenn die Datei fehlt (z.B. beim Cross-Compile-Test auf Linux) - in dem
// Fall greift die Fyne-Standardschrift als Fallback.
func loadSystemFont(filename string) fyne.Resource {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	data, err := os.ReadFile(filepath.Join(root, "Fonts", filename))
	if err != nil {
		return nil
	}
	return fyne.NewStaticResource(filename, data)
}

// Segoe UI ist die Windows-Systemschrift (Explorer, Einstellungen, etc.) -
// wird geladen, um die App optisch wie eine native Windows-Anwendung wirken
// zu lassen, statt mit Fynes gebuendelter Standardschrift.
var (
	segoeRegular    = loadSystemFont("segoeui.ttf")
	segoeBold       = loadSystemFont("segoeuib.ttf")
	segoeItalic     = loadSystemFont("segoeuii.ttf")
	segoeBoldItalic = loadSystemFont("segoeuiz.ttf")
	consolasRegular = loadSystemFont("consola.ttf")
)

func (w *winTheme) Font(style fyne.TextStyle) fyne.Resource {
	if style.Monospace {
		if consolasRegular != nil {
			return consolasRegular
		}
		return theme.DefaultTheme().Font(style)
	}
	switch {
	case style.Bold && style.Italic:
		if segoeBoldItalic != nil {
			return segoeBoldItalic
		}
	case style.Bold:
		if segoeBold != nil {
			return segoeBold
		}
	case style.Italic:
		if segoeItalic != nil {
			return segoeItalic
		}
	default:
		if segoeRegular != nil {
			return segoeRegular
		}
	}
	return theme.DefaultTheme().Font(style)
}
func (w *winTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}
func (w *winTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding, theme.SizeNameInnerPadding:
		return 3 // kompakter Abstand (Standard ist 4)
	case theme.SizeNameText:
		return 12 // Windows-übliche Textgröße (~9pt; Fyne-Standard ist 14)
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		if w.classic {
			return 0 // eckige Ecken im klassischen Design
		}
	}
	return theme.DefaultTheme().Size(name)
}

// isClassic erkennt den klassischen (eckigen) Modus – alte und neue Bezeichnung.
func isClassic(mode string) bool { return mode == "Klassisch" || mode == "Hell (klassisch)" }

// applyTheme setzt das App-Theme anhand des Modus. Akzeptiert auch die alten
// Bezeichnungen ("Hell"/"Dunkel"/"Klassisch") zur Migration.
func applyTheme(a fyne.App, mode string) {
	switch mode {
	case "Dunkel", "Dunkel (modern)":
		a.Settings().SetTheme(&winTheme{dark: true})
	case "Klassisch", "Hell (klassisch)":
		a.Settings().SetTheme(&winTheme{classic: true})
	default: // "Hell (modern)"
		a.Settings().SetTheme(&winTheme{})
	}
}

// =========================================

var (
	outputArea        *widget.Entry
	statusLabel       *widget.Label
	engineInfo        *widget.Label
	progressBar       *widget.ProgressBar
	micBtn            *tooltipButton
	customerField     *widget.Label   // CRM-Wert (reines Anzeige-Label); per Rufnummern-Webhook befuellt
	ibsAddressField   *widget.Label   // "Kundenv. ID": address_id der Kundenverwaltung zum Anrufer ("-" wenn keine)
	ibsIDBox          *fyne.Container // Label+Wert+Copy der Kundenv. ID; nur sichtbar bei konfigurierter IBS-API (refreshIBSCheck)
	callerNumberLabel *widget.Label   // zeigt die per Webhook empfangene Rufnummer (zwischen Feld und Start-Button)
	mainWin           fyne.Window     // Hauptfenster, u.a. fuer Dialoge aus webhook.go
	lastSoundTime     atomic.Value    // *time.Time, atomar für Zugriff aus Goroutines
	isSilent          atomic.Bool
	isRecording       atomic.Bool

	// Ticket-Suche ("Suche passende Tickets" + Auto-Scan). searchMatchingTickets
	// wird in main() nach buildKISupportPanel zugewiesen (paketweit, damit auch
	// toggleRecording/startAutoScan darauf zugreifen). autoScanCancel stoppt den
	// laufenden Zyklus, autoScanBusy schuetzt vor ueberlappenden Auto-Suchen.
	searchMatchingTickets func(recognizedText string, trigger *widget.Button, auto bool, crmFallback bool)
	// clearTicketResults leert die Ergebnis-/Ticketliste im KI-Support-Panel.
	// Wird in main() nach buildKISupportPanel zugewiesen und u.a. aufgerufen, wenn
	// der Inhalt des CRM-Felds geaendert wird.
	clearTicketResults func()
	autoScanCancel     context.CancelFunc
	autoScanBusy       atomic.Bool
	currentText        strings.Builder
	lastSpeaker        string // zuletzt ins Transkript geschriebener Sprecher; nur in fyne.Do-Callbacks
	// Whisper+LLM: aktueller, noch nicht korrigierter Rohblock (live angezeigt,
	// bei Sprechpause/Sprecherwechsel an die LLM-Korrektur übergeben). Nur fyne.Do.
	pendingRaw     strings.Builder
	pendingSpeaker string
	pendingTs      string          // Zeitstempel des aktuellen pending-Blocks (Whisper+LLM)
	inProgress     []*pendingBlock // Whisper+LLM: Blöcke in LLM-Korrektur (Rohtext bleibt sichtbar)

	audioDevice         *malgo.Device
	callerDevice        *malgo.Device
	modeIcon            *widget.Icon
	mctx                *malgo.AllocatedContext
	selectedMicID       *malgo.DeviceID
	selectedSpeakerID   *malgo.DeviceID
	analysisProgress    *widget.ProgressBarInfinite
	agentLevel          *widget.ProgressBar
	callerLevel         *widget.ProgressBar
	agentMeter          *fyne.Container // Pegelanzeige + Richtwert-Marker (Mic)
	callerMeter         *fyne.Container // Pegelanzeige + Richtwert-Marker (Speaker)
	agentMarkerVal      float64         // beobachteter Spitzenpegel Mic (0..1), nur Main-Thread
	callerMarkerVal     float64         // beobachteter Spitzenpegel Speaker (0..1), nur Main-Thread
	erkennungInfoLabel  *widget.Label
	exeDir              string
	spkGainSlider       *widget.Slider
	spkGainLabel        *widget.Label
	speakerControlGroup *fyne.Container

	// Zentrale Konfiguration
	config AppConfig

	// Audio-Lifecycle: schützt prepareAudio gegen parallele Aufrufe und hält die
	// Cancel-Funktionen der aktuell laufenden Buffer-Goroutinen, um sie bei einem
	// Geräte-Wechsel sauber (und ohne UI-Blockade) zu beenden.
	audioMu         sync.Mutex
	agentBufCancel  context.CancelFunc
	callerBufCancel context.CancelFunc

	// Race-freie Spiegelung der im Audio-/Worker-Hot-Path gelesenen Config-Werte.
	// Geschrieben im UI-Thread (via syncConfigToAtomics), gelesen aus Goroutinen.
	atMicGain         atomic.Uint64 // float64-Bits
	atSpkGain         atomic.Uint64 // float64-Bits
	atPauseThresh     atomic.Uint64 // float64-Bits
	atHeadsetMode     atomic.Bool
	atWhisperLocal    atomic.Bool // Erkennung lokal (Whisper) vs. remote
	atHasPostProc     atomic.Bool // Nachbearbeitung aktiv (PostProcModel != "none")
	atRemoteWhisperOK atomic.Bool // Remote-Whisper-Server erreichbar (vom Pill-Ticker gesetzt)
	atLogging         atomic.Bool

	// Serielle Warteschlange für die Satz-Korrektur (Whisper+LLM). Ein einziger
	// Worker hält die Reihenfolge der korrigierten Sätze stabil.
	correctionJobs = make(chan correctionJob, 32)

	// Debounce für das Schreiben der config.json (Slider/Eingaben feuern pro Tick).
	saveTimer   *time.Timer
	saveTimerMu sync.Mutex
)

// BackendCfg bündelt die Verbindungsdaten eines Analyse-Backends. Jedes
// Remote-/Cloud-Backend (Flash, Ollama, vLLM) behält so seine eigenen Werte.
type BackendCfg struct {
	ApiKey string `json:"apiKey"`
	Url    string `json:"url"`
	Model  string `json:"model"`
}

// AnalysisPromptDef ist eine gespeicherte Analyse-Vorgabe: Name ist die
// beschreibende Zeile (der Pulldown-Eintrag), Prompt der zugehoerige
// LLM-Prompt. Gepflegt in "Einstellungen" (Abschnitt "Spracherkennung und
// Analyse"); der STT-Tab zeigt nur noch die Beschreibungen als Pulldown.
// Das Loeschen einer Beschreibung loescht immer auch ihren Prompt.
type AnalysisPromptDef struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

type AppConfig struct {
	AppMode         string   `json:"appMode"`
	Theme           string   `json:"theme"`
	AnalysisPrompt  string   `json:"analysisPrompt"`  // Alt-Feld (nur Migration, s. migrateAnalysisPromptDefs)
	AnalysisPrompts []string `json:"analysisPrompts"` // Alt-Feld: Vorgaben nur als Text (nur Migration)

	// Analyse-Vorgaben: Beschreibung + Prompt (ersetzt AnalysisPrompt/-s).
	// AnalysisPromptName ist die im STT-Tab gewaehlte Beschreibung - ihr
	// Prompt wird beim "Analysieren" verwendet.
	AnalysisPromptDefs []AnalysisPromptDef `json:"analysisPromptDefs"`
	AnalysisPromptName string              `json:"analysisPromptName"`

	// Neue Pipeline-Struktur: Erkennung / Nachbearbeitung / Analyse.
	WhisperLocal     bool   `json:"whisperLocal"`     // Erkennung: lokaler Whisper (true) vs. Remote Whisper (false)
	RemoteWhisperUrl string `json:"remoteWhisperUrl"` // WebSocket-URL des Remote-Whisper-Servers
	PostProcModel    string `json:"postProcModel"`    // Nachbearbeitung: "none"/"e2b"/"12b"/"remote"
	AnalysisModel    string `json:"analysisModel"`    // Analyse: "e2b"/"12b"/"remote"
	RemoteBackend    string `json:"remoteBackend"`    // Remote-LLM: "Google Flash"/"Ollama"/"vLLM"

	// Pro-Backend-Konfiguration der Remote-LLMs (API-Key, URL, Modell je Backend).
	Flash  BackendCfg `json:"flash"`
	Ollama BackendCfg `json:"ollama"`
	Vllm   BackendCfg `json:"vllm"`

	// Jarvis-Support-API (Kundenverwaltung, Doku siehe /support-api am Jarvis-Host). ApiKey/Url
	// bewusst nur in config.json, nicht im Quellcode hinterlegt.
	Jarvis     BackendCfg `json:"jarvis"`
	JarvisLang string     `json:"jarvisLang"` // "de" oder "en"

	// Kundenverwaltungs-API (IBS): Verbindungsdaten für die Checkbox
	// "IBS Tickets" im KI-Support-Panel. Die eigentliche API-Abfrage wird
	// später hinterlegt; die Checkbox ist nur klickbar, wenn URL und
	// API-Key gesetzt sind. Model bleibt ungenutzt.
	IBS BackendCfg `json:"ibs"`

	// Zuletzt genutzte Filter/Optionen im KI-Support-Panel (STT-Tab).
	JarvisRAG            bool `json:"jarvisRAG"`
	JarvisIBS            bool `json:"jarvisIBS"`
	JarvisJira           bool `json:"jarvisJira"`
	JarvisOpenOnly       bool `json:"jarvisOpenOnly"`
	JarvisConfluence     bool `json:"jarvisConfluence"`
	JarvisAISummary      bool `json:"jarvisAISummary"`
	JarvisJiraLimit      int  `json:"jarvisJiraLimit"`
	JarvisIBSLimit       int  `json:"jarvisIBSLimit"`       // max. angezeigte Kundenverwaltungs-Tickets
	JarvisIBSSearchLimit int  `json:"jarvisIBSSearchLimit"` // request.limit der Schlagwortsuche (getMatchingEvents)

	// Zustand der Kopfzeile der Anruf-Ticketliste (Quellen-Haekchen "Jira"/
	// "Kundenv.", Radio "offen"/"alle") - bleibt wie die Such-Checkboxen ueber
	// Abfragen und Neustarts erhalten. Achtung: Default true wirkt nur, weil
	// LoadConfig die Defaults VOR dem Unmarshal setzt (fehlender Key bleibt true).
	JarvisCallShowJira     bool `json:"jarvisCallShowJira"`
	JarvisCallShowIBS      bool `json:"jarvisCallShowIBS"`
	JarvisCallOpenOnly     bool `json:"jarvisCallOpenOnly"`
	JarvisSummaryLines     int  `json:"jarvisSummaryLines"`
	JarvisAdvancedExpanded bool `json:"jarvisAdvancedExpanded"`
	// Sortierung der Anruf-Ticketliste ("Sortierung: unsortiert|erstellt|
	// geändert" oberhalb der Liste): Mode "" = unsortiert (Server-Reihenfolge),
	// "created" = nach Erstellt, "modified" = nach letztem Zugriff; SortAsc
	// false = neueste zuerst (Default beim Aktivieren).
	JarvisCallSortMode string `json:"jarvisCallSortMode"`
	JarvisCallSortAsc  bool   `json:"jarvisCallSortAsc"`

	// "Prompt für KI-Zusammenfassung" in "Einstellungen" (KI-Support (Jarvis)): Anweisung an
	// die LLM, die bei jeder Suche im Feld "prompt" der Anfrage mitgeschickt wird
	// (s. jarvisQueryRequest.Prompt). Bewusst UNABHÄNGIG vom Suchtext im STT-Tab
	// (queryEntry): der Suchtext ist die Frage, dieser Prompt die Instruktion -
	// die beiden werden NICHT miteinander vermischt/synchronisiert.
	JarvisSearchQuery string `json:"jarvisSearchQuery"`

	// "Prompt für passende Tickets" in "Einstellungen": Prompt-Vorlage für den
	// Button "Suche passende Tickets" (STT-Tab). Der Platzhalter "[Textfenster]"
	// wird vor dem API-Call durch den erkannten Text ersetzt. Default s.
	// defaultTicketSearchPrompt (jarvis_client.go).
	JarvisTicketSearchPrompt string `json:"jarvisTicketSearchPrompt"`

	// Automatischer, zyklischer Ticket-Scan bei aktiver Texterkennung.
	// AutoScanInterval in Sekunden, erlaubt 0..30 (s. clampAutoScanInterval);
	// 0 = keine zyklische Anfrage.
	AutoScanEnabled  bool `json:"autoScanEnabled"`
	AutoScanInterval int  `json:"autoScanInterval"`

	// AutoRecordOnCall: bei eingehendem Anruf (Rufnummern-Webhook) automatisch die
	// Mitschrift starten (sofern nicht bereits eine läuft). Checkbox in
	// "Einstellungen" unter "Suche nach passenden Tickets".
	AutoRecordOnCall bool `json:"autoRecordOnCall"`

	// AutoSearchCaller: bei eingehendem Anruf automatisch die CRM-Suche zur
	// Rufnummer starten (Default true). Ist sie aus, wird nur die Rufnummer
	// angezeigt; die Suche lässt sich dann per Wiederhol-Button manuell auslösen.
	// Achtung: Default true wirkt nur, weil LoadConfig die Defaults VOR dem
	// Unmarshal setzt (fehlender Key bleibt true).
	AutoSearchCaller bool `json:"autoSearchCaller"`

	// CallerTakeFirstMatch: liefert die Rufnummernsuche mehrere CRM-Treffer,
	// wird bei true still der erste genommen; bei false (Default) erscheint das
	// Auswahl-Popup.
	CallerTakeFirstMatch bool `json:"callerTakeFirstMatch"`

	// Rufnummern-Übergabe (Abschnitt "Rufnummern Übergabe" in Einstellungen):
	// eingehender HTTP-Webhook, über den ein externer Trigger (z.B. Telefon-
	// anlage) die Rufnummer eines Anrufers übergibt. Die App sucht damit in Jira
	// und trägt den Issue-Key des Top-Treffers ins CRM Feld ein.
	// Server lauscht auf 0.0.0.0:<Port><Pfad>. Siehe webhook.go.
	WebhookEnabled bool   `json:"webhookEnabled"`
	WebhookPath    string `json:"webhookPath"` // URL-Pfad, Default "/rufnummer"
	WebhookPort    int    `json:"webhookPort"` // Default 5555

	// Einklapp-Zustand der Mic+Spk-Pegelanzeige im STT-Tab (Standard: zugeklappt).
	SttMeterExpanded bool `json:"sttMeterExpanded"`

	PauseThreshold float64 `json:"pauseThreshold"`
	LoggingEnabled bool    `json:"loggingEnabled"`
	DebugMode      bool    `json:"debugMode"` // zeigt vor Suchen/Analysieren ein Popup mit der Anfrage
	MicName        string  `json:"micName"`
	SpeakerName    string  `json:"speakerName"`
	WinWidth       float32 `json:"winWidth"`
	WinHeight      float32 `json:"winHeight"`
	WinX           int32   `json:"winX"`
	WinY           int32   `json:"winY"`
	WinShowCmd     int32   `json:"winShowCmd"`
	PhysX          int32   `json:"physX"`
	PhysY          int32   `json:"physY"`
	PhysWidth      int32   `json:"physWidth"`
	PhysHeight     int32   `json:"physHeight"`
	MicGain        float64 `json:"micGain"`
	SpkGain        float64 `json:"spkGain"`

	// Veraltete Felder – nur noch zum Migrieren alter config.json gelesen.
	AnalysisMode    string `json:"analysisMode,omitempty"`
	SttPipeline     string `json:"sttPipeline,omitempty"`
	LocalGemmaModel string `json:"localGemmaModel,omitempty"`
	UseMTPDrafter   bool   `json:"useMTPDrafter,omitempty"`
	GeminiApiKey    string `json:"geminiApiKey,omitempty"`
	OllamaUrl       string `json:"ollamaUrl,omitempty"`
	OllamaModel     string `json:"ollamaModel,omitempty"`
	VllmUrl         string `json:"vllmUrl,omitempty"`
	VllmModel       string `json:"vllmModel,omitempty"`
}

func LoadConfig(a fyne.App) {
	configPath := filepath.Join(exeDir, "config.json")

	// Standardwerte
	config = AppConfig{
		AppMode:                  "Standard-Betrieb",
		Theme:                    "Hell (modern)",
		AnalysisPrompt:           "Fasse das wesentliche zusammen",
		AnalysisPrompts:          []string{"Fasse das wesentliche zusammen"},
		WhisperLocal:             true,
		RemoteWhisperUrl:         "ws://191.100.130.61:8090/ws/stt",
		PostProcModel:            "none",
		AnalysisModel:            "e2b",
		RemoteBackend:            "vLLM",
		Ollama:                   BackendCfg{Url: "http://localhost:11434", Model: "gemma2"},
		Vllm:                     BackendCfg{Url: "http://localhost:8000"},
		PauseThreshold:           1.5,
		LoggingEnabled:           true,
		MicName:                  "System-Standard",
		SpeakerName:              "System-Standard",
		WinWidth:                 1000,
		WinHeight:                800,
		WinX:                     100,
		WinY:                     100,
		WinShowCmd:               1, // SW_SHOWNORMAL
		PhysX:                    100,
		PhysY:                    100,
		PhysWidth:                1000,
		PhysHeight:               800,
		MicGain:                  1.0,
		SpkGain:                  1.0,
		JarvisLang:               "de",
		JarvisRAG:                true,
		JarvisJira:               true,
		JarvisAISummary:          true,
		JarvisJiraLimit:          10,
		JarvisIBSLimit:           10,
		JarvisIBSSearchLimit:     30,
		JarvisCallShowJira:       true,
		JarvisCallShowIBS:        true,
		JarvisCallOpenOnly:       true,
		JarvisSummaryLines:       5,
		JarvisAdvancedExpanded:   true,
		JarvisTicketSearchPrompt: defaultTicketSearchPrompt,
		AutoScanEnabled:          false,
		AutoScanInterval:         10,
		AutoRecordOnCall:         false,
		AutoSearchCaller:         true,
		WebhookEnabled:           false,
		WebhookPath:              "/rufnummer",
		WebhookPort:              5555,
	}
	syncConfigToAtomics() // Atomics mit Defaults füllen (u.a. Logging aktiv)

	data, err := os.ReadFile(configPath)
	if err == nil {
		json.Unmarshal(data, &config)
		migrateLegacyBackendFields()
		migrateLegacyPipelineFields()
		if len(config.AnalysisPrompts) == 0 && config.AnalysisPrompt != "" {
			config.AnalysisPrompts = []string{config.AnalysisPrompt}
		}
		migrateAnalysisPromptDefs()
		syncConfigToAtomics()
		return
	}

	// Migration: Falls config.json fehlt, versuchen wir aus Preferences zu laden
	if a != nil {
		p := a.Preferences()
		// Wir prüfen ein Schlüsselfeld, um zu sehen, ob Daten da sind
		if p.String("analysisMode") != "" {
			Log("Migration: Alte Einstellungen gefunden, konvertiere nach config.json...")
			config.AppMode = p.StringWithFallback("appMode", config.AppMode)
			config.Theme = p.StringWithFallback("theme", config.Theme)
			config.AnalysisPrompt = p.StringWithFallback("analysisPrompt", config.AnalysisPrompt)
			oldMode := p.StringWithFallback("analysisMode", "")
			switch oldMode {
			case "Lokal (Gemma 4)", "Gemma 4":
				config.AnalysisMode = "Gemma4 (lokal)"
			case "Google Gemini (Flash)":
				config.AnalysisMode = "Google Flash"
			case "Remote Ollama":
				config.AnalysisMode = "Ollama"
			default:
				config.AnalysisMode = "Gemma4 (lokal)"
			}
			config.GeminiApiKey = p.String("geminiApiKey")
			config.OllamaUrl = p.StringWithFallback("ollamaUrl", config.OllamaUrl)
			config.OllamaModel = p.StringWithFallback("ollamaModel", config.OllamaModel)
			config.VllmUrl = p.StringWithFallback("vllmUrl", config.VllmUrl)
			config.VllmModel = p.StringWithFallback("vllmModel", config.VllmModel)
			config.PauseThreshold = p.FloatWithFallback("pauseThreshold", config.PauseThreshold)
			config.LoggingEnabled = p.BoolWithFallback("loggingEnabled", config.LoggingEnabled)
			config.MicName = p.StringWithFallback("micName", config.MicName)
			config.SpeakerName = p.StringWithFallback("speakerName", config.SpeakerName)
			config.SttPipeline = p.StringWithFallback("sttPipeline", config.SttPipeline)
			config.WinWidth = float32(p.FloatWithFallback("winWidth", 1000))
			config.WinHeight = float32(p.FloatWithFallback("winHeight", 800))
			config.WinX = int32(p.IntWithFallback("winX", 100))
			config.WinY = int32(p.IntWithFallback("winY", 100))
			config.WinShowCmd = int32(p.IntWithFallback("winShowCmd", 1))
			config.PhysX = int32(p.IntWithFallback("physX", 100))
			config.PhysY = int32(p.IntWithFallback("physY", 100))
			config.PhysWidth = int32(p.IntWithFallback("physWidth", 1000))
			config.PhysHeight = int32(p.IntWithFallback("physHeight", 800))
			config.MicGain = p.FloatWithFallback("micGain", 1.0)
			config.SpkGain = p.FloatWithFallback("spkGain", 1.0)

			migrateLegacyBackendFields()
			migrateLegacyPipelineFields()
			SaveConfig()
			Log("Migration abgeschlossen.")

			// Alte Einstellungen löschen (Bereinigung)
			// Da Fyne kein 'ClearAll' hat, setzen wir die wichtigsten Felder zurück
			p.SetString("analysisMode", "")
			p.SetString("geminiApiKey", "")
			p.SetString("appMode", "")
		}
	}
	// Auch ohne config.json (Erststart/Preferences-Migration) die Vorgaben-
	// Paare aus den Alt-Feldern bzw. Defaults aufbauen.
	migrateAnalysisPromptDefs()
}

// migrateAnalysisPromptDefs baut die Analyse-Vorgaben (Beschreibung+Prompt)
// aus den Alt-Feldern auf: die fruehere Liste enthielt nur Prompt-TEXTE, die
// zugleich im Dropdown standen - der Text wird darum 1:1 als Beschreibung
// uebernommen (umbenennen geht jederzeit in "Einstellungen"). Zusaetzlich
// wird sichergestellt, dass die gewaehlte Beschreibung existiert.
func migrateAnalysisPromptDefs() {
	if len(config.AnalysisPromptDefs) == 0 {
		for _, p := range config.AnalysisPrompts {
			if strings.TrimSpace(p) == "" {
				continue
			}
			config.AnalysisPromptDefs = append(config.AnalysisPromptDefs, AnalysisPromptDef{Name: p, Prompt: p})
		}
		// Der bisher aktive Prompt war nicht zwingend Teil der alten Liste
		// (er wurde erst beim Analysieren uebernommen) - nicht verlieren.
		if p := strings.TrimSpace(config.AnalysisPrompt); p != "" && analysisPromptByName(p) == nil {
			config.AnalysisPromptDefs = append(config.AnalysisPromptDefs, AnalysisPromptDef{Name: p, Prompt: p})
		}
		// Der bisher aktive Prompt bleibt die aktive Vorgabe (Name = alter Text).
		if config.AnalysisPromptName == "" {
			config.AnalysisPromptName = strings.TrimSpace(config.AnalysisPrompt)
		}
	}
	if analysisPromptByName(config.AnalysisPromptName) == nil {
		config.AnalysisPromptName = ""
		if len(config.AnalysisPromptDefs) > 0 {
			config.AnalysisPromptName = config.AnalysisPromptDefs[0].Name
		}
	}
}

// analysisPromptByName liefert die gespeicherte Analyse-Vorgabe zur
// Beschreibung (nil, wenn unbekannt).
func analysisPromptByName(name string) *AnalysisPromptDef {
	for i := range config.AnalysisPromptDefs {
		if config.AnalysisPromptDefs[i].Name == name {
			return &config.AnalysisPromptDefs[i]
		}
	}
	return nil
}

// analysisPromptNames: alle Beschreibungen in Listen-Reihenfolge (Optionen
// der beiden Pulldowns in STT-Tab und "Einstellungen").
func analysisPromptNames() []string {
	out := make([]string, 0, len(config.AnalysisPromptDefs))
	for _, d := range config.AnalysisPromptDefs {
		out = append(out, d.Name)
	}
	return out
}

// currentBackend liefert die Konfiguration des in der 'remote LLM'-Sektion
// gewählten Remote-Backends (Flash/Ollama/vLLM).
func currentBackend() *BackendCfg {
	switch config.RemoteBackend {
	case "Google Flash":
		return &config.Flash
	case "Ollama":
		return &config.Ollama
	case "vLLM":
		return &config.Vllm
	}
	return nil
}

// remoteConfigured prüft, ob das aktuell gewählte Remote-Backend nutzbar konfiguriert ist.
func remoteConfigured() bool {
	switch config.RemoteBackend {
	case "Google Flash":
		return config.Flash.ApiKey != ""
	case "Ollama":
		return config.Ollama.Url != "" && config.Ollama.Model != ""
	case "vLLM":
		return config.Vllm.Url != "" && config.Vllm.Model != ""
	}
	return false
}

// migrateLegacyPipelineFields überführt die alten Felder (SttPipeline/AnalysisMode/
// LocalGemmaModel) einmalig in die neue Pipeline-Struktur und leert sie danach.
func migrateLegacyPipelineFields() {
	if config.AnalysisMode == "" && config.SttPipeline == "" {
		return // bereits neue Struktur
	}
	switch config.AnalysisMode {
	case "Google Flash", "Ollama", "vLLM":
		config.AnalysisModel = "remote"
		config.RemoteBackend = config.AnalysisMode
	case "Gemma4 (lokal)", "Gemma 4":
		if config.LocalGemmaModel == "gemma-4-12b-it-q8.gguf" {
			config.AnalysisModel = "12b"
		} else {
			config.AnalysisModel = "e2b"
		}
	}
	if config.SttPipeline == "Whisper + LLM" {
		config.PostProcModel = "remote"
	}
	config.WhisperLocal = config.SttPipeline != "Gemma Native"
	if config.RemoteBackend == "" {
		config.RemoteBackend = "vLLM"
	}
	config.AnalysisMode, config.SttPipeline, config.LocalGemmaModel = "", "", ""
}

// migrateLegacyBackendFields überträgt die alten Flach-Felder einmalig in die
// neuen Backend-Tripel und leert sie danach, damit sie nicht erneut persistiert
// werden. Idempotent: überschreibt keine bereits gesetzten neuen Werte.
func migrateLegacyBackendFields() {
	if config.GeminiApiKey != "" && config.Flash.ApiKey == "" {
		config.Flash.ApiKey = config.GeminiApiKey
	}
	if config.OllamaUrl != "" && config.Ollama.Url == "" {
		config.Ollama.Url = config.OllamaUrl
	}
	if config.OllamaModel != "" && config.Ollama.Model == "" {
		config.Ollama.Model = config.OllamaModel
	}
	if config.VllmUrl != "" && config.Vllm.Url == "" {
		config.Vllm.Url = config.VllmUrl
	}
	if config.VllmModel != "" && config.Vllm.Model == "" {
		config.Vllm.Model = config.VllmModel
	}
	config.GeminiApiKey, config.OllamaUrl, config.OllamaModel = "", "", ""
	config.VllmUrl, config.VllmModel = "", ""
}

// pill ist ein kleiner Status-Indikator (farbiger Punkt + Text).
type pill struct {
	dot *canvas.Text
	lbl *widget.Label
	box *fyne.Container
}

func newPill() *pill {
	dot := canvas.NewText("●", color.NRGBA{R: 190, G: 40, B: 40, A: 255})
	dot.TextSize = 16
	lbl := trLabel("…")
	return &pill{dot: dot, lbl: lbl, box: container.NewHBox(dot, lbl)}
}

// set färbt den Punkt grün (ok) bzw. rot und setzt den Text. Main-Thread.
func (p *pill) set(ok bool, text string) {
	if ok {
		p.dot.Color = color.NRGBA{R: 40, G: 160, B: 60, A: 255}
	} else {
		p.dot.Color = color.NRGBA{R: 190, G: 40, B: 40, A: 255}
	}
	p.dot.Refresh()
	p.lbl.SetText(text)
}

// showInfo zeigt einen Info-Dialog mit LINKSBÜNDIGEM Text (der Standard-Dialog
// dialog.ShowInformation zentriert den Text).
func showInfo(title, msg string, parent fyne.Window) {
	lbl := widget.NewLabel(msg)
	lbl.Alignment = fyne.TextAlignLeading
	dialog.ShowCustom(title, T("OK"), lbl, parent)
}

// showErr zeigt eine Fehlermeldung mit LINKSBÜNDIGEM Text (Ersatz für das
// zentrierende dialog.ShowError).
func showErr(err error, parent fyne.Window) {
	lbl := widget.NewLabel(err.Error())
	lbl.Alignment = fyne.TextAlignLeading
	dialog.ShowCustom(T("Fehler"), T("OK"), lbl, parent)
}

// showConfirm zeigt eine Ja/Nein-Sicherheitsabfrage mit LINKSBÜNDIGEM Text
// (Ersatz für das zentrierende dialog.ShowConfirm).
func showConfirm(title, msg string, cb func(bool), parent fyne.Window) {
	lbl := widget.NewLabel(msg)
	lbl.Alignment = fyne.TextAlignLeading
	dialog.ShowCustomConfirm(title, T("Ja"), T("Nein"), lbl, cb, parent)
}

// lastStatusDE/lastEngineDE halten den zuletzt gesetzten DEUTSCHEN Text der
// dynamischen Statusanzeigen. Diese Labels ändern ihren Text zur Laufzeit und
// können daher keinen statischen trXxx-Callback nutzen; stattdessen merken wir
// den deutschen Schlüssel und übersetzen ihn bei Sprachwechsel neu (Callback in
// main()). setStatus/setEngineInfo sind die einzigen Schreibpfade.
var lastStatusDE = "Initialisiere..."
var lastEngineDE = "Engine: Wartet..."

func setStatus(de string) {
	lastStatusDE = de
	if statusLabel != nil {
		statusLabel.SetText(T(de))
	}
}

func setEngineInfo(de string) {
	lastEngineDE = de
	if engineInfo != nil {
		engineInfo.SetText(T(de))
	}
}

// debugPreviewAndConfirm zeigt im Debug-Modus (config.DebugMode) vor dem
// eigentlichen Versand ein Popup mit dem Inhalt der Anfrage ("Senden" führt
// proceed aus, "Abbrechen" bricht ab, ohne zu senden). Außerhalb des
// Debug-Modus wird proceed sofort ausgeführt.
func debugPreviewAndConfirm(win fyne.Window, title, payload string, proceed func()) {
	if !config.DebugMode {
		proceed()
		return
	}
	// MultiLineEntry statt Label: Label unterstuetzt kein Markieren/Kopieren.
	// OnChanged setzt Bearbeitungen sofort zurueck (nur lesbar, aber der Text
	// bleibt markier- und kopierbar) - Aendern hier haette ohnehin keinen
	// Einfluss auf die bereits gebaute Anfrage.
	body := widget.NewMultiLineEntry()
	body.SetText(payload)
	body.Wrapping = fyne.TextWrapWord
	body.OnChanged = func(s string) {
		if s != payload {
			body.SetText(payload)
		}
	}
	scroll := container.NewScroll(body)
	scroll.SetMinSize(fyne.NewSize(560, 320))
	dialog.ShowCustomConfirm(title, T("Senden"), T("Abbrechen"), scroll, func(send bool) {
		if send {
			proceed()
		}
	}, win)
}

// showDebugResponse zeigt – nur bei aktivem Debug-Modus (config.DebugMode) –
// eine Serverantwort in einem scrollbaren, markier-/kopierbaren Popup. Gegen-
// stück zu debugPreviewAndConfirm (das die Anfrage VOR dem Senden zeigt).
func showDebugResponse(title, payload string) {
	if !config.DebugMode {
		return
	}
	body := widget.NewMultiLineEntry()
	body.SetText(payload)
	body.Wrapping = fyne.TextWrapWord
	body.OnChanged = func(s string) {
		if s != payload {
			body.SetText(payload) // nur lesbar, aber markier-/kopierbar
		}
	}
	scroll := container.NewScroll(body)
	scroll.SetMinSize(fyne.NewSize(560, 320))
	dialog.ShowCustom(title, T("OK"), scroll, mainWin)
}

// loadF64 liest einen als atomic.Uint64 gespiegelten float64-Wert.
func loadF64(a *atomic.Uint64) float64 { return math.Float64frombits(a.Load()) }

// syncConfigToAtomics spiegelt die im Hintergrund-Hot-Path gelesenen Config-Felder
// in atomare Variablen. Muss nach jeder Config-Änderung aufgerufen werden.
func syncConfigToAtomics() {
	atMicGain.Store(math.Float64bits(config.MicGain))
	atSpkGain.Store(math.Float64bits(config.SpkGain))
	atPauseThresh.Store(math.Float64bits(config.PauseThreshold))
	atHeadsetMode.Store(config.AppMode == "Headset-Betrieb")
	atWhisperLocal.Store(config.WhisperLocal)
	atHasPostProc.Store(config.PostProcModel != "" && config.PostProcModel != "none")
	atLogging.Store(config.LoggingEnabled)
}

func writeConfigFile() {
	configPath := filepath.Join(exeDir, "config.json")
	data, _ := json.MarshalIndent(config, "", "  ")
	os.WriteFile(configPath, data, 0644)
}

// SaveConfig aktualisiert die Atomics und schreibt die config.json sofort.
func SaveConfig() {
	syncConfigToAtomics()
	writeConfigFile()
}

// saveConfigDebounced aktualisiert die Atomics sofort, verzögert aber das teure
// Schreiben der Datei. Für Slider/Eingaben, die pro Tick/Tastendruck feuern.
func saveConfigDebounced() {
	syncConfigToAtomics()
	saveTimerMu.Lock()
	defer saveTimerMu.Unlock()
	if saveTimer != nil {
		saveTimer.Stop()
	}
	saveTimer = time.AfterFunc(600*time.Millisecond, writeConfigFile)
}

func main() {
	// EXE-Pfad global bestimmen
	p, _ := os.Executable()
	exeDir = filepath.Dir(p)

	// Mehrfachstart verhindern: laeuft bereits eine Instanz, kurzer nativer
	// Hinweis und Ende (sonst kollidieren u.a. Webhook-Port, llama-Server-
	// Ports und die Audio-Geraete der beiden Instanzen).
	if !ensureSingleInstance() {
		notifyAlreadyRunning()
		return
	}

	// 1. App & Theme (zuerst init für Migration)
	myApp := app.NewWithID("com.sst.gemma.hybrid")
	appIcon := fyne.NewStaticResource("app_icon.png", appIconPNG)
	myApp.SetIcon(appIcon) // Taskleisten-/Programmsymbol zur Laufzeit

	// Konfiguration laden (mit Migration)
	LoadConfig(myApp)
	initLang() // App-Sprache (currentLang) aus config.JarvisLang setzen, vor UI-Aufbau

	// Theme laden & anwenden (Hell = Windows-Look, sonst Dunkel)
	applyTheme(myApp, config.Theme)

	win := myApp.NewWindow("")
	mainWin = win // paketweiter Zugriff (z.B. Debug-Popup des Rufnummern-Webhooks)
	win.SetMaster()
	// Fenster-Icon transparent setzen; das (frueher unscharfe, weiss
	// hinterlegte) Titelleisten-Symbol wird unter Windows zusaetzlich NATIV
	// aus dem mehrstufigen .exe-Icon gesetzt (applyCrispWindowIcon, s.u.) -
	// Windows bekommt so exakt passende 16/32-px-Bilder statt dieses grosse
	// PNG selbst herunterzuskalieren.
	win.SetIcon(appIcon)
	updateWindowTitle(win)

	// Fenstergröße & Position wiederherstellen
	if config.WinWidth > 0 && config.WinHeight > 0 {
		win.Resize(fyne.NewSize(config.WinWidth, config.WinHeight))
	}

	// Close-Intercept zum Speichern der GUI-Metriken (Größe & Position)
	win.SetCloseIntercept(func() {
		config.WinWidth = win.Canvas().Size().Width
		config.WinHeight = win.Canvas().Size().Height
		saveWindowPosition(win)
		SaveConfig()
		stopWebhookServer()
		stopAllServers()
		closeAllRemoteSessions()
		win.Close()
	})

	// 2. UI Widgets
	outputArea = widget.NewMultiLineEntry()
	trPlaceholder(outputArea, "Gesprochener Text erscheint hier...")
	outputArea.Wrapping = fyne.TextWrapWord

	statusLabel = widget.NewLabel(T("Initialisiere...")) // dynamisch: SetText-Aufrufe nutzen T()
	statusLabel.Alignment = fyne.TextAlignCenter
	statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	engineInfo = widget.NewLabel(T("Engine: Wartet..."))
	engineInfo.Alignment = fyne.TextAlignTrailing // rechts in der Statuszeile (statusBar)

	progressBar = widget.NewProgressBar()
	progressBar.Hide()

	analysisProgress = widget.NewProgressBarInfinite()
	analysisProgress.Hide()

	agentLevel = widget.NewProgressBar()
	agentLevel.Min = 0
	agentLevel.Max = 1

	callerLevel = widget.NewProgressBar()
	callerLevel.Min = 0
	callerLevel.Max = 1

	agentMeter = newLevelMeter(agentLevel, &agentMarkerVal)
	callerMeter = newLevelMeter(callerLevel, &callerMarkerVal)

	// Peak-Hold langsam abklingen lassen, damit ein einzelner lauter Ausschlag den
	// Richtwert nicht dauerhaft hochhält. Neue, höhere Pegel heben ihn im Callback.
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			fyne.Do(func() {
				if agentMarkerVal > 0.005 {
					agentMarkerVal *= 0.96
					if agentMeter != nil {
						agentMeter.Refresh()
					}
				}
				if callerMarkerVal > 0.005 {
					callerMarkerVal *= 0.96
					if callerMeter != nil {
						callerMeter.Refresh()
					}
				}
			})
		}
	}()

	// Pulse Animation - entfernt: nutzlos und erzeugt Data Race

	micBtn = newTooltipButton(nil, toggleRecording, "Mitschrift")
	micBtn.SetText(T("Mitschrift"))
	micBtn.Importance = widget.HighImportance
	micBtn.Disable()

	// Dynamische Statuselemente bei Sprachwechsel neu übersetzen (sie halten
	// ihren zuletzt gesetzten deutschen Schlüssel bzw. leiten aus dem Zustand ab).
	onLangChange(func() {
		if statusLabel != nil {
			statusLabel.SetText(T(lastStatusDE))
		}
		if engineInfo != nil {
			engineInfo.SetText(T(lastEngineDE))
		}
		if micBtn != nil {
			if isRecording.Load() {
				micBtn.SetText(T("Mitschrift stoppen"))
			} else {
				micBtn.SetText(T("Mitschrift"))
			}
		}
	})

	InitLogger()
	Log("GUI gestartet")

	// Serieller Worker für die Whisper+LLM-Satzkorrektur (siehe correctionWorker).
	go correctionWorker()

	// 3. Audio & Engine Run
	go func() {
		fyne.Do(func() { progressBar.Show() })
		err := EnsureDependencies(func(task string, p float64) {
			fyne.Do(func() {
				statusLabel.SetText(task)
				progressBar.SetValue(p)
			})
		})
		fyne.Do(func() { progressBar.Hide() })

		if err != nil {
			fyne.Do(func() { setStatus("Fehler bei Dependencies!") })
			return
		}
		fyne.Do(func() {
			setStatus("Bereit")
			micBtn.Enable()
		})

		// Lokale llama-server bedarfsgesteuert starten (Nachbearbeitung/Analyse).
		go ensureLocalServers()

		prepareAudio()
	}()

	modeIcon = widget.NewIcon(theme.SettingsIcon()) // Platzhalter
	modeIcon.Hide()

	// Analyse-Vorgabe (STT-Tab): reines Pulldown der BESCHREIBUNGEN. Die
	// Vorgaben selbst (Beschreibung + Prompt) werden im Reiter "Einstellungen"
	// unter "Spracherkennung und Analyse" gepflegt ("LLM Prompt zur Analyse").
	// Die Auswahl bestimmt, mit welchem Prompt "Analysieren" den erkannten
	// Text nachbearbeitet.
	analysisPromptSelect := widget.NewSelect(analysisPromptNames(), func(s string) {
		if config.AnalysisPromptName != s {
			config.AnalysisPromptName = s
			saveConfigDebounced()
		}
	})
	bindText(func(s string) {
		analysisPromptSelect.PlaceHolder = s
		analysisPromptSelect.Refresh()
	}, "(keine Vorgabe)")
	analysisPromptSelect.SetSelected(config.AnalysisPromptName)
	// refreshAnalysisPromptSelect zieht Optionen + Auswahl nach, wenn die
	// Vorgaben in "Einstellungen" geaendert wurden (Speichern/Loeschen dort).
	refreshAnalysisPromptSelect := func() {
		analysisPromptSelect.Options = analysisPromptNames()
		if analysisPromptByName(config.AnalysisPromptName) == nil {
			config.AnalysisPromptName = ""
			if len(config.AnalysisPromptDefs) > 0 {
				config.AnalysisPromptName = config.AnalysisPromptDefs[0].Name
			}
		}
		analysisPromptSelect.Selected = config.AnalysisPromptName
		analysisPromptSelect.Refresh()
	}

	analysisBtn := trButton("Analysieren", func() {
		if isRecording.Load() {
			toggleRecording()
		}
		text := outputArea.Text
		// Prompt der im Pulldown gewaehlten Beschreibung (gepflegt in
		// "Einstellungen"); ohne Auswahl laeuft die Analyse ohne Prompt.
		prompt := ""
		if def := analysisPromptByName(config.AnalysisPromptName); def != nil {
			prompt = def.Prompt
		}
		if len(strings.TrimSpace(text)) < 10 {
			Log("Analyse übersprungen: Zu wenig Text")
			showErr(fmt.Errorf(T("Zu wenig Text für eine Analyse vorhanden.")), win)
			return
		}

		backendDesc := modelLabelFromSymbol(config.AnalysisModel)
		if config.AnalysisModel == "remote" {
			backendDesc = "remote (" + config.RemoteBackend + ")"
		}
		preview := fmt.Sprintf("Backend: %s\n\nPrompt:\n%s\n\nText (%d Zeichen):\n%s", backendDesc, prompt, len(text), text)

		debugPreviewAndConfirm(win, "Analyse-Anfrage", preview, func() {
			Log(fmt.Sprintf("KI-Analyse gestartet (Prompt: '%s' | Text-Länge: %d)", prompt, len(text)))
			setStatus("Analysiere...")
			analysisProgress.Show()

			start := time.Now()
			go func() {
				res := runAnalysisLogic(text, prompt)
				duration := time.Since(start).Seconds()
				Log(fmt.Sprintf("KI-Analyse abgeschlossen in %.1fs", duration))

				// Gesamter UI-Aufbau muss auf dem Main-Thread laufen.
				fyne.Do(func() {
					analysisProgress.Hide()
					win.Canvas().Refresh(statusLabel)

					resRichText := widget.NewRichTextFromMarkdown(res)
					resRichText.Wrapping = fyne.TextWrapWord

					copyBtn := widget.NewButtonWithIcon(T("In Zwischenablage kopieren"), theme.ContentCopyIcon(), func() {
						cleanText := strings.ReplaceAll(res, "**", "")
						cleanText = strings.ReplaceAll(cleanText, "## ", "")
						cleanText = strings.ReplaceAll(cleanText, "### ", "")
						copyToClipboardRich(cleanText, res)
						Log("Analyse-Ergebnis (Rich Text) in Zwischenablage kopiert")
					})

					scrollContainer := container.NewScroll(resRichText)
					scrollContainer.SetMinSize(fyne.NewSize(600, 300))

					dContent := container.NewBorder(nil, copyBtn, nil, nil, scrollContainer)
					win2 := fyne.CurrentApp().NewWindow(fmt.Sprintf("KI-Analyse Ergebnis (%.1f s)", duration))
					win2.SetContent(dContent)
					win2.Resize(fyne.NewSize(700, 500))
					moveWindowNear(win2, win)
					win2.Show()

					setStatus("Bereit")
				})
			}()
		})
	})
	analysisBtn.Importance = widget.HighImportance

	// 5. Layout TTS (ehemals Diktat)
	// Gain Slider für die Hauptansicht vorbereiten
	gainLabel := widget.NewLabel(fmt.Sprintf("%.1fx", config.MicGain))
	gainSlider := widget.NewSlider(1.0, 20.0)
	gainSlider.Step = 0.5
	gainSlider.SetValue(config.MicGain)
	gainSlider.OnChanged = func(f float64) {
		config.MicGain = f
		gainLabel.SetText(fmt.Sprintf("%.1fx", f))
		saveConfigDebounced()
		agentMarkerVal = 0 // Richtwert neu ermitteln nach Gain-Änderung
		if agentMeter != nil {
			agentMeter.Refresh()
		}
	}

	// CRM-Zeile zwischen Statuszeile ("Bereit") und Start-Button:
	// "CRM <wert> [copy]  Kundenv. ID <address_id> [copy]".
	// CRM wird per Rufnummern-Webhook (s. webhook.go) mit dem Jira-Issue-Key des
	// zur eingehenden Rufnummer passenden Tickets befuellt bzw. "-" (nicht
	// gefunden). Default "-" (noch kein Anruf). Reines Anzeige-Label (kein
	// Textfeld mehr): einzige Schreibquelle ist setCustomerField (webhook.go),
	// das auch den CRM-Status (currentCRM) pflegt und die Ticketliste leert.
	customerField = widget.NewLabel("-")
	setCurrentCRM(validCRM(customerField.Text)) // Startwert setzen
	crmCopyBtn := newTooltipButton(theme.ContentCopyIcon(), func() {
		if v := strings.TrimSpace(customerField.Text); v != "" && v != "-" {
			mainWin.Clipboard().SetContent(v)
		}
	}, "CRM in die Zwischenablage kopieren")
	// "Kundenv. ID": address_id der Kundenverwaltung zum Anrufer (gesetzt von
	// performIBSLookup via setIBSAddressField, s. ibs_client.go). Der ganze
	// Block ist nur sichtbar, wenn URL + API-Key der Kundenverwaltung
	// hinterlegt sind (Sichtbarkeit schaltet refreshIBSCheck, jarvis_client.go).
	ibsAddressField = widget.NewLabel("-")
	ibsCopyBtn := newTooltipButton(theme.ContentCopyIcon(), func() {
		if v := strings.TrimSpace(ibsAddressField.Text); v != "" && v != "-" {
			mainWin.Clipboard().SetContent(v)
		}
	}, "Kundenverwaltungs-ID in die Zwischenablage kopieren")
	ibsIDBox = container.NewHBox(trLabel("Kundenv. ID"), ibsAddressField, ibsCopyBtn)
	customerRow := container.NewHBox(trLabel("CRM"), customerField, crmCopyBtn, ibsIDBox)

	// Label zwischen Feld und Start-Button: zeigt die per Webhook empfangene
	// Rufnummer des aktuellen Anrufers (leer, bis der erste Anruf eingeht).
	callerNumberLabel = widget.NewLabel("")

	// Wiederhol-Button: startet die CRM-Abfrage zur zuletzt empfangenen Rufnummer
	// erneut (inkl. Auswahl-Popup bei mehreren Treffern). Nur Symbol + Mouseover-
	// Tooltip. In einer Goroutine, da handleIncomingCaller fyne.Do nutzt und der
	// Tap bereits im UI-Thread läuft.
	repeatLookupBtn := newTooltipButton(theme.ViewRefreshIcon(), func() {
		num := getLastCallerNumber()
		if num == "" {
			showInfo(T("Keine Rufnummer"), T("Es liegt noch keine Rufnummer eines Anrufs vor, die wiederholt werden könnte."), mainWin)
			return
		}
		go handleIncomingCaller(num, false)
	}, "CRM-Abfrage zur Rufnummer wiederholen")

	// Mindestens 20 px Abstand zwischen Wiederhol-Button und Start-Button.
	btnGap := canvas.NewRectangle(color.Transparent)
	btnGap.SetMinSize(fyne.NewSize(20, 0))

	statusAndStart := container.NewBorder(
		nil, nil,
		statusLabel,
		container.NewHBox(callerNumberLabel, repeatLookupBtn, btnGap, micBtn), // Rufnummer-Label, Wiederhol-Button, Abstand, Start-Button
		customerRow,
	)

	// 3-Pixel-Abstand zum oberen Rand
	topGap := canvas.NewRectangle(color.Transparent)
	topGap.SetMinSize(fyne.NewSize(0, 3))

	spkGainLabel = widget.NewLabel(fmt.Sprintf("%.1fx", config.SpkGain))
	spkGainSlider = widget.NewSlider(1.0, 20.0)
	spkGainSlider.Step = 0.5
	spkGainSlider.SetValue(config.SpkGain)
	spkGainSlider.OnChanged = func(f float64) {
		config.SpkGain = f
		spkGainLabel.SetText(fmt.Sprintf("%.1fx", f))
		saveConfigDebounced()
		callerMarkerVal = 0 // Richtwert neu ermitteln nach Gain-Änderung
		if callerMeter != nil {
			callerMeter.Refresh()
		}
	}

	speakerSection := container.NewVBox(
		callerMeter,
		container.NewBorder(nil, nil, spkGainLabel, nil, spkGainSlider),
	)

	// Hilfe-Button zur Pegelanzeige – rechts in der Mic-Zeile. Hier definiert, damit
	// die Spk-Zeile mit einem gleich breiten Platzhalter rechtsbündig abschließt.
	pegelHelpBtn := widget.NewButtonWithIcon("", theme.QuestionIcon(), func() {
		lmT, lmB := helpLevelMeter()
		showInfo(lmT, lmB, win)
	})
	pegelHelpBtn.Importance = widget.LowImportance // kein grauer Button-Hintergrund fuer das Hilfe-Symbol
	spkSpacer := canvas.NewRectangle(color.Transparent)
	spkSpacer.SetMinSize(fyne.NewSize(pegelHelpBtn.MinSize().Width, 0))

	speakerControlGroup = container.NewBorder(nil, nil, trLabel("Spk:"), spkSpacer, speakerSection)

	if config.AppMode != "Headset-Betrieb" {
		speakerControlGroup.Hide()
	}

	// Drei Status-Indikatoren: Erkennung, Nachbearbeitung, Analyse - EINE globale
	// Instanz je Status, angezeigt in der Statuszeile ganz unten am Fenster
	// (tabuebergreifend sichtbar, wie bei anderen Windows-Anwendungen).
	recPill, postPill, anaPill := newPill(), newPill(), newPill()

	whisperReady := func() bool {
		bin := "whisper-cli"
		if runtime.GOOS == "windows" {
			bin = "whisper-cli.exe"
		}
		_, err := os.Stat(filepath.Join(exeDir, "libs", bin))
		return err == nil
	}
	// modelState liefert (verfügbar?, Anzeigetext) für ein Modell-Symbol.
	modelState := func(sym string) (bool, string) {
		switch sym {
		case "none":
			return true, T("ohne")
		case "e2b", "12b":
			inst := instanceFor(sym)
			return inst != nil && inst.ready.Load(), modelLabelFromSymbol(sym) // Modellname = Eigenname
		case "remote":
			return remoteConfigured(), "remote (" + config.RemoteBackend + ")"
		}
		return false, "—"
	}
	updatePills := func() {
		recOK, recTxt := true, T("Erkennung: Whisper lokal")
		if !config.WhisperLocal {
			recOK, recTxt = atRemoteWhisperOK.Load(), T("Erkennung: Remote Whisper (GPU)")
		} else if !whisperReady() {
			recOK = false
		}
		postOK, postTxt := modelState(config.PostProcModel)
		anaOK, anaTxt := modelState(config.AnalysisModel)

		recPill.set(recOK, recTxt)
		postPill.set(postOK, T("Nachbearb.: ")+postTxt)
		anaPill.set(anaOK, T("Analyse: ")+anaTxt)
	}
	onLangChange(updatePills) // Pills bei Sprachwechsel sofort neu beschriften
	// Periodischer Live-Check der lokalen Server (aktualisiert die ready-Flags).
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for range t.C {
			refreshServerHealth()
			if !atWhisperLocal.Load() {
				ok, detail := remoteWhisperHealth()
				// Debug: Zustandswechsel der Remote-Whisper-Erreichbarkeit
				// loggen (nur Wechsel, nicht alle 2 s).
				if atRemoteWhisperOK.Load() != ok {
					if ok {
						Log("Remote-Whisper erreichbar: " + remoteWhisperURL())
					} else {
						Log("Remote-Whisper NICHT erreichbar (/health): " + detail)
					}
				}
				atRemoteWhisperOK.Store(ok)
			}
			fyne.Do(updatePills)
		}
	}()

	// Mic+Spk-Pegelanzeige einklappbar (Pills/Status/Fortschrittsbalken bleiben
	// immer sichtbar). refreshDiktatTab wird unten gesetzt, sobald diktatTab
	// existiert (Fyne layoutet den Elterncontainer bei Hide()/Show() nicht
	// automatisch neu).
	var refreshDiktatTab func()
	meterContent := container.NewVBox(
		container.NewBorder(nil, nil, trLabel("Mic:"), pegelHelpBtn,
			container.NewVBox(
				agentMeter,
				container.NewBorder(nil, nil, gainLabel, nil, gainSlider),
			),
		),
		speakerControlGroup,
	)
	meterToggle := newCollapsibleSection("Mic + Spk", meterContent, config.SttMeterExpanded, func(exp bool) {
		config.SttMeterExpanded = exp
		saveConfigDebounced()
	}, func() {
		if refreshDiktatTab != nil {
			refreshDiktatTab()
		}
	})

	topControls := container.NewVBox(
		topGap,
		statusAndStart,
		progressBar,
		meterToggle,
		meterContent,
	)

	saveTranscriptBtn := widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() {
		text := outputArea.Text
		if len(strings.TrimSpace(text)) == 0 {
			showErr(fmt.Errorf(T("Kein Text zum Speichern vorhanden.")), win)
			return
		}
		filename := fmt.Sprintf("transkript_%s.txt", time.Now().Format("20060102_150405"))
		err := os.WriteFile(filename, []byte(text), 0644)
		if err != nil {
			showErr(err, win)
		} else {
			showInfo(T("Export erfolgreich"), T("Text gespeichert unter:")+"\n"+filename, win)
		}
	})

	// "Zwischenablage" (nur Symbol): kopiert die KOMPLETTE Mitschrift als Text
	// in die Zwischenablage. Als Bestaetigung erscheint fuer 2 Sekunden ein
	// selbstschliessender Hinweis mittig im Fenster (PopUp statt Dialog:
	// verschwindet ohne Klick; ein Klick irgendwohin schliesst frueher).
	copyTranscriptBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		text := outputArea.Text
		if len(strings.TrimSpace(text)) == 0 {
			showErr(fmt.Errorf(T("Kein Text zum Kopieren vorhanden.")), win)
			return
		}
		win.Clipboard().SetContent(text)
		Log(fmt.Sprintf("Mitschrift in Zwischenablage kopiert (%d Zeichen)", len(text)))
		hint := widget.NewLabelWithStyle(T("Mitschrift wurde in die Zwischenablage kopiert."), fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
		popup := widget.NewPopUp(container.NewPadded(hint), win.Canvas())
		cs := win.Canvas().Size()
		ps := popup.MinSize()
		popup.ShowAtPosition(fyne.NewPos((cs.Width-ps.Width)/2, (cs.Height-ps.Height)/2))
		go func() {
			time.Sleep(2 * time.Second)
			fyne.Do(popup.Hide)
		}()
	})

	clearBtn := trButton("Inhalt leeren", func() {
		// Sowohl Anzeige als auch internen Puffer zurücksetzen, damit neuer Text
		// nicht an den alten Inhalt angehängt wird (currentText speist appendToOutput).
		currentText.Reset()
		lastSpeaker = ""
		pendingRaw.Reset()
		pendingSpeaker = ""
		inProgress = nil
		outputArea.SetText("")
	})
	clearBtn.Importance = widget.HighImportance

	// "Suche passende Tickets": sucht zum erkannten Text (outputArea) passende
	// Jira-Tickets. Die eigentliche Suchlogik liegt im KI-Support-Panel
	// (buildKISupportPanel), das weiter unten gebaut wird - searchMatchingTickets
	// (paketweit) wird nach dem Panel-Bau zugewiesen. auto=false: manueller Klick
	// mit Debug-Popup (falls aktiv).
	var ticketSearchBtn *widget.Button
	ticketSearchBtn = trButtonIcon("Suche passende Tickets", theme.SearchIcon(), func() {
		text := outputArea.Text
		// Jira-Ticketsuche nur, wenn eine Jira-Quelle angehakt ist (dasselbe
		// Gate wie beim Anruf, s. wantJiraCallTickets in webhook.go) UND eine
		// gueltige CRM vorliegt. Beides sonst nur im Log - KEIN Extra-Popup
		// neben den Schlagworten (Nutzer-Vorgabe).
		runSearch := func() {}
		switch {
		case !wantJiraCallTickets():
			Log("Suche passende Tickets: Jira-Suche übersprungen (keine Jira-Quelle angehakt)")
		case !hasCRM():
			Log("Suche passende Tickets: Jira-Suche übersprungen (keine gültige CRM-Nummer)")
		default:
			runSearch = func() {
				if searchMatchingTickets != nil {
					// crmFallback=true: bei leerem Textfenster alle Tickets zur CRM laden.
					searchMatchingTickets(text, ticketSearchBtn, false, true)
				}
			}
		}
		// (Fast) leeres Textfenster: Schlagwort-Extraktion nicht moeglich -
		// passenden Hinweis zeigen und (falls sinnvoll) direkt suchen.
		if len(strings.TrimSpace(text)) < 10 {
			switch {
			case wantJiraCallTickets() && !hasCRM():
				showErr(fmt.Errorf(T("Im CRM Feld steht keine gültige CRM-Nummer. Die Ticketsuche ist erst mit einer CRM möglich.")), win)
			case wantJiraCallTickets():
				showInfo(T("Suche passende Tickets"),
					T("Das Textfenster ist leer – es werden alle Tickets zur gefundenen CRM geladen.\nSchlagworte können erst extrahiert werden, wenn eine Mitschrift vorliegt."), win)
				runSearch()
			default: // nur IBS: keine Suche, keine Schlagworte
				showInfo(T("Suche passende Tickets"),
					T("Das Textfenster ist leer – es können keine Schlagworte extrahiert werden."), win)
			}
			return
		}
		// Nutzer-Vorgabe: ZUERST die Schlagworte extrahieren und ANZEIGEN,
		// erst DANACH startet die eigentliche Ticketsuche (runSearch laeuft
		// als Abschluss-Callback der Extraktion - auch im Fehlerfall, das
		// Fehler-Popup kommt dann als Erstes). Button solange sperren.
		// Schritt 2 (Abfrage der APIs MIT den Schlagworten) steht in TODO.md -
		// Jarvis/Kundenverwaltung muessen dafuer erst angepasst werden.
		ticketSearchBtn.Disable()
		extractTicketKeywords(text, win, func(keywords string) {
			ticketSearchBtn.Enable()
			runSearch()
			// Kundenverwaltung mit den Schlagworten abfragen (getMatchingEvents),
			// eingegrenzt auf den aktuellen Anrufer (currentIBSAddrID). Die
			// Funktion prueft selbst, ob IBS aktiv ist, und ueberspringt sonst.
			if strings.TrimSpace(keywords) != "" {
				go performIBSBuzzwordSearch(currentIBSAddrID, keywords)
			}
		})
	})

	diktatTab := container.NewBorder(
		topControls,
		container.NewVBox(
			// engineInfo lebt jetzt in der Statuszeile ganz unten (statusBar),
			// nicht mehr mitten im STT-Tab.
			analysisProgress,
			// Analyse-Zeile: links das Beschreibungs-Pulldown der Analyse-
			// Vorgaben (Pflege der Vorgaben im Reiter "Einstellungen"),
			// rechts der Analysieren-Button.
			container.NewBorder(nil, nil,
				trLabelStyle("Analyse-Vorgabe:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				analysisBtn,
				analysisPromptSelect,
			),
		),
		nil, nil,
		// Erkennungs-Textfenster, darunter direkt "Inhalt leeren",
		// Ticketsuche, "Zwischenablage" (Symbol) und "Speichern" (Symbol).
		container.NewBorder(nil, container.NewHBox(clearBtn, ticketSearchBtn, copyTranscriptBtn, saveTranscriptBtn), nil, nil, outputArea),
	)
	refreshDiktatTab = func() {
		topControls.Refresh()
		topControls.Resize(topControls.MinSize())
		diktatTab.Refresh()
	}

	// Audio Device Selection Placeholder
	var speakerSelect *widget.Select

	// Mode Switcher (RadioGroup) - Label/Wert-Trennung: interne Werte
	// "Standard-Betrieb"/"Headset-Betrieb" (in config.AppMode gespeichert, an
	// mehreren Stellen verglichen) bleiben fest, angezeigt werden übersetzte
	// Labels. currentMode hält den internen Wert sprachunabhängig.
	modeValues := []string{"Standard-Betrieb", "Headset-Betrieb"}
	modeLabels := func() []string {
		out := make([]string, len(modeValues))
		for i, v := range modeValues {
			out[i] = T(v)
		}
		return out
	}
	valueForModeLabel := func(label string) string {
		for _, v := range modeValues {
			if T(v) == label {
				return v
			}
		}
		return "Standard-Betrieb"
	}
	currentMode := config.AppMode
	if currentMode != "Headset-Betrieb" {
		currentMode = "Standard-Betrieb"
	}
	suppressModeChange := false
	modeRadio := widget.NewRadioGroup(modeLabels(), func(label string) {
		if suppressModeChange {
			return
		}
		s := valueForModeLabel(label)
		currentMode = s
		config.AppMode = s
		SaveConfig()
		if s == "Standard-Betrieb" {
			if speakerSelect != nil {
				speakerSelect.Disable()
			}
			speakerControlGroup.Hide()
		} else {
			if speakerSelect != nil {
				speakerSelect.Enable()
			}
			speakerControlGroup.Show()
		}
		Log("Betriebsmodus gewechselt: " + s)
	})
	modeRadio.Horizontal = true
	modeRadio.SetSelected(T(currentMode))
	onLangChange(func() {
		suppressModeChange = true
		modeRadio.Options = modeLabels()
		modeRadio.SetSelected(T(currentMode))
		suppressModeChange = false
		modeRadio.Refresh()
	})

	modeInfoBtn := newTooltipButton(theme.InfoIcon(), func() {
		helpTitle, infoText := helpHeadset()
		// "Soundeinstellungen ändern" hier bewusst mit T() (transientes Fenster,
		// kein trButtonIcon/Callback - das Fenster wird bei jedem Klick neu erzeugt).
		content := container.NewVBox(
			widget.NewLabel(infoText),
			widget.NewButtonWithIcon(T("Soundeinstellungen ändern"), theme.SettingsIcon(), func() {
				cmd := exec.Command("cmd", "/c", "start", "ms-settings:sound")
				cmd.Start()
			}),
		)
		winHelp := fyne.CurrentApp().NewWindow(helpTitle)
		winHelp.SetContent(content)
		winHelp.Resize(fyne.NewSize(550, 500))
		moveWindowNear(winHelp, win)
		winHelp.Show()
	}, "Hilfe zum Exklusivmodus anzeigen")
	modeInfoBtn.Importance = widget.LowImportance // kein grauer Button-Hintergrund fuer das Info-Symbol

	// Audio Device Selection
	micNames, micMap := getDeviceList(malgo.Capture)
	micSelect := widget.NewSelect(micNames, func(s string) {
		id := micMap[s]
		selectedMicID = &id
		config.MicName = s
		SaveConfig()
		Log("Mikrofon gewechselt: " + s)
		prepareAudio()
	})
	micSelect.PlaceHolder = "Standard-Mikrofon"
	if config.MicName != "System-Standard" {
		micSelect.SetSelected(config.MicName)
	}

	speakerNames, speakerMap := getDeviceList(malgo.Playback)
	speakerSelect = widget.NewSelect(speakerNames, func(s string) {
		id := speakerMap[s]
		selectedSpeakerID = &id
		config.SpeakerName = s
		SaveConfig()
		Log("Lautsprecher gewechselt: " + s)
		prepareAudio()
	})
	speakerSelect.PlaceHolder = "Standard-Lautsprecher"
	if config.AppMode != "Headset-Betrieb" {
		speakerSelect.Disable()
	}
	if config.SpeakerName != "System-Standard" {
		speakerSelect.SetSelected(config.SpeakerName)
	}

	// Pause Slider
	pauseLabel := widget.NewLabel(fmt.Sprintf(T("Satzpause: %.1fs"), config.PauseThreshold))
	onLangChange(func() { pauseLabel.SetText(fmt.Sprintf(T("Satzpause: %.1fs"), config.PauseThreshold)) })
	pauseSlider := widget.NewSlider(0.5, 5.0)
	pauseSlider.Step = 0.5
	pauseSlider.SetValue(config.PauseThreshold)
	pauseSlider.OnChanged = func(v float64) {
		config.PauseThreshold = v
		saveConfigDebounced()
		pauseLabel.SetText(fmt.Sprintf(T("Satzpause: %.1fs"), v))
	}

	// Logging Toggle
	logCheck := trCheck("System-Logging aktivieren", func(b bool) {
		config.LoggingEnabled = b
		SaveConfig()
		if b {
			Log("Logging aktiviert")
		}
	})
	logCheck.SetChecked(config.LoggingEnabled)

	debugCheck := trCheck("Debug-Modus (Anfrage vor Versand anzeigen: Suchen/Analysieren)", func(b bool) {
		config.DebugMode = b
		SaveConfig()
	})
	debugCheck.SetChecked(config.DebugMode)

	// Autostart bei der Windows-Anmeldung ("als Dienst starten"). Zustand kommt
	// direkt aus der Registry (autostart_windows.go), nicht aus config.json —
	// so bleibt die Checkbox auch mit extern gesetzten/geloeschten Eintraegen
	// synchron. autostartReverting verhindert, dass das Zuruecksetzen der
	// Checkbox im Fehlerfall den OnChanged-Callback erneut ausloest.
	var autostartCheck *widget.Check
	var autostartReverting bool
	autostartCheck = trCheck("Automatisch bei Windows-Anmeldung starten", func(b bool) {
		if autostartReverting {
			return
		}
		if err := setAutostart(b); err != nil {
			showErr(fmt.Errorf(T("Autostart konnte nicht geändert werden:\n%v"), err), win)
			autostartReverting = true
			autostartCheck.SetChecked(!b)
			autostartReverting = false
		}
	})
	if autostartEnabled() {
		// SetChecked(true) loest den Callback aus und schreibt den Eintrag mit
		// dem aktuellen exe-Pfad neu — haelt den Autostart nach einem
		// Verschieben der Datei aktuell.
		autostartCheck.SetChecked(true)
	}
	if !autostartSupported() {
		autostartCheck.Hide()
	}

	updateAnalysisUI := func() {} // Forward declaration

	// --- 'remote LLM': Auswahl des Remote-Backends (Flash/Ollama/vLLM) ---
	flashRadio := widget.NewButtonWithIcon("Google Flash", theme.RadioButtonIcon(), func() {
		config.RemoteBackend = "Google Flash"
		SaveConfig()
		updateAnalysisUI()
	})
	flashRadio.Importance = widget.LowImportance
	flashRadio.Alignment = widget.ButtonAlignLeading

	ollamaRadio := widget.NewButtonWithIcon("Ollama", theme.RadioButtonIcon(), func() {
		config.RemoteBackend = "Ollama"
		SaveConfig()
		updateAnalysisUI()
	})
	ollamaRadio.Importance = widget.LowImportance
	ollamaRadio.Alignment = widget.ButtonAlignLeading

	vllmRadio := widget.NewButtonWithIcon("vLLM", theme.RadioButtonIcon(), func() {
		config.RemoteBackend = "vLLM"
		SaveConfig()
		updateAnalysisUI()
	})
	vllmRadio.Importance = widget.LowImportance
	vllmRadio.Alignment = widget.ButtonAlignLeading

	// Verbindungsfelder des gewählten Remote-Backends (currentBackend).
	apiKeyEntry := NewMinSizeEntry(200)
	trPlaceholder(&apiKeyEntry.Entry, "API Key")
	apiKeyEntry.Entry.OnChanged = func(s string) {
		if b := currentBackend(); b != nil {
			b.ApiKey = s
			saveConfigDebounced()
		}
	}

	urlEntry := NewMinSizeEntry(200)
	urlEntry.Entry.SetPlaceHolder("http://localhost:...")
	urlEntry.Entry.OnChanged = func(s string) {
		if b := currentBackend(); b != nil {
			b.Url = s
			saveConfigDebounced()
		}
	}

	modelEntry := NewMinSizeEntry(200)
	trPlaceholder(&modelEntry.Entry, "Modell-Name")
	modelEntry.Entry.OnChanged = func(s string) {
		if b := currentBackend(); b != nil {
			b.Model = s
			saveConfigDebounced()
		}
	}

	// Verbindungsfelder fuer die Jarvis-KI-Support-API (siehe jarvis_client.go).
	jarvisServerEntry := NewMinSizeEntry(200)
	jarvisServerEntry.Entry.SetPlaceHolder("https://jarvis-host")
	jarvisServerEntry.Entry.SetText(config.Jarvis.Url)
	jarvisServerEntry.Entry.OnChanged = func(s string) {
		config.Jarvis.Url = s
		saveConfigDebounced()
	}
	jarvisApiKeyEntry := widget.NewPasswordEntry()
	trPlaceholder(jarvisApiKeyEntry, "API-Key")
	jarvisApiKeyEntry.SetText(config.Jarvis.ApiKey)
	jarvisApiKeyEntry.OnChanged = func(s string) {
		config.Jarvis.ApiKey = s
		saveConfigDebounced()
	}

	// Kundenverwaltungs-API (IBS): URL + API-Key schalten die Checkbox
	// "IBS Tickets" im KI-Support-Panel frei (refreshIBSCheck, jarvis_client.go).
	ibsUrlEntry := NewMinSizeEntry(200)
	ibsUrlEntry.Entry.SetPlaceHolder("https://kundenverwaltung-host")
	ibsUrlEntry.Entry.SetText(config.IBS.Url)
	ibsUrlEntry.Entry.OnChanged = func(s string) {
		config.IBS.Url = s
		saveConfigDebounced()
		if refreshIBSCheck != nil {
			refreshIBSCheck()
		}
	}
	ibsApiKeyEntry := widget.NewPasswordEntry()
	trPlaceholder(ibsApiKeyEntry, "API-Key")
	ibsApiKeyEntry.SetText(config.IBS.ApiKey)
	ibsApiKeyEntry.OnChanged = func(s string) {
		config.IBS.ApiKey = s
		saveConfigDebounced()
		if refreshIBSCheck != nil {
			refreshIBSCheck()
		}
	}

	// DE/EN-Umschalter (Segment-Pill, siehe design.png "de_en.png"). Klick
	// wechselt live die App-Sprache (currentLang / config.JarvisLang) über
	// setLanguage; alle übersetzbaren Widgets folgen via onLangChange. Die Pille
	// selbst besteht aus canvas-Objekten und wird per newTappable klickbar
	// gemacht; applyLangPill spiegelt die aktive Sprache optisch wider. Siehe
	// i18n.go und Memory [[i18n-todo-de-en-toggle]].
	deFg := canvas.NewText("DE", color.White)
	deFg.TextStyle = fyne.TextStyle{Bold: true}
	deFg.TextSize = 11
	enFg := canvas.NewText("EN", color.White)
	enFg.TextStyle = fyne.TextStyle{Bold: true}
	enFg.TextSize = 11
	deBg := canvas.NewRectangle(kiAccent)
	deBg.CornerRadius = 9
	enBg := canvas.NewRectangle(color.Transparent)
	enBg.CornerRadius = 9
	jarvisLangTrack := canvas.NewRectangle(color.NRGBA{R: 0xEA, G: 0xEA, B: 0xEA, A: 255})
	jarvisLangTrack.CornerRadius = 9
	applyLangPill := func() {
		if currentLang == "en" {
			enBg.FillColor = kiAccent
			enFg.Color = color.White
			deBg.FillColor = color.Transparent
			deFg.Color = theme.Color(theme.ColorNameDisabled)
		} else {
			deBg.FillColor = kiAccent
			deFg.Color = color.White
			enBg.FillColor = color.Transparent
			enFg.Color = theme.Color(theme.ColorNameDisabled)
		}
		deBg.Refresh()
		enBg.Refresh()
		deFg.Refresh()
		enFg.Refresh()
	}
	onLangChange(applyLangPill)
	applyLangPill() // Startzustand (z.B. wenn beim Start bereits EN aktiv ist)
	jarvisLangToggle := container.NewHBox(newTappable(
		container.NewStack(
			jarvisLangTrack,
			container.New(&segmentLayout{},
				container.NewStack(deBg, container.NewPadded(deFg)),
				container.NewStack(enBg, container.NewPadded(enFg)),
			),
		),
		func() {
			if currentLang == "de" {
				setLanguage("en")
			} else {
				setLanguage("de")
			}
		},
	))

	// Auto-Discovery der Modelle des gewählten Remote-Backends.
	discoverBtn := newTooltipButton(theme.SearchIcon(), func() {
		b := currentBackend()
		if b == nil {
			return
		}
		mode := config.RemoteBackend
		srvUrl := b.Url
		apiKey := b.ApiKey
		setStatus("Suche Modelle...")
		go func() {
			var models []string
			var derr error
			switch mode {
			case "vLLM":
				models, derr = fetchVllmModels(srvUrl, apiKey)
			case "Ollama":
				models, derr = fetchOllamaModels(srvUrl, apiKey)
			}
			fyne.Do(func() {
				setStatus("Bereit")
				if derr != nil {
					showErr(derr, win)
					return
				}
				if len(models) == 0 {
					showErr(fmt.Errorf(T("Server unter %s erreichbar, meldet aber keine Modelle."), srvUrl), win)
					return
				}
				sel := widget.NewSelect(models, nil)
				sel.SetSelectedIndex(0)
				content := container.NewVBox(
					widget.NewLabel(fmt.Sprintf("%d Modelle erkannt:", len(models))),
					sel,
				)
				dialog.ShowCustomConfirm(T("Modell auswählen"), T("Auswählen"), T("Abbrechen"), content, func(ok bool) {
					if ok && sel.Selected != "" {
						modelEntry.Entry.SetText(sel.Selected)
					}
				}, win)
			})
		}()
	}, "Verfügbare Modelle vom Server abrufen (Auto-Discovery)")

	modelRow := container.NewBorder(nil, nil, discoverBtn, nil, modelEntry)

	updateAnalysisUI = func() {
		flashRadio.SetIcon(theme.RadioButtonIcon())
		ollamaRadio.SetIcon(theme.RadioButtonIcon())
		vllmRadio.SetIcon(theme.RadioButtonIcon())
		switch config.RemoteBackend {
		case "Google Flash":
			flashRadio.SetIcon(theme.RadioButtonCheckedIcon())
		case "Ollama":
			ollamaRadio.SetIcon(theme.RadioButtonCheckedIcon())
		default:
			vllmRadio.SetIcon(theme.RadioButtonCheckedIcon())
		}
		if b := currentBackend(); b != nil {
			apiKeyEntry.Entry.SetText(b.ApiKey)
			urlEntry.Entry.SetText(b.Url)
			modelEntry.Entry.SetText(b.Model)
		}
		// Modell-Discovery nur für Ollama/vLLM sinnvoll.
		if config.RemoteBackend == "Ollama" || config.RemoteBackend == "vLLM" {
			discoverBtn.Enable()
		} else {
			discoverBtn.Disable()
		}
		updatePills()
	}
	updateAnalysisUI() // Initialer Aufruf

	// suppressModelChange unterdrückt den OnChanged-Handler der Modell-Dropdowns,
	// während wir sie programmatisch (per SetSelected) auf einen alten Wert
	// zurücksetzen – sonst würde das Zurücksetzen erneut selectLocalModel und damit
	// eine zweite Rückfrage auslösen.
	var suppressModelChange bool

	// selectLocalModel setzt die "download on demand"-Logik der Modell-Auswahl um:
	// Ist zum gewählten Symbol ein lokales Modell nötig und noch NICHT vorhanden,
	// wird der Download nur nach Rückfrage gestartet. onOK (dort: config setzen +
	// SaveConfig) wird ausschließlich aufgerufen, wenn das Modell lokal existiert –
	// erst dann darf die Auswahl gespeichert werden. Bei Abbruch oder Download-
	// Fehler läuft revert (Dropdown auf die zuletzt gespeicherte Auswahl zurück)
	// und es wird nichts gespeichert.
	selectLocalModel := func(sym string, onOK func(), revert func()) {
		f := modelFileForSymbol(sym)
		if f == "" || localModelExists(f) {
			// "none"/"remote" (kein lokales Modell nötig) oder Modell schon lokal:
			// direkt übernehmen.
			onOK()
			go ensureLocalServers()
			updatePills()
			return
		}
		// Modell fehlt lokal -> herunterladen anbieten.
		label := f
		if m := findLocalModel(f); m != nil {
			label = m.Label
		}
		dialog.ShowConfirm(T("Modell herunterladen?"),
			fmt.Sprintf(T("Das lokale Modell „%s“ ist noch nicht vorhanden.\n"+
				"Es muss einmalig heruntergeladen werden (mehrere GB), bevor die\n"+
				"Auswahl aktiv wird.\n\nJetzt herunterladen?"), label),
			func(ok bool) {
				if !ok {
					revert() // Auswahl verworfen, nicht gespeichert
					return
				}
				prog := widget.NewProgressBarInfinite()
				info := widget.NewLabel(T("Bereite Modell vor …"))
				dlg := dialog.NewCustom(T("Modell wird geladen"), T("Im Hintergrund weiter"), container.NewVBox(info, prog), win)
				dlg.Show()
				go func() {
					if err := ensureLocalModel(f, func(task string, p float64) {
						fyne.Do(func() { info.SetText(task) })
					}); err != nil {
						Log("Modell-Download-Fehler: " + err.Error())
						fyne.Do(func() {
							dlg.Hide()
							showErr(fmt.Errorf("Download fehlgeschlagen:\n%v", err), win)
							revert() // ohne Modell keine gültige Auswahl
						})
						return
					}
					// Modell liegt jetzt lokal vor -> Auswahl übernehmen & speichern.
					// Reihenfolge wichtig: erst onOK (setzt config.*Model), dann
					// ensureLocalServers – Letzteres entscheidet anhand der config,
					// welcher Server laufen muss.
					onOK()
					ensureLocalServers()
					fyne.Do(func() { dlg.Hide(); updatePills() })
				}()
			}, win)
	}

	// --- Erkennung: Whisper lokal vs. Remote Whisper (GPU) ---
	remoteUrlEntry := NewMinSizeEntry(260)
	remoteUrlEntry.Entry.SetPlaceHolder("ws://host:8090/ws/stt")
	remoteUrlEntry.Entry.SetText(config.RemoteWhisperUrl)
	remoteUrlEntry.Entry.OnChanged = func(s string) {
		config.RemoteWhisperUrl = s
		saveConfigDebounced()
	}
	// Label/Wert-Trennung wie bei den anderen Radios: die Auswahl steuert den
	// bool config.WhisperLocal; angezeigt werden übersetzte Labels ("Remote GPU"
	// bleibt sprachneutral).
	whisperLabels := func() []string { return []string{T("Whisper lokal"), T("Remote GPU")} }
	whisperSel := func() string {
		if config.WhisperLocal {
			return T("Whisper lokal")
		}
		return T("Remote GPU")
	}
	suppressWhisperChange := false
	whisperRadio := widget.NewRadioGroup(whisperLabels(), func(label string) {
		if suppressWhisperChange {
			return
		}
		b := label == T("Whisper lokal")
		config.WhisperLocal = b
		SaveConfig()
		if b {
			closeAllRemoteSessions() // zurück zu lokal -> Remote-Sessions beenden
			remoteUrlEntry.Disable()
		} else {
			remoteUrlEntry.Enable()
		}
		// whisper-server passend zur neuen Auswahl starten/stoppen
		// (asynchron - der Start wartet auf den Warmup-Health-Check).
		go ensureWhisperServer()
		updatePills()
	})
	whisperRadio.Horizontal = true
	whisperRadio.SetSelected(whisperSel())
	if config.WhisperLocal {
		remoteUrlEntry.Disable()
	} else {
		remoteUrlEntry.Enable()
	}
	onLangChange(func() {
		suppressWhisperChange = true
		whisperRadio.Options = whisperLabels()
		whisperRadio.SetSelected(whisperSel())
		suppressWhisperChange = false
		whisperRadio.Refresh()
	})

	// --- Nachbearbeitung der Erkennung ---
	var postProcSelect *MinSizeSelect
	postProcSelect = NewMinSizeSelect([]string{"ohne", "Gemma 4 E2B", "Gemma 4 12B", "remote LLM"}, func(s string) {
		if suppressModelChange {
			return
		}
		prev := config.PostProcModel // gespeicherte Auswahl (für Zurücksetzen bei Abbruch)
		sym := modelSymbolFromLabel(s)
		selectLocalModel(sym,
			func() { config.PostProcModel = sym; SaveConfig() },
			func() {
				suppressModelChange = true
				postProcSelect.SetSelected(modelLabelFromSymbol(prev))
				suppressModelChange = false
			})
	}, 170)
	// Initiale Anzeige der gespeicherten Auswahl: nur Label setzen, KEIN
	// On-Demand-Download/Dialog (die gespeicherte Auswahl impliziert ein bereits
	// vorhandenes Modell; die lokalen Server startet ensureLocalServers separat).
	suppressModelChange = true
	postProcSelect.SetSelected(modelLabelFromSymbol(config.PostProcModel))
	suppressModelChange = false
	postProcHelpBtn := widget.NewButtonWithIcon("", theme.QuestionIcon(), func() {
		rcT, rcB := helpRecognition()
		showInfo(rcT, rcB, win)
	})
	postProcHelpBtn.Importance = widget.LowImportance // kein grauer Button-Hintergrund fuer das Hilfe-Symbol

	// --- Analyse (manuell, mit Prompt) ---
	var analysisSelect *MinSizeSelect
	analysisSelect = NewMinSizeSelect([]string{"Gemma 4 E2B", "Gemma 4 12B", "remote LLM"}, func(s string) {
		if suppressModelChange {
			return
		}
		prev := config.AnalysisModel
		sym := modelSymbolFromLabel(s)
		selectLocalModel(sym,
			func() { config.AnalysisModel = sym; SaveConfig() },
			func() {
				suppressModelChange = true
				analysisSelect.SetSelected(modelLabelFromSymbol(prev))
				suppressModelChange = false
			})
	}, 170)
	suppressModelChange = true
	analysisSelect.SetSelected(modelLabelFromSymbol(config.AnalysisModel))

	// --- LLM Prompt zur Analyse: Verwaltung der Analyse-Vorgaben ---
	// (vom STT-Tab hierher gewandert.) Eine Vorgabe = beschreibende Zeile
	// (editierbares Pulldown) + zugehoeriger Prompt (scrollbares Textfenster,
	// 6 sichtbare Zeilen). Auswahl einer Beschreibung laedt ihren Prompt;
	// Prompt-Aenderungen an einer BESTEHENDEN Vorgabe werden direkt
	// mitgespeichert, eine NEUE Beschreibung legt der Speichern-Button an.
	// Loeschen entfernt Beschreibung UND Prompt. Das Pulldown im STT-Tab
	// (analysisPromptSelect) zeigt dieselben Beschreibungen.
	promptNameEntry := widget.NewSelectEntry(analysisPromptNames())
	bindText(func(s string) { promptNameEntry.SetPlaceHolder(s) }, "Beschreibung, z. B. \"Zusammenfassung\"")
	promptTextEntry := widget.NewMultiLineEntry()
	promptTextEntry.Wrapping = fyne.TextWrapWord
	promptTextEntry.SetMinRowsVisible(6)
	bindText(func(s string) { promptTextEntry.SetPlaceHolder(s) }, "KI-Analyse Prompt (z.B. Fasse zusammen)")
	// Vorbelegung: die aktuell im STT-Tab gewaehlte Vorgabe.
	if def := analysisPromptByName(config.AnalysisPromptName); def != nil {
		promptNameEntry.SetText(def.Name)
		promptTextEntry.SetText(def.Prompt)
	}
	promptNameEntry.OnChanged = func(s string) {
		// Auswahl aus dem Pulldown (bzw. exakt getippter Name) laedt den
		// gespeicherten Prompt ins Textfenster.
		if def := analysisPromptByName(strings.TrimSpace(s)); def != nil {
			promptTextEntry.SetText(def.Prompt)
		}
	}
	promptTextEntry.OnChanged = func(s string) {
		// Prompt einer bestehenden Vorgabe direkt mitschreiben ("Eintraege
		// lassen sich bearbeiten und speichern") - neue Beschreibungen werden
		// erst mit dem Speichern-Button angelegt.
		if def := analysisPromptByName(strings.TrimSpace(promptNameEntry.Text)); def != nil && def.Prompt != s {
			def.Prompt = s
			saveConfigDebounced()
		}
	}
	savePromptBtn := widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() {
		name := strings.TrimSpace(promptNameEntry.Text)
		if name == "" {
			showInfo(T("Vorgabe speichern"), T("Bitte zuerst eine Beschreibung eingeben."), win)
			return
		}
		if def := analysisPromptByName(name); def != nil {
			def.Prompt = promptTextEntry.Text
		} else {
			config.AnalysisPromptDefs = append(config.AnalysisPromptDefs, AnalysisPromptDef{Name: name, Prompt: promptTextEntry.Text})
		}
		if config.AnalysisPromptName == "" {
			config.AnalysisPromptName = name
		}
		SaveConfig()
		promptNameEntry.SetOptions(analysisPromptNames())
		refreshAnalysisPromptSelect()
		Log("Analyse-Vorgabe gespeichert: " + name)
	})
	delPromptBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		name := strings.TrimSpace(promptNameEntry.Text)
		if analysisPromptByName(name) == nil {
			showInfo(T("Vorgabe löschen"), T("Diese Vorgabe ist nicht in der Liste gespeichert."), win)
			return
		}
		showConfirm(T("Vorgabe löschen"), T("Analyse-Vorgabe wirklich löschen?")+"\n\n\""+name+"\"", func(ok bool) {
			if !ok {
				return
			}
			rest := config.AnalysisPromptDefs[:0:0]
			for _, d := range config.AnalysisPromptDefs {
				if d.Name != name {
					rest = append(rest, d)
				}
			}
			config.AnalysisPromptDefs = rest
			promptNameEntry.SetText("")
			promptTextEntry.SetText("")
			promptNameEntry.SetOptions(analysisPromptNames())
			refreshAnalysisPromptSelect() // waehlt im STT-Tab ggf. die erste verbliebene Vorgabe
			SaveConfig()
			Log("Analyse-Vorgabe gelöscht: " + name)
		}, win)
	})
	promptNameRow := container.NewBorder(nil, nil, nil, container.NewHBox(savePromptBtn, delPromptBtn), promptNameEntry)
	suppressModelChange = false

	saveBtn := trButtonIcon("Einstellungen jetzt speichern", theme.DocumentSaveIcon(), func() {
		Log("Manuelle Konfigurationsspeicherung ausgelöst")
		SaveConfig()
		restartWebhookServer() // geänderte Webhook-Einstellungen (aktiv/Port/Pfad) übernehmen
		showInfo(T("Erfolg"), T("Alle Einstellungen wurden dauerhaft gespeichert."), win)
	})

	// KI-Support: zweite Haelfte NUR des STT-Tabs, Anbindung an die Jarvis-Support-API.
	// searchMatchingTickets (oben vorwaerts deklariert) wird hier zugewiesen und
	// vom Button "Suche passende Tickets" im Diktat-Panel genutzt.
	kiSupportTab, searchMatchingTicketsFn, clearTicketResultsFn := buildKISupportPanel(win)
	searchMatchingTickets = searchMatchingTicketsFn
	clearTicketResults = clearTicketResultsFn

	// "Prompt für KI-Zusammenfassung" (config.JarvisSearchQuery): LLM-Anweisung,
	// die bei jeder Suche im "prompt"-Feld mitgeschickt wird - bewusst OHNE
	// Verbindung zur Sucheingabe (queryEntry) im KI-Support-Panel. Mehrzeilig
	// (mind. 6 sichtbare Zeilen), da hier laengere Instruktionen stehen.
	jarvisSearchPromptEntry := widget.NewMultiLineEntry()
	jarvisSearchPromptEntry.Wrapping = fyne.TextWrapWord
	jarvisSearchPromptEntry.SetMinRowsVisible(6)
	jarvisSearchPromptEntry.SetText(config.JarvisSearchQuery)
	jarvisSearchPromptEntry.OnChanged = func(s string) {
		config.JarvisSearchQuery = s
		saveConfigDebounced()
	}

	// "Prompt für passende Tickets" (config.JarvisTicketSearchPrompt): Vorlage
	// für den Button "Suche passende Tickets" (STT-Tab). "[Textfenster]" wird
	// vor dem API-Call durch den erkannten Text ersetzt.
	jarvisTicketSearchPromptEntry := NewMinSizeEntry(alignedFormValueW)
	jarvisTicketSearchPromptEntry.Entry.SetText(config.JarvisTicketSearchPrompt)
	jarvisTicketSearchPromptEntry.Entry.OnChanged = func(s string) {
		config.JarvisTicketSearchPrompt = s
		saveConfigDebounced()
	}

	// Automatischer zyklischer Ticket-Scan: Checkbox "aktiviert" + Intervall-
	// Slider (5..60 s, 5er-Schritte). Slider laesst sich nur ziehen, wenn die
	// Checkbox aktiviert ist (Disable/Enable). Der Slider sitzt in der Wert-Spalte
	// eines alignedFormLayout und ist dadurch links/rechts buendig zum Eingabefeld
	// "Prompt für passende Tickets".
	config.AutoScanInterval = clampAutoScanInterval(config.AutoScanInterval)
	autoScanIntervalLabel := widget.NewLabel(fmt.Sprintf(T("Scan-Intervall: %d s"), config.AutoScanInterval))
	onLangChange(func() { autoScanIntervalLabel.SetText(fmt.Sprintf(T("Scan-Intervall: %d s"), config.AutoScanInterval)) })
	autoScanSlider := widget.NewSlider(5, 60)
	autoScanSlider.Step = 5
	autoScanSlider.SetValue(float64(config.AutoScanInterval))
	autoScanSlider.OnChanged = func(v float64) {
		config.AutoScanInterval = clampAutoScanInterval(int(v))
		autoScanIntervalLabel.SetText(fmt.Sprintf(T("Scan-Intervall: %d s"), config.AutoScanInterval))
		saveConfigDebounced()
		// Laeuft gerade ein Scan-Zyklus, mit neuem Intervall neu starten.
		if isRecording.Load() && config.AutoScanEnabled {
			startAutoScan()
		}
	}
	autoScanCheck := trCheck("aktiviert", func(b bool) {
		config.AutoScanEnabled = b
		saveConfigDebounced()
		if b {
			autoScanSlider.Enable()
			if isRecording.Load() {
				startAutoScan()
			}
		} else {
			autoScanSlider.Disable()
			stopAutoScan()
		}
	})
	// "Automatische Ticketsuche" ist bis auf weiteres deaktiviert: Checkbox
	// ausgegraut (kein Häkchen setzbar) und die Automatik zwangsweise aus –
	// unabhängig von einem evtl. gespeichert 'true', damit sie nicht doch über
	// config.AutoScanEnabled anläuft. Zum Reaktivieren diese drei Zeilen durch die
	// ursprüngliche Zustandslogik ersetzen:
	//   autoScanCheck.SetChecked(config.AutoScanEnabled)
	//   if !config.AutoScanEnabled { autoScanSlider.Disable() }
	config.AutoScanEnabled = false
	autoScanCheck.Disable()
	autoScanSlider.Disable()
	// Fertige Zeilen fuer die Platzierung hinter "Analyse (manuell, mit Prompt)":
	// Checkbox "aktiviert" in der Wert-Spalte, Beschreibung links davon; darunter
	// der Slider in der Wert-Spalte (buendig zum Prompt-Eingabefeld).
	autoScanCheckRow := container.New(&alignedFormLayout{}, trLabel("Automatische Ticketsuche"), autoScanCheck)
	autoScanIntervalRow := container.New(&alignedFormLayout{}, autoScanIntervalLabel, autoScanSlider)

	// "Anrufer automatisch suchen": bei eingehendem Anruf automatisch die CRM-
	// Suche zur Rufnummer starten (Default an). Reiner Konfig-Schalter (Wirkung in
	// handleIncomingCaller, webhook.go); kein Sofort-Effekt beim Umschalten.
	autoSearchCheck := trCheck("aktiviert", func(b bool) {
		config.AutoSearchCaller = b
		saveConfigDebounced()
	})
	autoSearchCheck.SetChecked(config.AutoSearchCaller)
	autoSearchCheckRow := container.New(&alignedFormLayout{}, trLabel("Anrufer automatisch suchen"), autoSearchCheck)

	// "Mitschrift bei Anruf starten": startet bei eingehendem Anruf (Webhook)
	// automatisch die Aufnahme. Reiner Konfig-Schalter (Wirkung in
	// maybeAutoStartRecording, webhook.go); kein Sofort-Effekt beim Umschalten.
	autoRecordCheck := trCheck("aktiviert", func(b bool) {
		config.AutoRecordOnCall = b
		saveConfigDebounced()
	})
	autoRecordCheck.SetChecked(config.AutoRecordOnCall)
	autoRecordCheckRow := container.New(&alignedFormLayout{}, trLabel("Mitschrift bei Anruf starten"), autoRecordCheck)

	// "Mehrere CRM-Treffer": Verhalten, wenn die Rufnummernsuche >1 Treffer
	// liefert - Auswahl-Popup zeigen (Default) oder still den ersten nehmen.
	// Wirkung in performCallerJiraLookup (webhook.go).
	const (
		crmChoiceShow  = "Auswahl anzeigen"
		crmChoiceFirst = "ersten Treffer nehmen"
	)
	callerMatchSelect := widget.NewSelect([]string{crmChoiceShow, crmChoiceFirst}, func(s string) {
		config.CallerTakeFirstMatch = s == crmChoiceFirst
		saveConfigDebounced()
	})
	if config.CallerTakeFirstMatch {
		callerMatchSelect.SetSelected(crmChoiceFirst)
	} else {
		callerMatchSelect.SetSelected(crmChoiceShow)
	}
	callerMatchRow := container.New(&alignedFormLayout{}, trLabel("Mehrere CRM-Treffer"), framedSelect(callerMatchSelect))

	// --- Rufnummern Übergabe (eingehender Webhook, s. webhook.go) ---
	// Aktiv-Checkbox startet/stoppt den Listener sofort; Port/Pfad werden beim
	// manuellen Speichern (saveBtn) übernommen (restartWebhookServer).
	webhookEnableCheck := trCheck("aktiviert", func(b bool) {
		config.WebhookEnabled = b
		saveConfigDebounced()
		restartWebhookServer()
	})
	webhookEnableCheck.SetChecked(config.WebhookEnabled)

	webhookPathEntry := NewMinSizeEntry(alignedFormValueW)
	webhookPathEntry.Entry.SetText(effectiveWebhookPath())
	webhookPathEntry.Entry.OnChanged = func(s string) {
		config.WebhookPath = s
		saveConfigDebounced()
	}

	webhookPortEntry := NewMinSizeEntry(alignedFormValueW)
	webhookPortEntry.Entry.SetText(strconv.Itoa(effectiveWebhookPort()))
	webhookPortEntry.Entry.OnChanged = func(s string) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 && n <= 65535 {
			config.WebhookPort = n
			saveConfigDebounced()
		}
	}

	webhookHelpBtn := widget.NewButtonWithIcon("", theme.QuestionIcon(), func() {
		whT, whB := helpWebhook()
		showInfo(whT, whB, win)
	})
	webhookHelpBtn.Importance = widget.LowImportance

	settingsContent := container.New(&compactVBoxLayout{},
		trLabelStyle("Betriebsmodus", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(modeRadio, modeInfoBtn),

		widget.NewSeparator(),
		trLabelStyle("Audio-Geräte", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.New(&alignedFormLayout{},
			trLabel("Mikrofon:"), framedSelect(micSelect),
			trLabel("Lautsprecher:"), framedSelect(speakerSelect),
		),

		widget.NewSeparator(),
		trLabelStyle("Spracherkennung und Analyse", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.New(&alignedFormLayout{},
			trLabel("Transkribierung über:"), whisperRadio,
			trLabel("Nachbearbeitung:"), container.NewBorder(nil, nil, nil, postProcHelpBtn, framedSelect(postProcSelect)),
		),
		container.New(&alignedFormLayout{}, trLabel("Remote-Whisper-URL:"), remoteUrlEntry),
		container.New(&alignedFormLayout{}, trLabel("Analyse (manuell, mit Prompt):"), framedSelect(analysisSelect)),
		// Analyse-Vorgaben (Beschreibung + Prompt) - letzte Punkte des
		// Abschnitts; das STT-Pulldown "Analyse-Vorgabe" zeigt die
		// Beschreibungen (s. Kommentar bei promptNameEntry).
		container.New(&alignedFormLayout{}, trLabel("LLM Prompt zur Analyse:"), promptNameRow),
		container.New(&alignedFormLayout{}, trLabel("Prompt:"), promptTextEntry),

		// Automatischer zyklischer Ticket-Scan - inhaltlich zur Erkennung/Analyse
		// gehoerig, daher hier direkt hinter "Analyse (manuell, mit Prompt)".
		// Reihenfolge: Ueberschrift, Prompt, Checkbox-Zeile, Intervall-Slider.
		widget.NewLabel(""), // Leerzeile vor der Ueberschrift
		trLabelStyle("Suche nach passenden Tickets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		autoSearchCheckRow,
		autoRecordCheckRow,
		callerMatchRow,
		container.New(&alignedFormLayout{},
			trLabel("Prompt für passende Tickets:"), jarvisTicketSearchPromptEntry,
		),
		autoScanCheckRow,
		autoScanIntervalRow,

		widget.NewLabel(""), // Leerzeile vor 'remote LLM konfigurieren'

		// remote LLM - 3 Zeilen, 3 Spalten: Radio(180) | Label | Value
		func() fyne.CanvasObject {
			header := trLabelStyle("remote LLM konfigurieren", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			return container.New(&llmTableLayout{},
				header,
				flashRadio, trLabel("API key:"), apiKeyEntry,
				ollamaRadio, trLabel("URL:"), urlEntry,
				vllmRadio, trLabel("Modelname:"), modelRow,
			)
		}(),

		widget.NewSeparator(),
		trLabelStyle("KI-Support (Jarvis)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.New(&alignedFormLayout{},
			trLabel("Server-URL:"), jarvisServerEntry,
			trLabel("API-Key:"), jarvisApiKeyEntry,
			trLabel("URL Kundenverwaltung API:"), ibsUrlEntry,
			trLabel("API-Key Kundenverwaltung:"), ibsApiKeyEntry,
			trLabel("Prompt für KI-Zusammenfassung:"), jarvisSearchPromptEntry,
		),

		widget.NewSeparator(),
		trLabelStyle("Rufnummern Übergabe", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.New(&alignedFormLayout{},
			trLabel("Webhook aktiv:"), container.NewHBox(webhookEnableCheck, webhookHelpBtn),
			trLabel("Webhook-URL (Pfad):"), webhookPathEntry,
			trLabel("Port:"), webhookPortEntry,
		),

		widget.NewSeparator(),
		trLabelStyle("System-Einstellungen", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.New(&compactFormLayout{},
			trLabel("Sprache:"), jarvisLangToggle,
		),
		logCheck,
		debugCheck,
		autostartCheck,

		widget.NewSeparator(),
		trLabelStyle("Design", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		func() *widget.RadioGroup {
			// Interne Theme-Werte (werden in config.Theme gespeichert und von
			// applyTheme/isClassic geparst) getrennt von den angezeigten,
			// übersetzten Labels. valueForLabel mappt das gewählte Label auf den
			// festen internen Wert zurück.
			themeValues := []string{"Hell (klassisch)", "Hell (modern)", "Dunkel (modern)"}
			themeLabels := func() []string {
				out := make([]string, len(themeValues))
				for i, v := range themeValues {
					out[i] = T(v)
				}
				return out
			}
			valueForLabel := func(label string) string {
				for _, v := range themeValues {
					if T(v) == label {
						return v
					}
				}
				return "Hell (modern)"
			}
			cur := "Hell (modern)"
			switch config.Theme {
			case "Dunkel", "Dunkel (modern)":
				cur = "Dunkel (modern)"
			case "Klassisch", "Hell (klassisch)":
				cur = "Hell (klassisch)"
			}
			// currentThemeValue hält den gewählten INTERNEN Wert (sprachunabhängig).
			// Nicht aus r.Selected zurückmappen: nach einem Sprachwechsel enthielte
			// r.Selected noch das Label der alten Sprache, während valueForLabel
			// bereits in der neuen Sprache vergliche.
			currentThemeValue := cur
			suppressThemeChange := false
			r := widget.NewRadioGroup(themeLabels(), func(label string) {
				if suppressThemeChange {
					return
				}
				v := valueForLabel(label)
				currentThemeValue = v
				config.Theme = v
				SaveConfig()
				applyTheme(myApp, v)
				setWindowSquare(win, isClassic(v)) // klassisch -> eckiger Fensterrahmen
			})
			r.SetSelected(T(cur))
			r.Horizontal = true
			// Sprachwechsel: Labels + Auswahl neu setzen, ohne OnChanged (und damit
			// applyTheme) erneut auszulösen - der interne Wert bleibt unverändert.
			onLangChange(func() {
				suppressThemeChange = true
				r.Options = themeLabels()
				r.SetSelected(T(currentThemeValue))
				suppressThemeChange = false
				r.Refresh()
			})
			return r
		}(),

		widget.NewSeparator(),
		trLabelStyle("Speicherpfade", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		trLabel("Binaries: ./libs/"),
		trLabel("Modelle: ./models/"),
	)

	// Weisser Hintergrund statt des Theme-Graus (klassischer Modus) - wie beim
	// Windows Explorer, dessen Inhaltsbereich ebenfalls weiss bleibt.
	settingsBg := canvas.NewRectangle(color.White)
	configTab := container.NewBorder(saveBtn, nil, nil, nil,
		container.NewStack(settingsBg, container.NewVScroll(settingsContent)),
	)

	// STT-Tab in zwei per Maus verschiebbare Haelften teilen (Splitter mittig).
	// Die Einstellungen bleiben unangetastet ein eigener, ungeteilter Tab.
	sttSplit := container.NewHSplit(diktatTab, kiSupportTab)
	sttSplit.SetOffset(0.5)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("STT", theme.MediaRecordIcon(), sttSplit),
		container.NewTabItemWithIcon(T("Einstellungen"), theme.SettingsIcon(), configTab),
	)
	onLangChange(func() {
		tabs.Items[1].Text = T("Einstellungen")
		tabs.Refresh()
	})

	// Firmenlogo oben rechts, auf gleicher Hoehe wie die Tab-Reiter. AppTabs hat
	// keinen eigenen Slot dafuer - das Logo wird per Stack ueber die Tabs gelegt
	// und rechtsbuendig in eine eigene Kopfzeile einsortiert. Der Rest dieser
	// Overlay-Zeile bleibt leer/transparent, Klicks auf die Tabs darunter
	// funktionieren daher weiterhin normal.
	logoImg := canvas.NewImageFromResource(fyne.NewStaticResource("nexus-dp-hell.png", nexusLogoPNG))
	// ImageFillContain: skaliert das Bild UNTER BEIBEHALTUNG des Seitenverhaeltnisses
	// in die MinSize hinein (kein Stretch/Verzerren, ggf. Leerraum) - bewusst NICHT
	// ImageFillStretch. 141x36 entspricht (wie zuvor 86x22) nahezu exakt dem
	// Originalverhaeltnis der PNG (188x48 = 3.917 : 1), nur groesser dargestellt.
	logoImg.FillMode = canvas.ImageFillContain
	logoImg.SetMinSize(fyne.NewSize(141, 36))
	logoOverlay := container.New(&topRightOverlayLayout{rightMargin: 8, yOffset: -8}, logoImg)

	// Statuszeile ganz unten am Fenster (wie bei anderen Windows-Anwendungen) -
	// tabuebergreifend sichtbar, unabhaengig davon ob STT oder Einstellungen
	// aktiv ist. Links die drei Pipeline-Pills, rechts der Engine-Status
	// ("Engine: Wartet..."/"GPU beschleunigt" - frueher mitten im STT-Tab).
	statusBar := container.NewVBox(
		widget.NewSeparator(),
		container.NewPadded(container.NewBorder(nil, nil,
			container.NewHBox(recPill.box, postPill.box, anaPill.box),
			engineInfo,
		)),
	)

	win.SetContent(container.NewBorder(nil, statusBar, nil, nil, container.NewStack(tabs, logoOverlay)))
	restoreWindowPosition(win)
	setWindowSquare(win, isClassic(config.Theme)) // eckiger Fensterrahmen im klassischen Design
	applyCrispWindowIcon(win)                     // scharfes, transparentes Titelleisten-Symbol (nativ, s. main_windows.go)

	// Auto-Update: still im Hintergrund gegen die neueste GitHub-Release-Version
	// pruefen und bei Bedarf (nach Rueckfrage) herunterladen + neu starten.
	go runUpdateCheck(win)

	// Rufnummern-Webhook starten, falls in den Einstellungen aktiviert.
	startWebhookServer()

	win.ShowAndRun()
}

// clampAutoScanInterval begrenzt das Scan-Intervall auf den erlaubten Bereich
// 5..60 Sekunden. Ob überhaupt zyklisch gescannt wird, steuert die Checkbox
// config.AutoScanEnabled.
func clampAutoScanInterval(sec int) int {
	if sec < 5 {
		return 5
	}
	if sec > 60 {
		return 60
	}
	return sec
}

// startAutoScan startet den zyklischen Ticket-Scan (falls aktiviert). Ein evtl.
// laufender Zyklus wird zuvor beendet, sodass die Funktion auch zum Neustart
// nach Intervalländerung dient. Der Ticker feuert nur, solange die Erkennung
// aktiv ist; jeder Tick loest eine (stille) Ticketsuche zum aktuellen Textinhalt
// aus. Ueberlappende Laeufe werden im Suchcode via autoScanBusy verhindert.
func startAutoScan() {
	stopAutoScan()
	if !config.AutoScanEnabled || searchMatchingTickets == nil {
		return
	}
	interval := time.Duration(clampAutoScanInterval(config.AutoScanInterval)) * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	autoScanCancel = cancel
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !isRecording.Load() {
					return
				}
				fyne.Do(func() {
					if searchMatchingTickets != nil {
						// Zyklischer Scan: kein CRM-Fallback (leeren Text still überspringen).
						searchMatchingTickets(outputArea.Text, nil, true, false)
					}
				})
			}
		}
	}()
}

// stopAutoScan beendet einen laufenden zyklischen Scan (idempotent).
func stopAutoScan() {
	if autoScanCancel != nil {
		autoScanCancel()
		autoScanCancel = nil
	}
}

func toggleRecording() {
	if !isRecording.Load() {
		// Device ist bereits gestartet durch prepareAudio() beim Init/Wechsel
		now := time.Now()
		lastSoundTime.Store(&now)
		isSilent.Store(false)
		lastSpeaker = "" // Neustart -> erstes Segment bekommt wieder ein Label
		pendingRaw.Reset()
		pendingSpeaker = ""
		inProgress = nil
		resetSTTTail() // Kontext-Priming gehoert zur vorherigen Aufnahme
		isRecording.Store(true)
		micBtn.SetText(T("Mitschrift stoppen"))
		micBtn.tip = "Mitschrift stoppen"
		micBtn.Importance = widget.DangerImportance
		setStatus("Höre zu...")
		startAutoScan() // zyklischen Ticket-Scan starten (No-op, wenn deaktiviert)
	} else {
		stopAutoScan()
		// Offenen Whisper+LLM-Block beim Stoppen noch korrigieren (läuft im Main-Thread).
		if atHasPostProc.Load() {
			flushPendingToCorrection()
		}
		setStatus("Bereit")
		isRecording.Store(false)
		micBtn.SetText(T("Mitschrift"))
		micBtn.tip = "Mitschrift"
		micBtn.Importance = widget.HighImportance
	}
	micBtn.Refresh()
}

func prepareAudio() {
	// Serialisiert parallele Aufrufe (Init-Goroutine vs. Geräte-Wechsel im UI-Thread).
	audioMu.Lock()
	defer audioMu.Unlock()

	// Zuvor laufende Buffer-Goroutinen beenden. Non-blocking: die alten Goroutinen
	// erkennen das Cancel beim nächsten select-Durchlauf und kehren selbst zurück
	// (kein <-drain im UI-Thread mehr -> kein GUI-Freeze, kein Deadlock).
	if agentBufCancel != nil {
		agentBufCancel()
		agentBufCancel = nil
	}
	if callerBufCancel != nil {
		callerBufCancel()
		callerBufCancel = nil
	}

	if mctx == nil {
		backends := []malgo.Backend{}
		if runtime.GOOS == "windows" {
			backends = append(backends, malgo.BackendWasapi)
		}
		c, err := malgo.InitContext(backends, malgo.ContextConfig{}, nil)
		if err != nil {
			Log(fmt.Sprintf("FATAL: Audio-Kontext-Fehler: %v", err))
			return
		}
		mctx = c
	}

	// 1. Mikrofon (Agent)
	if audioDevice != nil {
		audioDevice.Uninit()
	}

	configMic := malgo.DefaultDeviceConfig(malgo.Capture)
	configMic.Capture.Format = malgo.FormatS16
	configMic.Capture.Channels = 1
	configMic.SampleRate = 16000
	if selectedMicID != nil {
		configMic.Capture.DeviceID = selectedMicID.Pointer()
	}

	// Kanalpuffer 600 Chunks (~6 s): Rueckstau der (synchronen) Transkription
	// fuehrte mit dem frueheren 1-s-Puffer schnell zu verworfenen Chunks.
	sampleChanAgent := make(chan []byte, 600)
	onDataMic := func(pOutput, pInput []byte, frameCount uint32) {
		if len(pInput) > 0 {
			// Digitale Verstärkung mit weichem Limiter (s. applyGainSoftClip);
			// Gain einmal lokal lesen für Race-Free.
			applyGainSoftClip(pInput, loadF64(&atMicGain))

			// Pegelanzeige (immer aktiv), gedrosselt auf ~15 Updates/s.
			level := chunkPeak(pInput) / 20000.0
			updateMeterThrottled(&agentMeterLastNs, &agentMeterPeak, level, func(lv float64) {
				agentLevel.SetValue(lv)
				if lv > agentMarkerVal { // Peak-Hold: höchsten Ausschlag als Richtwert merken
					agentMarkerVal = lv
					if agentMeter != nil {
						agentMeter.Refresh()
					}
				}
			})

			if isRecording.Load() {
				detectSilence(pInput)
				c := make([]byte, len(pInput))
				copy(c, pInput)
				// Non-blocking: Audio-Callback darf niemals blockieren. Läuft der
				// Puffer wegen langsamer Transkription voll, wird das Segment verworfen.
				select {
				case sampleChanAgent <- c:
				default:
					Log("WARN: Agent-Audiopuffer voll – Segment verworfen")
				}
			}
		}
	}

	if devMic, err := malgo.InitDevice(mctx.Context, configMic, malgo.DeviceCallbacks{Data: onDataMic}); err == nil {
		audioDevice = devMic
		audioDevice.Start() // Sofort starten für Pegelanzeige
	}

	// 2. Loopback (Anrufer) - Nur Windows
	if callerDevice != nil {
		callerDevice.Uninit()
	}

	sampleChanCaller := make(chan []byte, 600)
	if runtime.GOOS == "windows" {
		configLoop := malgo.DefaultDeviceConfig(malgo.Loopback)
		configLoop.Capture.Format = malgo.FormatS16
		configLoop.Capture.Channels = 1
		configLoop.SampleRate = 16000
		if selectedSpeakerID != nil {
			configLoop.Playback.DeviceID = selectedSpeakerID.Pointer()
		}

		onDataLoop := func(pOutput, pInput []byte, frameCount uint32) {
			if len(pInput) > 0 {
				// Digitale Verstärkung mit weichem Limiter (s. applyGainSoftClip).
				applyGainSoftClip(pInput, loadF64(&atSpkGain))

				// Pegelanzeige (immer aktiv im Modus), gedrosselt auf ~15 Updates/s.
				level := chunkPeak(pInput) / 20000.0
				updateMeterThrottled(&callerMeterLastNs, &callerMeterPeak, level, func(lv float64) {
					callerLevel.SetValue(lv)
					if lv > callerMarkerVal { // Peak-Hold als Richtwert
						callerMarkerVal = lv
						if callerMeter != nil {
							callerMeter.Refresh()
						}
					}
				})

				if isRecording.Load() && atHeadsetMode.Load() {
					c := make([]byte, len(pInput))
					copy(c, pInput)
					// Non-blocking (siehe Mic-Callback)
					select {
					case sampleChanCaller <- c:
					default:
						Log("WARN: Anrufer-Audiopuffer voll – Segment verworfen")
					}
				}
			}
		}

		if devLoop, err := malgo.InitDevice(mctx.Context, configLoop, malgo.DeviceCallbacks{Data: onDataLoop}); err == nil {
			callerDevice = devLoop
			callerDevice.Start() // Sofort starten für Pegelanzeige
		}
	}

	// Buffer-Loops: schneiden den Audiostrom per VAD an Sprechpausen (statt
	// des frueheren starren 4-s-Fensters, das Woerter an der Byte-Grenze
	// zerteilte - Details s. vad.go). Der Ticker flusht beim Aufnahme-Stopp
	// den Restpuffer: der Callback liefert dann keine Chunks mehr, ohne Flush
	// fehlten die letzten Worte des Gespraechs im Transkript.
	runVADLoop := func(ctx context.Context, ch <-chan []byte, speaker string, gain func() float64) {
		// Transkriptions-Warteschlange: processSegment (lokale whisper-
		// Inferenz, bei large-v3-turbo mehrere Sekunden je Segment) darf die
		// VAD-Schleife NICHT blockieren - sonst laeuft der 6-s-Audiopuffer
		// des Capture-Callbacks ueber ("Audiopuffer voll", verlorene
		// Segmente). Ein Worker je Sprecher arbeitet die Segmente sequenziell
		// ab (Reihenfolge im Transkript bleibt erhalten). Ist auch die
		// Warteschlange voll (Inferenz dauerhaft langsamer als Echtzeit),
		// wird das AELTESTE Segment verworfen - mit klarer Log-Meldung.
		queue := make(chan []byte, 16)
		defer close(queue) // beendet den Worker beim Stopp der Schleife
		go func() {
			for segAudio := range queue {
				processSegment(segAudio, speaker)
			}
		}()
		enqueue := func(segAudio []byte, sp string) {
			select {
			case queue <- segAudio:
				return
			default:
			}
			select {
			case old := <-queue:
				Log(fmt.Sprintf("WARN: %s-Transkription kommt nicht hinterher - ältestes Segment (%.1f s) verworfen",
					sp, float64(len(old))/float64(vadBytesPerSecond)))
			default:
			}
			select {
			case queue <- segAudio:
			default:
			}
		}
		seg := &vadSegmenter{speaker: speaker, gain: gain, emit: enqueue}
		stream := &remoteStreamer{speaker: speaker, gain: gain}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case samples, ok := <-ch:
				if !ok {
					return
				}
				if atWhisperLocal.Load() {
					seg.feed(samples)
				} else {
					// Remote-GPU: kontinuierlich streamen; die Äußerungs-
					// Segmentierung meldet der Streamer dem Server per
					// endOfUtterance (s. remoteStreamer in remote_stt.go).
					stream.feed(samples)
				}
			case <-ticker.C:
				if !isRecording.Load() {
					seg.flush()
					stream.flush()
				}
			}
		}
	}

	agentCtx, agentCancel := context.WithCancel(context.Background())
	go runVADLoop(agentCtx, sampleChanAgent, "Agent", func() float64 { return loadF64(&atMicGain) })

	callerCtx, callerCancel := context.WithCancel(context.Background())
	go runVADLoop(callerCtx, sampleChanCaller, "Anrufer", func() float64 { return loadF64(&atSpkGain) })

	// Cancel-Funktionen der NEUEN Goroutinen global merken, damit der nächste
	// prepareAudio-Aufruf genau diese (und nicht sich selbst) beenden kann.
	agentBufCancel = agentCancel
	callerBufCancel = callerCancel
}

func getDeviceList(kind malgo.DeviceType) ([]string, map[string]malgo.DeviceID) {
	if mctx == nil {
		backends := []malgo.Backend{}
		if runtime.GOOS == "windows" {
			backends = append(backends, malgo.BackendWasapi)
		}
		c, _ := malgo.InitContext(backends, malgo.ContextConfig{}, nil)
		mctx = c
	}

	devices, _ := mctx.Devices(kind)
	names := []string{"System-Standard"}
	mapping := make(map[string]malgo.DeviceID)

	for _, d := range devices {
		name := d.Name()
		names = append(names, name)
		mapping[name] = d.ID
	}
	return names, mapping
}

func detectSilence(samples []byte) {
	var max int16
	for i := 0; i < len(samples); i += 2 {
		val := int16(binary.LittleEndian.Uint16(samples[i : i+2]))
		if val < 0 {
			val = -val
		}
		if val > max {
			max = val
		}
	}

	// Threshold für Stille (ca. 2% Amplitude). Aufs ROH-Signal normalisieren:
	// die Samples sind bereits um den Mikro-Gain verstärkt - ohne die Division
	// hinge die Pausenerkennung (Satzende -> LLM-Korrektur) am Gain-Regler.
	g := loadF64(&atMicGain)
	if g < 1 {
		g = 1
	}
	if float64(max)/g > 600 {
		now := time.Now()
		lastSoundTime.Store(&now)
		isSilent.Store(false)
	} else {
		// lastSoundTime kann beim allerersten Aufruf noch leer sein (nil interface),
		// daher abgesicherte Type-Assertion statt direktem Dereferenzieren.
		lt, ok := lastSoundTime.Load().(*time.Time)
		if !isSilent.Load() && ok && time.Since(*lt) > time.Duration(loadF64(&atPauseThresh)*1000)*time.Millisecond {
			isSilent.Store(true)
			if atHasPostProc.Load() {
				// Whisper+LLM: Sprechpause beendet den Satz -> zur LLM-Korrektur geben.
				fyne.Do(flushPendingToCorrection)
			}
			// Ohne Nachbearbeitung: kein Umbruch bei Pause – Absätze entstehen nur
			// bei Sprecherwechsel (siehe writeSpeakerPrefix).
		}
	}
}

// nowStamp liefert den Zeitstempel für ein Sprecher-Label: TT.MM.JJJJ - HH:MM:SS.
func nowStamp() string { return time.Now().Format("02.01.2006 - 15:04:05") }

// correctionJob ist ein an die LLM-Satzkorrektur (Whisper+LLM) übergebener Block.
type correctionJob struct {
	speaker string
	raw     string
	ts      string
}

// pendingBlock ist ein Whisper-Rohblock, der gerade per LLM korrigiert wird. Sein
// Rohtext bleibt im Transkript sichtbar, bis die Korrektur eintrifft und ihn
// ersetzt – so verschwindet während der Korrektur kein bereits erkannter Text.
type pendingBlock struct {
	speaker string
	text    string
	ts      string
}

// refreshOutput baut das Transkript aus drei Ebenen zusammen: dem finalisierten
// Text (currentText), den noch in Korrektur befindlichen Rohblöcken (inProgress)
// und dem aktuell laufenden Live-Rohblock (pendingRaw). Sprecher-Labels nur bei
// Wechsel. MUSS im Fyne-Main-Thread laufen.
func refreshOutput() {
	var sb strings.Builder
	sb.WriteString(currentText.String())
	lastSp := lastSpeaker // lokale Fortschreibung für noch nicht finalisierte Teile
	emit := func(sp, ts, text string) {
		if text == "" {
			return
		}
		if sp != lastSp {
			if lastSp != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("%s [%s]: ", ts, sp))
			lastSp = sp
		} else {
			sb.WriteString(" ")
		}
		sb.WriteString(text)
	}
	for _, pb := range inProgress {
		emit(pb.speaker, pb.ts, pb.text)
	}
	if pendingRaw.Len() > 0 {
		emit(pendingSpeaker, pendingTs, pendingRaw.String())
	}
	full := strings.TrimLeft(sb.String(), " ")
	outputArea.SetText(full)
	outputArea.CursorRow = len(strings.Split(full, "\n"))
}

func appendToOutput(text string) {
	// Wird aus Worker-Goroutinen / Audio-Callbacks aufgerufen. Fyne (>=2.6)
	// verlangt UI-Zugriffe über fyne.Do; das schützt zugleich currentText.
	fyne.Do(func() {
		currentText.WriteString(text)
		refreshOutput()
	})
}

// writeSpeakerPrefix schreibt das Label [Sprecher] in currentText – aber nur bei
// Sprecherwechsel (sonst nur ein Trennzeichen). marker kennzeichnet die Pipeline
// (z.B. "*" für Gemma Native). Erwartet Ausführung im Main-Thread.
func writeSpeakerPrefix(speaker, marker, ts string) {
	if speaker != lastSpeaker {
		if lastSpeaker != "" {
			currentText.WriteString("\n") // Sprecherwechsel -> neuer Absatz/Zeile
		}
		currentText.WriteString(fmt.Sprintf("%s [%s%s]: ", ts, speaker, marker))
		lastSpeaker = speaker
	} else {
		currentText.WriteString(" ") // gleicher Sprecher -> fortlaufend, kein Label/Umbruch
	}
}

// appendSpeakerSegment hängt einen fertigen Abschnitt an (Whisper+Gemma, Gemma
// Native). Sprecher-Label nur bei Wechsel (siehe writeSpeakerPrefix).
func appendSpeakerSegment(speaker, marker, text string) {
	fyne.Do(func() {
		writeSpeakerPrefix(speaker, marker, nowStamp())
		currentText.WriteString(text)
		refreshOutput()
	})
}

// appendPendingRaw fügt einen Whisper-Rohabschnitt zum laufenden Block hinzu
// (live sichtbar). Bei Sprecherwechsel ohne Pause wird der bisherige Block zuvor
// zur Korrektur übergeben.
func appendPendingRaw(speaker, text string) {
	fyne.Do(func() {
		if pendingRaw.Len() > 0 && speaker != pendingSpeaker {
			flushPendingToCorrection()
		}
		if pendingRaw.Len() == 0 {
			pendingTs = nowStamp() // Zeitstempel beim Block-Start fixieren
		}
		pendingSpeaker = speaker
		if pendingRaw.Len() > 0 {
			pendingRaw.WriteString(" ")
		}
		pendingRaw.WriteString(text)
		refreshOutput()
	})
}

// flushPendingToCorrection übergibt den aktuellen Rohblock an die Korrektur-
// Warteschlange. Der Block bleibt als inProgress-Eintrag SICHTBAR (Rohtext), bis
// die Korrektur eintrifft. MUSS im Main-Thread laufen (Zugriff pendingRaw).
func flushPendingToCorrection() {
	if pendingRaw.Len() == 0 {
		return
	}
	speaker := pendingSpeaker
	ts := pendingTs
	raw := strings.TrimSpace(pendingRaw.String())
	pendingRaw.Reset()
	pendingSpeaker = ""
	select {
	case correctionJobs <- correctionJob{speaker: speaker, raw: raw, ts: ts}:
		// Rohblock weiterhin anzeigen, bis der Worker ihn korrigiert ersetzt.
		inProgress = append(inProgress, &pendingBlock{speaker: speaker, text: raw, ts: ts})
	default:
		// Warteschlange voll: Rohtext sofort final übernehmen (nichts geht verloren).
		writeSpeakerPrefix(speaker, "", ts)
		currentText.WriteString(raw)
	}
	refreshOutput()
}

// correctionWorker arbeitet Korrektur-Jobs seriell ab (stabile Reihenfolge).
// Das Ergebnis ersetzt den jeweils ältesten inProgress-Block: dieser wandert
// korrigiert in den finalisierten Text – der angezeigte Rohtext wird dadurch
// in-place ersetzt, ohne zwischenzeitlich zu verschwinden.
func correctionWorker() {
	for job := range correctionJobs {
		corr := correctWithLLM(job.raw)
		fyne.Do(func() {
			if len(inProgress) > 0 {
				pb := inProgress[0]
				inProgress = inProgress[1:]
				writeSpeakerPrefix(pb.speaker, "", pb.ts)
				currentText.WriteString(corr)
			}
			refreshOutput()
		})
	}
}

// correctWithLLM lässt den Rohtext vom aktuell gewählten Analyse-Backend
// grammatikalisch korrigieren. Bei Fehler wird der Rohtext unverändert behalten.
func correctWithLLM(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	const prompt = "Korrigiere den folgenden gesprochenen Text in Grammatik, Zeichensetzung und Groß-/Kleinschreibung. Behalte Sinn und Sprache exakt bei. Gib AUSSCHLIESSLICH den korrigierten Text zurück – ohne Einleitung, Anführungszeichen oder Erklärungen."
	res := strings.TrimSpace(runLLM(config.PostProcModel, raw, prompt))
	if res == "" || strings.HasPrefix(res, "Fehler") || strings.HasPrefix(res, "KI-Fehler") {
		Log("Whisper+LLM: Korrektur fehlgeschlagen, nutze Rohtext. (" + res + ")")
		return raw
	}
	return res
}

// hasSpeech schätzt per Energie (RMS), ob ein Segment überhaupt Sprache enthält.
// Stille/Rauschen wird NICHT transkribiert – das verhindert Whisper-Halluzinationen
// ("Vielen Dank", "Untertitelung …" u.ä.) auf einem stillen Kanal. Geprüft wird
// in 250-ms-SUBFENSTERN: die frühere RMS über das gesamte Segment verdünnte
// kurze Äußerungen (ein "Ja." in einem sonst stillen Fenster fiel unter die
// Schwelle und wurde komplett verworfen) – jetzt genügt EIN energiereiches
// Subfenster. Die RMS wird gegen den Kanal-Gain normalisiert, da das Audio
// bereits verstärkt vorliegt.
func hasSpeech(audio []byte, speaker string) bool {
	if len(audio) < 2 {
		return false
	}
	gain := loadF64(&atMicGain)
	if speaker == "Anrufer" {
		gain = loadF64(&atSpkGain)
	}
	if gain < 1 {
		gain = 1
	}
	const win = vadBytesPerSecond / 4 // 250 ms
	for start := 0; start < len(audio); start += win {
		end := start + win
		if end > len(audio) {
			end = len(audio)
		}
		var sumSq float64
		n := 0
		for i := start; i+1 < end; i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(audio[i : i+2])))
			sumSq += v * v
			n++
		}
		if n > 0 && math.Sqrt(sumSq/float64(n))/gain > 200 {
			return true // Schwelle auf das Roh-Audio (vor Verstärkung)
		}
	}
	return false
}

func processSegment(audio []byte, speaker string) {
	if !hasSpeech(audio, speaker) {
		return // stilles Segment überspringen (Schutz vor Whisper-Halluzinationen)
	}
	if !atWhisperLocal.Load() {
		// Erkennung über den Remote-GPU-Whisper-Server (Ergebnis kommt asynchron).
		remoteTranscribe(audio, speaker)
		return
	}
	// Whisper lokal: bevorzugt der staendig laufende whisper-server (Modell
	// bleibt im RAM, WAV geht direkt aus dem Speicher per HTTP - kein
	// Prozess-Spawn/Modell-Reload/Disk-I/O pro Segment, s. server_manager.go).
	if text, ok := whisperServerTranscribe(audio, getSTTTail()); ok {
		deliverSTTText(text, speaker)
		return
	}
	// Rueckfall: whisper-cli (Server fehlt noch/Warmup laeuft/Anfrage schlug fehl).
	tmpFile := filepath.Join(exeDir, fmt.Sprintf("tmp_%s_%d.wav", speaker, time.Now().UnixNano()))
	if err := writeWav(tmpFile, audio); err != nil {
		return
	}
	defer os.Remove(tmpFile)

	whisperBin := filepath.Join(exeDir, "libs", "whisper-cli")
	if runtime.GOOS == "windows" {
		whisperBin = filepath.Join(exeDir, "libs", "whisper-cli.exe")
	}

	args := []string{"-m", localWhisperModelPath(), "-f", tmpFile, "-l", "de", "-nt"}
	// Kontext-Priming: die letzten ~200 Zeichen erkannten Textes als initial
	// prompt mitgeben - konsistente Schreibweisen/Eigennamen ueber
	// Segmentgrenzen hinweg, weniger Fehler am Segmentanfang (s. vad.go).
	if tail := getSTTTail(); tail != "" {
		args = append(args, "--prompt", tail)
	}
	cmd := exec.Command(whisperBin, args...)

	// Setzt den Silent-Mode (plattformspezifisch)
	setSilent(cmd)

	// Wir loggen den Befehl, aber zeigen ihn nicht in der Konsole
	out, err := cmd.CombinedOutput()
	if err != nil {
		return
	}

	rawText := string(out)

	// CPU/GPU Erkennung
	if strings.Contains(rawText, "backend from") || strings.Contains(rawText, "backend_init_gpu") {
		engineText := "Engine: GPU beschleunigt"
		if strings.Contains(rawText, "CPU") && !strings.Contains(rawText, "GPU") {
			engineText = "Engine: CPU (AVX2/512)"
		}
		fyne.Do(func() { setEngineInfo(engineText) })
	}
	lines := strings.Split(rawText, "\n")
	var cleanLines []string
	re := regexp.MustCompile(`^(whisper_|load_|main:|system_info:|[^A-Za-z0-9äöüÄÖÜ])`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !re.MatchString(trimmed) {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	if len(cleanLines) > 0 {
		deliverSTTText(strings.Join(cleanLines, " "), speaker)
	}
}

// deliverSTTText: gemeinsame Weiterverarbeitung eines lokal erkannten
// Segment-Textes (whisper-server UND whisper-cli-Rueckfall): Kontext-Tail
// fuer das Priming des naechsten Segments nachziehen, dann anzeigen (roh bei
// aktiver Nachbearbeitung - Korrektur folgt am Satzende, s. detectSilence).
func deliverSTTText(text, speaker string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	updateSTTTail(text)
	if atHasPostProc.Load() {
		appendPendingRaw(speaker, text)
	} else {
		appendSpeakerSegment(speaker, "", text)
	}
}

func runAnalysisLogic(text, userPrompt string) string {
	return runLLM(config.AnalysisModel, text, userPrompt)
}

// extractTicketKeywords - Schritt 1 der Schlagwort-Ticketsuche ("Suche
// passende Tickets"): laesst das konfigurierte Analyse-LLM 2-3 Schlagworte
// aus der Mitschrift extrahieren und zeigt sie SOFORT in einem Popup
// (Erfolg ODER Fehler); die Schlagworte stehen FETT direkt hinter
// "Extrahierte Schlagworte:". Danach - und erst dann - laeuft then() (die
// eigentliche Ticketsuche; Nutzer-Vorgabe: Extraktion+Anzeige zuerst).
// Die Suche MIT den Schlagworten (Schritt 2) ist noch NICHT moeglich:
// Jarvis- und Kundenverwaltungs-API muessen dafuer erst angepasst werden -
// s. TODO.md Punkt "Schlagwort-Ticketsuche". Laeuft asynchron (LLM-Aufruf);
// UI-Zugriffe via fyne.Do; then() laeuft im Fyne-Main-Thread.
func extractTicketKeywords(text string, win fyne.Window, then func(keywords string)) {
	if then == nil {
		then = func(string) {}
	}
	if len(strings.TrimSpace(text)) < 10 {
		then("")
		return // (fast) leere Mitschrift - nichts zu extrahieren
	}
	prompt := T("Extrahiere aus dem folgenden Support-Gespräch 2 bis 3 Schlagworte, die das Anliegen am besten beschreiben. Antworte NUR mit den Schlagworten, durch Kommas getrennt, ohne weitere Erklärung.")
	Log(fmt.Sprintf("Schlagwort-Extraktion gestartet (Analyse-LLM %s, %d Zeichen Mitschrift)", config.AnalysisModel, len(text)))
	// Sichtbares "Ich arbeite": derselbe Endlos-Balken wie beim Analysieren
	// (Aufruf kommt aus dem Button-Handler, also Main-Thread).
	if analysisProgress != nil {
		analysisProgress.Show()
	}
	go func() {
		res := strings.TrimSpace(runAnalysisLogic(text, prompt))
		Log("Schlagwort-Extraktion (roh): " + res)
		isErr := res == "" || llmErrorText(res)
		if !isErr {
			res = strings.Join(strings.Fields(res), " ") // einzeilig glaetten
			// Ueblichen LLM-Vorspann abraeumen ("Schlagworte: A, B").
			low := strings.ToLower(res)
			for _, label := range []string{"schlagworte:", "schlagwörter:", "stichworte:", "keywords:"} {
				if strings.HasPrefix(low, label) {
					res = strings.TrimSpace(res[len(label):])
					break
				}
			}
			isErr = res == ""
		}
		fyne.Do(func() {
			if analysisProgress != nil {
				analysisProgress.Hide()
			}
			keywords := ""
			if isErr {
				if res == "" {
					res = T("(keine Antwort erhalten)")
				}
				showErr(fmt.Errorf(T("Schlagworte konnten nicht ermittelt werden: ")+"%s", res), win)
			} else {
				keywords = res
				// Schlagworte FETT direkt hinter dem Label (RichText-Segmente;
				// ein Label kann keine Teil-Fettung).
				line := widget.NewRichText(
					&widget.TextSegment{Text: T("Extrahierte Schlagworte:") + " ", Style: widget.RichTextStyleInline},
					&widget.TextSegment{Text: res, Style: widget.RichTextStyle{
						Inline:    true,
						TextStyle: fyne.TextStyle{Bold: true},
					}},
				)
				hint := widget.NewLabel(T("Hinweis: Die Jarvis-API muss noch angepasst werden, damit mit diesen Schlagworten auch dort passende Tickets gesucht werden können. Die Kundenverwaltung (getMatchingEvents) wird bereits abgefragt, sofern ein Anruf eine Kundenv.-ID geliefert hat."))
				hint.Alignment = fyne.TextAlignLeading
				// Wortumbruch + feste Dialogbreite: ohne Umbruch macht Fyne den
				// Dialog so breit wie der (lange) Hinweis in EINER Zeile und kappt
				// ihn am Fensterrand. NewCustom+Resize erzwingt eine feste Breite,
				// innerhalb derer der Hinweis sauber umbricht.
				line.Wrapping = fyne.TextWrapWord
				hint.Wrapping = fyne.TextWrapWord
				d := dialog.NewCustom(T("Schlagworte zur Ticketsuche"), T("OK"),
					container.NewVBox(line, hint), win)
				d.Resize(fyne.NewSize(480, 260))
				d.Show()
			}
			// Erst NACH der Anzeige der Schlagworte (bzw. des Fehlers) die
			// eigentliche Ticketsuche anstossen (Nutzer-Vorgabe). Die extrahierten
			// Schlagworte gehen an then() (fuer die getMatchingEvents-Suche).
			then(keywords)
		})
	}()
}

// llmErrorText erkennt die Fehler-Texte, die runLLM & Co. als STRING
// zurueckgeben (dort gibt es keinen error-Rueckgabewert). WICHTIG: nur klar
// fehlerfoermige Praefixe pruefen - ein blosses HasPrefix("Fehler") stufte
// echte Schlagwortlisten wie "Fehlermeldung, Webseite" faelschlich als
// Fehler ein.
func llmErrorText(s string) bool {
	for _, p := range []string{
		"Fehler:", "Fehler bei", // runLLM/Gemini/Ollama/vLLM ("Fehler bei"/"Fehler beim")
		"Das lokale Modell",        // llama-Instanz nicht bereit / wird neu geladen
		"Keine Analyse-Ergebnisse", // leere llama-Antwort
		"Keine Antwort von",        // leere Gemini-/vLLM-Antwort
	} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// runLLM routet eine LLM-Anfrage nach Modell-Symbol: "e2b"/"12b" an den jeweiligen
// lokalen Server, "remote" an das in der 'remote LLM'-Sektion gewählte Backend.
func runLLM(modelSymbol, text, userPrompt string) string {
	switch modelSymbol {
	case "e2b", "12b":
		inst := instanceFor(modelSymbol)
		if inst == nil {
			return "Fehler: unbekanntes lokales Modell."
		}
		return runLocalAnalysisAt(inst, text, userPrompt)
	case "remote":
		switch config.RemoteBackend {
		case "Google Flash":
			return runGeminiAnalysis(text, userPrompt)
		case "Ollama":
			return runOllamaAnalysis(text, userPrompt)
		default:
			return runVllmAnalysis(text, userPrompt)
		}
	}
	return "Fehler: kein Modell konfiguriert."
}

// runLocalAnalysisAt führt die Anfrage gegen den lokalen Server der Instanz aus.
func runLocalAnalysisAt(inst *llamaInstance, text, userPrompt string) string {
	if inst.restarting.Load() {
		return "Das lokale Modell wird gerade neu geladen. Bitte einen Moment warten und erneut versuchen."
	}
	if !inst.ready.Load() {
		return fmt.Sprintf("Das lokale Modell '%s' ist nicht bereit (Server nicht gestartet?).", inst.symbol)
	}
	inst.busy.Add(1)
	defer inst.busy.Add(-1)

	prompt := fmt.Sprintf("<|turn|>system\n<|think|>Gib AUSSCHLIESSLICH das Ergebnis der Analyse aus. Keine Einleitung, kein 'Thinking Process', keine technischen Erklärungen. Deine Antwort darf NUR die Zusammenfassung oder Auswertung enthalten.<turn|>\n<|turn|>user\nAnweisung: %s\n\nText:\n%s<turn|>\n<|turn|>model\n", userPrompt, text)

	payload := map[string]interface{}{
		"model": "gemma-4",
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"stream":      false,
		"temperature": 0.3,
		"top_p":       0.9,
	}

	jsonData, _ := json.Marshal(payload)

	// Timeout massiv erhöhen für langsame lokale Inferenz großer Texte
	client := &http.Client{Timeout: 600 * time.Second}
	resp, err := client.Post(inst.baseURL()+"/v1/chat/completions", "application/json", bytes.NewBuffer(jsonData))

	if err != nil {
		Log("ANALYSE-SERVER-ERROR: " + err.Error())
		return "Fehler: Der lokale Gemma-Server ist noch nicht bereit oder ausgelastet. Bitte warte einen Moment."
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Fehler: Server antwortet mit Status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "Fehler beim Dekodieren der Analyse-Antwort."
	}

	if len(result.Choices) > 0 {
		resRaw := result.Choices[0].Message.Content

		// Falls doch noch 'Thinking' Tags drin sind, wegschneiden
		if idx := strings.LastIndex(resRaw, "[End thinking]"); idx != -1 {
			resRaw = resRaw[idx+len("[End thinking]"):]
		}
		if idx := strings.LastIndex(resRaw, "<|think|>"); idx != -1 {
			// Falls es nach dem think-Tag kommt
			parts := strings.Split(resRaw, "<turn|>")
			if len(parts) > 1 {
				resRaw = parts[len(parts)-1]
			}
		}

		return strings.TrimSpace(resRaw)
	}

	return "Keine Analyse-Ergebnisse empfangen."
}

func updateWindowTitle(w fyne.Window) {
	title := fmt.Sprintf("SpeechToText und Support Assistent (v%s)", AppVersion)
	if u, err := user.Current(); err == nil && u.Username != "" {
		title = fmt.Sprintf("%s — %s", title, u.Username) // u.Username liefert unter Windows bereits "DOMAIN\Nutzer"
	}
	w.SetTitle(title)
}

func createWavData(data []byte) []byte {
	h := [44]byte{}
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(36+len(data)))
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 1)
	binary.LittleEndian.PutUint16(h[22:24], 1)
	binary.LittleEndian.PutUint32(h[24:28], 16000)
	binary.LittleEndian.PutUint32(h[28:32], 16000*2)
	binary.LittleEndian.PutUint16(h[32:34], 2)
	binary.LittleEndian.PutUint16(h[34:36], 16)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], uint32(len(data)))

	result := make([]byte, 44+len(data))
	copy(result[0:44], h[:])
	copy(result[44:], data)
	return result
}

func writeWav(filename string, data []byte) error {
	wav := createWavData(data)
	return os.WriteFile(filename, wav, 0644)
}

// ========= Remote LLM Implementations =========

type geminiRequest struct {
	Contents []struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func runGeminiAnalysis(text, userPrompt string) string {
	if config.Flash.ApiKey == "" {
		return "Fehler: Kein Gemini API Key hinterlegt."
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-flash-latest:generateContent?key=" + config.Flash.ApiKey

	prompt := fmt.Sprintf("Anweisung: %s\n\nText:\n%s", userPrompt, text)

	reqBody := geminiRequest{}
	reqBody.Contents = []struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}{{
		Parts: []struct {
			Text string `json:"text"`
		}{{Text: prompt}},
	}}

	jsonData, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "Fehler bei Gemini-Verbindung: " + err.Error()
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Fehler beim Lesen der Gemini-Antwort: " + err.Error()
	}

	if resp.StatusCode != http.StatusOK {
		Log(fmt.Sprintf("GEMINI-API-ERROR: Status %d | Body: %s", resp.StatusCode, string(body)))
		return fmt.Sprintf("KI-Fehler (Status %d): %s", resp.StatusCode, string(body))
	}

	var gResp geminiResponse
	if err := json.Unmarshal(body, &gResp); err != nil {
		return "Fehler beim Dekodieren der Gemini-Antwort: " + err.Error()
	}

	if len(gResp.Candidates) > 0 && len(gResp.Candidates[0].Content.Parts) > 0 {
		return strings.TrimSpace(gResp.Candidates[0].Content.Parts[0].Text)
	}

	return "Keine Antwort von Gemini erhalten. Status: " + resp.Status
}

type ollamaRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	System  string         `json:"system"`
	Stream  bool           `json:"stream"`
	Options map[string]any `json:"options"`
}

type ollamaResponse struct {
	Response string `json:"response"`
}

func runOllamaAnalysis(text, userPrompt string) string {
	url := strings.TrimRight(config.Ollama.Url, "/") + "/api/generate"

	// Wenn der Server sehr alt ist oder Parameter blockiert, übergeben wir "Gewalt-Prompting",
	// indem wir die Systemrolle und die Temperatur (falls unterstützt) zwingend als hart kodierten String senden.
	prompt := fmt.Sprintf("<start_of_turn>user\nWICHTIGE SYSTEMREGEL: Du bist ein extrem präziser, medizinischer Assistent. Gib zu 100%% NUR das gefilterte Ergebnis aus. Erfinde auf gar keinen Fall externe Nummern (wie LANR, BSNR) und nenne KEINE Einleitungs- oder Schlussformeln.\n\nANWEISUNG ZUM TEXT:\n%s\n\nTEXT:\n%s<end_of_turn>\n<start_of_turn>model\n", userPrompt, text)

	payload := map[string]interface{}{
		"model":  config.Ollama.Model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.05,
			"top_p":       0.9,
		},
	}

	jsonData, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "Fehler bei Server-Verbindung: " + err.Error()
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Fehler beim Lesen der Antwort: " + err.Error()
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Fehler: Server antwortet mit %s.\nRaw-Antwort:\n\n%s", resp.Status, string(body))
	}

	var oResp struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &oResp); err != nil {
		return "Fehler beim Dekodieren der JSON-Antwort: " + err.Error() + "\nRaw:\n" + string(body)
	}

	res := strings.TrimSpace(oResp.Response)
	if res == "" {
		return fmt.Sprintf("Der Server hat erfolgreich geantwortet, lieferte aber keinen Text zurück.\nRaw:\n%s", string(body))
	}
	return res
}

// ========= vLLM Integration =========

// httpGetJSON führt ein GET mit optionalem Bearer-Token aus und gibt den Body
// zurück. Liefert aussagekräftige Fehler (Verbindung, HTTP-Status), damit die
// Discovery nicht stillschweigend "keine Modelle" meldet.
func httpGetJSON(url, apiKey string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Server nicht erreichbar (%s):\n%v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d von %s", resp.StatusCode, url)
	}
	return body, nil
}

// vllmBaseURL bereinigt eine vLLM-/OpenAI-Basis-URL: entfernt abschließende
// Slashes und ein optionales "/v1"-Suffix. So führt eine vom Nutzer inkl. "/v1"
// eingegebene URL nicht zu doppelten Pfaden wie ".../v1/v1/models".
func vllmBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	u = strings.TrimSuffix(u, "/v1")
	return strings.TrimRight(u, "/")
}

// fetchVllmModels fragt die verfügbaren Modelle eines OpenAI-kompatiblen Servers
// (vLLM) über GET /v1/models ab.
func fetchVllmModels(baseURL, apiKey string) ([]string, error) {
	url := vllmBaseURL(baseURL) + "/v1/models"
	body, err := httpGetJSON(url, apiKey)
	if err != nil {
		Log(fmt.Sprintf("vLLM Model-Discover-Fehler: %v", err))
		return nil, err
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		Log("vLLM /v1/models Rohantwort: " + string(body))
		return nil, fmt.Errorf("Antwort nicht im erwarteten Format: %v", err)
	}
	var models []string
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}

// fetchOllamaModels fragt die lokal installierten Modelle eines Ollama-Servers
// über GET /api/tags ab.
func fetchOllamaModels(baseURL, apiKey string) ([]string, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/tags"
	body, err := httpGetJSON(url, apiKey)
	if err != nil {
		Log(fmt.Sprintf("Ollama Model-Discover-Fehler: %v", err))
		return nil, err
	}
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		Log("Ollama /api/tags Rohantwort: " + string(body))
		return nil, fmt.Errorf("Antwort nicht im erwarteten Format: %v", err)
	}
	var models []string
	for _, m := range result.Models {
		if m.Name != "" {
			models = append(models, m.Name)
		}
	}
	return models, nil
}

// runVllmAnalysis analysiert Text über den vLLM Server (OpenAI-kompatible Chat Completions API)
func runVllmAnalysis(text, userPrompt string) string {
	baseURL := vllmBaseURL(config.Vllm.Url)
	if baseURL == "" {
		return "Fehler: Keine vLLM-Server-URL konfiguriert."
	}
	if config.Vllm.Model == "" {
		return "Fehler: Kein vLLM-Modell ausgewählt."
	}

	apiURL := baseURL + "/v1/chat/completions"

	prompt := fmt.Sprintf("Du bist ein präziser Assistent. Gib AUSSCHLIESSLICH das Ergebnis der Analyse aus. Keine Einleitung, kein 'Thinking Process', keine technischen Erklärungen.\n\nAnweisung: %s\n\nText:\n%s", userPrompt, text)

	payload := map[string]interface{}{
		"model": config.Vllm.Model,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"stream":      false,
		"temperature": 0.3,
		"top_p":       0.9,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "Fehler beim Erstellen der Anfrage: " + err.Error()
	}

	client := &http.Client{Timeout: 600 * time.Second}
	resp, err := client.Post(apiURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "Fehler bei vLLM-Server-Verbindung: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Sprintf("Fehler: vLLM antwortet mit Status %d.\nRaw:\n%s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Fehler beim Lesen der vLLM-Antwort: " + err.Error()
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "Fehler beim Dekodieren der vLLM-Antwort: " + err.Error() + "\nRaw:\n" + string(body)
	}

	if len(result.Choices) > 0 && result.Choices[0].Message.Content != "" {
		return strings.TrimSpace(result.Choices[0].Message.Content)
	}

	return "Keine Antwort von vLLM erhalten."
}
