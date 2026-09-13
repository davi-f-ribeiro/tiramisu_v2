package arr

import (
	"context"
	"log"
	"sync"
	"time"

	"tiramisu/internal/catalog/tmdb"
	"tiramisu/internal/metadb"
)

// resolveCacheEntry holds a cached TMDB resolution result.
type resolveCacheEntry struct {
	tmdbID        int64
	releaseDate   string
	expiresAt     time.Time
}

// ResolveConfig holds configuration for the TMDB resolver.
type ResolveConfig struct {
	Client     *tmdb.Client
	DB         *metadb.DB
	Logger     *log.Logger
	MaxRetry   int
	RetryDelay time.Duration
}

// DefaultResolveConfig returns a config with sensible defaults.
func DefaultResolveConfig(logger *log.Logger) *ResolveConfig {
	return &ResolveConfig{
		Logger:     logger,
		MaxRetry:   3,
		RetryDelay: 2 * time.Second,
	}
}

// TMDBResolver resolves missing TMDB IDs for ARR media records in the background.
type TMDBResolver struct {
	config *ResolveConfig
	cache  sync.Map
}

// NewTMDBResolver creates a new resolver.
func NewTMDBResolver(cfg *ResolveConfig) *TMDBResolver {
	return &TMDBResolver{
		config: cfg,
	}
}

// Run starts the background resolver. Returns immediately.
func (r *TMDBResolver) Run(ctx context.Context) {
	go r.run(ctx)
}

func (r *TMDBResolver) run(ctx context.Context) {
	r.config.Logger.Printf("[TMDB resolver] started")

	// Wait 5s so the server is fully ready before resolving.
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return
	}

	// Run initial resolution, then every 30 minutes.
	r.resolveOnce(ctx)
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.config.Logger.Printf("[TMDB resolver] stopped")
			return
		case <-ticker.C:
			r.resolveOnce(ctx)
		}
	}
}

func (r *TMDBResolver) resolveOnce(ctx context.Context) {
	unresolved, err := r.config.DB.GetUnresolvedARRMedia(ctx)
	if err != nil {
		r.config.Logger.Printf("[TMDB resolver] query error: %v", err)
		return
	}
	if len(unresolved) == 0 {
		return
	}

	resolved := 0
	errored := 0

	for _, media := range unresolved {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Cache lookup (1h TTL).
		if entry, ok := r.cache.Load(media.IMDBID); ok {
			if e := entry.(*resolveCacheEntry); time.Now().Before(e.expiresAt) {
				if e.tmdbID > 0 {
					year := extractYearFrom(e.releaseDate)
					if err := r.config.DB.UpsertARRMediaTMDB(ctx, media.ID, e.tmdbID, year); err != nil {
						r.config.Logger.Printf("[TMDB resolver] upsert cache hit %s: %v", media.IMDBID, err)
						errored++
					} else {
						resolved++
					}
				}
				continue
			}
			r.cache.Delete(media.IMDBID)
		}

		// Skip non-IMDb IDs.
		if media.IMDBID == "" || len(media.IMDBID) < 2 {
			continue
		}

		// Retry loop.
		var tmdbID int64
		var releaseDate string
		var lastErr error

		for attempt := 0; attempt < r.config.MaxRetry; attempt++ {
			if attempt > 0 {
				select {
				case <-time.After(r.config.RetryDelay * time.Duration(attempt)):
				case <-ctx.Done():
					return
				}
			}
			tmdbID, releaseDate, lastErr = r.config.Client.FindByIMDbID(ctx, media.IMDBID)
			if lastErr == nil {
				break
			}
		}

		if lastErr != nil {
			r.config.Logger.Printf("[TMDB resolver] failed %s after %d attempts: %v", media.IMDBID, r.config.MaxRetry, lastErr)
			errored++
			continue
		}

		// Cache result for 1h.
		r.cache.Store(media.IMDBID, &resolveCacheEntry{
			tmdbID:      tmdbID,
			releaseDate: releaseDate,
			expiresAt:   time.Now().Add(1 * time.Hour),
		})

		year := extractYearFrom(releaseDate)
		if err := r.config.DB.UpsertARRMediaTMDB(ctx, media.ID, tmdbID, year); err != nil {
			r.config.Logger.Printf("[TMDB resolver] upsert %s: %v", media.IMDBID, err)
			errored++
		} else {
			resolved++
		}

		// Rate limit: 1s between requests.
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return
		}
	}

	if resolved > 0 || errored > 0 {
		r.config.Logger.Printf("[TMDB resolver] done: %d resolved, %d errored (%d total)", resolved, errored, len(unresolved))
	}
}

// extractYearFrom extracts YYYY from a "YYYY-MM-DD" date string.
func extractYearFrom(date string) int {
	if len(date) >= 4 {
		year := 0
		for i := 0; i < 4 && i < len(date); i++ {
			d := date[i] - '0'
			if d > 9 {
				return 0
			}
			year = year*10 + int(d)
		}
		return year
	}
	return 0
}
