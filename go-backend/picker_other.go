//go:build !windows

package main

import "errors"

func pickDirectory(_ string) (string, bool, error) {
	return "", false, errors.New("seletor nativo indisponível")
}
