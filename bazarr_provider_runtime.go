package main

import (
	"context"
	"sync"

	cfgpkg "tiramisu/internal/config"
	"tiramisu/internal/subprovider"
)

type runtimeSubtitleProvider struct {
	mu       sync.RWMutex
	provider subprovider.SubtitleProvider
}

func newRuntimeSubtitleProvider(cfg cfgpkg.BazarrConfig) *runtimeSubtitleProvider {
	r := &runtimeSubtitleProvider{}
	r.Set(newBazarrSubtitleProvider(cfg))
	return r
}

func newBazarrSubtitleProvider(cfg cfgpkg.BazarrConfig) subprovider.SubtitleProvider {
	return subprovider.NewBazarrClient(subprovider.BazarrConfig{
		Enabled:        cfg.Enabled,
		URL:            cfg.URL,
		APIKey:         cfg.APIKey,
		TimeoutSeconds: cfg.TimeoutSeconds,
		MaxResults:     cfg.MaxResults,
	})
}

func (r *runtimeSubtitleProvider) Set(p subprovider.SubtitleProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.provider = p
}

func (r *runtimeSubtitleProvider) IsEnabled() bool {
	r.mu.RLock()
	p := r.provider
	r.mu.RUnlock()
	return p != nil && p.IsEnabled()
}

func (r *runtimeSubtitleProvider) Search(ctx context.Context, mediaID int, title string, language string) ([]subprovider.SubtitleCandidate, error) {
	r.mu.RLock()
	p := r.provider
	r.mu.RUnlock()
	if p == nil {
		return nil, nil
	}
	return p.Search(ctx, mediaID, title, language)
}

func (r *runtimeSubtitleProvider) Download(ctx context.Context, subtitleID string, destPath string) error {
	r.mu.RLock()
	p := r.provider
	r.mu.RUnlock()
	if p == nil {
		return nil
	}
	return p.Download(ctx, subtitleID, destPath)
}
