package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"onessh/internal/config"
	"onessh/internal/cryptox"
	"onessh/internal/events"
	"onessh/internal/hostmanager"
	"onessh/internal/mcpserver"
	"onessh/internal/sshpool"
	"onessh/internal/store"
	"onessh/internal/webapi"
	"onessh/web"
)

const version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	hosts := hostmanager.New(st, box, pool)
	bus := events.New()
	mcpService := mcpserver.New(st, pool, bus, hosts, cfg.DataDir, cfg.PollInterval)
	defer mcpService.Close()
	adminAuth := webapi.NewAdminAuth(cfg.AdminPassword, cfg.MasterKey)
	adminAPI := webapi.NewAPI(st, box, pool, hosts, mcpService.Exec, mcpService.Files, mcpService.Jobs, mcpService.Monitor, bus)

	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/login", http.HandlerFunc(adminAuth.Login))
	mux.Handle("/mcp", mcpserver.Handler(st, mcpService))
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", adminAuth.Require(adminAPI.Handler())))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Health(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": version})
	})
	mux.Handle("/", web.Handler())
	server := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("OneSSH %s 监听 %s", version, cfg.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.New(os.Stderr, "", 0).Fatal(err)
	}
}
