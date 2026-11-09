package main

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

type NetworkAddress struct {
	Name  string `json:"name"`
	IPv4  string `json:"ipv4"`
	Score int    `json:"score"`
}

func (s *Server) addresses() []NetworkAddress {
	interfaces, _ := net.Interfaces()
	result := make([]NetworkAddress, 0)
	for _, current := range interfaces {
		if current.Flags&net.FlagUp == 0 || current.Flags&net.FlagLoopback != 0 {
			continue
		}
		lower := strings.ToLower(current.Name)
		if strings.Contains(lower, "virtual") || strings.Contains(lower, "vmware") || strings.Contains(lower, "hyper-v") || strings.Contains(lower, "vethernet") || strings.Contains(lower, "loopback") {
			continue
		}
		addresses, err := current.Addrs()
		if err != nil {
			continue
		}
		for _, candidate := range addresses {
			ip, _, err := net.ParseCIDR(candidate.String())
			if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			v4 := ip.To4()
			private := v4[0] == 10 || (v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) || (v4[0] == 192 && v4[1] == 168)
			if !private && !ip.IsLinkLocalUnicast() {
				continue
			}
			score := 20
			if strings.Contains(lower, "wi-fi") || strings.Contains(lower, "wifi") || strings.Contains(lower, "wireless") {
				score += 30
			}
			if strings.Contains(lower, "ethernet") {
				score += 25
			}
			if private {
				score += 20
			}
			result = append(result, NetworkAddress{Name: current.Name, IPv4: ip.String(), Score: score})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		return result[i].IPv4 < result[j].IPv4
	})
	return result
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

func (s *Server) handleNetwork(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"url": s.localURL(), "addresses": s.addresses()})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	downloads := s.downloadDirectory()
	writable := directoryWritable(downloads)
	writeJSON(w, map[string]any{
		"status":                    "READY",
		"version":                   version,
		"checkedAt":                 time.Now().UTC(),
		"port":                      s.cfg.Port,
		"url":                       s.localURL(),
		"addresses":                 s.addresses(),
		"downloadDirectoryWritable": writable,
		"runtime":                   "go",
	})
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	target := s.localURL()
	if address := strings.TrimSpace(r.URL.Query().Get("address")); address != "" {
		valid := false
		for _, candidate := range s.addresses() {
			if candidate.IPv4 == address {
				valid = true
				break
			}
		}
		if !valid {
			writeProblem(w, r, problem(400, "NETWORK_ADDRESS_INVALID", "O endereço não pertence a uma interface de rede ativa."))
			return
		}
		target = fmt.Sprintf("http://%s:%d", address, s.cfg.Port)
	}
	png, err := qrcode.Encode(target, qrcode.Medium, 320)
	if err != nil {
		writeProblem(w, r, problem(500, "QR_GENERATION_FAILED", "Não foi possível gerar o QR Code."))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
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
