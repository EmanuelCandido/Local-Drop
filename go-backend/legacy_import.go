package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var shortZonePattern = regexp.MustCompile(`([+-]\d{2})$`)

func readCSVRows(path string) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	for index := range headers {
		headers[index] = strings.ToUpper(strings.TrimSpace(headers[index]))
	}
	rows := make([]map[string]string, 0)
	for {
		values, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		row := make(map[string]string, len(headers))
		for index, header := range headers {
			if index < len(values) {
				row[header] = values[index]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseLegacyTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "null") {
		return nil, nil
	}
	value = strings.Replace(value, " ", "T", 1)
	if shortZonePattern.MatchString(value) {
		value += ":00"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	utc := parsed.UTC()
	return &utc, nil
}

func legacyBool(value string) bool {
	parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
	return parsed
}

func legacyInt(value string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
}

func (s *Server) ImportLegacy(directory string) error {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	deviceRows, err := readCSVRows(filepath.Join(absDirectory, "trusted-devices.csv"))
	if err != nil {
		return fmt.Errorf("ler dispositivos: %w", err)
	}
	settingsRows, err := readCSVRows(filepath.Join(absDirectory, "host-settings.csv"))
	if err != nil {
		return fmt.Errorf("ler configurações: %w", err)
	}
	transferRows, err := readCSVRows(filepath.Join(absDirectory, "transfers.csv"))
	if err != nil {
		return fmt.Errorf("ler transferências: %w", err)
	}
	fileRows, err := readCSVRows(filepath.Join(absDirectory, "transfer-files.csv"))
	if err != nil {
		return fmt.Errorf("ler arquivos: %w", err)
	}

	importedDevices := make(map[string]*DeviceRecord)
	for _, row := range deviceRows {
		created, err := parseLegacyTime(row["CREATED_AT"])
		if err != nil {
			return fmt.Errorf("data de criação do dispositivo: %w", err)
		}
		seen, err := parseLegacyTime(row["LAST_SEEN_AT"])
		if err != nil {
			return fmt.Errorf("último acesso do dispositivo: %w", err)
		}
		now := time.Now().UTC()
		if created == nil {
			created = &now
		}
		if seen == nil {
			seen = created
		}
		deviceID := row["DEVICE_ID"]
		if deviceID == "" || !sha256Pattern.MatchString(row["TOKEN_HASH"]) {
			return fmt.Errorf("registro de dispositivo legado inválido")
		}
		importedDevices[deviceID] = &DeviceRecord{
			DeviceID: deviceID, DisplayName: row["DISPLAY_NAME"], DeviceType: row["DEVICE_TYPE"],
			TokenHash: strings.ToLower(row["TOKEN_HASH"]), Host: legacyBool(row["HOST"]),
			Revoked: legacyBool(row["REVOKED"]), CreatedAt: *created, LastSeenAt: *seen,
		}
	}

	importedTransfers := make(map[string]*TransferRecord)
	for _, row := range transferRows {
		created, err := parseLegacyTime(row["CREATED_AT"])
		if err != nil || created == nil {
			return fmt.Errorf("data de transferência legada inválida")
		}
		updated, err := parseLegacyTime(row["UPDATED_AT"])
		if err != nil || updated == nil {
			return fmt.Errorf("atualização de transferência legada inválida")
		}
		accepted, err := parseLegacyTime(row["ACCEPTED_AT"])
		if err != nil {
			return err
		}
		completed, err := parseLegacyTime(row["COMPLETED_AT"])
		if err != nil {
			return err
		}
		total, err := legacyInt(row["TOTAL_BYTES"])
		if err != nil {
			return err
		}
		received, err := legacyInt(row["RECEIVED_BYTES"])
		if err != nil {
			return err
		}
		transfer := &TransferRecord{
			ID: row["ID"], SenderID: row["SENDER_ID"], SenderName: row["SENDER_NAME"],
			ReceiverID: row["RECEIVER_ID"], ReceiverName: row["RECEIVER_NAME"], State: row["STATE"],
			TotalBytes: total, ReceivedBytes: received, CreatedAt: *created, UpdatedAt: *updated,
			AcceptedAt: accepted, CompletedAt: completed, Files: make([]*TransferFile, 0),
		}
		if code := strings.TrimSpace(row["FAILURE_CODE"]); code != "" {
			transfer.FailureCode = &code
		}
		if message := strings.TrimSpace(row["FAILURE_MESSAGE"]); message != "" {
			transfer.FailureMessage = &message
		}
		importedTransfers[transfer.ID] = transfer
	}
	for _, row := range fileRows {
		transfer := importedTransfers[row["TRANSFER_ID"]]
		if transfer == nil {
			return fmt.Errorf("arquivo legado referencia transferência ausente")
		}
		ordinal, err := strconv.Atoi(strings.TrimSpace(row["ORDINAL"]))
		if err != nil {
			return err
		}
		size, err := legacyInt(row["SIZE"])
		if err != nil {
			return err
		}
		totalChunks, err := legacyInt(row["TOTAL_CHUNKS"])
		if err != nil {
			return err
		}
		receivedChunks, err := legacyInt(row["RECEIVED_CHUNKS"])
		if err != nil {
			return err
		}
		receivedBytes, err := legacyInt(row["RECEIVED_BYTES"])
		if err != nil {
			return err
		}
		transfer.Files = append(transfer.Files, &TransferFile{
			ID: row["ID"], Ordinal: ordinal, Name: row["NAME"], Size: size,
			MimeType: row["MIME_TYPE"], SHA256: strings.ToLower(row["SHA256"]),
			TotalChunks: totalChunks, ReceivedChunks: receivedChunks, ReceivedBytes: receivedBytes,
			State: row["STATE"], CompletedPath: row["COMPLETED_PATH"], DeliveredName: row["DELIVERED_NAME"],
			Receipts: make(map[int64]*ChunkReceipt),
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for id, device := range importedDevices {
		s.state.Devices[id] = device
	}
	for id, transfer := range importedTransfers {
		// Chunks antigos usam outra disposição no disco. Metadados concluídos são preservados;
		// estados ativos são encerrados com uma mensagem clara para evitar retomada corrompida.
		if !isTerminalState(transfer.State) {
			code := "TRANSFER_MIGRATED_INTERRUPTED"
			message := "A transferência ativa foi encerrada durante a migração para o servidor Go."
			transfer.State = "FAILED"
			transfer.FailureCode = &code
			transfer.FailureMessage = &message
			transfer.UpdatedAt = time.Now().UTC()
			for _, file := range transfer.Files {
				file.State = "FAILED"
			}
		}
		s.state.Transfers[id] = transfer
	}
	if len(settingsRows) > 0 {
		row := settingsRows[0]
		if directory := strings.TrimSpace(row["DOWNLOAD_DIRECTORY"]); directory != "" {
			s.state.Settings.DownloadDirectory = filepath.Clean(directory)
		}
		s.state.Settings.AutoAcceptTrusted = legacyBool(row["AUTO_ACCEPT_TRUSTED"])
		if updated, err := parseLegacyTime(row["UPDATED_AT"]); err == nil && updated != nil {
			s.state.Settings.UpdatedAt = *updated
		}
	}
	return s.saveLocked()
}
