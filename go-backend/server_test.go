package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testIdentity struct {
	DeviceID string
	Token    string
}

func testConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	return Config{
		Port: 8088, StorageRoot: filepath.Join(root, "state"), Downloads: filepath.Join(root, "downloads"),
		ChunkSize: 4, MaxTotalChunks: 10_000, PairingLifetime: time.Minute,
		PairingRateLimit: 20, PairingRateWindow: time.Minute,
		CreateRateLimit: 20, CreateRateWindow: time.Minute,
		InactivityTimeout: time.Minute, TemporaryLifetime: time.Hour,
	}
}

func TestEmbeddedWebInterface(t *testing.T) {
	server, err := NewServer(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	response, err := httpServer.Client().Get(httpServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `<div id="root"></div>`) {
		t.Fatalf("interface web inválida: status=%d tamanho=%d", response.StatusCode, len(body))
	}
	if response.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("index deve evitar cache permanente: %q", response.Header.Get("Cache-Control"))
	}

	removed, err := httpServer.Client().Get(httpServer.URL + "/downloads/LocalDrop-Android.apk")
	if err != nil {
		t.Fatal(err)
	}
	defer removed.Body.Close()
	if removed.StatusCode != http.StatusNotFound {
		t.Fatalf("download obsoleto do APK ainda está exposto: %d", removed.StatusCode)
	}
}

func jsonRequest(t *testing.T, client *http.Client, method, target string, body any, identity *testIdentity) *http.Response {
	t.Helper()
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, target, input)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if identity != nil {
		request.Header.Set("Authorization", "Device "+identity.Token)
		request.Header.Set("X-Device-Id", identity.DeviceID)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse[T any](t *testing.T, response *http.Response, expected int) T {
	t.Helper()
	defer response.Body.Close()
	bytes, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != expected {
		t.Fatalf("status = %d, esperado %d, corpo=%s", response.StatusCode, expected, bytes)
	}
	var value T
	if len(bytes) > 0 {
		if err := json.Unmarshal(bytes, &value); err != nil {
			t.Fatalf("JSON inválido: %v; corpo=%s", err, bytes)
		}
	}
	return value
}

func claimTestHost(t *testing.T, baseURL string, client *http.Client) testIdentity {
	t.Helper()
	response := jsonRequest(t, client, http.MethodPost, baseURL+"/api/v1/host/claim", map[string]any{
		"deviceId": "11111111-1111-4111-8111-111111111111", "displayName": "PC de teste", "deviceType": "DESKTOP",
	}, nil)
	created := decodeResponse[struct {
		DeviceID    string `json:"deviceId"`
		DeviceToken string `json:"deviceToken"`
	}](t, response, http.StatusCreated)
	return testIdentity{DeviceID: created.DeviceID, Token: created.DeviceToken}
}

func pairTestDevice(t *testing.T, baseURL string, client *http.Client, host testIdentity) testIdentity {
	t.Helper()
	deviceID := "22222222-2222-4222-8222-222222222222"
	response := jsonRequest(t, client, http.MethodPost, baseURL+"/api/v1/pairing/requests", map[string]any{
		"deviceId": deviceID, "displayName": "Celular de teste", "deviceType": "MOBILE",
	}, nil)
	created := decodeResponse[struct {
		RequestID string `json:"requestId"`
		Code      string `json:"code"`
		PollToken string `json:"pollToken"`
	}](t, response, http.StatusCreated)
	response = jsonRequest(t, client, http.MethodPost, baseURL+"/api/v1/pairing/requests/"+created.RequestID+"/approve", map[string]string{"code": created.Code}, &host)
	decodeResponse[any](t, response, http.StatusNoContent)
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/pairing/requests/"+created.RequestID+"/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Pairing-Poll-Token", created.PollToken)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	poll := decodeResponse[struct {
		Status      string  `json:"status"`
		DeviceToken *string `json:"deviceToken"`
	}](t, response, http.StatusOK)
	if poll.Status != "COMPLETED" || poll.DeviceToken == nil {
		t.Fatalf("pareamento não concluiu: %#v", poll)
	}
	return testIdentity{DeviceID: deviceID, Token: *poll.DeviceToken}
}

