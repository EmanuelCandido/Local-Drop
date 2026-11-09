//go:build !windows

package main

import "fmt"

func focusNativeWindow() error {
	return fmt.Errorf("a interface desktop nativa está disponível no Windows")
}

func showFatalError(message string) { fmt.Println(message) }
