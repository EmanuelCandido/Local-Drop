package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// isSHA256Hex confere se o texto é um SHA-256 em hexadecimal (64 dígitos).
func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		c := value[index]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// Transferências criadas antes de o tamanho do bloco ser persistido usavam 4 MiB.
const legacyChunkSize int64 = 4 * 1024 * 1024

// Intervalo mínimo entre eventos de progresso de uma mesma transferência. Sem
// esse limite, cada bloco gerava um evento e uma sincronização completa nos clientes.
const progressEventInterval = 250 * time.Millisecond

var copyBuffers = sync.Pool{New: func() any {
	buffer := make([]byte, 256*1024)
	return &buffer
}}

var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

type requestedFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
	SHA256   string `json:"sha256"`
}

type createTransferRequest struct {
	ReceiverID string          `json:"receiverId"`
	Files      []requestedFile `json:"files"`
}

func validateFilename(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || raw == "." || raw == ".." || utf8.RuneCountInString(raw) > 255 {
		return "", problem(422, "TRANSFER_FILENAME_INVALID", "O nome de um arquivo tem caracteres que o Windows não aceita. Renomeie o arquivo e tente de novo.")
	}
	if strings.HasSuffix(raw, ".") || strings.HasSuffix(raw, " ") {
		return "", problem(422, "TRANSFER_FILENAME_INVALID", "O nome de um arquivo tem caracteres que o Windows não aceita. Renomeie o arquivo e tente de novo.")
	}
	for _, character := range raw {
		if unicode.IsControl(character) || strings.ContainsRune(`<>:"/\|?*`, character) {
			return "", problem(422, "TRANSFER_FILENAME_INVALID", "O nome de um arquivo tem caracteres que o Windows não aceita. Renomeie o arquivo e tente de novo.")
		}
	}
	base := raw
	if dot := strings.IndexRune(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	if windowsReservedNames[strings.ToUpper(base)] {
		return "", problem(422, "TRANSFER_FILENAME_INVALID", "O nome de um arquivo tem caracteres que o Windows não aceita. Renomeie o arquivo e tente de novo.")
	}
	return raw, nil
}

func transferChunkSize(transfer *TransferRecord) int64 {
	if transfer.ChunkSize > 0 {
		return transfer.ChunkSize
	}
	return legacyChunkSize
}

func chunkCount(size, chunkSize int64) int64 {
	if size == 0 {
		return 0
	}
	return ((size - 1) / chunkSize) + 1
}

func isLocalSource(transfer *TransferRecord) bool {
	if len(transfer.Files) == 0 {
		return false
	}
	for _, file := range transfer.Files {
		if file.SourcePath == "" {
			return false
		}
	}
	return true
}

func mimeTypeFor(name string) string {
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); value != "" {
		return value
	}
	return "application/octet-stream"
}

func (s *Server) handleTransferCreate(w http.ResponseWriter, r *http.Request) {
	principal := principalOf(r)
	if !s.enforceAttempt(principal.DeviceID, s.cfg.CreateRateLimit, s.cfg.CreateRateWindow, s.createAttempts) {
		writeProblem(w, r, problem(429, "TRANSFER_RATE_LIMITED", "Muitos envios de uma vez. Espere alguns segundos e tente de novo."))
		return
	}
	var input createTransferRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeProblem(w, r, err)
		return
	}
	if len(input.Files) < 1 || len(input.Files) > 1000 {
		writeProblem(w, r, problem(422, "TRANSFER_FILES_REQUIRED", "Escolha entre 1 e 1000 arquivos por vez."))
		return
	}
	files := make([]*TransferFile, 0, len(input.Files))
	var totalChunks int64
	for ordinal, requested := range input.Files {
		name, err := validateFilename(requested.Name)
		if err != nil {
			writeProblem(w, r, err)
			return
		}
		// O SHA-256 completo é opcional: cada bloco já chega com o próprio hash e é
		// gravado na posição exata, então o remetente não precisa ler o arquivo duas vezes.
		if requested.Size < 0 || strings.TrimSpace(requested.MimeType) == "" || len(requested.MimeType) > 255 || (requested.SHA256 != "" && !isSHA256Hex(requested.SHA256)) {
			writeProblem(w, r, problem(422, "TRANSFER_FILE_INVALID", "Os metadados de um arquivo são inválidos."))
			return
		}
		chunks := chunkCount(requested.Size, s.cfg.ChunkSize)
		if totalChunks > (1<<63-1)-chunks {
			writeProblem(w, r, problem(422, "TRANSFER_SIZE_OVERFLOW", "Os metadados da transferência são grandes demais."))
			return
		}
		totalChunks += chunks
		if totalChunks > s.cfg.MaxTotalChunks {
			writeProblem(w, r, problem(422, "TRANSFER_TOO_MANY_CHUNKS", "A transferência possui blocos demais."))
			return
		}
		files = append(files, &TransferFile{
			Ordinal: ordinal, Name: name, Size: requested.Size,
			MimeType: requested.MimeType, SHA256: strings.ToLower(requested.SHA256),
			TotalChunks: chunks, State: "PENDING", Receipts: make(map[int64]*ChunkReceipt),
		})
	}
	view, autoAccept, err := s.createTransfer(principal.DeviceID, strings.TrimSpace(input.ReceiverID), files)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if autoAccept {
		if accepted, err := s.transferAction(view.ReceiverID, view.ID, "accept"); err == nil {
			view = accepted
		}
	}
	writeJSONStatus(w, http.StatusCreated, view)
}

