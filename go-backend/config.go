package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const version = "0.3.0"

type Config struct {
	Port              int
	StorageRoot       string
	Downloads         string
	AutoOpen          bool
	ChunkSize         int64
	MaxTotalChunks    int64
	PairingLifetime   time.Duration
	PairingRateLimit  int
	PairingRateWindow time.Duration
	CreateRateLimit   int
	CreateRateWindow  time.Duration
	InactivityTimeout time.Duration
	TemporaryLifetime time.Duration
	MinimumFreeSpace  uint64
}

func loadConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("localizar pasta do usuário: %w", err)
	}
	root := envString("LOCALDROP_STORAGE_ROOT", filepath.Join(home, ".localdrop"))
	cfg := Config{
		Port:              envInt("LOCALDROP_PORT", 8088),
		StorageRoot:       root,
		Downloads:         envString("LOCALDROP_DOWNLOADS", filepath.Join(home, "Downloads", "LocalDrop")),
		AutoOpen:          envBool("LOCALDROP_AUTO_OPEN", true),
		ChunkSize:         envInt64("LOCALDROP_TRANSFER_CHUNK_SIZE", 4*1024*1024),
		MaxTotalChunks:    envInt64("LOCALDROP_TRANSFER_MAX_TOTAL_CHUNKS", 1_000_000),
		PairingLifetime:   envDuration("LOCALDROP_SECURITY_PAIRING_EXPIRATION", 5*time.Minute),
		PairingRateLimit:  envInt("LOCALDROP_SECURITY_PAIRING_RATE_LIMIT", 10),
		PairingRateWindow: envDuration("LOCALDROP_SECURITY_PAIRING_RATE_WINDOW", time.Minute),
		CreateRateLimit:   envInt("LOCALDROP_TRANSFER_CREATE_RATE_LIMIT", 20),
		CreateRateWindow:  envDuration("LOCALDROP_TRANSFER_CREATE_RATE_WINDOW", time.Minute),
		InactivityTimeout: envDuration("LOCALDROP_TRANSFER_INACTIVITY_TIMEOUT", 2*time.Minute),
		TemporaryLifetime: envDuration("LOCALDROP_TRANSFER_TEMPORARY_LIFETIME", 24*time.Hour),
		MinimumFreeSpace:  uint64(envInt64("LOCALDROP_TRANSFER_MINIMUM_FREE_SPACE", 100*1024*1024)),
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("LOCALDROP_PORT deve estar entre 1 e 65535")
	}
	if cfg.ChunkSize <= 0 || cfg.MaxTotalChunks <= 0 {
		return Config{}, fmt.Errorf("limites de transferência devem ser positivos")
	}
	absRoot, err := filepath.Abs(cfg.StorageRoot)
	if err != nil {
		return Config{}, err
	}
	absDownloads, err := filepath.Abs(cfg.Downloads)
	if err != nil {
		return Config{}, err
	}
	cfg.StorageRoot = filepath.Clean(absRoot)
	cfg.Downloads = filepath.Clean(absDownloads)
	return cfg, nil
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}
	// Compatibilidade com os valores ISO-8601 usados pela versão Java (PT2M, PT24H, PT10S).
	upper := strings.ToUpper(value)
	if strings.HasPrefix(upper, "PT") {
		body := strings.TrimPrefix(upper, "PT")
		var unit time.Duration
		switch {
		case strings.HasSuffix(body, "H"):
			unit, body = time.Hour, strings.TrimSuffix(body, "H")
		case strings.HasSuffix(body, "M"):
			unit, body = time.Minute, strings.TrimSuffix(body, "M")
		case strings.HasSuffix(body, "S"):
			unit, body = time.Second, strings.TrimSuffix(body, "S")
		}
		if number, err := strconv.ParseFloat(body, 64); err == nil && unit > 0 {
			return time.Duration(number * float64(unit))
		}
	}
	return fallback
}
