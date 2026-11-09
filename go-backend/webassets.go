package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// O build guarda cada arquivo da interface do celular já comprimido (.gz). O
// executável fica menor e o celular baixa menos bytes no primeiro acesso.
//
//go:embed all:web
var embeddedWeb embed.FS

type webAsset struct {
	data       []byte // conteúdo como está no executável
	compressed bool   // data está em gzip
	etag       string
}

type webAssets struct {
	root  fs.FS
	mu    sync.Mutex
	cache map[string]*webAsset
}

func (a *webAssets) lookup(name string) *webAsset {
	a.mu.Lock()
	defer a.mu.Unlock()
	if asset, ok := a.cache[name]; ok {
		return asset
	}
	var asset *webAsset
	if data, err := fs.ReadFile(a.root, name+".gz"); err == nil {
		asset = &webAsset{data: data, compressed: true}
	} else if data, err := fs.ReadFile(a.root, name); err == nil {
		asset = &webAsset{data: data}
	}
	if asset != nil {
		digest := sha256.Sum256(asset.data)
		asset.etag = `"` + hex.EncodeToString(digest[:8]) + `"`
	}
	a.cache[name] = asset
	return asset
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		token := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(token, "gzip") || token == "*" {
			return !strings.Contains(strings.ReplaceAll(part, " ", ""), "q=0")
		}
	}
	return false
}

func contentTypeFor(name string) string {
	switch path.Ext(name) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".webmanifest":
		return "application/manifest+json"
	case ".wasm":
		return "application/wasm"
	}
	if value := mime.TypeByExtension(path.Ext(name)); value != "" {
		return value
	}
	return "application/octet-stream"
}

func embeddedWebHandler() http.Handler {
	root, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic("carregar interface web: " + err.Error())
	}
	assets := &webAssets{root: root, cache: make(map[string]*webAsset)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		asset := assets.lookup(name)
		if asset == nil {
			if strings.HasPrefix(name, "api/") || strings.HasPrefix(name, "ws/") || strings.HasPrefix(name, "downloads/") || path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			// Rotas da aplicação de página única caem no index.
			name = "index.html"
			if asset = assets.lookup(name); asset == nil {
				http.NotFound(w, r)
				return
			}
		}
		header := w.Header()
		header.Set("Content-Type", contentTypeFor(name))
		header.Set("Vary", "Accept-Encoding")
		header.Set("ETag", asset.etag)
		if strings.HasPrefix(name, "assets/") {
			header.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			header.Set("Cache-Control", "no-cache")
		}
		if match := r.Header.Get("If-None-Match"); match != "" && match == asset.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		body := asset.data
		if asset.compressed {
			if acceptsGzip(r) {
				header.Set("Content-Encoding", "gzip")
			} else {
				reader, err := gzip.NewReader(bytes.NewReader(asset.data))
				if err != nil {
					http.Error(w, "arquivo corrompido", http.StatusInternalServerError)
					return
				}
				body, err = io.ReadAll(reader)
				if err != nil {
					http.Error(w, "arquivo corrompido", http.StatusInternalServerError)
					return
				}
			}
		}
		header.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
}
