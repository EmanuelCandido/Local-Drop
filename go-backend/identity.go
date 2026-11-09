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
		writeProblem(w, r, problem(429, "PAIRING_RATE_LIMITED", "Muitas solicitações de pareamento. Tente novamente mais tarde."))
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
		writeProblem(w, r, problem(409, "DEVICE_ALREADY_TRUSTED", "O dispositivo já é confiável."))
		return
	}
	for _, existing := range s.state.Pairings {
		if existing.DeviceID == input.DeviceID && (existing.Status == "PENDING" || existing.Status == "APPROVED") && now.Before(existing.ExpiresAt) {
			s.mu.Unlock()
			writeProblem(w, r, problem(409, "PAIRING_ALREADY_PENDING", "O dispositivo já possui um pareamento pendente."))
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
	s.mu.Lock()
	pairing := s.state.Pairings[id]
	if pairing == nil {
		s.mu.Unlock()
		writeProblem(w, r, problem(404, "PAIRING_NOT_FOUND", "A solicitação de pareamento não foi encontrada."))
		return
	}
	if !secureHashMatch(pollToken, pairing.PollTokenHash) {
		s.mu.Unlock()
		writeProblem(w, r, problem(403, "PAIRING_POLL_TOKEN_INVALID", "O segredo de consulta do pareamento é inválido."))
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
			device.DisplayName = pairing.DisplayName
			device.DeviceType = pairing.DeviceType
			device.TokenHash = pairing.TokenHash
			device.Host = false
			device.Revoked = false
			device.LastSeenAt = now
			pairing.IssuedToken = ""
			pairing.Code = ""
			pairing.Status = "COMPLETED"
		}
		_ = s.saveLocked()
	}
	status := pairing.Status
	s.mu.Unlock()
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
	id := r.PathValue("requestId")
	now := time.Now().UTC()
	s.mu.Lock()
	pairing := s.state.Pairings[id]
	if pairing == nil {
		s.mu.Unlock()
		writeProblem(w, r, problem(404, "PAIRING_NOT_FOUND", "A solicitação de pareamento não foi encontrada."))
		return
	}
	if !now.Before(pairing.ExpiresAt) {
		pairing.Status = "EXPIRED"
		_ = s.saveLocked()
		s.mu.Unlock()
		writeProblem(w, r, problem(409, "PAIRING_EXPIRED", "A solicitação de pareamento expirou."))
		return
	}
	if pairing.Status != "PENDING" {
		s.mu.Unlock()
		writeProblem(w, r, problem(409, "PAIRING_NOT_PENDING", "A solicitação não está mais pendente."))
		return
	}
	if !secureHashMatch(input.Code, pairing.CodeHash) {
		s.mu.Unlock()
		writeProblem(w, r, problem(422, "PAIRING_CODE_INVALID", "O código de pareamento é inválido."))
		return
	}
	token, tokenHash, err := randomToken(32)
	if err != nil {
		s.mu.Unlock()
		writeProblem(w, r, err)
		return
	}
	pairing.Status = "APPROVED"
	pairing.TokenHash = tokenHash
	pairing.IssuedToken = token
	pairing.Code = ""
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	s.hub.SendToHost("PAIRING_ACCEPTED", map[string]any{"requestId": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePairingReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("requestId")
	s.mu.Lock()
	pairing := s.state.Pairings[id]
	if pairing == nil {
		s.mu.Unlock()
		writeProblem(w, r, problem(404, "PAIRING_NOT_FOUND", "A solicitação de pareamento não foi encontrada."))
		return
	}
	if pairing.Status != "PENDING" {
		s.mu.Unlock()
		writeProblem(w, r, problem(409, "PAIRING_NOT_PENDING", "A solicitação não está mais pendente."))
		return
	}
	pairing.Status = "REJECTED"
	pairing.Code = ""
	pairing.IssuedToken = ""
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	s.hub.SendToHost("PAIRING_REJECTED", map[string]any{"requestId": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	principal := principalOf(r)
	s.mu.RLock()
	device := s.state.Devices[principal.DeviceID]
	if device == nil || device.Revoked {
		s.mu.RUnlock()
		writeProblem(w, r, problem(404, "DEVICE_NOT_FOUND", "O dispositivo não foi encontrado."))
		return
	}
	view := s.deviceViewLocked(device)
	s.mu.RUnlock()
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
		writeProblem(w, r, problem(422, "DEVICE_PROFILE_INVALID", "Nome ou tipo do dispositivo é inválido."))
		return
	}
	principal := principalOf(r)
	s.mu.Lock()
	device := s.state.Devices[principal.DeviceID]
	device.DisplayName = input.DisplayName
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
	id := r.PathValue("deviceId")
	s.mu.Lock()
	device := s.state.Devices[id]
	if device == nil {
		s.mu.Unlock()
		writeProblem(w, r, problem(404, "DEVICE_NOT_FOUND", "O dispositivo não foi encontrado."))
		return
	}
	if device.Host {
		s.mu.Unlock()
		writeProblem(w, r, problem(409, "HOST_CANNOT_BE_REVOKED", "O computador host não pode ser revogado."))
		return
	}
	device.Revoked = true
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	s.hub.Disconnect(id)
	w.WriteHeader(http.StatusNoContent)
}
