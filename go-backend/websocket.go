package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Hub struct {
	server      *Server
	mu          sync.RWMutex
	connections map[string]map[*webSocketClient]struct{}

	localMu       sync.RWMutex
	localDeviceID string
	localListener func(eventType string, payload any)
}

type webSocketClient struct {
	deviceID string
	conn     *websocket.Conn
	writeMu  sync.Mutex
	closed   sync.Once
}

type eventEnvelope struct {
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	Payload    any       `json:"payload,omitempty"`
}

func NewHub(server *Server) *Hub {
	return &Hub{server: server, connections: make(map[string]map[*webSocketClient]struct{})}
}

func (h *Hub) IsOnline(deviceID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections[deviceID]) > 0
}

func (h *Hub) add(client *webSocketClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	connections := h.connections[client.deviceID]
	first := len(connections) == 0
	if connections == nil {
		connections = make(map[*webSocketClient]struct{})
		h.connections[client.deviceID] = connections
	}
	connections[client] = struct{}{}
	return first
}

func (h *Hub) remove(client *webSocketClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	connections := h.connections[client.deviceID]
	if connections == nil {
		return false
	}
	delete(connections, client)
	if len(connections) == 0 {
		delete(h.connections, client.deviceID)
		return true
	}
	return false
}

func (h *Hub) snapshot(deviceID string) []*webSocketClient {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]*webSocketClient, 0)
	if deviceID != "" {
		for client := range h.connections[deviceID] {
			result = append(result, client)
		}
		return result
	}
	for _, connections := range h.connections {
		for client := range connections {
			result = append(result, client)
		}
	}
	return result
}

// SetLocalListener entrega à janela nativa, no mesmo processo, os eventos
// destinados ao host — sem WebSocket e sem consultas periódicas.
func (h *Hub) SetLocalListener(deviceID string, listener func(eventType string, payload any)) {
	h.localMu.Lock()
	h.localDeviceID, h.localListener = deviceID, listener
	h.localMu.Unlock()
}

func (h *Hub) notifyLocal(deviceID, eventType string, payload any) {
	h.localMu.RLock()
	listener, local := h.localListener, h.localDeviceID
	h.localMu.RUnlock()
	if listener != nil && (deviceID == "" || deviceID == local) {
		listener(eventType, payload)
	}
}

func (h *Hub) SendToDevice(deviceID, eventType string, payload any) {
	h.notifyLocal(deviceID, eventType, payload)
	message := eventEnvelope{Type: eventType, OccurredAt: time.Now().UTC(), Payload: payload}
	for _, client := range h.snapshot(deviceID) {
		if err := client.writeJSON(message); err != nil {
			client.close()
		}
	}
}

func (h *Hub) Broadcast(eventType string, payload any) {
	h.notifyLocal("", eventType, payload)
	message := eventEnvelope{Type: eventType, OccurredAt: time.Now().UTC(), Payload: payload}
	for _, client := range h.snapshot("") {
		if err := client.writeJSON(message); err != nil {
			client.close()
		}
	}
}

func (h *Hub) SendToHost(eventType string, payload any) {
	h.server.mu.RLock()
	hostID := ""
	for _, device := range h.server.state.Devices {
		if device.Host && !device.Revoked {
			hostID = device.DeviceID
			break
		}
	}
	h.server.mu.RUnlock()
	if hostID != "" {
		h.SendToDevice(hostID, eventType, payload)
	}
}

func (h *Hub) Disconnect(deviceID string) {
	for _, client := range h.snapshot(deviceID) {
		client.close()
	}
}

func (c *webSocketClient) writeJSON(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteJSON(value)
}

func (c *webSocketClient) close() {
	c.closed.Do(func() {
		c.writeMu.Lock()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = c.conn.Close()
		c.writeMu.Unlock()
	})
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     sameOrigin,
	}
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(64 * 1024)
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, bytes, err := connection.ReadMessage()
	if err != nil {
		_ = connection.Close()
		return
	}
	var authentication struct {
		Type     string `json:"type"`
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if json.Unmarshal(bytes, &authentication) != nil || authentication.Type != "AUTHENTICATE" {
		_ = connection.WriteJSON(eventEnvelope{Type: "AUTHENTICATION_FAILED", OccurredAt: time.Now().UTC()})
		_ = connection.Close()
		return
	}
	request := &http.Request{Header: make(http.Header)}
	request.Header.Set("X-Device-Id", authentication.DeviceID)
	request.Header.Set("Authorization", "Device "+authentication.Token)
	principal, authErr := s.authenticate(request)
	if authErr != nil {
		_ = connection.WriteJSON(eventEnvelope{Type: "AUTHENTICATION_FAILED", OccurredAt: time.Now().UTC()})
		_ = connection.Close()
		return
	}
	client := &webSocketClient{deviceID: principal.DeviceID, conn: connection}
	first := s.hub.add(client)
	_ = client.writeJSON(eventEnvelope{
		Type: "AUTHENTICATED", OccurredAt: time.Now().UTC(), Payload: map[string]string{"deviceId": principal.DeviceID},
	})
	if first {
		s.hub.Broadcast("DEVICE_CONNECTED", map[string]string{"deviceId": principal.DeviceID})
	}
	defer func() {
		last := s.hub.remove(client)
		client.close()
		if last {
			s.hub.Broadcast("DEVICE_DISCONNECTED", map[string]string{"deviceId": principal.DeviceID})
		}
	}()

	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(45 * time.Second))
	})
	for {
		_ = connection.SetReadDeadline(time.Now().Add(45 * time.Second))
		messageType, bytes, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var message struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(bytes, &message) != nil {
			continue
		}
		if message.Type == "HEARTBEAT" {
			s.mu.Lock()
			if device := s.state.Devices[principal.DeviceID]; device != nil {
				device.LastSeenAt = time.Now().UTC()
			}
			s.mu.Unlock()
			if err := client.writeJSON(eventEnvelope{Type: "HEARTBEAT_ACK", OccurredAt: time.Now().UTC()}); err != nil {
				return
			}
		}
	}
}
