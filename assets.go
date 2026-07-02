package main

import _ "embed"

// nexusLogoPNG ist das Firmenlogo (oben rechts neben den Tabs), fest in die
// Binary eingebettet - die App bleibt dadurch portabel/eigenstaendig.
//
//go:embed assets/nexus-dp-hell.png
var nexusLogoPNG []byte

// appIconPNG ist das Programm-/Taskleisten-Symbol (Fenster-Icon zur Laufzeit).
// Das .exe-Datei-Icon selbst kommt separat aus rsrc_windows_amd64.syso
// (eingebettete Windows-Ressource, aus derselben Bilddatei erzeugt).
//
//go:embed assets/app_icon.png
var appIconPNG []byte