// createTransfer registra uma oferta já validada e devolve se o destinatário
// (sempre o host) aceita automaticamente.
func (s *Server) createTransfer(senderID, receiverID string, files []*TransferFile) (TransferView, bool, error) {
	if receiverID == "" {
		return TransferView{}, false, problem(422, "TRANSFER_RECEIVER_REQUIRED", "Escolha para qual aparelho enviar.")
	}
	if receiverID == senderID {
		return TransferView{}, false, problem(422, "TRANSFER_SAME_DEVICE", "Remetente e destinatário devem ser diferentes.")
	}
	var totalBytes int64
	for _, file := range files {
		if totalBytes > (1<<63-1)-file.Size {
			return TransferView{}, false, problem(422, "TRANSFER_SIZE_OVERFLOW", "Os metadados da transferência são grandes demais.")
		}
		totalBytes += file.Size
		fileID, err := randomID()
		if err != nil {
			return TransferView{}, false, err
		}
		file.ID = fileID
	}
	transferID, err := randomID()
	if err != nil {
		return TransferView{}, false, err
	}
	now := time.Now().UTC()
	s.mu.Lock()
	sender := s.state.Devices[senderID]
	receiver := s.state.Devices[receiverID]
	if sender == nil || sender.Revoked {
		s.mu.Unlock()
		return TransferView{}, false, problem(403, "TRANSFER_SENDER_NOT_TRUSTED", "Este aparelho não está mais conectado ao computador. Escaneie o QR Code de novo.")
	}
	if receiver == nil || receiver.Revoked {
		s.mu.Unlock()
		return TransferView{}, false, problem(422, "TRANSFER_RECEIVER_NOT_TRUSTED", "Esse aparelho não está mais conectado. Conecte de novo pelo QR Code.")
	}
	transfer := &TransferRecord{
		ID: transferID, SenderID: sender.DeviceID, SenderName: sender.DisplayName,
		ReceiverID: receiver.DeviceID, ReceiverName: receiver.DisplayName,
		State: "WAITING_ACCEPTANCE", TotalBytes: totalBytes, ChunkSize: s.cfg.ChunkSize,
		CreatedAt: now, UpdatedAt: now, Files: files,
	}
	s.state.Transfers[transferID] = transfer
	view := s.transferViewLocked(transfer)
	autoAccept := receiver.Host && s.state.Settings.AutoAcceptTrusted
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		return TransferView{}, false, saveErr
	}
	s.emitTransfer(view, "TRANSFER_OFFERED", map[string]any{"transfer": view})
	return view, autoAccept, nil
}

// offerLocalFiles oferece arquivos do próprio computador host. Como o servidor
// roda na mesma máquina, nada é copiado: depois do aceite, o destinatário baixa
// direto do arquivo original.
func (s *Server) offerLocalFiles(senderID, receiverID string, paths []string) (TransferView, error) {
	if len(paths) < 1 || len(paths) > 1000 {
		return TransferView{}, problem(422, "TRANSFER_FILES_REQUIRED", "Escolha entre 1 e 1000 arquivos por vez.")
	}
	files := make([]*TransferFile, 0, len(paths))
	for ordinal, raw := range paths {
		path, err := filepath.Abs(raw)
		if err != nil {
			return TransferView{}, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return TransferView{}, fmt.Errorf("não foi possível ler %s: %w", filepath.Base(path), err)
		}
		if !info.Mode().IsRegular() {
			return TransferView{}, problem(422, "TRANSFER_FILE_INVALID", fmt.Sprintf("%s não é um arquivo comum.", info.Name()))
		}
		name, err := validateFilename(info.Name())
		if err != nil {
			return TransferView{}, err
		}
		files = append(files, &TransferFile{
			Ordinal: ordinal, Name: name, Size: info.Size(), MimeType: mimeTypeFor(name),
			TotalChunks: chunkCount(info.Size(), s.cfg.ChunkSize), State: "PENDING",
			Receipts: make(map[int64]*ChunkReceipt), SourcePath: path,
		})
	}
	view, _, err := s.createTransfer(senderID, receiverID, files)
	return view, err
}

func (s *Server) transferViewLocked(transfer *TransferRecord) TransferView {
	files := make([]TransferFileView, 0, len(transfer.Files))
	sorted := append([]*TransferFile(nil), transfer.Files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ordinal < sorted[j].Ordinal })
	receiverIsHost := false
	if receiver := s.state.Devices[transfer.ReceiverID]; receiver != nil {
		receiverIsHost = receiver.Host
	}
	for _, file := range sorted {
		var delivered, saved *string
		if file.DeliveredName != "" {
			name := file.DeliveredName
			delivered = &name
		}
		if receiverIsHost && file.CompletedPath != "" {
			path := file.CompletedPath
			saved = &path
		}
		files = append(files, TransferFileView{
			ID: file.ID, Ordinal: file.Ordinal, Name: file.Name, Size: file.Size,
			MimeType: file.MimeType, SHA256: file.SHA256, TotalChunks: file.TotalChunks,
			ReceivedChunks: file.ReceivedChunks, ReceivedBytes: file.ReceivedBytes,
			// Sem os.Stat aqui: a listagem é montada a cada evento. Se o arquivo
			// sumir, o download responde TRANSFER_DOWNLOAD_EXPIRED.
			State: file.State, DownloadAvailable: file.CompletedPath != "",
			DeliveredName: delivered, SavedPath: saved,
		})
	}
	return TransferView{
		ID: transfer.ID, SenderID: transfer.SenderID, ReceiverID: transfer.ReceiverID,
		State: transfer.State, TotalBytes: transfer.TotalBytes, ReceivedBytes: transfer.ReceivedBytes,
		ChunkSize: transferChunkSize(transfer), CreatedAt: transfer.CreatedAt, UpdatedAt: transfer.UpdatedAt,
		AcceptedAt: transfer.AcceptedAt, CompletedAt: transfer.CompletedAt,
		FailureCode: transfer.FailureCode, FailureMessage: transfer.FailureMessage, Files: files,
	}
}

func (s *Server) transferListLocked(deviceID string, limit int) []TransferView {
	transfers := make([]*TransferRecord, 0)
	for _, transfer := range s.state.Transfers {
		if (transfer.SenderID == deviceID || transfer.ReceiverID == deviceID) && !hiddenFor(transfer, deviceID) {
			transfers = append(transfers, transfer)
		}
	}
	sort.Slice(transfers, func(i, j int) bool { return transfers[i].CreatedAt.After(transfers[j].CreatedAt) })
	if len(transfers) > limit {
		transfers = transfers[:limit]
	}
	views := make([]TransferView, 0, len(transfers))
	for _, transfer := range transfers {
		views = append(views, s.transferViewLocked(transfer))
	}
	return views
}

func (s *Server) handleTransferList(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	views := s.transferListLocked(principalOf(r).DeviceID, 100)
	s.mu.RUnlock()
	writeJSON(w, views)
}

