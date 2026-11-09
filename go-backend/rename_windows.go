//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// moveNoReplace move o arquivo sem sobrescrever o destino. Na mesma unidade é
// uma renomeação instantânea; entre unidades o Windows copia e remove a origem.
func moveNoReplace(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_COPY_ALLOWED); err != nil {
		return &os.LinkError{Op: "move", Old: source, New: destination, Err: err}
	}
	return nil
}
