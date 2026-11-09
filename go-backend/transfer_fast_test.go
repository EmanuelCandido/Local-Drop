package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uploadChunk(t *testing.T, client *http.Client, baseURL string, sender testIdentity, transfer TransferView, index int, chunk []byte) {
	t.Helper()
	target := fmt.Sprintf("%s/api/v1/transfers/%s/files/%s/chunks/%d", baseURL, transfer.ID, transfer.Files[0].ID, index)
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

func TestChunksOutOfOrderWithoutFileHashAreWrittenInPlace(t *testing.T) {
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

	data := []byte("blocos chegando fora de ordem, sem hash completo")
	response := jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers", map[string]any{
		"receiverId": host.DeviceID,
		"files":      []map[string]any{{"name": "fora-de-ordem.txt", "size": len(data), "mimeType": "text/plain", "sha256": ""}},
	}, &peer)
	transfer := decodeResponse[TransferView](t, response, http.StatusCreated)
	if transfer.ChunkSize != cfg.ChunkSize {
		t.Fatalf("chunkSize = %d, esperado %d", transfer.ChunkSize, cfg.ChunkSize)
	}
	response = jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+transfer.ID+"/accept", nil, &host)
	transfer = decodeResponse[TransferView](t, response, http.StatusOK)

	size := int(cfg.ChunkSize)
	for index := int(transfer.Files[0].TotalChunks) - 1; index >= 0; index-- {
		end := min((index+1)*size, len(data))
		uploadChunk(t, client, httpServer.URL, peer, transfer, index, data[index*size:end])
	}
	completed := waitCompleted(t, httpServer.URL, client, host, transfer.ID)
	delivered, err := os.ReadFile(filepath.Join(cfg.Downloads, *completed.Files[0].DeliveredName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(delivered, data) {
		t.Fatalf("conteúdo montado difere: %q", delivered)
	}
	entries, _ := os.ReadDir(cfg.Downloads)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".localdrop") {
			t.Fatalf("arquivo parcial ficou na pasta: %s", entry.Name())
		}
	}
}

func TestCancelRemovesPartialFile(t *testing.T) {
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

	data := []byte("12345678")
	transfer := createTransfer(t, httpServer.URL, client, peer, host.DeviceID, "parcial.bin", data)
	response := jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+transfer.ID+"/accept", nil, &host)
	transfer = decodeResponse[TransferView](t, response, http.StatusOK)
	uploadChunk(t, client, httpServer.URL, peer, transfer, 0, data[:4])

	server.mu.RLock()
	part := server.state.Transfers[transfer.ID].Files[0].PartPath
	server.mu.RUnlock()
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("arquivo parcial não foi criado: %v", err)
	}
	response = jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+transfer.ID+"/cancel", nil, &peer)
	decodeResponse[TransferView](t, response, http.StatusOK)
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("arquivo parcial deveria ter sido removido: %v", err)
	}
}

