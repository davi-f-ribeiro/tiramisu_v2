package arr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MediaStore provides data access for the HTTP API handler.
type MediaStore interface {
	GetMovies(ctx context.Context) ([]*RadarrMovie, error)
	GetMovieByID(ctx context.Context, id int64) (*RadarrMovie, error)
	GetSeries(ctx context.Context) ([]*SonarrSeries, error)
	GetSeriesByID(ctx context.Context, id int64) (*SonarrSeries, error)
	GetEpisodesBySeries(ctx context.Context, seriesID int64) ([]*SonarrEpisode, error)
	GetEpisodeFilesBySeries(ctx context.Context, seriesID int64) ([]*SonarrEpisodeFile, error)
}

// DBStore adapts *sql.DB to both MediaStore and MediaStoreWriter interfaces.
type DBStore struct {
	db *sql.DB
}

// NewDBStore creates a MediaStore from an existing *sql.DB.
func NewDBStore(db *sql.DB) *DBStore {
	return &DBStore{db: db}
}

// --- MediaStore read methods ---

// GetMovies returns all movies from arr_media.
func (s *DBStore) GetMovies(ctx context.Context) ([]*RadarrMovie, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, year, path, tmdb_id, imdb_id, size, updated_at
		FROM arr_media WHERE media_type = 'movie' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("arr: get movies: %w", err)
	}
	defer rows.Close()

	var movies []*RadarrMovie
	for rows.Next() {
		var updatedAt string
		var m RadarrMovie
		if err := rows.Scan(&m.ID, &m.Title, &m.Year, &m.Path,
			&m.TmdbID, &m.ImdbID, &m.Size, &updatedAt); err != nil {
			return nil, fmt.Errorf("arr: scan movie: %w", err)
		}
		m.HasFile = true
		m.IsAvailable = true
		m.Monitored = true
		movies = append(movies, &m)
	}
	return movies, rows.Err()
}

// GetMovieByID returns a single movie by its arr_media id.
func (s *DBStore) GetMovieByID(ctx context.Context, id int64) (*RadarrMovie, error) {
	var m RadarrMovie
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, year, path, tmdb_id, imdb_id, size, updated_at
		FROM arr_media WHERE id = $1 AND media_type = 'movie'`, id).
		Scan(&m.ID, &m.Title, &m.Year, &m.Path, &m.TmdbID, &m.ImdbID, &m.Size, &updatedAt)
	if err != nil {
		return nil, err
	}
	m.HasFile = true
	m.IsAvailable = true
	m.Monitored = true
	return &m, nil
}

// UpsertARRMovie inserts or updates a movie record (MediaStoreWriter).
func (s *DBStore) UpsertARRMovie(ctx context.Context, tmdbID int64, imdbID, title string, year int, fullPath string, size int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO arr_media (media_type, tmdb_id, imdb_id, series_id, season_number, episode_number, title, year, path, size)
		 VALUES ('movie', $1, $2, 0, 0, 0, $3, $4, $5, $6)
		 ON CONFLICT(path) DO UPDATE SET
			 tmdb_id=EXCLUDED.tmdb_id, imdb_id=EXCLUDED.imdb_id, series_id=0,
			 season_number=0, episode_number=0, title=EXCLUDED.title,
			 year=EXCLUDED.year, size=EXCLUDED.size, updated_at=datetime('now')`,
		tmdbID, imdbID, title, year, fullPath, size)
	return err
}

