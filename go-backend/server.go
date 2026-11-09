package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type contextKey string

const principalContextKey contextKey = "localdrop-principal"

type Principal struct {
	DeviceID string
	Host     bool
}

type Server struct {
	cfg       Config
	statePath string
	state     *State
	mu        sync.RWMutex
	mux       *http.ServeMux
	hub       *Hub

	pairingAttempts map[string][]time.Time
	createAttempts  map[string][]time.Time
	attemptsMu      sync.Mutex
	assemblyMu      sync.Mutex
	assembling      map[string]bool
	chunkLocks      keyedLocks

	saveTimer *time.Timer
	saveDirty bool

	progressMu   sync.Mutex
	lastProgress map[string]time.Time

	ticketsMu sync.Mutex
	tickets   map[string]downloadTicket

	urlMu     sync.Mutex
	urlCache  string
	urlExpiry time.Time

	inviteMu sync.Mutex
	invite   *pairingInvite

	// lastRemote guarda (em nanossegundos Unix) o último pedido vindo da rede,
	// usado para perceber quando o celular não consegue chegar ao computador.
	lastRemote atomic.Int64
}

func NewServer(cfg Config) (*Server, error) {
	if err := os.MkdirAll(cfg.StorageRoot, 0o700); err != nil {
		return nil, fmt.Errorf("criar armazenamento: %w", err)
	}
	if err := os.MkdirAll(cfg.Downloads, 0o755); err != nil {
		return nil, fmt.Errorf("criar pasta de recebimento: %w", err)
	}
	statePath := filepath.Join(cfg.StorageRoot, "localdrop-go.json")
	state, err := loadState(statePath, cfg.Downloads)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:             cfg,
		statePath:       statePath,
		state:           state,
		mux:             http.NewServeMux(),
		pairingAttempts: make(map[string][]time.Time),
		createAttempts:  make(map[string][]time.Time),
		assembling:      make(map[string]bool),
		lastProgress:    make(map[string]time.Time),
		tickets:         make(map[string]downloadTicket),
	}
	s.hub = NewHub(s)
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	// Rotas públicas.
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/host/claim", s.handleHostClaim)
	s.mux.HandleFunc("POST /api/v1/pairing/invite", s.handleInviteRedeem)
	s.mux.HandleFunc("POST /api/v1/pairing/requests", s.handlePairingRequest)
	s.mux.HandleFunc("GET /api/v1/pairing/requests/{requestId}/status", s.handlePairingPoll)
	s.mux.HandleFunc("GET /ws/events", s.handleWebSocket)

	// Identidade autenticada.
	s.mux.Handle("GET /api/v1/device/me", s.auth(http.HandlerFunc(s.handleMe)))
	s.mux.Handle("PATCH /api/v1/device/me", s.auth(http.HandlerFunc(s.handleUpdateMe)))
	s.mux.Handle("GET /api/v1/devices", s.auth(http.HandlerFunc(s.handleDevices)))
	s.mux.Handle("GET /api/v1/trusted-devices", s.host(http.HandlerFunc(s.handleDevices)))
	s.mux.Handle("DELETE /api/v1/trusted-devices/{deviceId}", s.host(http.HandlerFunc(s.handleRevoke)))
	s.mux.Handle("GET /api/v1/pairing/requests", s.host(http.HandlerFunc(s.handlePendingPairings)))
	s.mux.Handle("POST /api/v1/pairing/requests/{requestId}/approve", s.host(http.HandlerFunc(s.handlePairingApprove)))
	s.mux.Handle("POST /api/v1/pairing/requests/{requestId}/reject", s.host(http.HandlerFunc(s.handlePairingReject)))

	// Configurações.
	s.mux.Handle("GET /api/v1/settings", s.auth(http.HandlerFunc(s.handleSettingsGet)))
	s.mux.Handle("PATCH /api/v1/settings", s.auth(http.HandlerFunc(s.handleSettingsPatch)))

	// Transferências.
	s.mux.Handle("POST /api/v1/transfers", s.auth(http.HandlerFunc(s.handleTransferCreate)))
	s.mux.Handle("GET /api/v1/transfers", s.auth(http.HandlerFunc(s.handleTransferList)))
	s.mux.Handle("POST /api/v1/transfers/clear-finished", s.auth(http.HandlerFunc(s.handleClearFinished)))
	s.mux.Handle("GET /api/v1/transfers/{transferId}", s.auth(http.HandlerFunc(s.handleTransferView)))
	s.mux.Handle("POST /api/v1/transfers/{transferId}/{action}", s.auth(http.HandlerFunc(s.handleTransferAction)))
	s.mux.Handle("GET /api/v1/transfers/{transferId}/files/{fileId}/missing", s.auth(http.HandlerFunc(s.handleMissingChunks)))
	s.mux.Handle("PUT /api/v1/transfers/{transferId}/files/{fileId}/chunks/{chunkIndex}", s.auth(http.HandlerFunc(s.handleChunkUpload)))
	s.mux.Handle("GET /api/v1/transfers/{transferId}/files/{fileId}/download", s.auth(http.HandlerFunc(s.handleFileDownload)))
	s.mux.Handle("POST /api/v1/transfers/{transferId}/files/{fileId}/download-link", s.auth(http.HandlerFunc(s.handleDownloadLink)))
	s.mux.Handle("POST /api/v1/transfers/{transferId}/download-all-link", s.auth(http.HandlerFunc(s.handleDownloadAllLink)))
	s.mux.HandleFunc("GET /api/v1/downloads/{ticket}/{name}", s.handleTicketDownload)

	// A interface do celular, embutida no executável já comprimida.
	s.mux.Handle("GET /", embeddedWebHandler())
}

