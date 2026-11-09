package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	headless := flag.Bool("headless", false, "só o servidor, sem janela nem ícone na bandeja")
	tray := flag.Bool("tray", false, "abrir direto na bandeja, sem mostrar a janela (usado ao iniciar com o Windows)")
	port := flag.Int("port", 0, "usar somente esta porta HTTP")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		fatalStartup(err)
		return
	}
	if *port != 0 {
		cfg.Port, cfg.PortFixed = *port, true
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
	fixed := 0
	if cfg.PortFixed {
		fixed = cfg.Port
	}
	listener, chosen, err := listenLocalDrop(candidatePorts(server.savedPort(), fixed))
	if err != nil {
		var running errAlreadyRunning
		if errors.As(err, &running) {
			// Outra instância já atende: traz a janela dela para a frente.
			if cfg.AutoOpen && !*tray {
				_ = focusNativeWindow()
			}
			return
		}
		fatalStartup(err)
		return
	}
	server.usePort(chosen)

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	server.RecoverTransfers()
	go func() {
		// Pausas por inatividade e limpezas leves; nada pesado roda aqui.
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			server.Cleanup()
		}
	}()
	log.Printf("LocalDrop %s pronto na porta %d (rede: %s)", version, chosen, server.localURL())

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- httpServer.Serve(listener)
	}()

	if cfg.AutoOpen {
		if !showDesktopPanel(server, *tray) {
			fatalStartup(fmt.Errorf("não foi possível abrir a janela do LocalDrop"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
		server.Flush()
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

// configureLogging grava o log na pasta de dados e o mantém pequeno: ao passar
// de 1 MB, o arquivo vira .old e um novo começa.
func configureLogging(root string) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return
	}
	path := filepath.Join(root, "localdrop-go.log")
	if info, err := os.Stat(path); err == nil && info.Size() > 1<<20 {
		_ = os.Remove(path + ".old")
		_ = os.Rename(path, path+".old")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	// O arquivo vem primeiro: sem console (-H=windowsgui), escrever no stderr
	// falha e o MultiWriter pararia antes de gravar o log.
	log.SetOutput(io.MultiWriter(file, os.Stderr))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

func fatalStartup(err error) {
	log.Printf("falha de inicialização: %v", err)
	message := err.Error()
	var api *apiProblem
	if errors.As(err, &api) {
		message = api.Message
	}
	showFatalError("O LocalDrop não conseguiu abrir.\n\n" + message)
}