func (s *Server) handleTransferView(w http.ResponseWriter, r *http.Request) {
	deviceID := principalOf(r).DeviceID
	s.mu.RLock()
	transfer := s.state.Transfers[r.PathValue("transferId")]
	if transfer == nil {
		s.mu.RUnlock()
		writeProblem(w, r, problem(404, "TRANSFER_NOT_FOUND", "Este envio não existe mais. Ele pode ter sido cancelado no outro aparelho."))
		return
	}
	if transfer.SenderID != deviceID && transfer.ReceiverID != deviceID {
		s.mu.RUnlock()
		writeProblem(w, r, problem(403, "TRANSFER_PARTICIPANT_REQUIRED", "Somente participantes podem acessar a transferência."))
		return
	}
	view := s.transferViewLocked(transfer)
	s.mu.RUnlock()
	writeJSON(w, view)
}

func (s *Server) handleTransferAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "accept", "reject", "cancel", "pause", "resume":
	default:
		http.NotFound(w, r)
		return
	}
	view, err := s.transferAction(principalOf(r).DeviceID, r.PathValue("transferId"), action)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, view)
}

func (s *Server) transferAction(actorID, transferID, action string) (TransferView, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	transfer := s.state.Transfers[transferID]
	if transfer == nil {
		s.mu.Unlock()
		return TransferView{}, problem(404, "TRANSFER_NOT_FOUND", "Este envio não existe mais. Ele pode ter sido cancelado no outro aparelho.")
	}
	participant := transfer.SenderID == actorID || transfer.ReceiverID == actorID
	if !participant {
		s.mu.Unlock()
		return TransferView{}, problem(403, "TRANSFER_PARTICIPANT_REQUIRED", "Somente participantes podem alterar a transferência.")
	}
	shouldAssemble := false
	event := ""
	var discard []string
	switch action {
	case "accept":
		if transfer.ReceiverID != actorID {
			s.mu.Unlock()
			return TransferView{}, problem(403, "TRANSFER_RECEIVER_REQUIRED", "Somente o destinatário pode aceitar.")
		}
		if transfer.State != "WAITING_ACCEPTANCE" {
			s.mu.Unlock()
			return TransferView{}, problem(409, "TRANSFER_INVALID_STATE", "Esse envio já foi respondido.")
		}
		transfer.AcceptedAt = &now
		if isLocalSource(transfer) {
			// Os bytes já estão no disco do host: a oferta fica pronta para download na hora.
			completeLocalSourceLocked(transfer, now)
			event = "TRANSFER_COMPLETED"
			break
		}
		if err := s.ensureTransferSpaceLocked(transfer); err != nil {
			transfer.AcceptedAt = nil
			s.mu.Unlock()
			return TransferView{}, err
		}
		transfer.State = "ACCEPTED"
		if allFilesReceived(transfer) {
			markVerifying(transfer)
			shouldAssemble = true
		}
		event = "TRANSFER_ACCEPTED"
	case "reject":
		if transfer.ReceiverID != actorID {
			s.mu.Unlock()
			return TransferView{}, problem(403, "TRANSFER_RECEIVER_REQUIRED", "Somente o destinatário pode recusar.")
		}
		if transfer.State != "WAITING_ACCEPTANCE" {
			s.mu.Unlock()
			return TransferView{}, problem(409, "TRANSFER_INVALID_STATE", "Esse envio já foi respondido.")
		}
		transfer.State = "REJECTED"
		event, discard = "TRANSFER_REJECTED", partPathsLocked(transfer)
	case "cancel":
		if isTerminalState(transfer.State) {
			s.mu.Unlock()
			return TransferView{}, problem(409, "TRANSFER_INVALID_STATE", "Esse envio já terminou.")
		}
		transfer.State = "CANCELLED"
		event, discard = "TRANSFER_CANCELLED", partPathsLocked(transfer)
	case "pause":
		if transfer.State == "PAUSED" {
			view := s.transferViewLocked(transfer)
			s.mu.Unlock()
			return view, nil
		}
		if transfer.State != "ACCEPTED" && transfer.State != "TRANSFERRING" {
			s.mu.Unlock()
			return TransferView{}, problem(409, "TRANSFER_INVALID_STATE", "Esse envio não pode ser pausado agora.")
		}
		transfer.State = "PAUSED"
		event = "TRANSFER_PAUSED"
	case "resume":
		if transfer.SenderID != actorID {
			s.mu.Unlock()
			return TransferView{}, problem(403, "TRANSFER_SENDER_REQUIRED", "Somente o remetente pode retomar.")
		}
		if transfer.State != "PAUSED" {
			s.mu.Unlock()
			return TransferView{}, problem(409, "TRANSFER_INVALID_STATE", "Esse envio não está pausado.")
		}
		if allFilesReceived(transfer) {
			markVerifying(transfer)
			shouldAssemble = true
		} else {
			transfer.State = "TRANSFERRING"
		}
		event = "TRANSFER_RESUMED"
	}
	transfer.UpdatedAt = now
	view := s.transferViewLocked(transfer)
	saveErr := s.saveLocked()
	s.mu.Unlock()
	if saveErr != nil {
		return TransferView{}, saveErr
	}
	if discard != nil {
		s.discardTransferData(transferID, discard, false)
	}
	if event != "" {
		s.emitTransfer(view, event, map[string]any{"transfer": view})
	}
	if shouldAssemble {
		s.scheduleAssembly(transferID)
	}
	return view, nil
}

func completeLocalSourceLocked(transfer *TransferRecord, now time.Time) {
	for _, file := range transfer.Files {
		file.CompletedPath = file.SourcePath
		file.DeliveredName = file.Name
		file.ReceivedBytes = file.Size
		file.ReceivedChunks = file.TotalChunks
		file.State = "COMPLETED"
	}
	transfer.ReceivedBytes = transfer.TotalBytes
	transfer.State = "COMPLETED"
	transfer.CompletedAt = &now
}

func allFilesReceived(transfer *TransferRecord) bool {
	for _, file := range transfer.Files {
		if file.ReceivedChunks != file.TotalChunks || file.ReceivedBytes != file.Size {
			return false
		}
	}
	return true
}

func markVerifying(transfer *TransferRecord) {
	transfer.State = "VERIFYING"
	for _, file := range transfer.Files {
		file.State = "VERIFYING"
	}
}

