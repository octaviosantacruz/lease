// Package web serves a static product site and a loopback-only lab console.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/store"
)

//go:embed assets
var assets embed.FS

// Export writes a standalone static site to a new or empty directory.
func Export(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("web: output path required")
	}
	entries, err := os.ReadDir(path)
	if err == nil && len(entries) > 0 {
		return errors.New("web: output directory must be empty")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.MkdirAll(path, 0755); err != nil {
		return err
	}
	return fs.WalkDir(assets, "assets", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		dest := filepath.Join(path, strings.TrimPrefix(name, "assets/"))
		if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		return os.WriteFile(dest, body, 0644)
	})
}

type App struct {
	Broker      *broker.Broker
	host        string
	mu          sync.Mutex
	workerError bool
	missing     int
	drift       int
}

func New(b *broker.Broker, host string) (*App, error) {
	for _, e := range b.Config.Entitlements {
		if e.Provider != "lab" {
			return nil, errors.New("web: console only supports simulated lab targets")
		}
	}
	return &App{Broker: b, host: host}, nil
}
func (a *App) Worker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		operation, cancel := context.WithTimeout(ctx, 8*time.Second)
		report, err := a.Broker.Reconcile(operation)
		cancel()
		a.mu.Lock()
		a.workerError = err != nil
		a.missing = len(report.Missing)
		a.drift = len(report.Drift)
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", a.state)
	for _, operation := range []string{"request", "approve", "deny", "revoke"} {
		mux.HandleFunc("POST /api/"+operation, a.mutate)
	}
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("POST /api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	subtree, _ := fs.Sub(assets, "assets")
	fileServer := http.FileServer(http.FS(subtree))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || strings.HasSuffix(path, "/") {
			path += "index.html"
		}
		if info, err := fs.Stat(subtree, path); err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Host != a.host {
			http.Error(w, "untrusted host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != "GET" && (r.Header.Get("Origin") != "http://"+a.host || r.Header.Get("X-Lease-Demo") != "1" || r.Header.Get("Content-Type") != "application/json") {
				http.Error(w, "same-origin lab request required", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(result)
}
func (a *App) state(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.Broker.Store.Snapshot(r.Context())
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "El registro requiere revisión. No se puede operar."})
		return
	}
	a.mu.Lock()
	workerError := a.workerError
	missing, drift := a.missing, a.drift
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{"snapshot": snapshot, "policy": a.Broker.Config.Entitlements, "now": a.Broker.Now().UTC(), "worker_error": workerError, "missing": missing, "drift": drift})
}
func (a *App) mutate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Actor       string `json:"actor"`
		ID          string `json:"id"`
		Entitlement string `json:"entitlement"`
		Reason      string `json:"reason"`
		Seconds     int64  `json:"seconds"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		writeJSON(w, 400, map[string]string{"error": "Solicitud inválida."})
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "Solicitud inválida."})
		return
	}
	var result store.Grant
	var err error
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	switch strings.TrimPrefix(r.URL.Path, "/api/") {
	case "request":
		if input.Seconds < 1 || input.Seconds > 86400 || len(strings.TrimSpace(input.Reason)) == 0 || len(input.Reason) > 500 {
			writeJSON(w, 400, map[string]string{"error": "Revisá la duración y el motivo de la solicitud."})
			return
		}
		result, err = a.Broker.Request(ctx, input.Entitlement, input.Actor, input.Reason, time.Duration(input.Seconds)*time.Second)
	case "approve":
		result, err = a.Broker.Approve(ctx, input.ID, input.Actor)
	case "deny":
		result, err = a.Broker.Deny(ctx, input.ID, input.Actor)
	case "revoke":
		result, err = a.Broker.Revoke(ctx, input.ID, input.Actor)
	}
	if err != nil {
		status, message := 409, "No se pudo completar. Actualizá la vista y revisá el estado."
		if errors.Is(err, broker.ErrForbidden) {
			status = 403
			message = "Ese rol no puede realizar la operación ni aprobar su propio acceso."
		}
		if errors.Is(err, store.ErrInvalid) {
			status = 400
			message = "La ventana venció o la solicitud no cumple la política."
		}
		if errors.Is(err, store.ErrNotFound) {
			status = 404
			message = "La solicitud ya no está disponible."
		}
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	writeJSON(w, 200, result)
}
