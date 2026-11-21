package main

import (
	"strings"
	"testing"
	"time"
)

func TestBotHandle(t *testing.T) {
	d := &botDeps{started: time.Now().Add(-90 * time.Minute)}
	if out := d.handle("help", ""); !strings.Contains(out, "/progreso") || !strings.Contains(out, "Actualizado") {
		t.Fatalf("ayuda: %q", out)
	}
	if out := d.handle("status", ""); !strings.Contains(out, "En marcha desde hace 1 h 30 min") {
		t.Fatalf("estado: %q", out)
	}
	if out := d.handle("nada", ""); !strings.Contains(out, "/ayuda") {
		t.Fatalf("desconocido: %q", out)
	}
	if bar(35) != "▓▓▓░░░░░░░" || shortName("/datos/Fotos/Serie/Season 1/cap.mkv") != "Season 1/cap.mkv" {
		t.Fatal("barra o nombre corto")
	}
}
