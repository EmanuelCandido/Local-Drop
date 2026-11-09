//go:build windows

package main

import (
	"unsafe"

	"github.com/lxn/walk"
	"golang.org/x/sys/windows"
)

type nativeWindowFrameStyle struct {
	caption uint32
	border  uint32
	text    uint32
}

func localDropWindowFrameStyle() nativeWindowFrameStyle {
	return nativeWindowFrameStyle{
		caption: uint32(sidebar),
		border:  uint32(primary),
		text:    uint32(walk.RGB(255, 255, 255)),
	}
}

// applyLocalDropWindowFrame keeps the standard Windows frame behavior while
// making its non-client area part of the LocalDrop visual system. Unsupported
// attributes are deliberately ignored so older Windows versions retain their
// native frame instead of preventing the application from opening.
func applyLocalDropWindowFrame(window walk.Window) {
	if window == nil || window.Handle() == 0 {
		return
	}

	handle := windows.HWND(window.Handle())
	darkMode := uint32(1)
	if err := setDWMWindowAttribute(handle, windows.DWMWA_USE_IMMERSIVE_DARK_MODE, darkMode); err != nil {
		// Windows 10 versions before build 18985 used attribute 19.
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
