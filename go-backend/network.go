package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

type NetworkAddress struct {
	Name  string `json:"name"`
	IPv4  string `json:"ipv4"`
	Score int    `json:"score"`
}

// adapterAddress descreve um IPv4 local e a placa a que ele pertence. No
// Windows os dados vêm do próprio sistema (tipo da placa, descrição e gateway).
type adapterAddress struct {
	name, description string
	ip                net.IP
	wireless, wired   bool
	hasGateway        bool
}

var virtualAdapterHints = []string{
	"virtual", "vmware", "hyper-v", "vethernet", "loopback", "vpn", "tap-", "tap ", "tunnel",
	"wireguard", "tailscale", "zerotier", "openvpn", "wsl", "docker", "bluetooth", "npcap", "pseudo",
}

func looksVirtual(text string) bool {
	lower := strings.ToLower(text)
	for _, hint := range virtualAdapterHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func isPrivateIPv4(v4 net.IP) bool {
	return v4[0] == 10 || (v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) || (v4[0] == 192 && v4[1] == 168)
}

// defaultRouteIPv4 pergunta ao sistema qual endereço local seria usado para
// sair da rede. Conectar um socket UDP só consulta a tabela de rotas: nenhum
// pacote é enviado.
func defaultRouteIPv4() net.IP {
	conn, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return nil
	}
	defer conn.Close()
	if address, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return address.IP.To4()
	}
	return nil
}

// scoreAddresses ordena os endereços do mais provável ao menos provável de
// funcionar no celular. A rota padrão decide; o nome da placa só desempata.
func scoreAddresses(candidates []adapterAddress, route net.IP) []NetworkAddress {
	result := make([]NetworkAddress, 0, len(candidates))
	for _, candidate := range candidates {
		v4 := candidate.ip.To4()
		if v4 == nil || v4.IsLoopback() || v4.IsUnspecified() {
			continue
		}
		private := isPrivateIPv4(v4)
		linkLocal := v4.IsLinkLocalUnicast()
		if !private && !linkLocal {
			continue
		}
		virtual := looksVirtual(candidate.name) || looksVirtual(candidate.description)
		if virtual && !candidate.hasGateway {
			continue
		}
		score := 20
		if private {
			score += 20
		}
		if linkLocal {
			score -= 30
		}
		if candidate.hasGateway {
			score += 40
		}
		if route != nil && v4.Equal(route) {
			score += 40
		}
		lower := strings.ToLower(candidate.name)
		switch {
		case candidate.wireless || strings.Contains(lower, "wi-fi") || strings.Contains(lower, "wifi") || strings.Contains(lower, "wireless"):
			score += 30
		case candidate.wired || strings.Contains(lower, "ethernet"):
			score += 25
		}
		if virtual {
			score -= 60
		}
		result = append(result, NetworkAddress{Name: candidate.name, IPv4: v4.String(), Score: score})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		return result[i].IPv4 < result[j].IPv4
	})
	return result
}

func (s *Server) addresses() []NetworkAddress {
	return scoreAddresses(localAdapterAddresses(), defaultRouteIPv4())
}

// localURL é consultado a cada pintura da janela; as interfaces de rede são
// enumeradas no máximo a cada 10 segundos.
func (s *Server) localURL() string {
	s.urlMu.Lock()
	defer s.urlMu.Unlock()
	if s.urlCache != "" && time.Now().Before(s.urlExpiry) {
		return s.urlCache
	}
	s.urlCache = s.computeLocalURL()
	s.urlExpiry = time.Now().Add(10 * time.Second)
	return s.urlCache
}

func (s *Server) computeLocalURL() string {
	addresses := s.addresses()
	if len(addresses) == 0 {
		return fmt.Sprintf("http://localhost:%d", s.cfg.Port)
	}
	return fmt.Sprintf("http://%s:%d", addresses[0].IPv4, s.cfg.Port)
}

func (s *Server) urlForAddress(ipv4 string) string {
	return fmt.Sprintf("http://%s:%d", ipv4, s.cfg.Port)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"status":    "READY",
		"app":       "LocalDrop",
		"version":   version,
		"checkedAt": time.Now().UTC(),
		"port":      s.cfg.Port,
		"runtime":   "go",
	})
}

func (s *Server) downloadDirectory() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Settings.DownloadDirectory
}

func directoryWritable(directory string) bool {
	if err := osMkdirAll(directory); err != nil {
		return false
	}
	file, err := osCreateTemp(directory, ".localdrop-write-test-")
	if err != nil {
		return false
	}
	name := file.Name()
	_ = file.Close()
	_ = osRemove(name)
	return true
}

// Variáveis substituíveis em testes sem espalhar wrappers pelo restante do servidor.
var (
	osMkdirAll   = func(path string) error { return mkdirAll(path) }
	osCreateTemp = createTemp
	osRemove     = removeFile
)

// localDropAt diz se a porta já é usada por outro LocalDrop. A consulta é feita
// com um pedido HTTP mínimo escrito à mão, sem carregar o cliente HTTP completo.
func localDropAt(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 700*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET /api/v1/health HTTP/1.0\r\nHost: 127.0.0.1:%d\r\n\r\n", port); err != nil {
		return false
	}
	reader := bufio.NewReaderSize(conn, 4096)
	status, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(status, "HTTP/1.") || !strings.Contains(status, " 200 ") {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(reader, 4096))
	text := string(body)
	return strings.Contains(text, `"app":"LocalDrop"`) || strings.Contains(text, `"runtime":"go"`)
}

// candidatePorts devolve a porta gravada primeiro, para o endereço do celular
// não mudar, e depois a faixa padrão.
func candidatePorts(saved int, fixed int) []int {
	if fixed != 0 {
		return []int{fixed}
	}
	ports := make([]int, 0, lastPort-defaultPort+2)
	if saved >= 1 && saved <= 65535 {
		ports = append(ports, saved)
	}
	for port := defaultPort; port <= lastPort; port++ {
		if port != saved {
			ports = append(ports, port)
		}
	}
	return ports
}

// errAlreadyRunning indica que outra instância do LocalDrop já atende.
type errAlreadyRunning struct{ port int }

func (e errAlreadyRunning) Error() string {
	return fmt.Sprintf("o LocalDrop já está aberto na porta %d", e.port)
}

func listenLocalDrop(ports []int) (net.Listener, int, error) {
	var lastErr error
	for _, port := range ports {
		listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			return listener, port, nil
		}
		if localDropAt(port) {
			return nil, port, errAlreadyRunning{port: port}
		}
		lastErr = err
	}
	if len(ports) == 1 {
		return nil, 0, problem(0, "PORT_BUSY", fmt.Sprintf("A porta %d está sendo usada por outro programa. Feche esse programa ou remova LOCALDROP_PORT para o LocalDrop escolher outra sozinho. (%v)", ports[0], lastErr))
	}
	return nil, 0, problem(0, "PORT_BUSY", fmt.Sprintf("As portas %d a %d estão todas ocupadas por outros programas. Feche algum deles ou reinicie o computador e abra o LocalDrop de novo.", defaultPort, lastPort))
}
