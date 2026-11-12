package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Only the refusal is served: no setup, sign-in, library or background work starts.
func oldTestFolder(ctx context.Context, listen string, log *slog.Logger) error {
	log.Error(db.ErrOldTestFolder.Error())
	srv := &http.Server{Addr: listen, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]any{"error": api.Error{Code: "old_test_data_folder", Message: db.ErrOldTestFolder.Error()}})
	})}
	defer srv.Close()
	go func() { <-ctx.Done(); srv.Close() }()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
