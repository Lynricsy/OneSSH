package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"onessh/internal/config"
	"onessh/internal/store"
)

const version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil { log.Fatal(err) }
	st, err := store.Open(cfg.DataDir)
	if err != nil { log.Fatal(err) }
	defer st.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Health(r.Context()); err != nil { http.Error(w, err.Error(), http.StatusServiceUnavailable); return }
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
	})
	server := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("OneSSH %s 监听 %s", version, cfg.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.New(os.Stderr, "", 0).Fatal(err) }
}
