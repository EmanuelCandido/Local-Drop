//go:build windows

package main

import "testing"

func TestWindowFrameUsesLocalDropPalette(t *testing.T) {
	t.Parallel()

	style := localDropWindowFrameStyle()
	if style.caption != uint32(panel) {
		t.Fatalf("barra de título fora da paleta: %#x", style.caption)
	}
	if style.border != uint32(line) {
		t.Fatalf("borda fora da paleta: %#x", style.border)
	}
	if style.text != uint32(ink) {
		t.Fatalf("texto da barra deve ser escuro sobre a barra clara: %#x", style.text)
	}
}
