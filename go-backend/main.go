package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	headless := flag.Bool("headless", false, "não abrir a janela do aplicativo")
	port := flag.Int("port", 0, "sobrescrever porta HTTP")
	legacyDirectory := flag.String("import-legacy", "", "importar arquivos CSV exportados da versão Java")
	importOnly := flag.Bool("import-only", false, "encerrar depois da importação legada")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		fatalStartup(err)
		return
	}
	if *port != 0 {
		cfg.Port = *port
	}
	if *headless {
		cfg.AutoOpen = false
	}
	configureLogging(cfg.StorageRoot)

	server, err := NewServer(cfg)
	if err != nil {
		fatalStartup(err)
		return
	}
	if *legacyDirectory != "" {
		if err := server.ImportLegacy(*legacyDirectory); err != nil {
			fatalStartup(fmt.Errorf("importar dados da versão Java: %w", err))
			return
		}
		log.Printf("dados da versão Java importados de %s", *legacyDirectory)
		if *importOnly {
			return
		}
	}
	address := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		if serviceReady(cfg.Port) {
			if cfg.AutoOpen {
				_ = focusNativeWindow()
			}
			return
		}
		fatalStartup(fmt.Errorf("não foi possível usar a porta %d: %w", cfg.Port, err))
		return
	}

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	server.RecoverTransfers()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			server.Cleanup()
		}
	}()
	log.Printf("LocalDrop %s pronto em %s (rede: %s)", version, address, server.localURL())

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpServer.Serve(listener)
	}()

	if cfg.AutoOpen && showDesktopPanel(server, fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		server.Flush()
		return
	}
	if cfg.AutoOpen {
		fatalStartup(fmt.Errorf("não foi possível abrir a interface nativa do desktop"))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		return
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	select {
	case <-shutdown:
	case err := <-serveDone:
		if err != nil && err != http.ErrServerClosed {
			fatalStartup(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = httpServer.Shutdown(ctx)
	cancel()
	server.Flush()
	if err := <-serveDone; err != nil && err != http.ErrServerClosed {
		fatalStartup(err)
	}
}

func configureLogging(root string) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(root, "localdrop-go.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, file))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

func serviceReady(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func fatalStartup(err error) {
	log.Printf("falha de inicialização: %v", err)
	showFatalError("O LocalDrop não conseguiu iniciar.\n\n" + err.Error())
}