func TestLocalSourceOfferIsServedFromOriginalThroughTicket(t *testing.T) {
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

	data := bytes.Repeat([]byte("video-do-pc-"), 1000)
	source := filepath.Join(t.TempDir(), "férias.mp4")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	offered, err := server.offerLocalFiles(host.DeviceID, peer.DeviceID, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	if offered.State != "WAITING_ACCEPTANCE" || offered.Files[0].MimeType != "video/mp4" {
		t.Fatalf("oferta inesperada: %#v", offered)
	}
	response := jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+offered.ID+"/accept", nil, &peer)
	accepted := decodeResponse[TransferView](t, response, http.StatusOK)
	if accepted.State != "COMPLETED" || !accepted.Files[0].DownloadAvailable || accepted.ReceivedBytes != int64(len(data)) {
		t.Fatalf("oferta local deveria ficar pronta no aceite: %#v", accepted)
	}
	if _, err := os.Stat(filepath.Join(cfg.StorageRoot, "go-completed", offered.ID)); !os.IsNotExist(err) {
		t.Fatal("oferta local não deveria copiar o arquivo")
	}

	linkURL := fmt.Sprintf("%s/api/v1/transfers/%s/files/%s/download-link", httpServer.URL, offered.ID, offered.Files[0].ID)
	response = jsonRequest(t, client, http.MethodPost, linkURL, nil, &host)
	decodeResponse[apiErrorBody](t, response, http.StatusForbidden)
	response = jsonRequest(t, client, http.MethodPost, linkURL, nil, &peer)
	link := decodeResponse[struct {
		URL string `json:"url"`
	}](t, response, http.StatusOK)
	if !strings.HasPrefix(link.URL, "/api/v1/downloads/") || !strings.HasSuffix(link.URL, "/f%C3%A9rias.mp4") {
		t.Fatalf("link inesperado: %s", link.URL)
	}

	download, err := client.Get(httpServer.URL + link.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(download.Body)
	_ = download.Body.Close()
	if download.StatusCode != http.StatusOK || !bytes.Equal(body, data) {
		t.Fatalf("download pelo link falhou: status=%d tamanho=%d", download.StatusCode, len(body))
	}
	if !strings.Contains(download.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download deveria ser anexo: %q", download.Header.Get("Content-Disposition"))
	}

	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+link.URL, nil)
	request.Header.Set("Range", "bytes=0-9")
	partial, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(partial.Body)
	_ = partial.Body.Close()
	if partial.StatusCode != http.StatusPartialContent || !bytes.Equal(body, data[:10]) {
		t.Fatalf("retomada por Range falhou: status=%d corpo=%q", partial.StatusCode, body)
	}

	if err := os.WriteFile(source, []byte("mudou"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := client.Get(httpServer.URL + link.URL)
	if err != nil {
		t.Fatal(err)
	}
	decodeResponse[apiErrorBody](t, changed, http.StatusConflict)

	invalid, err := client.Get(httpServer.URL + "/api/v1/downloads/inexistente/arquivo.bin")
	if err != nil {
		t.Fatal(err)
	}
	decodeResponse[apiErrorBody](t, invalid, http.StatusNotFound)
}

func TestRecoverResetsLegacyChunkReceipts(t *testing.T) {
	cfg := testConfig(t)
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.state.Transfers["legado"] = &TransferRecord{
		ID: "legado", SenderID: "a", ReceiverID: "b", State: "VERIFYING", TotalBytes: 8, ReceivedBytes: 8,
		Files: []*TransferFile{{ID: "arquivo", Name: "x.bin", Size: 8, TotalChunks: 2, ReceivedChunks: 2, ReceivedBytes: 8,
			Receipts: map[int64]*ChunkReceipt{0: {Index: 0, Size: 4}, 1: {Index: 1, Size: 4}}}},
	}
	server.mu.Unlock()
	server.RecoverTransfers()
	server.mu.RLock()
	defer server.mu.RUnlock()
	transfer := server.state.Transfers["legado"]
	if transfer.State != "PAUSED" || transfer.ReceivedBytes != 0 || len(transfer.Files[0].Receipts) != 0 {
		t.Fatalf("transferência legada não foi reiniciada: estado=%s bytes=%d", transfer.State, transfer.ReceivedBytes)
	}
}

func TestHostIsAlwaysOnlineForPeers(t *testing.T) {
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
	server.mu.Lock()
	server.state.Devices[host.DeviceID].LastSeenAt = time.Now().Add(-time.Hour)
	server.mu.Unlock()
	response := jsonRequest(t, client, http.MethodGet, httpServer.URL+"/api/v1/devices", nil, &peer)
	for _, device := range decodeResponse[[]DeviceView](t, response, http.StatusOK) {
		if device.Host && !device.Online {
			t.Fatal("o host deve aparecer online enquanto o servidor responde")
		}
	}
}
