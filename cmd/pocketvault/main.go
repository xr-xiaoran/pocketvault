package main

import (
	"context"
	"errors"
	"github.com/xr-xiaoran/pocketvault/internal/httpapi"
	"github.com/xr-xiaoran/pocketvault/internal/vault"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	keys := map[string]string{}
	seen := map[string]bool{}
	for _, entry := range strings.Split(os.Getenv("VAULT_KEYS"), ",") {
		owner, key, ok := strings.Cut(entry, "=")
		if !ok || owner == "" || len(key) < 16 || seen[key] || keys[owner] != "" {
			return errors.New("set VAULT_KEYS=alice=at-least-16-characters,bob=another-unique-key")
		}
		keys[owner] = key
		seen[key] = true
	}
	path := os.Getenv("DB_PATH")
	if path == "" {
		path = "pocketvault.db"
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8082"
	}
	s, err := vault.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	srv := &http.Server{Addr: addr, Handler: httpapi.Router(s, keys), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	ch := make(chan error, 1)
	go func() { slog.Info("PocketVault ready", "addr", addr); ch <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err = <-ch:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