// UpsertARRSeries inserts or updates a series record and returns its id.
func (s *DBStore) UpsertARRSeries(ctx context.Context, tmdbID, tvdbID int64, imdbID, title, seriesDir string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO arr_media (media_type, tmdb_id, tvdb_id, imdb_id, series_id, season_number, episode_number, title, path)
		 VALUES ('series', $1, $2, $3, 0, 0, 0, $4, $5)
		 ON CONFLICT(path) DO UPDATE SET
			 tmdb_id=EXCLUDED.tmdb_id, tvdb_id=EXCLUDED.tvdb_id, imdb_id=EXCLUDED.imdb_id,
			 title=EXCLUDED.title, updated_at=datetime('now')
		 RETURNING id`,
		tmdbID, tvdbID, imdbID, title, seriesDir).Scan(&id)
	return id, err
}

// UpsertARREpisode inserts or updates an episode record.
func (s *DBStore) UpsertARREpisode(ctx context.Context, seriesID int64, season, episode int, title, fullPath string, size int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO arr_media (media_type, series_id, season_number, episode_number, title, path, size)
		 VALUES ('episode', $1, $2, $3, $4, $5, $6)
		 ON CONFLICT(path) DO UPDATE SET
			 series_id=EXCLUDED.series_id, season_number=EXCLUDED.season_number,
			 episode_number=EXCLUDED.episode_number, title=EXCLUDED.title,
			 size=EXCLUDED.size, updated_at=datetime('now')`,
		seriesID, season, episode, title, fullPath, size)
	return err
}

// DeleteARRMediaByPath removes a record by its path.
func (s *DBStore) DeleteARRMediaByPath(ctx context.Context, fullPath string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM arr_media WHERE path = $1", fullPath)
	return err
}

// GetSeries returns all series from arr_media.
func (s *DBStore) GetSeries(ctx context.Context) ([]*SonarrSeries, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, tvdb_id, imdb_id, path
		FROM arr_media WHERE media_type = 'series' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("arr: get series: %w", err)
	}
	defer rows.Close()

	var series []*SonarrSeries
	for rows.Next() {
		var s SonarrSeries
		if err := rows.Scan(&s.ID, &s.Title, &s.TvdbID, &s.ImdbID, &s.Path); err != nil {
			return nil, fmt.Errorf("arr: scan series: %w", err)
		}
		s.Monitored = true
		series = append(series, &s)
	}
	return series, rows.Err()
}

// GetSeriesByID returns a single series by its arr_media id.
func (s *DBStore) GetSeriesByID(ctx context.Context, id int64) (*SonarrSeries, error) {
	var series SonarrSeries
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, tvdb_id, imdb_id, path
		FROM arr_media WHERE id = $1 AND media_type = 'series'`, id).
		Scan(&series.ID, &series.Title, &series.TvdbID, &series.ImdbID, &series.Path)
	if err != nil {
		return nil, err
	}
	series.Monitored = true
	return &series, nil
}

// GetEpisodesBySeries returns all episodes for a given series ID.
func (s *DBStore) GetEpisodesBySeries(ctx context.Context, seriesID int64) ([]*SonarrEpisode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, series_id, season_number, episode_number, title, size, updated_at
		FROM arr_media WHERE media_type = 'episode' AND series_id = $1 ORDER BY season_number, episode_number`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("arr: get episodes: %w", err)
	}
	defer rows.Close()

	var eps []*SonarrEpisode
	for rows.Next() {
		var updatedAt string
		var e SonarrEpisode
		if err := rows.Scan(&e.ID, &e.SeriesID, &e.SeasonNumber, &e.EpisodeNumber,
			&e.Title, &e.Size, &updatedAt); err != nil {
			return nil, fmt.Errorf("arr: scan episode: %w", err)
		}
		e.HasFile = true
		e.Monitored = true
		eps = append(eps, &e)
	}
	return eps, rows.Err()
}

