package bazarrvfs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "tiramisu/internal/config"
)

func TestNormalizeBazarrConfigDoesNotInjectStaticURL(t *testing.T) {
	cfg := cfgpkg.BazarrConfig{Enabled: false, URL: ""}
	if err := normalizeBazarrConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "" {
		t.Fatalf("URL = %q, want empty when disabled", cfg.URL)
	}
}

func TestBazarrConfigPutDisabledEmptyURLPersistsAndHotReloadsDisabled(t *testing.T) {
	dir := t.TempDir()
	current := &cfgpkg.Config{
		ConfigPath: filepath.Join(dir, "config.json"),
		Bazarr:     cfgpkg.BazarrConfig{Enabled: true, URL: "http://example.invalid", TimeoutSeconds: 30, MaxResults: 5},
	}
	runtime := NewRuntimeProvider(current.Bazarr)
	mux := http.NewServeMux()
	RegisterConfigAPI(mux, ConfigAPIOptions{
		Runtime: runtime,
		Get: func() *cfgpkg.Config {
			return current
		},
		Store: func(next *cfgpkg.Config) {
			current = next
		},
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config/bazarr", strings.NewReader(`{"enabled":false,"url":""}`))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if runtime.IsEnabled() {
		t.Fatal("runtime should be disabled immediately after PUT")
	}
	data, err := os.ReadFile(current.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved cfgpkg.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Bazarr.Enabled || saved.Bazarr.URL != "" {
		t.Fatalf("saved Bazarr = %+v, want disabled empty URL", saved.Bazarr)
	}
}

func TestBazarrConfigPutEnabledValidatesStatusAndHotReloads(t *testing.T) {
	statusHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		statusHit = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"appName":"Bazarr"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	current := &cfgpkg.Config{ConfigPath: filepath.Join(dir, "config.json"), Bazarr: cfgpkg.BazarrConfig{Enabled: false}}
	runtime := NewRuntimeProvider(current.Bazarr)
	mux := http.NewServeMux()
	RegisterConfigAPI(mux, ConfigAPIOptions{
		Runtime: runtime,
		Get: func() *cfgpkg.Config {
			return current
		},
		Store: func(next *cfgpkg.Config) {
			current = next
		},
	})

	body := `{"enabled":true,"url":"` + srv.URL + `","timeout_seconds":5,"max_results":7}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config/bazarr", strings.NewReader(body))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !statusHit {
		t.Fatal("PUT did not validate /api/system/status before saving")
	}
	if !runtime.IsEnabled() {
		t.Fatal("runtime should be enabled immediately after successful PUT")
	}
}

func TestBazarrConfigPutEnabledRejectsUnreachableURLBeforePersisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	current := &cfgpkg.Config{ConfigPath: path, Bazarr: cfgpkg.BazarrConfig{Enabled: false}}
	mux := http.NewServeMux()
	RegisterConfigAPI(mux, ConfigAPIOptions{Get: func() *cfgpkg.Config { return current }})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config/bazarr", strings.NewReader(`{"enabled":true,"url":"http://127.0.0.1:1","timeout_seconds":1}`))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config file should not be written on failed validation; stat err=%v", err)
	}
}
