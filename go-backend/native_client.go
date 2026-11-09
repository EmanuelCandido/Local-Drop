package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type nativeSession struct {
	DeviceID string
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

// nativeClient é a ponte entre a janela e o servidor. Tudo roda no mesmo
// processo, então as ações chamam o servidor direto, sem passar pela rede.
type nativeClient struct {
	server  *Server
	session nativeSession
}

func newNativeHostClient(server *Server) (*nativeClient, error) {
	_, tokenHash, err := randomToken(32)
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
	// O token do host é renovado a cada abertura: a janela não usa HTTP, então
	// nenhum token válido do computador fica exposto.
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
	session := nativeSession{DeviceID: host.DeviceID, Name: host.DisplayName}
	server.mu.Unlock()
	return &nativeClient{server: server, session: session}, nil
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

func (c *nativeClient) principal() Principal {
	return Principal{DeviceID: c.session.DeviceID, Host: true}
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
	return c.server.approvePairing(pairing.RequestID, pairing.Code)
}

func (c *nativeClient) rejectPairing(requestID string) error {
	return c.server.rejectPairing(requestID)
}

func (c *nativeClient) updateSettings(input settingsUpdate) (settingsResponse, error) {
	settings, err := c.server.updateSettings(c.principal(), input)
	if err == nil {
		c.session.Name = settings.Device.DisplayName
	}
	return settings, err
}

func (c *nativeClient) revoke(deviceID string) error {
	return c.server.revokeDevice(deviceID)
}

func (c *nativeClient) clearFinished() int {
	return c.server.clearFinished(c.session.DeviceID)
}

// offer oferece arquivos locais sem copiá-los: o destinatário baixa do original.
func (c *nativeClient) offer(receiverID string, paths []string) (TransferView, error) {
	return c.server.offerLocalFiles(c.session.DeviceID, receiverID, paths)
}

// sendText transforma um texto em um arquivo pequeno e o oferece.
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