func (s *Server) Handler() http.Handler {
	return s.trackRemote(s.securityHeaders(s.recoverPanics(s.mux)))
}

func (s *Server) trackRemote(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRequest(r) {
			s.lastRemote.Store(time.Now().UnixNano())
		}
		next.ServeHTTP(w, r)
	})
}

// lastRemoteRequest informa quando o último pedido de outro aparelho chegou.
func (s *Server) lastRemoteRequest() time.Time {
	if value := s.lastRemote.Load(); value != 0 {
		return time.Unix(0, value)
	}
	return time.Time{}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self' ws: wss:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic em %s %s: %v", r.Method, r.URL.Path, recovered)
				writeProblem(w, r, problem(500, "INTERNAL_ERROR", "O LocalDrop encontrou uma falha interna."))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.authenticate(r)
		if err != nil {
			writeProblem(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) host(next http.Handler) http.Handler {
	return s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !principalOf(r).Host {
			writeProblem(w, r, problem(403, "DEVICE_ACCESS_DENIED", "Só o computador pode fazer isso."))
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) authenticate(r *http.Request) (Principal, error) {
	deviceID := strings.TrimSpace(r.Header.Get("X-Device-Id"))
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if deviceID == "" || !strings.HasPrefix(authorization, "Device ") {
		return Principal{}, problem(401, "DEVICE_AUTHENTICATION_REQUIRED", "Este aparelho não está conectado ao computador. Escaneie o QR Code de novo.")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Device "))
	hash := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	device := s.state.Devices[deviceID]
	if device == nil || device.Revoked {
		return Principal{}, problem(401, "DEVICE_AUTHENTICATION_REQUIRED", "Este aparelho foi desconectado do computador. Escaneie o QR Code de novo.")
	}
	expected, err := hex.DecodeString(device.TokenHash)
	if err != nil || len(expected) != len(hash) || subtle.ConstantTimeCompare(hash[:], expected) != 1 {
		return Principal{}, problem(401, "DEVICE_AUTHENTICATION_REQUIRED", "Este aparelho foi desconectado do computador. Escaneie o QR Code de novo.")
	}
	device.LastSeenAt = time.Now().UTC()
	return Principal{DeviceID: deviceID, Host: device.Host}, nil
}

func principalOf(r *http.Request) Principal {
	principal, _ := r.Context().Value(principalContextKey).(Principal)
	return principal
}

func (s *Server) saveLocked() error {
	s.saveDirty = false
	return saveState(s.statePath, s.state)
}

// saveSoonLocked agrupa alterações frequentes, como recibos de blocos, em uma
// única gravação do estado por segundo.
func (s *Server) saveSoonLocked() {
	s.saveDirty = true
	if s.saveTimer == nil {
		s.saveTimer = time.AfterFunc(time.Second, func() {
			s.mu.Lock()
			s.saveTimer = nil
			if s.saveDirty {
				_ = s.saveLocked()
			}
			s.mu.Unlock()
		})
	}
}

// Flush grava alterações pendentes antes do encerramento.
func (s *Server) Flush() {
	s.mu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	if s.saveDirty {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
}

func (s *Server) deviceViewLocked(device *DeviceRecord) DeviceView {
	// O host é o próprio servidor: se a resposta chegou, ele está recebendo,
	// mesmo com a janela escondida na bandeja.
	online := device.Host || s.hub.IsOnline(device.DeviceID)
	// Atividade REST recente mantém clientes sem WebSocket visíveis por um tempo.
	if !online && !device.LastSeenAt.IsZero() {
		online = time.Since(device.LastSeenAt) < 45*time.Second
	}
	return DeviceView{
		DeviceID:    device.DeviceID,
		DisplayName: device.DisplayName,
		DeviceType:  device.DeviceType,
		Host:        device.Host,
		Online:      online,
		LastSeenAt:  device.LastSeenAt,
	}
}

func (s *Server) trustedDeviceViewsLocked() []DeviceView {
	views := make([]DeviceView, 0, len(s.state.Devices))
	for _, device := range s.state.Devices {
		if !device.Revoked {
			views = append(views, s.deviceViewLocked(device))
		}
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Host != views[j].Host {
			return views[i].Host
		}
		return strings.ToLower(views[i].DisplayName) < strings.ToLower(views[j].DisplayName)
	})
	return views
}
