package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func redeemInvite(t *testing.T, client *http.Client, baseURL, invite, deviceID, name string) *http.Response {
	t.Helper()
	return jsonRequest(t, client, http.MethodPost, baseURL+"/api/v1/pairing/invite", map[string]any{
		"invite": invite, "deviceId": deviceID, "displayName": name, "deviceType": "MOBILE",
	}, nil)
}

func TestInviteConnectsOnceAndNamesAreUnique(t *testing.T) {
	server, err := NewServer(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := httpServer.Client()
	host := claimTestHost(t, httpServer.URL, client)

	invite, expires, err := server.currentInvite()
	if err != nil || invite == "" || expires.IsZero() {
		t.Fatalf("convite inválido: %q %v %v", invite, expires, err)
	}
	if again, _, _ := server.currentInvite(); again != invite {
		t.Fatal("o mesmo convite deve valer enquanto o QR aparece")
	}
	first := decodeResponse[struct {
		DeviceID    string `json:"deviceId"`
		DeviceToken string `json:"deviceToken"`
		DisplayName string `json:"displayName"`
	}](t, redeemInvite(t, client, httpServer.URL, invite, "33333333-3333-4333-8333-333333333333", "Celular Android"), http.StatusCreated)
	if first.DeviceToken == "" || first.DisplayName != "Celular Android" {
		t.Fatalf("resposta do convite: %#v", first)
	}
	// O convite vale uma única vez.
	decodeResponse[map[string]any](t, redeemInvite(t, client, httpServer.URL, invite, "44444444-4444-4444-8444-444444444444", "Celular Android"), http.StatusGone)

	// Um novo convite conecta outro aparelho com o mesmo nome automático.
	second, _, _ := server.currentInvite()
	if second == invite {
		t.Fatal("depois de usado, um novo convite deve ser criado")
	}
	other := decodeResponse[struct {
		DisplayName string `json:"displayName"`
	}](t, redeemInvite(t, client, httpServer.URL, second, "44444444-4444-4444-8444-444444444444", "Celular Android"), http.StatusCreated)
	if other.DisplayName != "Celular Android 2" {
		t.Fatalf("nome repetido não foi diferenciado: %q", other.DisplayName)
	}

	// O token entregue pelo convite autentica o celular.
	phone := testIdentity{DeviceID: first.DeviceID, Token: first.DeviceToken}
	me := decodeResponse[DeviceView](t, jsonRequest(t, client, http.MethodGet, httpServer.URL+"/api/v1/device/me", nil, &phone), http.StatusOK)
	if me.DisplayName != "Celular Android" {
		t.Fatalf("aparelho conectado: %#v", me)
	}
	// Convite descartado (QR fechado) não funciona mais.
	third, _, _ := server.currentInvite()
	server.dropInvite()
	decodeResponse[map[string]any](t, redeemInvite(t, client, httpServer.URL, third, "55555555-5555-4555-8555-555555555555", "iPhone"), http.StatusGone)
	_ = host
}

func TestDownloadAllZipAndClearFinished(t *testing.T) {
	cfg := testConfig(t)
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := httpServer.Client()
	host := claimTestHost(t, httpServer.URL, client)
	phone := pairTestDevice(t, httpServer.URL, client, host)

	directory := t.TempDir()
	paths := []string{filepath.Join(directory, "a.txt"), filepath.Join(t.TempDir(), "a.txt"), filepath.Join(directory, "b.bin")}
	contents := []string{"primeiro", "segundo com o mesmo nome", strings.Repeat("x", 5000)}
	for index, path := range paths {
		if err := os.WriteFile(path, []byte(contents[index]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	offer, err := server.offerLocalFiles(host.DeviceID, phone.DeviceID, paths)
	if err != nil {
		t.Fatal(err)
	}
	decodeResponse[TransferView](t, jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+offer.ID+"/accept", nil, &phone), http.StatusOK)
	link := decodeResponse[struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}](t, jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+offer.ID+"/download-all-link", nil, &phone), http.StatusOK)
	if !strings.HasSuffix(link.Name, ".zip") {
		t.Fatalf("nome do zip: %q", link.Name)
	}
	response, err := client.Get(httpServer.URL + link.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("zip: status=%d tipo=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, entry := range archive.File {
		if entry.Method != zip.Store {
			t.Fatalf("%s foi comprimido; o zip deve só juntar os arquivos", entry.Name)
		}
		reader, _ := entry.Open()
		data, _ := io.ReadAll(reader)
		_ = reader.Close()
		names[entry.Name] = string(data)
	}
	if names["a.txt"] != "primeiro" || names["a (2).txt"] != "segundo com o mesmo nome" || len(names["b.bin"]) != 5000 {
		t.Fatalf("conteúdo do zip: %v", names)
	}
	// Só quem recebe pode pedir o zip.
	decodeResponse[map[string]any](t, jsonRequest(t, client, http.MethodPost, httpServer.URL+"/api/v1/transfers/"+offer.ID+"/download-all-link", nil, &host), http.StatusForbidden)

	// Limpar a lista do computador não some com o envio do celular.
	if cleared := server.clearFinished(host.DeviceID); cleared != 1 {
		t.Fatalf("itens limpos = %d", cleared)
	}
	server.mu.RLock()
	hostList := server.transferListLocked(host.DeviceID, 10)
	phoneList := server.transferListLocked(phone.DeviceID, 10)
	server.mu.RUnlock()
	if len(hostList) != 0 || len(phoneList) != 1 {
		t.Fatalf("listas depois de limpar: host=%d celular=%d", len(hostList), len(phoneList))
	}
	if server.storageUsage() <= 0 {
		t.Fatal("o espaço usado deve contar o estado salvo")
	}
	server.cleanStorage()
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatal("limpar não pode apagar arquivos do computador")
	}
}

func TestAddressScoringPrefersDefaultRouteOverLookalikeVPN(t *testing.T) {
	t.Parallel()
	candidates := []adapterAddress{
		{name: "Ethernet 2", description: "TAP-Windows Adapter V9", ip: net.IPv4(10, 8, 0, 2), wired: true},
		{name: "Ethernet", description: "Realtek PCIe GbE", ip: net.IPv4(192, 168, 0, 20), wired: true, hasGateway: true},
		{name: "vEthernet (WSL)", description: "Hyper-V Virtual Ethernet Adapter", ip: net.IPv4(172, 20, 0, 1)},
		{name: "Wi-Fi", description: "Intel Wi-Fi 6", ip: net.IPv4(169, 254, 3, 4), wireless: true},
	}
	scored := scoreAddresses(candidates, net.IPv4(192, 168, 0, 20))
	if len(scored) == 0 || scored[0].IPv4 != "192.168.0.20" {
		t.Fatalf("endereço escolhido: %+v", scored)
	}
	for _, address := range scored {
		if address.IPv4 == "172.20.0.1" || address.IPv4 == "10.8.0.2" {
			t.Fatalf("placa virtual sem gateway não deve aparecer: %+v", scored)
		}
	}
}

func TestPortFallbackSkipsBusyPortAndDetectsRunningInstance(t *testing.T) {
	busy, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	free, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := free.Addr().(*net.TCPAddr).Port
	_ = free.Close()
	listener, port, err := listenLocalDrop([]int{busyPort, freePort})
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if port != freePort {
		t.Fatalf("porta escolhida = %d, esperada %d", port, freePort)
	}

	server, err := NewServer(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	running, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = http.Serve(running, server.Handler()) }()
	defer running.Close()
	runningPort := running.Addr().(*net.TCPAddr).Port
	if !localDropAt(runningPort) {
		t.Fatal("a instância aberta não foi reconhecida")
	}
	if localDropAt(busyPort) {
		t.Fatal("um programa qualquer foi confundido com o LocalDrop")
	}
	ports := candidatePorts(8093, 0)
	if ports[0] != 8093 || len(ports) != lastPort-defaultPort+1 {
		t.Fatalf("ordem das portas: %v", ports)
	}
	if fixed := candidatePorts(8093, 9000); len(fixed) != 1 || fixed[0] != 9000 {
		t.Fatalf("porta fixa: %v", fixed)
	}
}

func TestFriendlyErrorsExplainWhatToDo(t *testing.T) {
	t.Parallel()
	explained := explainError(problem(422, "INSUFFICIENT_STORAGE", "texto técnico"))
	if !strings.Contains(explained.Title, "espaço") || explained.Hint == "" {
		t.Fatalf("erro amigável: %#v", explained)
	}
	if generic := explainError(os.ErrNotExist); !strings.Contains(generic.Title, "não foi encontrado") {
		t.Fatalf("arquivo ausente: %#v", generic)
	}
}

func TestEmbeddedWebServesGzipWhenAvailable(t *testing.T) {
	server, err := NewServer(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	transport := &http.Transport{DisableCompression: true}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("ETag") == "" || response.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("index: status=%d etag=%q vary=%q", response.StatusCode, response.Header.Get("ETag"), response.Header.Get("Vary"))
	}
	again, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/", nil)
	again.Header.Set("If-None-Match", response.Header.Get("ETag"))
	cached, err := http.DefaultClient.Do(again)
	if err != nil {
		t.Fatal(err)
	}
	_ = cached.Body.Close()
	if cached.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidação deveria responder 304: %d", cached.StatusCode)
	}
}
