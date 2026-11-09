//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// focusNativeWindow traz de volta a janela da instância já aberta, inclusive
// quando ela está escondida na bandeja.
func focusNativeWindow() error {
	user32 := syscall.NewLazyDLL("user32.dll")
	findWindow := user32.NewProc("FindWindowW")
	showWindow := user32.NewProc("ShowWindow")
	setForeground := user32.NewProc("SetForegroundWindow")
	title, _ := syscall.UTF16PtrFromString(nativeWindowTitle)
	window, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(title)))
	if window == 0 {
		return fmt.Errorf("a janela nativa do LocalDrop não foi encontrada")
	}
	showWindow.Call(window, 5) // SW_SHOW
	showWindow.Call(window, 9) // SW_RESTORE
	setForeground.Call(window)
	return nil
}

func showFatalError(message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("LocalDrop")
	_, _, _ = messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

// openPath abre um arquivo ou pasta com o programa padrão do Windows.
func openPath(path string) error {
	if path == "" {
		return fmt.Errorf("o arquivo não está mais disponível")
	}
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

// revealPath abre o Explorer com o arquivo selecionado.
func revealPath(path string) error {
	if path == "" || strings.ContainsRune(path, '"') {
		return fmt.Errorf("o arquivo não está mais disponível")
	}
	command := exec.Command("explorer.exe")
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	return command.Start()
}
