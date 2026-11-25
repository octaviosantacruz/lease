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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/identity"
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
	Broker *broker.Broker
	host   string
	// ident is nil for the lab console. When set, the app runs behind Ozal:
	// every request carries a verified identity and the actor comes from it.
	ident       *identity.Verifier
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

// NewOperational serves the console behind Ozal for real targets. The actor
// of every action is the principal mapped from Ozal's verified assertion; the
// browser cannot choose it. There is no actor selector and no lab-only check.
func NewOperational(b *broker.Broker, v *identity.Verifier) (*App, error) {
	if v == nil {
		return nil, errors.New("web: operational mode requires an identity verifier")
	}
	return &App{Broker: b, ident: v}, nil
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
	if a.ident != nil {
		return a.operational(mux)
	}
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

// securityHeaders are sent on every response, in both modes.
func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// operational wraps the mux for the Ozal mode: identity first (loopback peer
// and a valid assertion for a mapped person), then a same-origin rule for
// mutations, because the browser attaches Ozal's session cookie to any
// request, including one a hostile page triggers.
func (a *App) operational(mux http.Handler) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != "GET" && (r.Header.Get("Origin") != a.ident.Origin() || r.Header.Get("X-Lease-Request") != "1" || r.Header.Get("Content-Type") != "application/json") {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "Se requiere una solicitud del mismo origen."})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
	verified := identity.Middleware(a.ident, nil, inner)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		verified.ServeHTTP(w, r)
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
	result := map[string]any{"snapshot": snapshot, "policy": a.Broker.Config.Entitlements, "now": a.Broker.Now().UTC(), "worker_error": workerError, "missing": missing, "drift": drift}
	if a.ident != nil {
		actor, ok := identity.From(r.Context())
		if !ok {
			writeJSON(w, 401, map[string]string{"error": "missing"})
			return
		}
		visible := Visible(a.Broker.Config, snapshot, actor.Principal)
		result["snapshot"] = visible.Snapshot
		result["policy"] = visible.Policy
		result["identity"] = map[string]string{"principal": actor.Principal, "name": actor.Person.Name, "email": actor.Person.Email}
	}
	writeJSON(w, 200, result)
}

// View is what one principal may see.
type View struct {
	Snapshot store.Snapshot
	Policy   []broker.Entitlement
}

// Visible filters a snapshot for principal: entitlements they may request or
// approve, grants they requested or may act on as approver, and only the
// events of those grants. The hash-chain head is kept so the view still names
// the ledger revision it came from.
func Visible(c broker.Config, s store.Snapshot, principal string) View {
	v := View{
		Snapshot: store.Snapshot{Entitlements: []store.Entitlement{}, Grants: []store.Grant{}, Events: []store.Event{}, Head: s.Head},
		Policy:   []broker.Entitlement{},
	}
	approves := map[string]bool{}
	for _, e := range c.Entitlements {
		requester, approver := slices.Contains(e.Requesters, principal), slices.Contains(e.Approvers, principal)
		if requester || approver {
			v.Policy = append(v.Policy, e)
		}
		approves[e.ID] = approver
	}
	inPolicy := map[string]bool{}
	for _, e := range v.Policy {
		inPolicy[e.ID] = true
	}
	for _, e := range s.Entitlements {
		if inPolicy[e.ID] {
			v.Snapshot.Entitlements = append(v.Snapshot.Entitlements, e)
		}
	}
	grants := map[string]bool{}
	for _, g := range s.Grants {
		if g.Key.Principal == principal || approves[g.EntitlementID] {
			v.Snapshot.Grants = append(v.Snapshot.Grants, g)
			grants[g.ID] = true
		}
	}
	for _, e := range s.Events {
		if grants[e.GrantID] {
			v.Snapshot.Events = append(v.Snapshot.Events, e)
		}
	}
	return v
}
func (a *App) mutate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Actor       *string `json:"actor"`
		ID          string  `json:"id"`
		Entitlement string  `json:"entitlement"`
		Reason      string  `json:"reason"`
		Seconds     int64   `json:"seconds"`
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
	// Un puntero (*string) distingue "campo ausente" (nil) de "campo vacío":
	// en modo operativo el actor no puede venir del navegador en absoluto.
	actor := ""
	if a.ident != nil {
		if input.Actor != nil {
			writeJSON(w, 400, map[string]string{"error": "El actor lo determina la identidad verificada."})
			return
		}
		verified, ok := identity.From(r.Context())
		if !ok {
			writeJSON(w, 401, map[string]string{"error": "missing"})
			return
		}
		actor = verified.Principal
	} else if input.Actor != nil {
		actor = *input.Actor
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
		result, err = a.Broker.Request(ctx, input.Entitlement, actor, input.Reason, time.Duration(input.Seconds)*time.Second)
	case "approve":
		result, err = a.Broker.Approve(ctx, input.ID, actor)
	case "deny":
		result, err = a.Broker.Deny(ctx, input.ID, actor)
	case "revoke":
		result, err = a.Broker.Revoke(ctx, input.ID, actor)
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
