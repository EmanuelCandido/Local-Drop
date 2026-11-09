package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func loadState(path, downloads string) (*State, error) {
	bytes, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newState(downloads), nil
	}
	if err != nil {
		return nil, fmt.Errorf("ler estado: %w", err)
	}
	state := newState(downloads)
	if err := json.Unmarshal(bytes, state); err != nil {
		return nil, fmt.Errorf("estado local inválido: %w", err)
	}
	if state.Devices == nil {
		state.Devices = make(map[string]*DeviceRecord)
	}
	if state.Pairings == nil {
		state.Pairings = make(map[string]*PairingRecord)
	}
	if state.Transfers == nil {
		state.Transfers = make(map[string]*TransferRecord)
	}
	if state.Settings.DownloadDirectory == "" {
		state.Settings.DownloadDirectory = downloads
	}
	for _, transfer := range state.Transfers {
		for _, file := range transfer.Files {
			if file.Receipts == nil {
				file.Receipts = make(map[int64]*ChunkReceipt)
			}
		}
	}
	return state, nil
}

func saveState(path string, state *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("criar diretório do estado: %w", err)
	}
	bytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("codificar estado: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, bytes, 0o600); err != nil {
		return fmt.Errorf("gravar estado temporário: %w", err)
	}
	backup := path + ".bak"
	_ = os.Remove(backup)
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, backup); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("preservar estado anterior: %w", err)
		}
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Rename(backup, path)
		return fmt.Errorf("publicar estado: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}
