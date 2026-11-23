package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
)

func existingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		if info, err := os.Stat(current); err == nil && info.IsDir() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("nenhum diretório existente para %s", path)
		}
		current = parent
	}
}

func saturatingAdd(values ...uint64) uint64 {
	var result uint64
	for _, value := range values {
		if math.MaxUint64-result < value {
			return math.MaxUint64
		}
		result += value
	}
	return result
}

// ensureTransferSpaceLocked confere a pasta onde o arquivo é montado. Os blocos
// são gravados direto no arquivo final, então só os bytes restantes precisam caber.
func (s *Server) ensureTransferSpaceLocked(transfer *TransferRecord) error {
	receiver := s.state.Devices[transfer.ReceiverID]
	destination := filepath.Join(s.cfg.StorageRoot, "go-completed", transfer.ID)
	if receiver != nil && receiver.Host {
		destination = s.state.Settings.DownloadDirectory
	}
	remaining := transfer.TotalBytes - transfer.ReceivedBytes
	if remaining < 0 {
		remaining = 0
	}
	destinationPath, err := existingAncestor(destination)
	if err != nil {
		return problem(422, "STORAGE_UNAVAILABLE", "Não deu para usar a pasta de recebimento. Confira a pasta em Ajustes.")
	}
	free, err := availableDiskBytes(destinationPath)
	if err != nil {
		return problem(422, "STORAGE_UNAVAILABLE", "Não deu para conferir o espaço livre da pasta de recebimento. Confira a pasta em Ajustes.")
	}
	if free < saturatingAdd(uint64(remaining), s.cfg.MinimumFreeSpace) {
		return problem(422, "INSUFFICIENT_STORAGE", "O computador está sem espaço livre para receber estes arquivos. Libere espaço e tente de novo.")
	}
	return nil
}
