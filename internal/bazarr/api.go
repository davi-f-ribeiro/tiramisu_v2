package bazarr

import (
	"encoding/json"
	"net/http"

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
				http.Error(w, "config unavailable", http.StatusServiceUnavailable)
				return
			}
			writeConfigResponse(w, cfg.Bazarr)
		case http.MethodPut:
			var req configUpdate
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			cur := opts.Get()
			if cur == nil {
				http.Error(w, "config unavailable", http.StatusServiceUnavailable)
				return
			}
			cfg := *cur
			if req.Enabled != nil {
				cfg.Bazarr.Enabled = *req.Enabled
			}
			if req.URL != nil {
				cfg.Bazarr.URL = *req.URL
			}
			if req.APIKey != nil {
				cfg.Bazarr.APIKey = *req.APIKey
			}
			if req.TimeoutSeconds != nil && *req.TimeoutSeconds > 0 {
				cfg.Bazarr.TimeoutSeconds = *req.TimeoutSeconds
			}
			if req.MaxResults != nil && *req.MaxResults > 0 {
				cfg.Bazarr.MaxResults = *req.MaxResults
			}
			if err := cfg.Save(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
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
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func writeConfigResponse(w http.ResponseWriter, cfg cfgpkg.BazarrConfig) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(configResponse{
		Enabled:        cfg.Enabled,
		URL:            cfg.URL,
		TimeoutSeconds: cfg.TimeoutSeconds,
		MaxResults:     cfg.MaxResults,
		HasAPIKey:      cfg.APIKey != "",
	})
}
