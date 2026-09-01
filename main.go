package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	switch {
	case len(os.Args) >= 2 && os.Args[1] == attachmentExecArgument:
		if err := runAttachmentClient(os.Args[2:]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	case len(os.Args) == 2 && os.Args[1] == "--version":
		fmt.Println(version)
		return
	case len(os.Args) != 1:
		fmt.Fprintln(os.Stderr, "usage: herdr-web-client [--version]")
		os.Exit(2)
	}
	if err := run(context.Background()); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	launcher := NewPTYLauncher(cfg.HerdrPath, cfg.HerdrWorkdir)
	completions := newHerdrCompletionSource(cfg.HerdrSocket)
	application, err := NewServer(cfg, launcher, completions)
	if err != nil {
		return err
	}
	defer application.Close()
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           application,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 * 1024,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		_ = application.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		serveErr := <-serverErrors
		if shutdownErr != nil {
			return shutdownErr
		}
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	case serveErr := <-serverErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serveErr
	}
}