func isTerminalState(state string) bool {
	return state == "COMPLETED" || state == "REJECTED" || state == "CANCELLED" || state == "FAILED"
}

func findTransferFile(transfer *TransferRecord, fileID string) *TransferFile {
	for _, file := range transfer.Files {
		if file.ID == fileID {
			return file
		}
	}
	return nil
}

func partPathsLocked(transfer *TransferRecord) []string {
	paths := make([]string, 0, len(transfer.Files))
	for _, file := range transfer.Files {
		if file.PartPath != "" {
			paths = append(paths, file.PartPath)
		}
	}
	return paths
}

func (s *Server) handleMissingChunks(w http.ResponseWriter, r *http.Request) {
	actorID := principalOf(r).DeviceID
	cursor, err := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if r.URL.Query().Get("cursor") == "" {
		cursor = 0
		err = nil
	}
	limit, limitErr := strconv.Atoi(r.URL.Query().Get("limit"))
	if r.URL.Query().Get("limit") == "" {
		limit = 1000
		limitErr = nil
	}
	if err != nil || limitErr != nil || limit < 1 || limit > 1000 {
		writeProblem(w, r, problem(422, "CHUNK_RANGE_LIMIT_INVALID", "O cursor ou limite de blocos é inválido."))
		return
	}
	s.mu.RLock()
	transfer := s.state.Transfers[r.PathValue("transferId")]
	if transfer == nil {
		s.mu.RUnlock()
		writeProblem(w, r, problem(404, "TRANSFER_NOT_FOUND", "Este envio não existe mais. Ele pode ter sido cancelado no outro aparelho."))
		return
	}
	if transfer.SenderID != actorID {
		s.mu.RUnlock()
		writeProblem(w, r, problem(403, "TRANSFER_SENDER_REQUIRED", "Somente o remetente pode consultar blocos."))
		return
	}
	file := findTransferFile(transfer, r.PathValue("fileId"))
	if file == nil {
		s.mu.RUnlock()
		writeProblem(w, r, problem(404, "TRANSFER_FILE_NOT_FOUND", "O arquivo da transferência não foi encontrado."))
		return
	}
	if cursor < 0 || cursor > file.TotalChunks {
		s.mu.RUnlock()
		writeProblem(w, r, problem(422, "CHUNK_CURSOR_INVALID", "O cursor está fora dos limites do arquivo."))
		return
	}
	if transfer.State == "COMPLETED" {
		total := file.TotalChunks
		s.mu.RUnlock()
		writeJSON(w, map[string]any{"totalChunks": total, "receivedChunks": total, "ranges": []any{}, "nextCursor": nil})
		return
	}
	if transfer.State != "ACCEPTED" && transfer.State != "TRANSFERRING" && transfer.State != "PAUSED" && transfer.State != "VERIFYING" {
		s.mu.RUnlock()
		writeProblem(w, r, problem(409, "TRANSFER_INVALID_STATE", "A transferência não pode ser retomada neste estado."))
		return
	}
	type chunkRange struct {
		StartInclusive int64 `json:"startInclusive"`
		EndExclusive   int64 `json:"endExclusive"`
	}
	ranges := make([]chunkRange, 0)
	index := cursor
	var next *int64
	for index < file.TotalChunks {
		if _, received := file.Receipts[index]; received {
			index++
			continue
		}
		start := index
		for index < file.TotalChunks {
			if _, received := file.Receipts[index]; received {
				break
			}
			index++
		}
		ranges = append(ranges, chunkRange{StartInclusive: start, EndExclusive: index})
		if len(ranges) == limit && index < file.TotalChunks {
			value := index
			next = &value
			break
		}
	}
	result := map[string]any{"totalChunks": file.TotalChunks, "receivedChunks": file.ReceivedChunks, "ranges": ranges, "nextCursor": next}
	s.mu.RUnlock()
	writeJSON(w, result)
}

// keyedLocks serializa operações com a mesma chave e libera a entrada quando
// ninguém mais a usa, evitando que o mapa cresça a cada bloco recebido.
type keyedLocks struct {
	mu      sync.Mutex
	entries map[string]*keyedLock
}

type keyedLock struct {
	sync.Mutex
	refs int
}

func (k *keyedLocks) lock(key string) func() {
	k.mu.Lock()
	if k.entries == nil {
		k.entries = make(map[string]*keyedLock)
	}
	entry := k.entries[key]
	if entry == nil {
		entry = &keyedLock{}
		k.entries[key] = entry
	}
	entry.refs++
	k.mu.Unlock()
	entry.Lock()
	return func() {
		entry.Unlock()
		k.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
	}
}

func expectedChunkSize(file *TransferFile, chunkSize, index int64) (int64, error) {
	if index < 0 || index >= file.TotalChunks {
		return 0, problem(422, "CHUNK_INDEX_INVALID", "O índice do bloco está fora dos limites do arquivo.")
	}
	remaining := file.Size - index*chunkSize
	if remaining > chunkSize {
		return chunkSize, nil
	}
	return remaining, nil
}

// ensurePartPath escolhe onde o arquivo será montado. Para o host, o arquivo
// parcial fica na própria pasta de recebimento, então a conclusão é só uma
// renomeação — sem cópia extra de gigabytes.
func (s *Server) ensurePartPath(transferID, fileID string) (string, error) {
	s.mu.Lock()
	transfer := s.state.Transfers[transferID]
	if transfer == nil {
		s.mu.Unlock()
		return "", problem(404, "TRANSFER_NOT_FOUND", "Este envio não existe mais. Ele pode ter sido cancelado no outro aparelho.")
	}
	file := findTransferFile(transfer, fileID)
	if file == nil {
		s.mu.Unlock()
		return "", problem(404, "TRANSFER_FILE_NOT_FOUND", "O arquivo da transferência não foi encontrado.")
	}
	if file.PartPath == "" {
		directory := filepath.Join(s.cfg.StorageRoot, "go-completed", transferID)
		name := file.ID + ".part"
		if receiver := s.state.Devices[transfer.ReceiverID]; receiver != nil && receiver.Host {
			directory = s.state.Settings.DownloadDirectory
			shortID := file.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			name = shortID + ".localdrop"
			if utf8.RuneCountInString(file.Name) <= 200 {
				name = file.Name + "." + name
			}
		}
		file.PartPath = filepath.Join(directory, name)
		s.saveSoonLocked()
	}
	path := file.PartPath
	s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", problem(500, "TRANSFER_STORAGE_FAILED", "Não foi possível preparar o armazenamento do bloco.")
	}
	return path, nil
}

