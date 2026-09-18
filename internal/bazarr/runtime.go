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
	r := &RuntimeProvider{}
	r.Set(NewSubtitleProvider(cfg))
	return r
}

// NewSubtitleProvider builds the concrete Bazarr provider from application config.
func NewSubtitleProvider(cfg cfgpkg.BazarrConfig) subprovider.SubtitleProvider {
	return subprovider.NewBazarrClient(subprovider.BazarrConfig{
		Enabled:        cfg.Enabled,
		URL:            cfg.URL,
		APIKey:         cfg.APIKey,
		TimeoutSeconds: cfg.TimeoutSeconds,
		MaxResults:     cfg.MaxResults,
	})
}

// Set replaces the active provider.
func (r *RuntimeProvider) Set(p subprovider.SubtitleProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.provider = p
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
