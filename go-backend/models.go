package main

import "time"

type DeviceRecord struct {
	DeviceID    string    `json:"deviceId"`
	DisplayName string    `json:"displayName"`
	DeviceType  string    `json:"deviceType"`
	TokenHash   string    `json:"tokenHash"`
	Host        bool      `json:"host"`
	Revoked     bool      `json:"revoked"`
	CreatedAt   time.Time `json:"createdAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	// HardwareKey reconhece o mesmo aparelho físico quando ele volta com outro
	// identificador (ver hardware.go).
	HardwareKey string `json:"hardwareKey,omitempty"`
}

type DeviceView struct {
	DeviceID    string    `json:"deviceId"`
	DisplayName string    `json:"displayName"`
	DeviceType  string    `json:"deviceType"`
	Host        bool      `json:"host"`
	Online      bool      `json:"online"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
}

type PairingRecord struct {
	ID            string    `json:"id"`
	DeviceID      string    `json:"deviceId"`
	DisplayName   string    `json:"displayName"`
	DeviceType    string    `json:"deviceType"`
	Code          string    `json:"code,omitempty"`
	CodeHash      string    `json:"codeHash"`
	PollTokenHash string    `json:"pollTokenHash"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	TokenHash     string    `json:"tokenHash,omitempty"`
	IssuedToken   string    `json:"issuedToken,omitempty"`
}

type HostSettings struct {
	DownloadDirectory string    `json:"downloadDirectory"`
	AutoAcceptTrusted bool      `json:"autoAcceptTrusted"`
	Port              int       `json:"port,omitempty"`
	AutostartAsked    bool      `json:"autostartAsked,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type ChunkReceipt struct {
	Index      int64     `json:"index"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	ReceivedAt time.Time `json:"receivedAt"`
}

type TransferFile struct {
	ID             string                  `json:"id"`
	Ordinal        int                     `json:"ordinal"`
	Name           string                  `json:"name"`
	Size           int64                   `json:"size"`
	MimeType       string                  `json:"mimeType"`
	SHA256         string                  `json:"sha256"`
	TotalChunks    int64                   `json:"totalChunks"`
	ReceivedChunks int64                   `json:"receivedChunks"`
	ReceivedBytes  int64                   `json:"receivedBytes"`
	State          string                  `json:"state"`
	CompletedPath  string                  `json:"completedPath,omitempty"`
	DeliveredName  string                  `json:"deliveredName,omitempty"`
	PartPath       string                  `json:"partPath,omitempty"`
	SourcePath     string                  `json:"sourcePath,omitempty"`
	Receipts       map[int64]*ChunkReceipt `json:"receipts,omitempty"`
}

type TransferRecord struct {
	ID             string          `json:"id"`
	SenderID       string          `json:"senderId"`
	SenderName     string          `json:"senderName"`
	ReceiverID     string          `json:"receiverId"`
	ReceiverName   string          `json:"receiverName"`
	State          string          `json:"state"`
	TotalBytes     int64           `json:"totalBytes"`
	ReceivedBytes  int64           `json:"receivedBytes"`
	ChunkSize      int64           `json:"chunkSize,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	AcceptedAt     *time.Time      `json:"acceptedAt,omitempty"`
	CompletedAt    *time.Time      `json:"completedAt,omitempty"`
	FailureCode    *string         `json:"failureCode,omitempty"`
	FailureMessage *string         `json:"failureMessage,omitempty"`
	Files          []*TransferFile `json:"files"`
	// HiddenFor guarda quem tirou o envio da própria lista ("Limpar lista").
	HiddenFor []string `json:"hiddenFor,omitempty"`
}

type State struct {
	Version   int                        `json:"version"`
	Devices   map[string]*DeviceRecord   `json:"devices"`
	Pairings  map[string]*PairingRecord  `json:"pairings"`
	Transfers map[string]*TransferRecord `json:"transfers"`
	Settings  HostSettings               `json:"settings"`
}

type TransferFileView struct {
	ID                string  `json:"id"`
	Ordinal           int     `json:"ordinal"`
	Name              string  `json:"name"`
	Size              int64   `json:"size"`
	MimeType          string  `json:"mimeType"`
	SHA256            string  `json:"sha256"`
	TotalChunks       int64   `json:"totalChunks"`
	ReceivedChunks    int64   `json:"receivedChunks"`
	ReceivedBytes     int64   `json:"receivedBytes"`
	State             string  `json:"state"`
	DownloadAvailable bool    `json:"downloadAvailable"`
	DeliveredName     *string `json:"deliveredName"`
	SavedPath         *string `json:"-"`
}

type TransferView struct {
	ID             string             `json:"id"`
	SenderID       string             `json:"senderId"`
	ReceiverID     string             `json:"receiverId"`
	State          string             `json:"state"`
	TotalBytes     int64              `json:"totalBytes"`
	ReceivedBytes  int64              `json:"receivedBytes"`
	ChunkSize      int64              `json:"chunkSize"`
	CreatedAt      time.Time          `json:"createdAt"`
	UpdatedAt      time.Time          `json:"updatedAt"`
	AcceptedAt     *time.Time         `json:"acceptedAt"`
	CompletedAt    *time.Time         `json:"completedAt"`
	FailureCode    *string            `json:"failureCode"`
	FailureMessage *string            `json:"failureMessage"`
	Files          []TransferFileView `json:"files"`
}

func newState(downloads string) *State {
	now := time.Now().UTC()
	return &State{
		Version:   1,
		Devices:   make(map[string]*DeviceRecord),
		Pairings:  make(map[string]*PairingRecord),
		Transfers: make(map[string]*TransferRecord),
		Settings: HostSettings{
			DownloadDirectory: downloads,
			UpdatedAt:         now,
		},
	}
}