// GetEpisodeFilesBySeries returns all episode files for a given series ID.
func (s *DBStore) GetEpisodeFilesBySeries(ctx context.Context, seriesID int64) ([]*SonarrEpisodeFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, series_id, season_number, path, size, updated_at
		FROM arr_media WHERE media_type = 'episode' AND series_id = $1 ORDER BY season_number, episode_number`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("arr: get episode files: %w", err)
	}
	defer rows.Close()

	var files []*SonarrEpisodeFile
	for rows.Next() {
		var updatedAt string
		var f SonarrEpisodeFile
		if err := rows.Scan(&f.ID, &f.SeriesID, &f.SeasonNumber, &f.Path, &f.Size, &updatedAt); err != nil {
			return nil, fmt.Errorf("arr: scan episode file: %w", err)
		}
		f.DateAdded = updatedAt
		f.RelativePath = f.Path // full path as fallback
		files = append(files, &f)
	}
	return files, rows.Err()
}

// Handler serves the Servarr v3 API.
type Handler struct {
	store        MediaStore
	storeWriter  MediaStoreWriter // nil for read-only (standalone listeners)
	appName      string
	urlBase      string
	moviesDir    string
	tvDir        string
	logger       *log.Logger
	mu           sync.RWMutex
}

// NewHandler creates a new Handler with the given store and optional app name override.
func NewHandler(store MediaStore, opts ...HandlerOption) *Handler {
	h := &Handler{
		store:   store,
		appName: "Radarr",
		urlBase: "",
		moviesDir: "",
		tvDir:       "",
		logger:    log.Default(),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// HandlerOption configures a Handler.
type HandlerOption func(*Handler)

// WithAppName sets the application name.
func WithAppName(name string) HandlerOption {
	return func(h *Handler) { h.appName = name }
}

// WithDirs sets the movies and TV directories for manual sync.
func WithDirs(moviesDir, tvDir string) HandlerOption {
	return func(h *Handler) {
		h.moviesDir = moviesDir
		h.tvDir = tvDir
	}
}

// WithLogger sets the logger.
func WithLogger(l *log.Logger) HandlerOption {
	return func(h *Handler) { h.logger = l }
}

// WithStoreWriter sets the MediaStoreWriter for backfill operations.
func WithStoreWriter(w MediaStoreWriter) HandlerOption {
	return func(h *Handler) { h.storeWriter = w }
}

// SetAppName overrides the application name.
func (h *Handler) SetAppName(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.appName = name
}

func (h *Handler) getAppName() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.appName
}

// RegisterRoutes mounts all API routes on the given ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Versioned API routes
	mux.HandleFunc("GET /api/v3/system/status", h.handleSystemStatus)
	mux.HandleFunc("GET /api/v3/rootfolder", h.handleRootFolder)
	mux.HandleFunc("GET /api/v3/qualityprofile", h.handleQualityProfile)
	mux.HandleFunc("GET /api/v3/tag", h.handleTag)
	mux.HandleFunc("GET /api/v3/languageprofile", h.handleEmptyArray)
	mux.HandleFunc("GET /api/v3/movie", h.handleMovieList)
	mux.HandleFunc("/api/v3/movie/", h.handleMovieDetail)
	mux.HandleFunc("GET /api/v3/series", h.handleSeriesList)
	mux.HandleFunc("/api/v3/series/", h.handleSeriesDetail)
	mux.HandleFunc("GET /api/v3/episode", h.handleEpisodesBySeries)
	mux.HandleFunc("GET /api/v3/episodefile", h.handleEpisodeFilesBySeries)

	// Legacy unversioned routes (Bazarr sometimes uses these)
	mux.HandleFunc("/api/system/status", h.handleSystemStatus)
	mux.HandleFunc("/api/rootfolder", h.handleRootFolder)
	mux.HandleFunc("/api/qualityprofile", h.handleQualityProfile)
	mux.HandleFunc("/api/tag", h.handleTag)
	mux.HandleFunc("/api/languageprofile", h.handleEmptyArray)
	mux.HandleFunc("/api/movie", h.handleMovieList)
	mux.HandleFunc("/api/movie/", h.handleMovieDetail)
	mux.HandleFunc("/api/series", h.handleSeriesList)
	mux.HandleFunc("/api/series/", h.handleSeriesDetail)
	mux.HandleFunc("/api/episode", h.handleEpisodesBySeries)
	mux.HandleFunc("/api/episodefile", h.handleEpisodeFilesBySeries)

	// Manual sync endpoints
	mux.HandleFunc("POST /api/v3/arr/sync", h.handleManualSync)
	mux.HandleFunc("POST /api/arr/sync", h.handleManualSync)

	// SignalR Handshake/Negotiate (evita HubError no Bazarr)
	// Legacy paths
	mux.HandleFunc("/signalr/negotiate", h.handleSignalRNegotiate)
	mux.HandleFunc("/signalr", h.handleSignalRNegotiate)
	// V3 paths (Bazarr signalrcore library uses negotiateVersion=1)
	mux.HandleFunc("/api/v3/signalr/negotiate", h.handleSignalRNegotiate)
	mux.HandleFunc("/api/v3/signalr", h.handleSignalRNegotiate)

	// Endpoints adicionais que o Bazarr consulta no sync
	mux.HandleFunc("/api/v3/command", h.handleEmptyArray)
	mux.HandleFunc("/api/v3/health", h.handleEmptyArray)
	mux.HandleFunc("/api/v3/diskspace", h.handleEmptyArray)
}

// jsonResponse writes a JSON response with proper headers.
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *Handler) handleManualSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.storeWriter == nil {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "error",
			"message":      "backfill not configured",
			"movies_indexed":   0,
			"series_indexed":   0,
			"episodes_indexed": 0,
			"errors_count":     0,
			"duration_ms":      0,
		})
		return
	}
	if h.moviesDir == "" && h.tvDir == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]any{
			"status":       "error",
			"message":      "no media directories configured",
			"movies_indexed":   0,
			"series_indexed":   0,
			"episodes_indexed": 0,
			"errors_count":     0,
			"duration_ms":      0,
		})
		return
	}

	stats, err := RunBackfill(r.Context(), h.moviesDir, h.tvDir, h.storeWriter, h.logger)
	if err != nil && h.logger != nil {
		h.logger.Printf("[ARR sync] backfill error: %v", err)
	}

	resp := map[string]any{
		"status":           "success",
		"movies_indexed":   stats.MoviesIndexed,
		"series_indexed":   stats.SeriesIndexed,
		"episodes_indexed": stats.EpisodesIndexed,
		"errors_count":     stats.ErrorsCount,
		"duration_ms":      stats.DurationMs,
	}
	if err != nil {
		resp["status"] = "error"
		resp["message"] = err.Error()
	}
	jsonResponse(w, http.StatusOK, resp)
}

func (h *Handler) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	appName := h.getAppName()
	if r.URL.Query().Get("app") == "sonarr" {
		appName = "Sonarr"
	}
	host := r.Host
	if strings.Contains(host, ":8989") {
		appName = "Sonarr"
	}

	resp := SystemStatusResponse{
		Version:           "4.8.2",
		BuildTime:         "2024-01-15T00:00:00.0000000Z",
		IsDebug:           false,
		IsProduction:      true,
		IsAdmin:           true,
		IsUserInteractive: true,
		OsName:            "linux",
		OsVersion:         "6.1.0",
		IsLinux:           true,
		IsDocker:          true,
		Mode:              "cli",
		Authentication:    "forms",
		UrlBase:           h.urlBase,
		PackageVersion:    "4.8.2",
		AppName:           appName,
		InstanceName:      appName,
	}
	jsonResponse(w, http.StatusOK, resp)
}

func (h *Handler) handleRootFolder(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, []any{})
}

func (h *Handler) handleQualityProfile(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, []any{})
}

func (h *Handler) handleTag(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, []any{})
}

func (h *Handler) handleMovieList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	movies, err := h.store.GetMovies(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if movies == nil {
		movies = []*RadarrMovie{}
	}
	jsonResponse(w, http.StatusOK, movies)
}

func (h *Handler) handleMovieDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pathParts := strings.TrimPrefix(r.URL.Path, "/api/v3/movie/")
	pathParts = strings.TrimPrefix(pathParts, "/api/movie/")
	id, err := strconv.ParseInt(pathParts, 10, 64)
	if err != nil {
		http.Error(w, "invalid movie id", http.StatusBadRequest)
		return
	}

	movie, err := h.store.GetMovieByID(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "movie not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, movie)
}

func (h *Handler) handleSeriesList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	series, err := h.store.GetSeries(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if series == nil {
		series = []*SonarrSeries{}
	}
	jsonResponse(w, http.StatusOK, series)
}

func (h *Handler) handleSeriesDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pathParts := strings.TrimPrefix(r.URL.Path, "/api/v3/series/")
	pathParts = strings.TrimPrefix(pathParts, "/api/series/")
	id, err := strconv.ParseInt(pathParts, 10, 64)
	if err != nil {
		http.Error(w, "invalid series id", http.StatusBadRequest)
		return
	}

	series, err := h.store.GetSeriesByID(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "series not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, series)
}

func (h *Handler) handleEpisodesBySeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	seriesIDStr := r.URL.Query().Get("seriesId")
	if seriesIDStr == "" {
		jsonResponse(w, http.StatusOK, []*SonarrEpisode{})
		return
	}
	seriesID, err := strconv.ParseInt(seriesIDStr, 10, 64)
	if err != nil {
		jsonResponse(w, http.StatusOK, []*SonarrEpisode{})
		return
	}

	eps, err := h.store.GetEpisodesBySeries(ctx, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if eps == nil {
		eps = []*SonarrEpisode{}
	}
	jsonResponse(w, http.StatusOK, eps)
}

func (h *Handler) handleEpisodeFilesBySeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	seriesIDStr := r.URL.Query().Get("seriesId")
	if seriesIDStr == "" {
		jsonResponse(w, http.StatusOK, []*SonarrEpisodeFile{})
		return
	}
	seriesID, err := strconv.ParseInt(seriesIDStr, 10, 64)
	if err != nil {
		jsonResponse(w, http.StatusOK, []*SonarrEpisodeFile{})
		return
	}

	files, err := h.store.GetEpisodeFilesBySeries(ctx, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if files == nil {
		files = []*SonarrEpisodeFile{}
	}
	jsonResponse(w, http.StatusOK, files)
}

// handleSignalRNegotiate answers SignalR handshake requests so Bazarr doesn't log HubError.
func (h *Handler) handleSignalRNegotiate(w http.ResponseWriter, r *http.Request) {
	res := map[string]any{
		"Url":                     "/signalr",
		"ConnectionToken":         "tiramisu-dummy-token",
		"ConnectionId":            "tiramisu-dummy-id",
		"KeepAliveTimeout":        20.0,
		"DisconnectTimeout":       30.0,
		"ConnectionTimeout":       110.0,
		"TryWebSockets":           false,
		"ProtocolVersion":         "1.4",
		"TransportConnectTimeout": 5.0,
		"LongPollDelay":           0.0,
		"negotiateVersion":        1,
		"connectionId":            "tiramisu-dummy-id",
		"availableTransports":     []map[string]any{},
	}
	jsonResponse(w, http.StatusOK, res)
}

// handleEmptyArray responds with an empty JSON array for endpoints Bazarr queries.
func (h *Handler) handleEmptyArray(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, []any{})
}

// arrServers holds references to the Radarr and Sonarr HTTP servers
// so they can be gracefully shut down on context cancellation.
type arrServers struct {
	radarrSrv *http.Server
	sonarrSrv *http.Server
}

// StartStandaloneListeners launches Radarr (7878) and Sonarr (8989) HTTP
// servers in background goroutines so Bazarr can target distinct ports.
// Returns an arrServers struct so the caller can perform graceful shutdown.
func StartStandaloneListeners(ctx context.Context, store MediaStore) *arrServers {
	radarrSrv := &http.Server{Addr: ":7878"}
	sonarrSrv := &http.Server{Addr: ":8989"}

	go func() {
		mux := http.NewServeMux()
		h := NewHandler(store)
		h.RegisterRoutes(mux)
		radarrSrv.Handler = mux
		log.Printf("[arr] starting Radarr server on :7878")
		if err := radarrSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[arr] radarr server error: %v", err)
		}
	}()

	go func() {
		mux := http.NewServeMux()
		h := NewHandler(store, WithAppName("Sonarr"))
		h.RegisterRoutes(mux)
		sonarrSrv.Handler = mux
		log.Printf("[arr] starting Sonarr server on :8989")
		if err := sonarrSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[arr] sonarr server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		log.Println("[arr] stopping standalone listeners")
		// Graceful shutdown with 5s timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		radarrSrv.Shutdown(shutdownCtx)
		sonarrSrv.Shutdown(shutdownCtx)
	}()

	return &arrServers{radarrSrv: radarrSrv, sonarrSrv: sonarrSrv}
}
