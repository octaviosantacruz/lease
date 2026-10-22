package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/octaviosantacruz/lease/internal/broker"
	"github.com/octaviosantacruz/lease/internal/store"
)

func app(t *testing.T) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lab.sqlite")
	ctx := context.Background()
	if err := store.Migrate(ctx, path); err != nil {
		t.Fatal(err)
	}
	r, err := broker.Open(ctx, path, broker.DemoConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if err = r.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := New(r.Broker, "127.0.0.1:12345")
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func request(t *testing.T, a *App, operation string, body any, origin string) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "http://127.0.0.1:12345/api/"+operation, bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	r.Header.Set("X-Lease-Demo", "1")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func TestHTTPRealWorkflowAndBoundary(t *testing.T) {
	a := app(t)
	body := map[string]any{"actor": "user:ines", "entitlement": "analytics-read", "reason": "INC-284", "seconds": 60}
	for _, origin := range []string{"", "https://evil.invalid"} {
		w := request(t, a, "request", body, origin)
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := request(t, a, "request", body, "http://127.0.0.1:12345")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var g store.Grant
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	w = request(t, a, "approve", map[string]string{"id": g.ID, "actor": "user:ines"}, "http://127.0.0.1:12345")
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	w = request(t, a, "approve", map[string]string{"id": g.ID, "actor": "user:mateo"}, "http://127.0.0.1:12345")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = request(t, a, "revoke", map[string]string{"id": g.ID, "actor": "user:ines"}, "http://127.0.0.1:12345")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for _, path := range []string{"/", "/console/", "/guide/", "/site.css", "/console.js", "/media/access-key.webp", "/api/state"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:12345"+path, nil)
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing CSP")
		}
	}
	r := httptest.NewRequest("GET", "http://rebinding.invalid/api/state", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("untrusted host accepted")
	}
}
func TestConsoleRejectsRealProvider(t *testing.T) {
	b := &broker.Broker{Config: broker.Config{Entitlements: []broker.Entitlement{{Provider: "postgres"}}}}
	if _, err := New(b, "127.0.0.1:12345"); err == nil {
		t.Fatal("live target accepted")
	}
}
func TestExportDoesNotOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "site")
	if err := Export(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "site.css", "console.js", "console/index.html", "guide/index.html", "media/access-key.webp"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Export(dir); err == nil {
		t.Fatal("nonempty output directory overwritten")
	}
}

func TestStaticAssetReferencesExist(t *testing.T) {
	err := fs.WalkDir(assets, "assets", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".html") {
			return nil
		}
		body, err := assets.ReadFile(name)
		if err != nil {
			return err
		}
		for _, match := range regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllSubmatch(body, -1) {
			ref := string(match[1])
			if strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "#") {
				continue
			}
			ref = strings.Split(ref, "#")[0]
			if !strings.HasPrefix(ref, "/") {
				t.Errorf("non-local reference in %s: %s", name, ref)
				continue
			}
			target := "assets" + ref
			if strings.HasSuffix(target, "/") {
				target += "index.html"
			}
			if _, err := assets.ReadFile(target); err != nil {
				t.Errorf("broken reference in %s: %s", name, ref)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