func (s *Server) handleChunkUpload(w http.ResponseWriter, r *http.Request) {
	actorID := principalOf(r).DeviceID
	transferID, fileID := r.PathValue("transferId"), r.PathValue("fileId")
	index, err := strconv.ParseInt(r.PathValue("chunkIndex"), 10, 64)
	if err != nil {
		writeProblem(w, r, problem(422, "CHUNK_INDEX_INVALID", "O índice do bloco é inválido."))
		return
	}
	declaredHash := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Chunk-SHA256")))
	if !isSHA256Hex(declaredHash) {
		writeProblem(w, r, problem(422, "CHUNK_HASH_INVALID", "O SHA-256 do bloco é inválido."))
		return
	}
	unlock := s.chunkLocks.lock(transferID + ":" + fileID + ":" + strconv.FormatInt(index, 10))
	defer unlock()

	s.mu.RLock()
	transfer := s.state.Transfers[transferID]
	if transfer == nil {
		s.mu.RUnlock()
		writeProblem(w, r, problem(404, "TRANSFER_NOT_FOUND", "Este envio não existe mais. Ele pode ter sido cancelado no outro aparelho."))
		return
	}
	if transfer.SenderID != actorID {
		s.mu.RUnlock()
		writeProblem(w, r, problem(403, "TRANSFER_SENDER_REQUIRED", "Somente o remetente pode enviar blocos."))
		return
	}
	if transfer.State != "ACCEPTED" && transfer.State != "TRANSFERRING" {
		s.mu.RUnlock()
		writeProblem(w, r, problem(409, "TRANSFER_INVALID_STATE", "O envio está pausado ou terminou. Toque em Continuar se ele estiver pausado."))
		return
	}
	file := findTransferFile(transfer, fileID)
	if file == nil {
		s.mu.RUnlock()
		writeProblem(w, r, problem(404, "TRANSFER_FILE_NOT_FOUND", "O arquivo da transferência não foi encontrado."))
		return
	}
	chunkSize := transferChunkSize(transfer)
	expectedSize, expectedErr := expectedChunkSize(file, chunkSize, index)
	if expectedErr != nil {
		s.mu.RUnlock()
		writeProblem(w, r, expectedErr)
		return
	}
	if receipt := file.Receipts[index]; receipt != nil {
		if receipt.Size == expectedSize && strings.EqualFold(receipt.SHA256, declaredHash) {
			result := map[string]any{"duplicate": true, "receivedBytes": transfer.ReceivedBytes, "totalBytes": transfer.TotalBytes, "state": transfer.State}
			s.mu.RUnlock()
			writeJSON(w, result)
			return
		}
		s.mu.RUnlock()
		writeProblem(w, r, problem(409, "CHUNK_RECEIPT_CONFLICT", "Já existe um bloco diferente neste índice."))
		return
	}
	s.mu.RUnlock()
	if r.ContentLength >= 0 && r.ContentLength != expectedSize {
		writeProblem(w, r, problem(422, "CHUNK_SIZE_INVALID", "O tamanho do bloco não corresponde ao esperado."))
		return
	}

	partPath, err := s.ensurePartPath(transferID, fileID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	part, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		writeProblem(w, r, problem(500, "TRANSFER_STORAGE_FAILED", "Não foi possível abrir o arquivo em montagem."))
		return
	}
	// O bloco vai direto para a posição final. Se o hash não conferir, nenhum
	// recibo é registrado e o trecho será sobrescrito na próxima tentativa.
	hasher := sha256.New()
	buffer := copyBuffers.Get().(*[]byte)
	written, copyErr := io.CopyBuffer(io.MultiWriter(io.NewOffsetWriter(part, index*chunkSize), hasher), io.LimitReader(r.Body, expectedSize+1), *buffer)
	copyBuffers.Put(buffer)
	if closeErr := part.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		writeProblem(w, r, problem(500, "TRANSFER_STORAGE_FAILED", "Não foi possível gravar o bloco recebido."))
		return
	}
	if written != expectedSize {
		writeProblem(w, r, problem(422, "CHUNK_SIZE_INVALID", "O tamanho do bloco não corresponde ao esperado."))
		return
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualHash, declaredHash) {
		writeProblem(w, r, problem(422, "CHUNK_HASH_MISMATCH", "O bloco recebido falhou na verificação SHA-256."))
		return
	}

	now := time.Now().UTC()
	s.mu.Lock()
	transfer = s.state.Transfers[transferID]
	file = findTransferFile(transfer, fileID)
	if transfer.State != "ACCEPTED" && transfer.State != "TRANSFERRING" {
		s.mu.Unlock()
		writeProblem(w, r, problem(409, "TRANSFER_INVALID_STATE", "O envio foi pausado ou cancelado no outro aparelho."))
		return
	}
	if file.Receipts[index] != nil {
		result := map[string]any{"duplicate": true, "receivedBytes": transfer.ReceivedBytes, "totalBytes": transfer.TotalBytes, "state": transfer.State}
		s.mu.Unlock()
		writeJSON(w, result)
		return
	}
	file.Receipts[index] = &ChunkReceipt{Index: index, Size: written, SHA256: actualHash, ReceivedAt: now}
	file.ReceivedChunks++
	file.ReceivedBytes += written
	file.State = "TRANSFERRING"
	transfer.ReceivedBytes += written
	transfer.State = "TRANSFERRING"
	transfer.UpdatedAt = now
	shouldAssemble := allFilesReceived(transfer)
	var saveErr error
	if shouldAssemble {
		markVerifying(transfer)
		saveErr = s.saveLocked()
	} else {
		// Recibos intermediários são agrupados em uma gravação por segundo. Se o
		// processo cair antes, os blocos sem recibo são apenas reenviados.
		s.saveSoonLocked()
	}
	emit := shouldAssemble || s.progressDue(transferID)
	var view TransferView
	if emit {
		view = s.transferViewLocked(transfer)
	}
	result := map[string]any{"duplicate": false, "receivedBytes": transfer.ReceivedBytes, "totalBytes": transfer.TotalBytes, "state": transfer.State}
	s.mu.Unlock()
	if saveErr != nil {
		writeProblem(w, r, saveErr)
		return
	}
	if emit {
		s.emitTransfer(view, "TRANSFER_PROGRESS", map[string]any{"transfer": view, "fileId": fileID, "chunkIndex": index})
	}
	if shouldAssemble {
		s.scheduleAssembly(transferID)
	}
	writeJSON(w, result)
}

