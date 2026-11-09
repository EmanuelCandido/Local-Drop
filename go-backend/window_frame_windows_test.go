//go:build windows

package main

import (
	"testing"

	"github.com/lxn/walk"
)

func TestWindowFrameUsesLocalDropPalette(t *testing.T) {
	t.Parallel()

	style := localDropWindowFrameStyle()
	if style.caption != uint32(sidebar) {
		t.Fatalf("barra de título fora da paleta: %#x", style.caption)
	}
	if style.border != uint32(primary) {
		t.Fatalf("borda fora da paleta: %#x", style.border)
	}
	if style.text != uint32(walk.RGB(255, 255, 255)) {
		t.Fatalf("texto da barra deve permanecer claro: %#x", style.text)
	}
}
