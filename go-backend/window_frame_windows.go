//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type nativeWindowFrameStyle struct {
	caption uint32
	border  uint32
	text    uint32
}

func localDropWindowFrameStyle() nativeWindowFrameStyle {
	return nativeWindowFrameStyle{
		caption: uint32(panel),
		border:  uint32(line),
		text:    uint32(ink),
	}
}

// applyLocalDropWindowFrame mantém a moldura padrão do Windows, só com as
// cores do LocalDrop. Versões antigas ignoram os atributos e seguem normais.
func applyLocalDropWindowFrame(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	handle := windows.HWND(hwnd)
	darkMode := uint32(0) // barra clara, igual ao cabeçalho
	if err := setDWMWindowAttribute(handle, windows.DWMWA_USE_IMMERSIVE_DARK_MODE, darkMode); err != nil {
		// Versões do Windows 10 anteriores ao build 18985 usavam o atributo 19.
		_ = setDWMWindowAttribute(handle, 19, darkMode)
	}
	style := localDropWindowFrameStyle()
	_ = setDWMWindowAttribute(handle, windows.DWMWA_CAPTION_COLOR, style.caption)
	_ = setDWMWindowAttribute(handle, windows.DWMWA_BORDER_COLOR, style.border)
	_ = setDWMWindowAttribute(handle, windows.DWMWA_TEXT_COLOR, style.text)
}

func setDWMWindowAttribute(handle windows.HWND, attribute uint32, value uint32) error {
	return windows.DwmSetWindowAttribute(handle, attribute, unsafe.Pointer(&value), uint32(unsafe.Sizeof(value)))
}