func (s *Server) progressDue(transferID string) bool {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	now := time.Now()
	if now.Sub(s.lastProgress[transferID]) < progressEventInterval {
		return false
	}
	s.lastProgress[transferID] = now
	return true
}

func (s *Server) transferRoot(transferID string) string {
	return filepath.Join(s.cfg.StorageRoot, "go-transfers", transferID)
}

// discardTransferData remove os dados parciais de uma transferência encerrada.
func (s *Server) discardTransferData(transferID string, parts []string, keepCompleted bool) {
	// transferID só chega aqui depois de ser encontrado no mapa interno e é sempre um UUID gerado.
	_ = os.RemoveAll(s.transferRoot(transferID))
	for _, part := range parts {
		_ = os.Remove(part)
	}
	if !keepCompleted {
		_ = os.RemoveAll(filepath.Join(s.cfg.StorageRoot, "go-completed", transferID))
	}
	s.progressMu.Lock()
	delete(s.lastProgress, transferID)
	s.progressMu.Unlock()
}

func (s *Server) scheduleAssembly(transferID string) {
	s.assemblyMu.Lock()
	if s.assembling[transferID] {
		s.assemblyMu.Unlock()
		return
	}
	s.assembling[transferID] = true
	s.assemblyMu.Unlock()
	go func() {
		defer func() {
			s.assemblyMu.Lock()
			delete(s.assembling, transferID)
			s.assemblyMu.Unlock()
		}()
		s.assembleTransfer(transferID)
	}()
}

type assemblyItem struct {
	fileID, name, sha256, partPath, finalPath string
	size                                      int64
}

func verifyPartFile(path string, size int64, expected string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != size {
		if info.Size() > size {
			return fmt.Errorf("tamanho divergente")
		}
		// Só acontece com arquivos vazios, que não têm blocos.
		if err := file.Truncate(size); err != nil {
			return err
		}
	}
	if expected == "" {
		return nil
	}
	hasher := sha256.New()
	buffer := copyBuffers.Get().(*[]byte)
	_, err = io.CopyBuffer(hasher, file, *buffer)
	copyBuffers.Put(buffer)
	if err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected) {
		return fmt.Errorf("hash divergente")
	}
	return nil
}

func (s *Server) assembleTransfer(transferID string) {
	s.mu.RLock()
	current := s.state.Transfers[transferID]
	if current == nil || current.State != "VERIFYING" {
		s.mu.RUnlock()
		return
	}
	receiver := s.state.Devices[current.ReceiverID]
	hostReceiver := receiver != nil && receiver.Host
	downloadDirectory := s.state.Settings.DownloadDirectory
	items := make([]assemblyItem, 0, len(current.Files))
	for _, file := range current.Files {
		items = append(items, assemblyItem{fileID: file.ID, name: file.Name, sha256: file.SHA256, partPath: file.PartPath, size: file.Size})
	}
	s.mu.RUnlock()

	for index := range items {
		item := &items[index]
		if item.partPath == "" {
			path, err := s.ensurePartPath(transferID, item.fileID)
			if err != nil {
				s.failTransfer(transferID, "TRANSFER_ASSEMBLY_FAILED", "Não deu para usar a pasta de recebimento. Confira a pasta em Ajustes.")
				return
			}
			item.partPath = path
		}
		if err := verifyPartFile(item.partPath, item.size, item.sha256); err != nil {
			s.failTransfer(transferID, "TRANSFER_HASH_MISMATCH", "O arquivo chegou corrompido (a conferência falhou). Envie de novo.")
			return
		}
	}
	for index := range items {
		item := &items[index]
		var err error
		if hostReceiver {
			if err = os.MkdirAll(downloadDirectory, 0o755); err == nil {
				item.finalPath, err = publishUnique(downloadDirectory, item.name, item.partPath)
			}
		} else {
			item.finalPath = filepath.Join(s.cfg.StorageRoot, "go-completed", transferID, item.fileID+"-"+item.name)
			err = moveNoReplace(item.partPath, item.finalPath)
		}
		if err != nil {
			s.failTransfer(transferID, "TRANSFER_ASSEMBLY_FAILED", "Não deu para salvar o arquivo na pasta de recebimento. Confira o espaço livre e a pasta em Ajustes.")
			return
		}
	}

	now := time.Now().UTC()
	s.mu.Lock()
	current = s.state.Transfers[transferID]
	if current == nil || current.State != "VERIFYING" {
		s.mu.Unlock()
		return
	}
	for _, item := range items {
		file := findTransferFile(current, item.fileID)
		file.CompletedPath = item.finalPath
		file.DeliveredName = filepath.Base(item.finalPath)
		if !hostReceiver {
			file.DeliveredName = item.name
		}
		file.PartPath = ""
		file.State = "COMPLETED"
		file.Receipts = make(map[int64]*ChunkReceipt)
	}
	current.State = "COMPLETED"
	current.UpdatedAt = now
	current.CompletedAt = &now
	view := s.transferViewLocked(current)
	_ = s.saveLocked()
	s.mu.Unlock()
	s.discardTransferData(transferID, nil, true)
	s.emitTransfer(view, "TRANSFER_COMPLETED", map[string]any{"transfer": view})
}

func publishUnique(directory, name, source string) (string, error) {
	extension := filepath.Ext(name)
	base := strings.TrimSuffix(name, extension)
	for index := 0; index < 10_000; index++ {
		candidateName := name
		if index > 0 {
			candidateName = fmt.Sprintf("%s (%d)%s", base, index, extension)
		}
		candidate := filepath.Join(directory, candidateName)
		if _, err := os.Lstat(candidate); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if err := moveNoReplace(source, candidate); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		return candidate, nil
	}
	return "", fmt.Errorf("não foi possível encontrar um nome disponível")
}

