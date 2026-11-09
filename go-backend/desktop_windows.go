//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// focusNativeWindow pede para a instância já aberta mostrar a janela, mesmo
// quando ela está escondida na bandeja.
func focusNativeWindow() error {
	window := call(procFindWindowW, uintptr(unsafe.Pointer(utf16Ptr(mainWindowClass))), 0)
	if window == 0 {
		return fmt.Errorf("a janela do LocalDrop não foi encontrada")
	}
	message := call(procRegisterWindowMessageW, uintptr(unsafe.Pointer(utf16Ptr("LocalDrop.ShowWindow"))))
	call(procPostMessageW, window, message, 0, 0)
	return nil
}

func showFatalError(message string) {
	call(procMessageBoxW, 0, uintptr(unsafe.Pointer(utf16Ptr(message))), uintptr(unsafe.Pointer(utf16Ptr("LocalDrop"))), mbIconError)
}

// openPath abre um arquivo ou pasta com o programa padrão do Windows.
func openPath(path string) error {
	return shellOpen(path)
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
