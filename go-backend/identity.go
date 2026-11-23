package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

type deviceRequest struct {
	DeviceID    string `json:"deviceId"`
	DisplayName string `json:"displayName"`
	DeviceType  string `json:"deviceType"`
}

func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}

func randomToken(size int) (string, string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(bytes)
	digest := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(digest[:]), nil
}

func hashValue(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func secureHashMatch(raw, expected string) bool {
	actual := hashValue(raw)
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func validateDeviceRequest(input deviceRequest) error {
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.DeviceType = strings.TrimSpace(input.DeviceType)
	if input.DeviceID == "" || len(input.DeviceID) > 128 {
		return problem(422, "DEVICE_ID_INVALID", "O identificador do dispositivo é inválido.")
	}
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 100 {
		return problem(422, "DEVICE_NAME_INVALID", "O nome do dispositivo é inválido.")
	}
	if input.DeviceType == "" || len(input.DeviceType) > 32 {
		return problem(422, "DEVICE_TYPE_INVALID", "O tipo do dispositivo é inválido.")
	}
	return nil
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopbackRequest(r *http.Request) bool {
	ip := net.ParseIP(remoteIP(r))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) handleHostClaim(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeProblem(w, r, problem(403, "HOST_CLAIM_REMOTE_DENIED", "O host só pode ser iniciado neste computador."))
		return
	}
	var input deviceRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.DeviceType = strings.TrimSpace(input.DeviceType)
	if err := validateDeviceRequest(input); err != nil {
		writeProblem(w, r, err)
		return
	}
	token, tokenHash, err := randomToken(32)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	now := time.Now().UTC()
	status := http.StatusCreated
	s.mu.Lock()
	var host *DeviceRecord
	for _, device := range s.state.Devices {
		if device.Host {
			host = device
			break
		}
	}
	if host != nil {
		status = http.StatusOK
		host.TokenHash = tokenHash
		host.DisplayName = input.DisplayName
		host.DeviceType = input.DeviceType
		host.Revoked = false
		host.LastSeenAt = now
	} else {
		if existing := s.state.Devices[input.DeviceID]; existing != nil && !existing.Revoked {
			s.mu.Unlock()
			writeProblem(w, r, problem(409, "DEVICE_ID_ALREADY_IN_USE", "O identificador já está em uso."))
			return
		}
		host = &DeviceRecord{
			DeviceID: input.DeviceID, DisplayName: input.DisplayName, DeviceType: input.DeviceType,
			TokenHash: tokenHash, Host: true, CreatedAt: now, LastSeenAt: now,
		}
		s.state.Devices[host.DeviceID] = host
	}
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	if status == http.StatusOK {
		s.hub.Disconnect(host.DeviceID)
	}
	writeJSONStatus(w, status, map[string]string{"deviceId": host.DeviceID, "deviceToken": token})
}

func (s *Server) enforceAttempt(key string, limit int, window time.Duration, attempts map[string][]time.Time) bool {
	s.attemptsMu.Lock()
	defer s.attemptsMu.Unlock()
	now := time.Now().UTC()
	cutoff := now.Add(-window)
	current := attempts[key]
	kept := current[:0]
	for _, at := range current {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= limit {
		attempts[key] = kept
		return false
	}
	attempts[key] = append(kept, now)
	return true
}

func (s *Server) handlePairingRequest(w http.ResponseWriter, r *http.Request) {
	if !s.enforceAttempt(remoteIP(r), s.cfg.PairingRateLimit, s.cfg.PairingRateWindow, s.pairingAttempts) {
		writeProblem(w, r, problem(429, "PAIRING_RATE_LIMITED", "Muitas tentativas de conexão. Espere um minuto e tente de novo."))
		return
	}
	var input deviceRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.DeviceType = strings.TrimSpace(input.DeviceType)
	if err := validateDeviceRequest(input); err != nil {
		writeProblem(w, r, err)
		return
	}
	id, err := randomID()
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	pollToken, pollHash, err := randomToken(24)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	codeBytes := make([]byte, 4)
	if _, err := rand.Read(codeBytes); err != nil {
		writeProblem(w, r, err)
		return
	}
	codeNumber := (uint32(codeBytes[0])<<24 | uint32(codeBytes[1])<<16 | uint32(codeBytes[2])<<8 | uint32(codeBytes[3])) % 1_000_000
	code := fmt.Sprintf("%06d", codeNumber)
	now := time.Now().UTC()
	pairing := &PairingRecord{
		ID: id, DeviceID: input.DeviceID, DisplayName: input.DisplayName, DeviceType: input.DeviceType,
		Code: code, CodeHash: hashValue(code), PollTokenHash: pollHash, Status: "PENDING",
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.PairingLifetime),
	}
	s.mu.Lock()
	if device := s.state.Devices[input.DeviceID]; device != nil && !device.Revoked {
		s.mu.Unlock()
		writeProblem(w, r, problem(409, "DEVICE_ALREADY_TRUSTED", "Este aparelho já está conectado. Recarregue a página."))
		return
	}
	for _, existing := range s.state.Pairings {
		if existing.DeviceID == input.DeviceID && (existing.Status == "PENDING" || existing.Status == "APPROVED") && now.Before(existing.ExpiresAt) {
			s.mu.Unlock()
			writeProblem(w, r, problem(409, "PAIRING_ALREADY_PENDING", "Já existe um pedido de conexão deste aparelho esperando resposta no computador."))
			return
		}
	}
	s.state.Pairings[id] = pairing
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	s.hub.SendToHost("PAIRING_REQUESTED", map[string]any{
		"requestId": id, "deviceId": input.DeviceID, "displayName": input.DisplayName,
		"deviceType": input.DeviceType, "code": code, "expiresAt": pairing.ExpiresAt,
	})
	writeJSONStatus(w, http.StatusCreated, map[string]any{
		"requestId": id, "code": code, "pollToken": pollToken,
		"status": pairing.Status, "expiresAt": pairing.ExpiresAt,
	})
}

func (s *Server) handlePairingPoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("requestId")
	pollToken := r.Header.Get("X-Pairing-Poll-Token")
	now := time.Now().UTC()
	// A consulta à rede só é necessária quando o pedido acabou de ser aprovado.
	s.mu.RLock()
	approved := s.state.Pairings[id] != nil && s.state.Pairings[id].Status == "APPROVED"
	s.mu.RUnlock()
	hardwareKey := ""
	if approved {
		hardwareKey = resolveHardwareKey(r)
	}
	var removed []string
	s.mu.Lock()
	pairing := s.state.Pairings[id]
	if pairing == nil {
		s.mu.Unlock()
		writeProblem(w, r, problem(404, "PAIRING_NOT_FOUND", "O pedido de conexão não existe mais. Toque em Conectar de novo."))
		return
	}
	if !secureHashMatch(pollToken, pairing.PollTokenHash) {
		s.mu.Unlock()
		writeProblem(w, r, problem(403, "PAIRING_POLL_TOKEN_INVALID", "Este pedido de conexão não pertence a este aparelho. Toque em Conectar de novo."))
		return
	}
	if (pairing.Status == "PENDING" || pairing.Status == "APPROVED") && !now.Before(pairing.ExpiresAt) {
		pairing.Status = "EXPIRED"
		pairing.IssuedToken = ""
		_ = s.saveLocked()
	}
	var token *string
	if pairing.Status == "APPROVED" {
		if pairing.IssuedToken == "" {
			pairing.Status = "EXPIRED"
		} else {
			raw := pairing.IssuedToken
			token = &raw
			device := s.state.Devices[pairing.DeviceID]
			if device == nil {
				device = &DeviceRecord{DeviceID: pairing.DeviceID, CreatedAt: now}
				s.state.Devices[pairing.DeviceID] = device
			}
			device.Host = false
			device.Revoked = false
			var inherited string
			removed, inherited = s.adoptHardwareLocked(device, hardwareKey)
			device.DisplayName = s.pairedNameLocked(pairing.DisplayName, inherited, pairing.DeviceID)
			device.DeviceType = pairing.DeviceType
			device.TokenHash = pairing.TokenHash
			device.LastSeenAt = now
			pairing.IssuedToken = ""
			pairing.Code = ""
			pairing.Status = "COMPLETED"
		}
		_ = s.saveLocked()
	}
	status := pairing.Status
	s.mu.Unlock()
	s.announceAbsorbed(removed)
	writeJSON(w, map[string]any{"requestId": id, "status": status, "deviceToken": token})
}

type pairingView struct {
	RequestID   string    `json:"requestId"`
	DeviceID    string    `json:"deviceId"`
	DisplayName string    `json:"displayName"`
	DeviceType  string    `json:"deviceType"`
	Code        string    `json:"code"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// pendingPairingsLocked lista os pedidos aguardando o host e expira os
// vencidos. O retorno indica se o estado mudou e precisa ser gravado.
func (s *Server) pendingPairingsLocked(now time.Time) ([]pairingView, bool) {
	result := make([]pairingView, 0)
	changed := false
	for _, pairing := range s.state.Pairings {
		if pairing.Status != "PENDING" {
			continue
		}
		if !now.Before(pairing.ExpiresAt) {
			pairing.Status = "EXPIRED"
			pairing.Code = ""
			changed = true
			continue
		}
		result = append(result, pairingView{
			RequestID: pairing.ID, DeviceID: pairing.DeviceID, DisplayName: pairing.DisplayName,
			DeviceType: pairing.DeviceType, Code: pairing.Code, Status: pairing.Status,
			CreatedAt: pairing.CreatedAt, ExpiresAt: pairing.ExpiresAt,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, changed
}

func (s *Server) handlePendingPairings(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	result, changed := s.pendingPairingsLocked(time.Now().UTC())
	if changed {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	writeJSON(w, result)
}

func (s *Server) handlePairingApprove(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.approvePairing(r.PathValue("requestId"), input.Code); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// approvePairing autoriza um pedido feito com o código de 6 dígitos.
func (s *Server) approvePairing(id, code string) error {
	now := time.Now().UTC()
	s.mu.Lock()
	pairing := s.state.Pairings[id]
	if pairing == nil {
		s.mu.Unlock()
		return problem(404, "PAIRING_NOT_FOUND", "O pedido de conexão não existe mais. No celular, toque em Conectar de novo.")
	}
	if !now.Before(pairing.ExpiresAt) {
		pairing.Status = "EXPIRED"
		_ = s.saveLocked()
		s.mu.Unlock()
		return problem(409, "PAIRING_EXPIRED", "O pedido de conexão venceu. No celular, toque em Conectar de novo.")
	}
	if pairing.Status != "PENDING" {
		s.mu.Unlock()
		return problem(409, "PAIRING_NOT_PENDING", "Esse pedido já foi respondido.")
	}
	if !secureHashMatch(code, pairing.CodeHash) {
		s.mu.Unlock()
		return problem(422, "PAIRING_CODE_INVALID", "O código não confere. Peça para o celular tentar de novo.")
	}
	token, tokenHash, err := randomToken(32)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	pairing.Status = "APPROVED"
	pairing.TokenHash = tokenHash
	pairing.IssuedToken = token
	pairing.Code = ""
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	s.hub.SendToHost("PAIRING_ACCEPTED", map[string]any{"requestId": id})
	return nil
}

func (s *Server) handlePairingReject(w http.ResponseWriter, r *http.Request) {
	if err := s.rejectPairing(r.PathValue("requestId")); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rejectPairing(id string) error {
	s.mu.Lock()
	pairing := s.state.Pairings[id]
	if pairing == nil {
		s.mu.Unlock()
		return problem(404, "PAIRING_NOT_FOUND", "O pedido de conexão não existe mais.")
	}
	if pairing.Status != "PENDING" {
		s.mu.Unlock()
		return problem(409, "PAIRING_NOT_PENDING", "Esse pedido já foi respondido.")
	}
	pairing.Status = "REJECTED"
	pairing.Code = ""
	pairing.IssuedToken = ""
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	s.hub.SendToHost("PAIRING_REJECTED", map[string]any{"requestId": id})
	return nil
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	principal := principalOf(r)
	// Chamado a cada abertura da página: aproveita para registrar (ou atualizar)
	// a chave de hardware de aparelhos conectados antes dela existir.
	hardwareKey := ""
	if !principal.Host {
		hardwareKey = resolveHardwareKey(r)
	}
	s.mu.Lock()
	device := s.state.Devices[principal.DeviceID]
	if device == nil || device.Revoked {
		s.mu.Unlock()
		writeProblem(w, r, problem(404, "DEVICE_NOT_FOUND", "Este aparelho não está mais conectado ao computador."))
		return
	}
	var removed []string
	if hardwareKey != "" && device.HardwareKey != hardwareKey {
		removed, _ = s.adoptHardwareLocked(device, hardwareKey)
		_ = s.saveLocked()
	}
	view := s.deviceViewLocked(device)
	s.mu.Unlock()
	s.announceAbsorbed(removed)
	writeJSON(w, view)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"displayName"`
		DeviceType  string `json:"deviceType"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.DeviceType = strings.TrimSpace(input.DeviceType)
	if input.DisplayName == "" || len([]rune(input.DisplayName)) > 100 || input.DeviceType == "" || len(input.DeviceType) > 32 {
		writeProblem(w, r, problem(422, "DEVICE_PROFILE_INVALID", "Esse nome não pode ser usado. Use de 1 a 60 letras ou números."))
		return
	}
	principal := principalOf(r)
	s.mu.Lock()
	device := s.state.Devices[principal.DeviceID]
	device.DisplayName = s.uniqueDeviceNameLocked(input.DisplayName, device.DeviceID)
	device.DeviceType = input.DeviceType
	device.LastSeenAt = time.Now().UTC()
	view := s.deviceViewLocked(device)
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	s.hub.Broadcast("DEVICE_UPDATED", map[string]any{"device": view})
	writeJSON(w, view)
}

func (s *Server) handleDevices(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	views := s.trustedDeviceViewsLocked()
	s.mu.RUnlock()
	writeJSON(w, views)
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.revokeDevice(r.PathValue("deviceId")); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeDevice(id string) error {
	s.mu.Lock()
	device := s.state.Devices[id]
	if device == nil {
		s.mu.Unlock()
		return problem(404, "DEVICE_NOT_FOUND", "Esse aparelho não está mais na lista.")
	}
	if device.Host {
		s.mu.Unlock()
		return problem(409, "HOST_CANNOT_BE_REVOKED", "Este computador não pode ser removido.")
	}
	device.Revoked = true
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		return saveErr
	}
	s.hub.Disconnect(id)
	s.hub.Broadcast("DEVICE_REVOKED", map[string]string{"deviceId": id})
	return nil
}

// uniqueDeviceNameLocked evita dois aparelhos com o mesmo nome na lista,
// acrescentando " 2", " 3"... ao nome automático.
func (s *Server) uniqueDeviceNameLocked(name, deviceID string) string {
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > 60 {
		name = strings.TrimSpace(string(runes[:60]))
	}
	taken := func(candidate string) bool {
		for _, device := range s.state.Devices {
			if device.DeviceID != deviceID && !device.Revoked && strings.EqualFold(device.DisplayName, candidate) {
				return true
			}
		}
		return false
	}
	if !taken(name) {
		return name
	}
	for index := 2; index < 100; index++ {
		candidate := fmt.Sprintf("%s %d", name, index)
		if !taken(candidate) {
			return candidate
		}
	}
	return name
}

// ---------------------------------------------------------------- convite

// pairingInvite é o segredo levado pelo QR Code. Vale uma única vez e por
// tempo curto; só existe um convite ativo por vez e ele fica apenas na memória.
type pairingInvite struct {
	token     string
	hash      string
	expiresAt time.Time
}

// currentInvite devolve o convite ativo e cria outro quando falta menos de
// 30 segundos para vencer.
func (s *Server) currentInvite() (string, time.Time, error) {
	s.inviteMu.Lock()
	defer s.inviteMu.Unlock()
	now := time.Now()
	if s.invite != nil && now.Add(30*time.Second).Before(s.invite.expiresAt) {
		return s.invite.token, s.invite.expiresAt, nil
	}
	token, hash, err := randomToken(24)
	if err != nil {
		return "", time.Time{}, err
	}
	s.invite = &pairingInvite{token: token, hash: hash, expiresAt: now.Add(s.cfg.PairingLifetime)}
	return token, s.invite.expiresAt, nil
}

// dropInvite invalida o convite, por exemplo quando o QR deixa de aparecer.
func (s *Server) dropInvite() {
	s.inviteMu.Lock()
	s.invite = nil
	s.inviteMu.Unlock()
}

// consumeInvite confere e invalida o convite em uma única operação.
func (s *Server) consumeInvite(raw string) bool {
	s.inviteMu.Lock()
	defer s.inviteMu.Unlock()
	if s.invite == nil || raw == "" || !time.Now().Before(s.invite.expiresAt) || !secureHashMatch(raw, s.invite.hash) {
		return false
	}
	s.invite = nil
	return true
}

func (s *Server) handleInviteRedeem(w http.ResponseWriter, r *http.Request) {
	if !s.enforceAttempt(remoteIP(r), s.cfg.PairingRateLimit, s.cfg.PairingRateWindow, s.pairingAttempts) {
		writeProblem(w, r, problem(429, "PAIRING_RATE_LIMITED", "Muitas tentativas de conexão. Espere um minuto e tente de novo."))
		return
	}
	var input struct {
		DeviceID    string `json:"deviceId"`
		DisplayName string `json:"displayName"`
		DeviceType  string `json:"deviceType"`
		Invite      string `json:"invite"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	request := deviceRequest{
		DeviceID: strings.TrimSpace(input.DeviceID), DisplayName: strings.TrimSpace(input.DisplayName),
		DeviceType: strings.TrimSpace(input.DeviceType),
	}
	if err := validateDeviceRequest(request); err != nil {
		writeProblem(w, r, err)
		return
	}
	s.mu.RLock()
	existing := s.state.Devices[request.DeviceID]
	s.mu.RUnlock()
	if existing != nil && existing.Host {
		writeProblem(w, r, problem(409, "DEVICE_ID_ALREADY_IN_USE", "Este aparelho não pode usar esse convite."))
		return
	}
	if !s.consumeInvite(strings.TrimSpace(input.Invite)) {
		writeProblem(w, r, problem(410, "PAIRING_INVITE_INVALID", "Este QR Code já foi usado ou venceu. No computador, clique em Conectar celular para mostrar outro."))
		return
	}
	token, tokenHash, err := randomToken(32)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	hardwareKey := resolveHardwareKey(r)
	now := time.Now().UTC()
	s.mu.Lock()
	device := s.state.Devices[request.DeviceID]
	if device == nil {
		device = &DeviceRecord{DeviceID: request.DeviceID, CreatedAt: now}
		s.state.Devices[request.DeviceID] = device
	}
	device.Host = false
	device.Revoked = false
	removed, inherited := s.adoptHardwareLocked(device, hardwareKey)
	device.DisplayName = s.pairedNameLocked(request.DisplayName, inherited, request.DeviceID)
	device.DeviceType = request.DeviceType
	device.TokenHash = tokenHash
	device.LastSeenAt = now
	name := device.DisplayName
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	s.announceAbsorbed(removed)
	s.hub.SendToHost("DEVICE_PAIRED", map[string]any{"deviceId": request.DeviceID, "displayName": name})
	writeJSONStatus(w, http.StatusCreated, map[string]string{"deviceId": request.DeviceID, "deviceToken": token, "displayName": name})
}