func sha(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestAcceptRejectsInsufficientDiskSpace(t *testing.T) {
	cfg := testConfig(t)
	cfg.MinimumFreeSpace = math.MaxUint64
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	server.mu.Lock()
	server.state.Devices["sender"] = &DeviceRecord{DeviceID: "sender", DisplayName: "Origem", DeviceType: "DESKTOP", CreatedAt: now}
	server.state.Devices["receiver"] = &DeviceRecord{DeviceID: "receiver", DisplayName: "Destino", DeviceType: "MOBILE", CreatedAt: now}
	server.state.Transfers["transfer"] = &TransferRecord{
		ID: "transfer", SenderID: "sender", ReceiverID: "receiver", State: "WAITING_ACCEPTANCE",
		TotalBytes: 1, CreatedAt: now, UpdatedAt: now,
		Files: []*TransferFile{{ID: "file", Name: "arquivo.bin", Size: 1, TotalChunks: 1, Receipts: map[int64]*ChunkReceipt{}}},
	}
	server.mu.Unlock()

	_, err = server.transferAction("receiver", "transfer", "accept")
	var api *apiProblem
	if !errors.As(err, &api) || api.Code != "INSUFFICIENT_STORAGE" {
		t.Fatalf("erro = %#v, esperado INSUFFICIENT_STORAGE", err)
	}
	server.mu.RLock()
	state := server.state.Transfers["transfer"].State
	server.mu.RUnlock()
	if state != "WAITING_ACCEPTANCE" {
		t.Fatalf("estado = %s, esperado WAITING_ACCEPTANCE", state)
	}
}

func createTransfer(t *testing.T, baseURL string, client *http.Client, sender testIdentity, receiverID string, name string, data []byte) TransferView {
	t.Helper()
	response := jsonRequest(t, client, http.MethodPost, baseURL+"/api/v1/transfers", map[string]any{
		"receiverId": receiverID,
		"files":      []map[string]any{{"name": name, "size": len(data), "mimeType": "application/octet-stream", "sha256": sha(data)}},
	}, &sender)
	return decodeResponse[TransferView](t, response, http.StatusCreated)
}

func uploadAll(t *testing.T, baseURL string, client *http.Client, sender testIdentity, transfer TransferView, data []byte, chunkSize int) {
	t.Helper()
	file := transfer.Files[0]
	for index, start := int64(0), 0; start < len(data); index, start = index+1, start+chunkSize {
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]
		target := fmt.Sprintf("%s/api/v1/transfers/%s/files/%s/chunks/%d", baseURL, transfer.ID, file.ID, index)
		request, err := http.NewRequest(http.MethodPut, target, bytes.NewReader(chunk))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set("X-Chunk-SHA256", sha(chunk))
		request.Header.Set("Authorization", "Device "+sender.Token)
		request.Header.Set("X-Device-Id", sender.DeviceID)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		decodeResponse[map[string]any](t, response, http.StatusOK)
	}
}

func waitCompleted(t *testing.T, baseURL string, client *http.Client, identity testIdentity, transferID string) TransferView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response := jsonRequest(t, client, http.MethodGet, baseURL+"/api/v1/transfers/"+transferID, nil, &identity)
		view := decodeResponse[TransferView](t, response, http.StatusOK)
		if view.State == "COMPLETED" {
			return view
		}
		if view.State == "FAILED" {
			code, message := "", ""
			if view.FailureCode != nil {
				code = *view.FailureCode
			}
			if view.FailureMessage != nil {
				message = *view.FailureMessage
			}
			t.Fatalf("transferência falhou: %s: %s", code, message)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("transferência não concluiu")
	return TransferView{}
}

func TestPairingTransferToHostAndPersistence(t *testing.T) {
	cfg := testConfig(t)
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := httpServer.Client()
	host := claimTestHost(t, httpServer.URL, client)
	peer := pairTestDevice(t, httpServer.URL, client, host)

	data := []byte("conteúdo íntegro enviado em vários blocos")
	transfer := createTransfer(t, httpServer.URL, client, peer, host.DeviceID, "relatorio.txt", data)
	response := jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+transfer.ID+"/accept", nil, &host)
	transfer = decodeResponse[TransferView](t, response, http.StatusOK)
	if transfer.State != "ACCEPTED" {
		t.Fatalf("estado após aceite = %s", transfer.State)
	}
	uploadAll(t, httpServer.URL, client, peer, transfer, data, int(cfg.ChunkSize))
	completed := waitCompleted(t, httpServer.URL, client, host, transfer.ID)
	if !completed.Files[0].DownloadAvailable || completed.Files[0].DeliveredName == nil {
		t.Fatalf("arquivo concluído indisponível: %#v", completed.Files[0])
	}
	delivered, err := os.ReadFile(filepath.Join(cfg.Downloads, *completed.Files[0].DeliveredName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(delivered, data) {
		t.Fatal("conteúdo publicado difere do original")
	}

	reloaded, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reloadedHTTP := httptest.NewServer(reloaded.Handler())
	defer reloadedHTTP.Close()
	response = jsonRequest(t, reloadedHTTP.Client(), http.MethodGet, reloadedHTTP.URL+"/api/v1/device/me", nil, &host)
	me := decodeResponse[DeviceView](t, response, http.StatusOK)
	if !me.Host || me.DeviceID != host.DeviceID {
		t.Fatalf("host não persistiu: %#v", me)
	}
}

func TestTransferToWebReceiverDownloadAndMissingRanges(t *testing.T) {
	cfg := testConfig(t)
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := httpServer.Client()
	host := claimTestHost(t, httpServer.URL, client)
	peer := pairTestDevice(t, httpServer.URL, client, host)
	data := []byte("arquivo para o navegador")
	transfer := createTransfer(t, httpServer.URL, client, host, peer.DeviceID, "foto.bin", data)
	response := jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+transfer.ID+"/accept", nil, &peer)
	transfer = decodeResponse[TransferView](t, response, http.StatusOK)

	missingURL := fmt.Sprintf("%s/api/v1/transfers/%s/files/%s/missing?cursor=0&limit=1000", httpServer.URL, transfer.ID, transfer.Files[0].ID)
	response = jsonRequest(t, client, http.MethodGet, missingURL, nil, &host)
	missing := decodeResponse[struct {
		Ranges []struct {
			Start int64 `json:"startInclusive"`
			End   int64 `json:"endExclusive"`
		} `json:"ranges"`
	}](t, response, http.StatusOK)
	if len(missing.Ranges) != 1 || missing.Ranges[0].Start != 0 || missing.Ranges[0].End != transfer.Files[0].TotalChunks {
		t.Fatalf("faixas ausentes inesperadas: %#v", missing.Ranges)
	}

	uploadAll(t, httpServer.URL, client, host, transfer, data, int(cfg.ChunkSize))
	completed := waitCompleted(t, httpServer.URL, client, peer, transfer.ID)
	downloadURL := fmt.Sprintf("%s/api/v1/transfers/%s/files/%s/download", httpServer.URL, transfer.ID, completed.Files[0].ID)
	response = jsonRequest(t, client, http.MethodGet, downloadURL, nil, &peer)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d", response.StatusCode)
	}
	downloaded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, data) {
		t.Fatal("download difere do conteúdo enviado")
	}
}

