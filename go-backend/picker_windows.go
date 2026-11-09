//go:build windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func pickDirectory(initial string) (string, bool, error) {
	const script = `[Console]::OutputEncoding=[Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = 'Escolha a pasta de recebimento do LocalDrop'
$dialog.ShowNewFolderButton = $true
if (Test-Path -LiteralPath $env:LOCALDROP_PICKER_INITIAL) { $dialog.SelectedPath = $env:LOCALDROP_PICKER_INITIAL }
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Write($dialog.SelectedPath) }`
	command := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script)
	command.Env = append(os.Environ(), "LOCALDROP_PICKER_INITIAL="+initial)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.Output()
	if err != nil {
		return "", false, err
	}
	selected := strings.TrimSpace(string(output))
	return selected, selected != "", nil
}
