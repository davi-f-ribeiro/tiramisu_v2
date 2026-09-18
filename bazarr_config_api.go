package main

import (
	"encoding/json"
	"net/http"

	cfgpkg "tiramisu/internal/config"
)

type bazarrConfigResponse struct {
	Enabled        bool   `json:"enabled"`
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxResults     int    `json:"max_results"`
	HasAPIKey      bool   `json:"has_api_key"`
}

type bazarrConfigUpdate struct {
	Enabled        *bool   `json:"enabled"`
	URL            *string `json:"url"`
	APIKey         *string `json:"api_key"`
	TimeoutSeconds *int    `json:"timeout_seconds"`
	MaxResults     *int    `json:"max_results"`
}

func registerBazarrConfigAPI(runtime *runtimeSubtitleProvider) {
	http.HandleFunc("/api/v1/config/bazarr", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeBazarrConfigResponse(w, gc().Bazarr)
		case http.MethodPut:
			var req bazarrConfigUpdate
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			cfg := *gc()
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
			globalConfig.Store(&cfg)
			if runtime != nil {
				runtime.Set(newBazarrSubtitleProvider(cfg.Bazarr))
			}
			clearBazarrVirtualSRTCache()
			if globalDirCache != nil {
				globalDirCache.Delete(cfg.PhysicalSourcePath)
				globalDirCache.Delete(cfg.FuseMountPath)
			}
			writeBazarrConfigResponse(w, cfg.Bazarr)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func writeBazarrConfigResponse(w http.ResponseWriter, cfg cfgpkg.BazarrConfig) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(bazarrConfigResponse{
		Enabled:        cfg.Enabled,
		URL:            cfg.URL,
		TimeoutSeconds: cfg.TimeoutSeconds,
		MaxResults:     cfg.MaxResults,
		HasAPIKey:      cfg.APIKey != "",
	})
}
