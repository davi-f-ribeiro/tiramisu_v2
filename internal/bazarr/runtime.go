package bazarr

import (
	"context"
	"sync"

	cfgpkg "tiramisu/internal/config"
	"tiramisu/internal/subprovider"
)

// RuntimeProvider is a thread-safe, hot-swappable SubtitleProvider wrapper.
type RuntimeProvider struct {
	mu       sync.RWMutex
	provider subprovider.SubtitleProvider
}

// NewRuntimeProvider creates a provider from config and wraps it for hot reloads.
func NewRuntimeProvider(cfg cfgpkg.BazarrConfig) *RuntimeProvider {
	client := subprovider.NewBazarrClient(toSubproviderConfig(cfg))
	return &RuntimeProvider{provider: client}
}

// NewSubtitleProvider builds the concrete Bazarr provider from application config.
func NewSubtitleProvider(cfg cfgpkg.BazarrConfig) subprovider.SubtitleProvider {
	return subprovider.NewBazarrClient(toSubproviderConfig(cfg))
}

func toSubproviderConfig(cfg cfgpkg.BazarrConfig) subprovider.BazarrConfig {
	return subprovider.BazarrConfig{
		Enabled:        cfg.Enabled,
		URL:            cfg.URL,
		APIKey:         cfg.APIKey,
		TimeoutSeconds: cfg.TimeoutSeconds,
		MaxResults:     cfg.MaxResults,
	}
}

// Set replaces the active provider.
func (r *RuntimeProvider) Set(p subprovider.SubtitleProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.provider = p
}

// UpdateConfig hot-reloads the active provider in place when possible.
func (r *RuntimeProvider) UpdateConfig(cfg subprovider.BazarrConfig) {
	if r == nil {
		return
	}
	r.mu.RLock()
	p := r.provider
	r.mu.RUnlock()
	if p != nil {
		p.UpdateConfig(cfg)
		return
	}
	r.Set(subprovider.NewBazarrClient(cfg))
}

// IsEnabled reports whether the active provider is configured and enabled.
func (r *RuntimeProvider) IsEnabled() bool {
	r.mu.RLock()
	p := r.provider
	r.mu.RUnlock()
	return p != nil && p.IsEnabled()
}

// Search delegates to the active provider.
func (r *RuntimeProvider) Search(ctx context.Context, mediaID int, title string, language string) ([]subprovider.SubtitleCandidate, error) {
	r.mu.RLock()
	p := r.provider
	r.mu.RUnlock()
	if p == nil {
		return nil, nil
	}
	return p.Search(ctx, mediaID, title, language)
}

// Download delegates to the active provider.
func (r *RuntimeProvider) Download(ctx context.Context, subtitleID string, destPath string) error {
	r.mu.RLock()
	p := r.provider
	r.mu.RUnlock()
	if p == nil {
		return nil
	}
	return p.Download(ctx, subtitleID, destPath)
}
