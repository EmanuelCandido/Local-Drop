package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type settingsResponse struct {
	Device            DeviceView `json:"device"`
	DownloadDirectory *string    `json:"downloadDirectory"`
	AutoAcceptTrusted bool       `json:"autoAcceptTrusted"`
	Port              int        `json:"port"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

func (s *Server) settingsResponseLocked(device *DeviceRecord) settingsResponse {
	response := settingsResponse{
		Device:    s.deviceViewLocked(device),
		Port:      s.cfg.Port,
		UpdatedAt: s.state.Settings.UpdatedAt,
	}
	if device.Host {
		directory := s.state.Settings.DownloadDirectory
		response.DownloadDirectory = &directory
		response.AutoAcceptTrusted = s.state.Settings.AutoAcceptTrusted
	}
	return response
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	principal := principalOf(r)
	s.mu.RLock()
	device := s.state.Devices[principal.DeviceID]
	response := s.settingsResponseLocked(device)
	s.mu.RUnlock()
	writeJSON(w, response)
}

func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName       *string `json:"displayName"`
		DeviceType        *string `json:"deviceType"`
		DownloadDirectory *string `json:"downloadDirectory"`
		AutoAcceptTrusted *bool   `json:"autoAcceptTrusted"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	principal := principalOf(r)
	if (input.DownloadDirectory != nil || input.AutoAcceptTrusted != nil) && !principal.Host {
		writeProblem(w, r, problem(403, "HOST_SETTINGS_REQUIRED", "Somente o computador host pode alterar essas configurações."))
		return
	}
	var normalizedDirectory *string
	if input.DownloadDirectory != nil {
		trimmed := strings.TrimSpace(*input.DownloadDirectory)
		if trimmed == "" || len(trimmed) > 2048 || !filepath.IsAbs(trimmed) {
			writeProblem(w, r, problem(422, "DOWNLOAD_DIRECTORY_INVALID", "Escolha uma pasta absoluta válida."))
			return
		}
		clean := filepath.Clean(trimmed)
		if err := os.MkdirAll(clean, 0o755); err != nil || !directoryWritable(clean) {
			writeProblem(w, r, problem(422, "DOWNLOAD_DIRECTORY_NOT_WRITABLE", "A pasta escolhida não permite gravação."))
			return
		}
		normalizedDirectory = &clean
	}

	s.mu.Lock()
	device := s.state.Devices[principal.DeviceID]
	if input.DisplayName != nil {
		name := strings.TrimSpace(*input.DisplayName)
		if name == "" || len([]rune(name)) > 100 {
			s.mu.Unlock()
			writeProblem(w, r, problem(422, "DEVICE_NAME_INVALID", "O nome do dispositivo é inválido."))
			return
		}
		device.DisplayName = name
	}
	if input.DeviceType != nil {
		kind := strings.TrimSpace(*input.DeviceType)
		if kind == "" || len(kind) > 32 {
			s.mu.Unlock()
			writeProblem(w, r, problem(422, "DEVICE_TYPE_INVALID", "O tipo do dispositivo é inválido."))
			return
		}
		device.DeviceType = kind
	}
	if normalizedDirectory != nil && *normalizedDirectory != s.state.Settings.DownloadDirectory {
		if s.hasActiveReceiverLocked(principal.DeviceID) {
			s.mu.Unlock()
			writeProblem(w, r, problem(409, "DOWNLOAD_DIRECTORY_IN_USE", "A pasta não pode mudar durante um recebimento ativo."))
			return
		}
		s.state.Settings.DownloadDirectory = *normalizedDirectory
		s.state.Settings.UpdatedAt = time.Now().UTC()
	}
	if input.AutoAcceptTrusted != nil {
		s.state.Settings.AutoAcceptTrusted = *input.AutoAcceptTrusted
		s.state.Settings.UpdatedAt = time.Now().UTC()
	}
	device.LastSeenAt = time.Now().UTC()
	response := s.settingsResponseLocked(device)
	deviceView := response.Device
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	if input.DisplayName != nil || input.DeviceType != nil {
		s.hub.Broadcast("DEVICE_UPDATED", map[string]any{"device": deviceView})
	}
	writeJSON(w, response)
}

func (s *Server) hasActiveReceiverLocked(deviceID string) bool {
	for _, transfer := range s.state.Transfers {
		if transfer.ReceiverID != deviceID {
			continue
		}
		switch transfer.State {
		case "ACCEPTED", "TRANSFERRING", "PAUSED", "VERIFYING":
			return true
		}
	}
	return false
}

func (s *Server) handleDirectoryPick(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	current := s.state.Settings.DownloadDirectory
	s.mu.RUnlock()
	selected, ok, err := pickDirectory(current)
	if err != nil {
		writeProblem(w, r, problem(422, "DOWNLOAD_DIRECTORY_PICKER_UNAVAILABLE", "Não foi possível abrir o seletor de pasta."))
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"downloadDirectory": nil})
		return
	}
	writeJSON(w, map[string]any{"downloadDirectory": selected})
}
