package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type nativeSession struct {
	DeviceID string
	Token    string
	Name     string
}

// nativeSnapshot é o estado que a janela do computador desenha. Ele é lido
// diretamente da memória do servidor, no mesmo processo, sem HTTP.
type nativeSnapshot struct {
	Devices   []DeviceView
	Transfers []TransferView
	Pairings  []pairingView
	Settings  settingsResponse
}

type nativeAPIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *nativeAPIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("o servidor respondeu %d", e.Status)
}

type nativeClient struct {
	server  *Server
	baseURL string
	session nativeSession
	http    *http.Client
}

func newNativeHostClient(server *Server) (*nativeClient, error) {
	token, tokenHash, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	server.mu.Lock()
	var host *DeviceRecord
	for _, device := range server.state.Devices {
		if device.Host && !device.Revoked {
			host = device
			break
		}
	}
	if host == nil {
		id, idErr := randomID()
		if idErr != nil {
			server.mu.Unlock()
			return nil, idErr
		}
		host = &DeviceRecord{
			DeviceID: id, DisplayName: defaultHostName(), DeviceType: "DESKTOP",
			Host: true, CreatedAt: now,
		}
		server.state.Devices[id] = host
	}
	host.TokenHash = tokenHash
	host.Revoked = false
	host.LastSeenAt = now
	if strings.TrimSpace(host.DisplayName) == "" {
		host.DisplayName = defaultHostName()
	}
	if strings.TrimSpace(host.DeviceType) == "" {
		host.DeviceType = "DESKTOP"
	}
	if err := server.saveLocked(); err != nil {
		server.mu.Unlock()
		return nil, err
	}
	session := nativeSession{DeviceID: host.DeviceID, Token: token, Name: host.DisplayName}
	server.mu.Unlock()
	return &nativeClient{
		server:  server,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", server.cfg.Port),
		session: session,
		http:    &http.Client{Timeout: 45 * time.Second},
	}, nil
}

// defaultHostName usa o nome do computador, mais fácil de reconhecer no celular.
func defaultHostName() string {
	if name, err := os.Hostname(); err == nil {
		name = strings.TrimSpace(name)
		if name != "" && len([]rune(name)) <= 60 {
			return name
		}
	}
	return "Meu PC"
}

func (c *nativeClient) request(method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Device "+c.session.Token)
	request.Header.Set("X-Device-Id", c.session.DeviceID)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		problem := &nativeAPIError{Status: response.StatusCode}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(problem)
		return problem
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func (c *nativeClient) snapshot() nativeSnapshot {
	s := c.server
	s.mu.Lock()
	defer s.mu.Unlock()
	pairings, changed := s.pendingPairingsLocked(time.Now().UTC())
	if changed {
		s.saveSoonLocked()
	}
	snapshot := nativeSnapshot{
		Devices:   s.trustedDeviceViewsLocked(),
		Transfers: s.transferListLocked(c.session.DeviceID, 60),
		Pairings:  pairings,
	}
	if device := s.state.Devices[c.session.DeviceID]; device != nil {
		device.LastSeenAt = time.Now().UTC()
		snapshot.Settings = s.settingsResponseLocked(device)
	}
	return snapshot
}

func (c *nativeClient) action(transferID, action string) error {
	_, err := c.server.transferAction(c.session.DeviceID, transferID, action)
	return err
}

func (c *nativeClient) approvePairing(pairing pairingView) error {
	return c.request(http.MethodPost, "/api/v1/pairing/requests/"+url.PathEscape(pairing.RequestID)+"/approve", map[string]string{"code": pairing.Code}, nil)
}

func (c *nativeClient) rejectPairing(requestID string) error {
	return c.request(http.MethodPost, "/api/v1/pairing/requests/"+url.PathEscape(requestID)+"/reject", nil, nil)
}

func (c *nativeClient) updateSettings(input map[string]any) (settingsResponse, error) {
	var settings settingsResponse
	err := c.request(http.MethodPatch, "/api/v1/settings", input, &settings)
	if err == nil {
		c.session.Name = settings.Device.DisplayName
	}
	return settings, err
}

func (c *nativeClient) revoke(deviceID string) error {
	return c.request(http.MethodDelete, "/api/v1/trusted-devices/"+url.PathEscape(deviceID), nil, nil)
}

// offer oferece arquivos locais sem copiá-los: o destinatário baixa do original.
func (c *nativeClient) offer(receiverID string, paths []string) (TransferView, error) {
	return c.server.offerLocalFiles(c.session.DeviceID, receiverID, paths)
}

// sendText transforma um texto copiado em um arquivo pequeno e o oferece.
func (c *nativeClient) sendText(receiverID, text string) (TransferView, error) {
	id, err := randomID()
	if err != nil {
		return TransferView{}, err
	}
	directory := filepath.Join(c.server.cfg.StorageRoot, "outbox", id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return TransferView{}, err
	}
	path := filepath.Join(directory, "Mensagem LocalDrop.txt")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return TransferView{}, err
	}
	return c.offer(receiverID, []string{path})
}