// copyNoReplace é o caminho lento usado quando origem e destino estão em
// volumes diferentes e a renomeação não é possível.
func copyNoReplace(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = input.Close()
		return err
	}
	buffer := copyBuffers.Get().(*[]byte)
	_, copyErr := io.CopyBuffer(output, input, *buffer)
	copyBuffers.Put(buffer)
	if syncErr := output.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if closeErr := input.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	return os.Remove(source)
}

func (s *Server) failTransfer(transferID, code, message string) {
	now := time.Now().UTC()
	s.mu.Lock()
	transfer := s.state.Transfers[transferID]
	if transfer == nil || isTerminalState(transfer.State) {
		s.mu.Unlock()
		return
	}
	transfer.State = "FAILED"
	transfer.UpdatedAt = now
	transfer.FailureCode = &code
	transfer.FailureMessage = &message
	parts := partPathsLocked(transfer)
	for _, file := range transfer.Files {
		file.State = "FAILED"
		file.PartPath = ""
		file.Receipts = make(map[int64]*ChunkReceipt)
	}
	view := s.transferViewLocked(transfer)
	_ = s.saveLocked()
	s.mu.Unlock()
	s.discardTransferData(transferID, parts, false)
	s.emitTransfer(view, "TRANSFER_FAILED", map[string]any{"transfer": view})
}

type downloadTicket struct {
	transferID, fileID, deviceID string
	expiresAt                    time.Time
}

const downloadTicketLifetime = 15 * time.Minute

// handleDownloadLink troca a autenticação por cabeçalho por um link temporário.
// Assim o navegador do celular baixa com o gerenciador nativo, em streaming,
// em vez de carregar o arquivo inteiro na memória da página.
func (s *Server) handleDownloadLink(w http.ResponseWriter, r *http.Request) {
	actorID := principalOf(r).DeviceID
	transferID, fileID := r.PathValue("transferId"), r.PathValue("fileId")
	s.mu.RLock()
	file, err := s.downloadableFileLocked(actorID, transferID, fileID)
	name := ""
	if file != nil {
		name = file.DeliveredName
		if name == "" {
			name = file.Name
		}
	}
	s.mu.RUnlock()
	if err != nil {
		writeProblem(w, r, err)
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
	s.tickets[token] = downloadTicket{transferID: transferID, fileID: fileID, deviceID: actorID, expiresAt: now.Add(downloadTicketLifetime)}
	s.ticketsMu.Unlock()
	writeJSON(w, map[string]any{"url": "/api/v1/downloads/" + token + "/" + url.PathEscape(name), "expiresAt": now.Add(downloadTicketLifetime).UTC()})
}

func (s *Server) handleTicketDownload(w http.ResponseWriter, r *http.Request) {
	s.ticketsMu.Lock()
	ticket, ok := s.tickets[r.PathValue("ticket")]
	s.ticketsMu.Unlock()
	if !ok || time.Now().After(ticket.expiresAt) {
		writeProblem(w, r, problem(404, "TRANSFER_DOWNLOAD_EXPIRED", "O link de download venceu. Toque em Baixar de novo."))
		return
	}
	if ticket.fileID == allFilesTicket {
		s.serveTransferZip(w, r, ticket.deviceID, ticket.transferID)
		return
	}
	s.serveTransferFile(w, r, ticket.deviceID, ticket.transferID, ticket.fileID)
}

func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	s.serveTransferFile(w, r, principalOf(r).DeviceID, r.PathValue("transferId"), r.PathValue("fileId"))
}

func (s *Server) downloadableFileLocked(actorID, transferID, fileID string) (*TransferFile, error) {
	transfer := s.state.Transfers[transferID]
	if transfer == nil {
		return nil, problem(404, "TRANSFER_NOT_FOUND", "Este envio não existe mais. Ele pode ter sido cancelado no outro aparelho.")
	}
	if transfer.ReceiverID != actorID {
		return nil, problem(403, "TRANSFER_RECEIVER_REQUIRED", "Somente o destinatário pode baixar o arquivo.")
	}
	file := findTransferFile(transfer, fileID)
	if file == nil {
		return nil, problem(404, "TRANSFER_FILE_NOT_FOUND", "O arquivo da transferência não foi encontrado.")
	}
	if file.CompletedPath == "" {
		return nil, problem(404, "TRANSFER_DOWNLOAD_EXPIRED", "Esse arquivo não está mais disponível. Peça para enviar de novo.")
	}
	return file, nil
}

func (s *Server) serveTransferFile(w http.ResponseWriter, r *http.Request, actorID, transferID, fileID string) {
	s.mu.RLock()
	file, err := s.downloadableFileLocked(actorID, transferID, fileID)
	if err != nil {
		s.mu.RUnlock()
		writeProblem(w, r, err)
		return
	}
	path, name, mimeType, size, source := file.CompletedPath, file.DeliveredName, file.MimeType, file.Size, file.SourcePath != ""
	if name == "" {
		name = file.Name
	}
	s.mu.RUnlock()
	opened, err := os.Open(path)
	if err != nil {
		writeProblem(w, r, problem(404, "TRANSFER_DOWNLOAD_EXPIRED", "Esse arquivo não está mais disponível. Peça para enviar de novo."))
		return
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeProblem(w, r, problem(404, "TRANSFER_DOWNLOAD_EXPIRED", "Esse arquivo não está mais disponível. Peça para enviar de novo."))
		return
	}
	if source && info.Size() != size {
		writeProblem(w, r, problem(409, "TRANSFER_SOURCE_CHANGED", "O arquivo mudou no computador depois de ser oferecido. Peça para enviar de novo."))
		return
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), opened)
}

type historyFile struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

