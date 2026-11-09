//go:build !windows

package main

import "os"

// moveNoReplace move o arquivo sem sobrescrever o destino: o link falha se o
// destino existir; entre volumes ou sem suporte a links, recorre à cópia.
func moveNoReplace(source, destination string) error {
	err := os.Link(source, destination)
	if err == nil {
		return os.Remove(source)
	}
	if os.IsExist(err) {
		return err
	}
	return copyNoReplace(source, destination)
}
