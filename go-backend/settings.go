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

type settingsUpdate struct {
	DisplayName       *string `json:"displayName"`
	DeviceType        *string `json:"deviceType"`
	DownloadDirectory *string `json:"downloadDirectory"`
	AutoAcceptTrusted *bool   `json:"autoAcceptTrusted"`
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
	var input settingsUpdate
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	response, err := s.updateSettings(principalOf(r), input)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, response)
}

// updateSettings é usado pela API do celular e, direto na memória, pela janela.
func (s *Server) updateSettings(principal Principal, input settingsUpdate) (settingsResponse, error) {
	if (input.DownloadDirectory != nil || input.AutoAcceptTrusted != nil) && !principal.Host {
		return settingsResponse{}, problem(403, "HOST_SETTINGS_REQUIRED", "Só o computador pode mudar esse ajuste.")
	}
	var normalizedDirectory *string
	if input.DownloadDirectory != nil {
		trimmed := strings.TrimSpace(*input.DownloadDirectory)
		if trimmed == "" || len(trimmed) > 2048 || !filepath.IsAbs(trimmed) {
			return settingsResponse{}, problem(422, "DOWNLOAD_DIRECTORY_INVALID", "Escolha uma pasta do computador.")
		}
		clean := filepath.Clean(trimmed)
		if err := os.MkdirAll(clean, 0o755); err != nil || !directoryWritable(clean) {
			return settingsResponse{}, problem(422, "DOWNLOAD_DIRECTORY_NOT_WRITABLE", "O Windows não deixa salvar nessa pasta. Escolha outra.")
		}
		normalizedDirectory = &clean
	}

	s.mu.Lock()
	device := s.state.Devices[principal.DeviceID]
	if device == nil {
		s.mu.Unlock()
		return settingsResponse{}, problem(404, "DEVICE_NOT_FOUND", "Este aparelho não está mais conectado ao computador.")
	}
	if input.DisplayName != nil {
		name := strings.TrimSpace(*input.DisplayName)
		if name == "" || len([]rune(name)) > 100 {
			s.mu.Unlock()
			return settingsResponse{}, problem(422, "DEVICE_NAME_INVALID", "Esse nome não pode ser usado. Use de 1 a 60 letras ou números.")
		}
		device.DisplayName = s.uniqueDeviceNameLocked(name, device.DeviceID)
	}
	if input.DeviceType != nil {
		kind := strings.TrimSpace(*input.DeviceType)
		if kind == "" || len(kind) > 32 {
			s.mu.Unlock()
			return settingsResponse{}, problem(422, "DEVICE_TYPE_INVALID", "O tipo do aparelho é inválido.")
		}
		device.DeviceType = kind
	}
	if normalizedDirectory != nil && *normalizedDirectory != s.state.Settings.DownloadDirectory {
		if s.hasActiveReceiverLocked(principal.DeviceID) {
			s.mu.Unlock()
			return settingsResponse{}, problem(409, "DOWNLOAD_DIRECTORY_IN_USE", "A pasta não pode mudar durante um recebimento. Espere terminar.")
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
		return settingsResponse{}, saveErr
	}
	if input.DisplayName != nil || input.DeviceType != nil {
		s.hub.Broadcast("DEVICE_UPDATED", map[string]any{"device": deviceView})
	}
	return response, nil
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

// savedPort devolve a porta usada da última vez, para o endereço não mudar.
func (s *Server) savedPort() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Settings.Port
}

// usePort registra a porta em que o servidor de fato abriu.
func (s *Server) usePort(port int) {
	s.cfg.Port = port
	s.mu.Lock()
	if s.state.Settings.Port != port {
		s.state.Settings.Port = port
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	s.urlMu.Lock()
	s.urlCache = ""
	s.urlMu.Unlock()
}

// autostartAsked e markAutostartAsked controlam a pergunta "abrir com o
// Windows", feita uma única vez.
func (s *Server) autostartAsked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Settings.AutostartAsked
}

func (s *Server) markAutostartAsked() {
	s.mu.Lock()
	if !s.state.Settings.AutostartAsked {
		s.state.Settings.AutostartAsked = true
		_ = s.saveLocked()
	}
	s.mu.Unlock()
}
