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

// DBStore adapts *sql.DB to the MediaStore interface.
type DBStore struct {
	db *sql.DB
}

// NewDBStore creates a MediaStore from an existing *sql.DB.
func NewDBStore(db *sql.DB) *DBStore {
	return &DBStore{db: db}
}

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
	store   MediaStore
	appName string
	urlBase string
	mu      sync.RWMutex
}

// NewHandler creates a new Handler with the given store and optional app name override.
func NewHandler(store MediaStore, appName ...string) *Handler {
	h := &Handler{
		store:   store,
		appName: "Radarr",
		urlBase: "",
	}
	if len(appName) > 0 && appName[0] != "" {
		h.appName = appName[0]
	}
	return h
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
	mux.HandleFunc("/api/movie", h.handleMovieList)
	mux.HandleFunc("/api/movie/", h.handleMovieDetail)
	mux.HandleFunc("/api/series", h.handleSeriesList)
	mux.HandleFunc("/api/series/", h.handleSeriesDetail)
	mux.HandleFunc("/api/episode", h.handleEpisodesBySeries)
	mux.HandleFunc("/api/episodefile", h.handleEpisodeFilesBySeries)
}

// jsonResponse writes a JSON response with proper headers.
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
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

// StartStandaloneListeners launches Radarr (7878) and Sonarr (8989) HTTP
// servers in background goroutines so Bazarr can target distinct ports.
func StartStandaloneListeners(ctx context.Context, store MediaStore) {
	go func() {
		mux := http.NewServeMux()
		h := NewHandler(store)
		h.RegisterRoutes(mux)
		srv := &http.Server{Addr: ":7878", Handler: mux}
		log.Printf("[arr] starting Radarr server on :7878")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[arr] radarr server error: %v", err)
		}
	}()

	go func() {
		mux := http.NewServeMux()
		h := NewHandler(store, "Sonarr")
		h.RegisterRoutes(mux)
		srv := &http.Server{Addr: ":8989", Handler: mux}
		log.Printf("[arr] starting Sonarr server on :8989")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[arr] sonarr server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[arr] stopping standalone listeners")
}