type historyItem struct {
	ID          string        `json:"id"`
	Direction   string        `json:"direction"`
	PeerID      string        `json:"peerId"`
	PeerName    string        `json:"peerName"`
	State       string        `json:"state"`
	TotalBytes  int64         `json:"totalBytes"`
	CreatedAt   time.Time     `json:"createdAt"`
	CompletedAt *time.Time    `json:"completedAt"`
	FailureCode *string       `json:"failureCode"`
	Files       []historyFile `json:"files"`
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	actorID := principalOf(r).DeviceID
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeProblem(w, r, problem(422, "HISTORY_LIMIT_INVALID", "O limite do histórico deve estar entre 1 e 100."))
			return
		}
		limit = parsed
	}
	before := time.Now().UTC().Add(100 * 365 * 24 * time.Hour)
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeProblem(w, r, problem(422, "HISTORY_CURSOR_INVALID", "O cursor do histórico é inválido."))
			return
		}
		before = parsed
	}
	s.mu.RLock()
	transfers := make([]*TransferRecord, 0)
	for _, transfer := range s.state.Transfers {
		if (transfer.SenderID == actorID || transfer.ReceiverID == actorID) && transfer.CreatedAt.Before(before) {
			transfers = append(transfers, transfer)
		}
	}
	sort.Slice(transfers, func(i, j int) bool { return transfers[i].CreatedAt.After(transfers[j].CreatedAt) })
	if len(transfers) > limit {
		transfers = transfers[:limit]
	}
	result := make([]historyItem, 0, len(transfers))
	for _, transfer := range transfers {
		item := historyItem{
			ID: transfer.ID, Direction: "INCOMING", PeerID: transfer.SenderID, PeerName: transfer.SenderName,
			State: transfer.State, TotalBytes: transfer.TotalBytes, CreatedAt: transfer.CreatedAt,
			CompletedAt: transfer.CompletedAt, FailureCode: transfer.FailureCode,
			Files: make([]historyFile, 0, len(transfer.Files)),
		}
		if transfer.SenderID == actorID {
			item.Direction, item.PeerID, item.PeerName = "OUTGOING", transfer.ReceiverID, transfer.ReceiverName
		}
		for _, file := range transfer.Files {
			item.Files = append(item.Files, historyFile{Name: file.Name, Size: file.Size, State: file.State})
		}
		result = append(result, item)
	}
	s.mu.RUnlock()
	writeJSON(w, result)
}

func (s *Server) emitTransfer(view TransferView, event string, payload map[string]any) {
	s.hub.SendToDevice(view.SenderID, event, payload)
	s.hub.SendToDevice(view.ReceiverID, event, payload)
}

// cleanOutbox remove textos enviados pelo computador há mais de maxAge.
func (s *Server) cleanOutbox(maxAge time.Duration) {
	root := filepath.Join(s.cfg.StorageRoot, "outbox")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil && time.Since(info.ModTime()) > maxAge {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}

func (s *Server) Cleanup() {
	s.cleanOutbox(s.cfg.TemporaryLifetime)
	now := time.Now().UTC()
	type action struct{ id, kind string }
	actions := make([]action, 0)
	s.mu.RLock()
	for _, transfer := range s.state.Transfers {
		idle := now.Sub(transfer.UpdatedAt)
		switch {
		case (transfer.State == "ACCEPTED" || transfer.State == "TRANSFERRING") && idle > s.cfg.InactivityTimeout:
			actions = append(actions, action{transfer.ID, "pause"})
		case (transfer.State == "WAITING_ACCEPTANCE" || transfer.State == "PAUSED") && idle > s.cfg.TemporaryLifetime:
			actions = append(actions, action{transfer.ID, "expire"})
		case transfer.State == "COMPLETED" && idle > s.cfg.TemporaryLifetime:
			actions = append(actions, action{transfer.ID, "downloads"})
		}
	}
	s.mu.RUnlock()
	for _, current := range actions {
		switch current.kind {
		case "pause":
			s.mu.Lock()
			transfer := s.state.Transfers[current.id]
			if transfer != nil && (transfer.State == "ACCEPTED" || transfer.State == "TRANSFERRING") {
				transfer.State = "PAUSED"
				transfer.UpdatedAt = now
				view := s.transferViewLocked(transfer)
				_ = s.saveLocked()
				s.mu.Unlock()
				s.emitTransfer(view, "TRANSFER_PAUSED", map[string]any{"transfer": view})
			} else {
				s.mu.Unlock()
			}
		case "expire":
			s.failTransfer(current.id, "TRANSFER_EXPIRED", "O envio ficou parado por muito tempo e foi encerrado. Envie de novo.")
		case "downloads":
			s.expireManagedDownloads(current.id)
		}
	}
}

func (s *Server) expireManagedDownloads(transferID string) {
	managedRoot := filepath.Clean(filepath.Join(s.cfg.StorageRoot, "go-completed")) + string(os.PathSeparator)
	s.mu.Lock()
	transfer := s.state.Transfers[transferID]
	if transfer == nil || transfer.State != "COMPLETED" {
		s.mu.Unlock()
		return
	}
	changed := false
	for _, file := range transfer.Files {
		if file.CompletedPath == "" {
			continue
		}
		clean := filepath.Clean(file.CompletedPath)
		switch {
		case strings.HasPrefix(clean, managedRoot):
			_ = os.Remove(clean)
		case file.SourcePath != "":
			// O original continua no computador; apenas o acesso pelo celular expira.
		default:
			continue
		}
		file.CompletedPath = ""
		changed = true
	}
	if changed {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
}

func (s *Server) RecoverTransfers() {
	ids := make([]string, 0)
	s.mu.Lock()
	changed := false
	for _, transfer := range s.state.Transfers {
		if isTerminalState(transfer.State) {
			continue
		}
		// Versões anteriores guardavam cada bloco em um arquivo separado. Esses
		// recibos não apontam para um arquivo em montagem, então os blocos são
		// pedidos de novo ao remetente.
		for _, file := range transfer.Files {
			if file.PartPath == "" && file.SourcePath == "" && len(file.Receipts) > 0 {
				transfer.ReceivedBytes -= file.ReceivedBytes
				file.Receipts = make(map[int64]*ChunkReceipt)
				file.ReceivedBytes, file.ReceivedChunks = 0, 0
				file.State = "PENDING"
				changed = true
				if transfer.State == "VERIFYING" {
					transfer.State = "PAUSED"
				}
			}
		}
		if transfer.State == "VERIFYING" {
			ids = append(ids, transfer.ID)
		}
	}
	if changed {
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	_ = os.RemoveAll(filepath.Join(s.cfg.StorageRoot, "go-transfers"))
	s.cleanOutboxUnused()
	for _, id := range ids {
		s.scheduleAssembly(id)
	}
}