func TestWebSocketAuthenticationAndHeartbeat(t *testing.T) {
	cfg := testConfig(t)
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	host := claimTestHost(t, httpServer.URL, httpServer.Client())
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/events"
	header := http.Header{"Origin": []string{httpServer.URL}}
	connection, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.WriteJSON(map[string]string{"type": "AUTHENTICATE", "deviceId": host.DeviceID, "token": host.Token}); err != nil {
		t.Fatal(err)
	}
	seenAuthenticated := false
	for index := 0; index < 3; index++ {
		var message map[string]any
		if err := connection.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message["type"] == "AUTHENTICATED" {
			seenAuthenticated = true
			break
		}
	}
	if !seenAuthenticated {
		t.Fatal("WebSocket não autenticou")
	}
	if err := connection.WriteJSON(map[string]string{"type": "HEARTBEAT"}); err != nil {
		t.Fatal(err)
	}
	seenHeartbeat := false
	for index := 0; index < 4; index++ {
		var message map[string]any
		if err := connection.ReadJSON(&message); err != nil {
			t.Fatal(err)
		}
		if message["type"] == "HEARTBEAT_ACK" {
			seenHeartbeat = true
			break
		}
	}
	if !seenHeartbeat {
		t.Fatal("WebSocket não respondeu ao heartbeat")
	}
}

func TestInvalidChunkHashIsRejected(t *testing.T) {
	cfg := testConfig(t)
	server, _ := NewServer(cfg)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	host := claimTestHost(t, httpServer.URL, httpServer.Client())
	peer := pairTestDevice(t, httpServer.URL, httpServer.Client(), host)
	data := []byte("abcd")
	transfer := createTransfer(t, httpServer.URL, httpServer.Client(), host, peer.DeviceID, "hash.bin", data)
	response := jsonRequest(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/api/v1/transfers/"+transfer.ID+"/accept", nil, &peer)
	transfer = decodeResponse[TransferView](t, response, http.StatusOK)
	target := fmt.Sprintf("%s/api/v1/transfers/%s/files/%s/chunks/0", httpServer.URL, transfer.ID, transfer.Files[0].ID)
	request, _ := http.NewRequest(http.MethodPut, target, bytes.NewReader(data))
	request.Header.Set("X-Chunk-SHA256", strings.Repeat("0", 64))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Authorization", "Device "+host.Token)
	request.Header.Set("X-Device-Id", host.DeviceID)
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	problem := decodeResponse[apiErrorBody](t, response, http.StatusUnprocessableEntity)
	if problem.Code != "CHUNK_HASH_MISMATCH" {
		t.Fatalf("código inesperado: %s", problem.Code)
	}
}

func TestLargeTransferStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("teste de vazão desativado no modo curto")
	}
	cfg := testConfig(t)
	cfg.ChunkSize = 4 * 1024 * 1024
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := httpServer.Client()
	host := claimTestHost(t, httpServer.URL, client)
	peer := pairTestDevice(t, httpServer.URL, client, host)
	data := bytes.Repeat([]byte("LocalDrop-Go-0123456789abcdef"), (32*1024*1024)/28+1)
	data = data[:32*1024*1024]
	started := time.Now()
	transfer := createTransfer(t, httpServer.URL, client, peer, host.DeviceID, "desempenho.bin", data)
	response := jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+transfer.ID+"/accept", nil, &host)
	transfer = decodeResponse[TransferView](t, response, http.StatusOK)
	uploadAll(t, httpServer.URL, client, peer, transfer, data, int(cfg.ChunkSize))
	waitCompleted(t, httpServer.URL, client, host, transfer.ID)
	elapsed := time.Since(started)
	throughput := float64(len(data)) / elapsed.Seconds() / (1024 * 1024)
	t.Logf("32 MiB transferidos e verificados em %s (%.1f MiB/s)", elapsed.Round(time.Millisecond), throughput)
}
