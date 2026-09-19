package bazarr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	cfgpkg "tiramisu/internal/config"
)

// ConfigAPIOptions wires the Bazarr config endpoint without depending on main package globals.
type ConfigAPIOptions struct {
	Runtime *RuntimeProvider
	Get     func() *cfgpkg.Config
	Store   func(*cfgpkg.Config)
	After   func(cfgpkg.Config)
}

type configResponse struct {
	Enabled        bool   `json:"enabled"`
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxResults     int    `json:"max_results"`
	HasAPIKey      bool   `json:"has_api_key"`
}

type configUpdate struct {
	Enabled        *bool   `json:"enabled"`
	URL            *string `json:"url"`
	APIKey         *string `json:"api_key"`
	TimeoutSeconds *int    `json:"timeout_seconds"`
	MaxResults     *int    `json:"max_results"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type testResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	URL     string `json:"url"`
}

// RegisterConfigAPI registers the native dashboard Bazarr config endpoint on mux.
func RegisterConfigAPI(mux *http.ServeMux, opts ConfigAPIOptions) {
	if mux == nil {
		mux = http.DefaultServeMux
	}
	mux.HandleFunc("/api/v1/config/bazarr", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg := opts.Get()
			if cfg == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "config unavailable")
				return
			}
			writeConfigResponse(w, cfg.Bazarr)
		case http.MethodPut:
			var req configUpdate
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			cur := opts.Get()
			if cur == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "config unavailable")
				return
			}
			cfg := *cur
			if req.Enabled != nil {
				cfg.Bazarr.Enabled = *req.Enabled
			}
			if req.URL != nil {
				cfg.Bazarr.URL = strings.TrimSpace(*req.URL)
			}
			if req.APIKey != nil {
				cfg.Bazarr.APIKey = strings.TrimSpace(*req.APIKey)
			}
			if req.TimeoutSeconds != nil {
				cfg.Bazarr.TimeoutSeconds = *req.TimeoutSeconds
			}
			if req.MaxResults != nil {
				cfg.Bazarr.MaxResults = *req.MaxResults
			}
			if err := normalizeBazarrConfig(&cfg.Bazarr); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := cfg.Save(); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if opts.Store != nil {
				opts.Store(&cfg)
			}
			if opts.Runtime != nil {
				opts.Runtime.Set(NewSubtitleProvider(cfg.Bazarr))
			}
			if opts.After != nil {
				opts.After(cfg)
			}
			writeConfigResponse(w, cfg.Bazarr)
		default:
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
	mux.HandleFunc("/api/v1/config/bazarr/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		cfg := opts.Get()
		if cfg == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "config unavailable")
			return
		}
		resp := testBazarrConnection(r.Context(), cfg.Bazarr)
		status := http.StatusOK
		if !resp.OK {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, resp)
	})
}

func normalizeBazarrConfig(cfg *cfgpkg.BazarrConfig) error {
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	if cfg.Enabled && cfg.URL == "" {
		return fmt.Errorf("bazarr url is required when enabled")
	}
	if cfg.URL == "" {
		cfg.URL = "http://127.0.0.1:6767"
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("bazarr url must be a valid http(s) URL")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if cfg.TimeoutSeconds > 120 {
		cfg.TimeoutSeconds = 120
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 5
	}
	if cfg.MaxResults > 50 {
		cfg.MaxResults = 50
	}
	return nil
}

func testBazarrConnection(ctx context.Context, cfg cfgpkg.BazarrConfig) testResponse {
	if err := normalizeBazarrConfig(&cfg); err != nil {
		return testResponse{OK: false, Message: err.Error(), URL: cfg.URL}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"/api/system/status", nil)
	if err != nil {
		return testResponse{OK: false, Message: err.Error(), URL: cfg.URL}
	}
	if cfg.APIKey != "" {
		req.Header.Set("X-API-KEY", cfg.APIKey)
	}
	resp, err := (&http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}).Do(req)
	if err != nil {
		return testResponse{OK: false, Message: err.Error(), URL: cfg.URL}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return testResponse{OK: false, Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))), URL: cfg.URL}
	}
	if !strings.Contains(ct, "application/json") {
		return testResponse{OK: false, Message: fmt.Sprintf("non-JSON response from /api/system/status: %s", ct), URL: cfg.URL}
	}
	return testResponse{OK: true, Message: "Bazarr conectado", URL: cfg.URL}
}

func writeConfigResponse(w http.ResponseWriter, cfg cfgpkg.BazarrConfig) {
	writeJSON(w, http.StatusOK, configResponse{
		Enabled:        cfg.Enabled,
		URL:            cfg.URL,
		TimeoutSeconds: cfg.TimeoutSeconds,
		MaxResults:     cfg.MaxResults,
		HasAPIKey:      cfg.APIKey != "",
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
