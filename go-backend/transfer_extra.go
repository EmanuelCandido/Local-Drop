package main

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// allFilesTicket marca o link temporário que entrega todos os arquivos em .zip.
const allFilesTicket = "*"

func hiddenFor(transfer *TransferRecord, deviceID string) bool {
	for _, id := range transfer.HiddenFor {
		if id == deviceID {
			return true
		}
	}
	return false
}

// handleDownloadAllLink cria um link temporário para baixar todos os arquivos
// prontos de uma vez, em um único .zip.
func (s *Server) handleDownloadAllLink(w http.ResponseWriter, r *http.Request) {
	actorID := principalOf(r).DeviceID
	transferID := r.PathValue("transferId")
	s.mu.RLock()
	transfer := s.state.Transfers[transferID]
	ready := 0
	if transfer != nil && transfer.ReceiverID == actorID {
		for _, file := range transfer.Files {
			if file.CompletedPath != "" {
				ready++
			}
		}
	}
	var created time.Time
	if transfer != nil {
		created = transfer.CreatedAt
	}
	s.mu.RUnlock()
	switch {
	case transfer == nil:
		writeProblem(w, r, problem(404, "TRANSFER_NOT_FOUND", "Este envio não existe mais. Ele pode ter sido cancelado no outro aparelho."))
		return
	case transfer.ReceiverID != actorID:
		writeProblem(w, r, problem(403, "TRANSFER_RECEIVER_REQUIRED", "Só quem recebe pode baixar estes arquivos."))
		return
	case ready == 0:
		writeProblem(w, r, problem(404, "TRANSFER_DOWNLOAD_EXPIRED", "Esses arquivos não estão mais disponíveis. Peça para enviar de novo."))
		return
	}
	token, _, err := randomToken(24)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	now := time.Now()
	s.ticketsMu.Lock()
	for key, ticket := range s.tickets {
		if now.After(ticket.expiresAt) {
			delete(s.tickets, key)
		}
	}
	s.tickets[token] = downloadTicket{transferID: transferID, fileID: allFilesTicket, deviceID: actorID, expiresAt: now.Add(downloadTicketLifetime)}
	s.ticketsMu.Unlock()
	name := "LocalDrop " + created.Local().Format("2006-01-02 15-04") + ".zip"
	writeJSON(w, map[string]any{"url": "/api/v1/downloads/" + token + "/" + url.PathEscape(name), "name": name, "expiresAt": now.Add(downloadTicketLifetime).UTC()})
}

// serveTransferZip monta o .zip enquanto envia, sem compressão: a CPU quase
// não trabalha e nada é gravado em disco.
func (s *Server) serveTransferZip(w http.ResponseWriter, r *http.Request, actorID, transferID string) {
	type entry struct{ path, name string }
	s.mu.RLock()
	transfer := s.state.Transfers[transferID]
	if transfer == nil || transfer.ReceiverID != actorID {
		s.mu.RUnlock()
		writeProblem(w, r, problem(404, "TRANSFER_DOWNLOAD_EXPIRED", "Esses arquivos não estão mais disponíveis. Peça para enviar de novo."))
		return
	}
	entries := make([]entry, 0, len(transfer.Files))
	used := make(map[string]bool, len(transfer.Files))
	for _, file := range transfer.Files {
		if file.CompletedPath == "" {
			continue
		}
		name := file.DeliveredName
		if name == "" {
			name = file.Name
		}
		entries = append(entries, entry{path: file.CompletedPath, name: uniqueZipName(name, used)})
	}
	created := transfer.CreatedAt
	s.mu.RUnlock()
	if len(entries) == 0 {
		writeProblem(w, r, problem(404, "TRANSFER_DOWNLOAD_EXPIRED", "Esses arquivos não estão mais disponíveis. Peça para enviar de novo."))
		return
	}
	name := "LocalDrop " + created.Local().Format("2006-01-02 15-04") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
	archive := zip.NewWriter(w)
	buffer := copyBuffers.Get().(*[]byte)
	defer copyBuffers.Put(buffer)
	for _, item := range entries {
		file, err := os.Open(item.path)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			continue
		}
		header := &zip.FileHeader{Name: item.name, Method: zip.Store, Modified: info.ModTime()}
		header.SetMode(0o644)
		target, err := archive.CreateHeader(header)
		if err == nil {
			_, err = io.CopyBuffer(target, file, *buffer)
		}
		_ = file.Close()
		if err != nil {
			// A conexão caiu no meio: não há como avisar o navegador depois que o envio começou.
			return
		}
	}
	_ = archive.Close()
}

func uniqueZipName(name string, used map[string]bool) string {
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	candidate := name
	extension := filepath.Ext(name)
	base := strings.TrimSuffix(name, extension)
	for index := 2; used[strings.ToLower(candidate)]; index++ {
		candidate = fmt.Sprintf("%s (%d)%s", base, index, extension)
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

func (s *Server) handleClearFinished(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]int{"cleared": s.clearFinished(principalOf(r).DeviceID)})
}

// clearFinished tira da lista do aparelho os envios já encerrados. Os
// arquivos salvos continuam onde estão; o outro aparelho não é afetado.
func (s *Server) clearFinished(deviceID string) int {
	s.mu.Lock()
	cleared := 0
	for _, transfer := range s.state.Transfers {
		if (transfer.SenderID != deviceID && transfer.ReceiverID != deviceID) || !isTerminalState(transfer.State) || hiddenFor(transfer, deviceID) {
			continue
		}
		transfer.HiddenFor = append(transfer.HiddenFor, deviceID)
		cleared++
	}
	if cleared > 0 {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	if cleared > 0 {
		s.hub.SendToDevice(deviceID, "TRANSFERS_CLEARED", map[string]int{"cleared": cleared})
	}
	return cleared
}

// storageUsage soma o que o LocalDrop guarda na própria pasta de dados. Os
// arquivos recebidos na pasta escolhida pela pessoa não entram na conta.
func (s *Server) storageUsage() int64 {
	var total int64
	_ = filepath.WalkDir(s.cfg.StorageRoot, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// cleanStorage apaga cópias temporárias que não são mais necessárias: textos
// já entregues, cópias guardadas para outros aparelhos e o log. Arquivos do
// computador oferecidos ao celular continuam disponíveis.
func (s *Server) cleanStorage() {
	managedRoot := filepath.Clean(filepath.Join(s.cfg.StorageRoot, "go-completed")) + string(os.PathSeparator)
	s.mu.Lock()
	changed := false
	for _, transfer := range s.state.Transfers {
		if transfer.State != "COMPLETED" {
			continue
		}
		for _, file := range transfer.Files {
			if file.CompletedPath != "" && strings.HasPrefix(filepath.Clean(file.CompletedPath), managedRoot) {
				_ = os.Remove(file.CompletedPath)
				file.CompletedPath = ""
				changed = true
			}
		}
	}
	if changed {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	_ = os.RemoveAll(filepath.Join(s.cfg.StorageRoot, "go-transfers"))
	s.cleanOutboxUnused()
	logPath := filepath.Join(s.cfg.StorageRoot, "localdrop-go.log")
	_ = os.Remove(logPath + ".old")
	_ = os.Truncate(logPath, 0)
	_ = os.Remove(s.statePath + ".bak")
	_ = os.Remove(s.statePath + ".tmp")
}

// cleanOutboxUnused remove textos enviados pelo computador que nenhum envio
// pendente ou ainda disponível usa.
func (s *Server) cleanOutboxUnused() {
	root := filepath.Clean(filepath.Join(s.cfg.StorageRoot, "outbox")) + string(os.PathSeparator)
	inUse := make(map[string]bool)
	s.mu.RLock()
	for _, transfer := range s.state.Transfers {
		pending := transfer.State == "WAITING_ACCEPTANCE"
		for _, file := range transfer.Files {
			if file.SourcePath == "" || !strings.HasPrefix(filepath.Clean(file.SourcePath), root) {
				continue
			}
			if pending || file.CompletedPath != "" {
				inUse[filepath.Dir(filepath.Clean(file.SourcePath))] = true
			}
		}
	}
	s.mu.RUnlock()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		directory := filepath.Join(root, entry.Name())
		if !inUse[directory] {
			_ = os.RemoveAll(directory)
		}
	}
}
