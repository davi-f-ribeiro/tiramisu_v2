package arr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
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
		SELECT id, title, year, path, tmdb_id, imdb_id, raw_title, size, updated_at, poster_path, backdrop_path
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
			&m.TmdbID, &m.ImdbID, &m.RawTitle, &m.Size, &updatedAt,
			&m.PosterPath, &m.BackdropPath); err != nil {
			return nil, fmt.Errorf("arr: scan movie: %w", err)
		}
		m.HasFile = true
		m.IsAvailable = true
		m.Monitored = true
		m.AlternativeTitles = []AlternativeTitle{}
		m.Genres = []string{}
		m.Tags = []int{}
		m.Images = []MediaImage{}
		movies = append(movies, &m)
	}
	return movies, rows.Err()
}

// GetMovieByID returns a single movie by its arr_media id.
func (s *DBStore) GetMovieByID(ctx context.Context, id int64) (*RadarrMovie, error) {
	var m RadarrMovie
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, year, path, tmdb_id, imdb_id, raw_title, size, updated_at, poster_path, backdrop_path
		FROM arr_media WHERE id = $1 AND media_type = 'movie'`, id).
		Scan(&m.ID, &m.Title, &m.Year, &m.Path, &m.TmdbID, &m.ImdbID, &m.RawTitle, &m.Size, &updatedAt,
			&m.PosterPath, &m.BackdropPath)
	if err != nil {
		return nil, err
	}
	m.HasFile = true
	m.IsAvailable = true
	m.Monitored = true
	m.AlternativeTitles = []AlternativeTitle{}
	m.Genres = []string{}
	m.Tags = []int{}
	m.Images = []MediaImage{}
	return &m, nil
}

func (s *DBStore) UpsertARRMovie(ctx context.Context, tmdbID int64, imdbID, title, rawTitle string, year int, fullPath string, size int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO arr_media (media_type, tmdb_id, imdb_id, raw_title, series_id, season_number, episode_number, title, year, path, size)
		 VALUES ('movie', $1, $2, $3, 0, 0, 0, $4, $5, $6, $7)
		 ON CONFLICT(path) DO UPDATE SET
		 tmdb_id=EXCLUDED.tmdb_id, imdb_id=EXCLUDED.imdb_id, raw_title=EXCLUDED.raw_title, series_id=0,
		 season_number=0, episode_number=0, title=EXCLUDED.title,
		 year=EXCLUDED.year, size=EXCLUDED.size, updated_at=datetime('now')`,
		tmdbID, imdbID, rawTitle, title, year, fullPath, size)
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
		SELECT id, title, tvdb_id, imdb_id, path, poster_path, backdrop_path
		FROM arr_media WHERE media_type = 'series' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("arr: get series: %w", err)
	}
	defer rows.Close()

	var series []*SonarrSeries
	for rows.Next() {
		var s SonarrSeries
		if err := rows.Scan(&s.ID, &s.Title, &s.TvdbID, &s.ImdbID, &s.Path,
			&s.PosterPath, &s.BackdropPath); err != nil {
			return nil, fmt.Errorf("arr: scan series: %w", err)
		}
		s.Monitored = true
		s.AlternativeTitles = []AlternativeTitle{}
		s.Genres = []string{}
		s.Tags = []int{}
		s.Images = []MediaImage{}
		series = append(series, &s)
	}
	return series, rows.Err()
}

// GetSeriesByID returns a single series by its arr_media id.
func (s *DBStore) GetSeriesByID(ctx context.Context, id int64) (*SonarrSeries, error) {
	var series SonarrSeries
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, tvdb_id, imdb_id, path, poster_path, backdrop_path
		FROM arr_media WHERE id = $1 AND media_type = 'series'`, id).
		Scan(&series.ID, &series.Title, &series.TvdbID, &series.ImdbID, &series.Path,
			&series.PosterPath, &series.BackdropPath)
	if err != nil {
		return nil, err
	}
	series.Monitored = true
	series.AlternativeTitles = []AlternativeTitle{}
	series.Genres = []string{}
	series.Tags = []int{}
	series.Images = []MediaImage{}
	series.SeasonCount = 1
	series.Seasons = []SonarrSeason{{SeasonNumber: 1, Monitored: true}}
	return &series, nil
}

// GetEpisodesBySeries returns all episodes for a given series ID.
func (s *DBStore) GetEpisodesBySeries(ctx context.Context, seriesID int64) ([]*SonarrEpisode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, series_id, season_number, episode_number, title, path, size, updated_at
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
			&e.Title, &e.Path, &e.Size, &updatedAt); err != nil {
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
	store       MediaStore
	storeWriter MediaStoreWriter // nil for read-only (standalone listeners)
	appName     string
	urlBase     string
	moviesDir   string
	tvDir       string
	logger      *log.Logger
	resolver    *TMDBResolver // optional: JIT resolve tmdb_id from imdb_id
}

// NewHandler creates a new Handler with the given store and optional app name override.
func NewHandler(store MediaStore, opts ...HandlerOption) *Handler {
	h := &Handler{
		store:     store,
		appName:   "Radarr",
		urlBase:   "",
		moviesDir: "",
		tvDir:     "",
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

// WithResolver sets the TMDB resolver for JIT resolution of missing IDs.
func WithResolver(r *TMDBResolver) HandlerOption {
	return func(h *Handler) { h.resolver = r }
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

// parseQualityFromFilename infers resolution and source from a media filename
// so Bazarr indexes the exact release quality.
// When rawTitle is not empty it is used for both source and resolution detection;
// fileName is used as fallback and for resolution detection in legacy dummies.
func parseQualityFromFilename(rawTitle, fileName string) QualityModel {
	// Pick the best text to scan for source tags.
	text := rawTitle
	if text == "" {
		text = fileName
	}

	lower := strings.ToLower(text)

	// Resolution: prefer rawTitle, fall back to fileName
	res := 1080
	resStr := "1080p"
	resText := rawTitle
	if resText == "" {
		resText = fileName
	}
	resLower := strings.ToLower(resText)
	if strings.Contains(resLower, "2160p") || strings.Contains(resLower, "4k") {
		res = 2160
		resStr = "2160p"
	} else if strings.Contains(resLower, "720p") {
		res = 720
		resStr = "720p"
	} else if strings.Contains(resLower, "480p") {
		res = 480
		resStr = "480p"
	}

	// Source detection — full prioritized list
	source := "webdl"
	sourceName := "WEBDL"
	if strings.Contains(lower, "remux") {
		source = "bluray"
		sourceName = "Remux"
	} else if strings.Contains(lower, "bluray") || strings.Contains(lower, "bdrip") || strings.Contains(lower, "brrip") {
		source = "bluray"
		sourceName = "Bluray"
	} else if strings.Contains(lower, "web-dl") || strings.Contains(lower, "webdl") {
		source = "webdl"
		sourceName = "WEBDL"
	} else if strings.Contains(lower, "webrip") || strings.Contains(lower, "web-rip") {
		source = "webrip"
		sourceName = "WEBRip"
	} else if strings.Contains(lower, "hdtv") {
		source = "hdtv"
		sourceName = "HDTV"
	} else {
		// Legacy heuristic for dummies without raw_title
		if strings.Contains(lower, "truehd") || strings.Contains(lower, "dts-hd") || (res == 2160) {
			source = "bluray"
			sourceName = "Bluray"
		}
	}

	return QualityModel{
		Quality: QualityDetail{
			ID:         1,
			Name:       fmt.Sprintf("%s-%s", sourceName, resStr),
			Source:     source,
			Resolution: res,
		},
		Revision: RevisionDetail{
			Version:  1,
			Real:     0,
			IsRepack: false,
		},
	}
}

func (h *Handler) handleManualSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.storeWriter == nil {
		jsonResponse(w, http.StatusServiceUnavailable, map[string]any{
			"status":           "error",
			"message":          "backfill not configured",
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
			"status":           "error",
			"message":          "no media directories configured",
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
	appName := h.appName
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

// populateMovieFields enriches a RadarrMovie with computed title fields,
// availability flags, images, and an optional MovieFile subobject for Bazarr.
// Uniqueness guard: when tmdbId is absent (≤ 0) we fall back to the
// internal arr_media.id so Bazarr never sees duplicate zero values that
// trigger UNIQUE constraint failures.
func (h *Handler) populateMovieFields(ctx context.Context, m *RadarrMovie) {
	if m == nil {
		return
	}
	m.HasFile = true
	m.IsAvailable = true
	m.Monitored = true
	m.AlternativeTitles = []AlternativeTitle{}
	m.Genres = []string{}
	m.Tags = []int{}

	// JIT resolution: if tmdbId is zero and we have an imdbId, try to
	// resolve it synchronously from the TMDb API (with in-memory cache).
	if m.TmdbID <= 0 && m.ImdbID != "" && h.resolver != nil {
		if res, err := h.resolver.ResolveIMDbID(ctx, m.ImdbID); err == nil && res != nil && res.TmdbID > 0 {
			m.TmdbID = res.TmdbID
			if res.Title != "" {
				m.Title = res.Title
			}
			if m.Year == 0 && res.ReleaseDate != "" {
				m.Year = extractYearFrom(res.ReleaseDate)
			}
			m.PosterPath = res.PosterPath
			m.BackdropPath = res.BackdropPath
			// Persist to DB (fire-and-forget, non-blocking).
			h.resolver.PersistJITResult(m.ID, res)
		}
	}

	// Uniqueness fallback for Bazarr: tmdbId=0 would collide across
	// multiple stubs. When absent, use the unique internal ID instead.
	if m.TmdbID <= 0 {
		m.TmdbID = m.ID
	}

	// Compute title fields Bazarr requires.
	cleanTitle := strings.ReplaceAll(m.Title, "_", " ")
	if cleanTitle == "" {
		cleanTitle = strings.ReplaceAll(m.RawTitle, "_", " ")
	}
	m.SortTitle = strings.ToLower(cleanTitle)
	m.CleanTitle = cleanTitle
	m.TitleSlug = strings.ToLower(strings.ReplaceAll(cleanTitle, " ", "-"))
	m.Status = "released"

	// Mount TMDb image URLs into the images array.
	mountTMDBImages(m, m.PosterPath, m.BackdropPath)

	if m.MovieFile == nil && m.Path != "" {
		fullPath := m.Path
		basename := filepath.Base(fullPath)
		rawTitle := m.RawTitle
		if rawTitle == "" {
			rawTitle = m.Title // fallback: use title as rawTitle if not set
			if rawTitle == "" {
				rawTitle = basename
			}
		}
		m.RawTitle = rawTitle
		m.MovieFile = &RadarrMovieFile{
			ID:           m.ID,
			MovieID:      m.ID,
			RelativePath: basename,
			Path:         fullPath,
			Size:         m.Size,
			DateAdded:    time.Now().UTC().Format(time.RFC3339),
			Quality:      parseQualityFromFilename(m.RawTitle, basename),
		}
		// Directory path goes into m.Path.
		m.Path = filepath.Dir(fullPath)
	}
}

// mountTMDBImages builds the images array from TMDb poster/backdrop paths.
// Always initializes v.Images to ensure the JSON field is an empty array []
// rather than null when no images are available.
func mountTMDBImages(m interface{}, posterPath, backdropPath string) {
	switch v := m.(type) {
	case *RadarrMovie:
		images := make([]MediaImage, 0, 2)
		if posterPath != "" {
			images = append(images, MediaImage{
				CoverType: "poster",
				URL:       "https://image.tmdb.org/t/p/w500" + posterPath,
				RemoteURL: "https://image.tmdb.org/t/p/w500" + posterPath,
			})
		}
		if backdropPath != "" {
			images = append(images, MediaImage{
				CoverType: "fanart",
				URL:       "https://image.tmdb.org/t/p/w1280" + backdropPath,
				RemoteURL: "https://image.tmdb.org/t/p/w1280" + backdropPath,
			})
		}
		v.Images = images
	case *SonarrSeries:
		images := make([]MediaImage, 0, 2)
		if posterPath != "" {
			images = append(images, MediaImage{
				CoverType: "poster",
				URL:       "https://image.tmdb.org/t/p/w500" + posterPath,
				RemoteURL: "https://image.tmdb.org/t/p/w500" + posterPath,
			})
		}
		if backdropPath != "" {
			images = append(images, MediaImage{
				CoverType: "fanart",
				URL:       "https://image.tmdb.org/t/p/w1280" + backdropPath,
				RemoteURL: "https://image.tmdb.org/t/p/w1280" + backdropPath,
			})
		}
		v.Images = images
	}
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
	for _, m := range movies {
		h.populateMovieFields(ctx, m)
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
	h.populateMovieFields(ctx, movie)
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
	// JIT resolution: if tvdbId is zero and we have an imdbId, try to
	// resolve it synchronously from the TMDb API (with in-memory cache).
	for _, s := range series {
		// Ensure all arrays are never null (Bazarr requires [] not None)
		s.AlternativeTitles = []AlternativeTitle{}
		s.Genres = []string{}
		s.Tags = []int{}
		s.SeasonCount = 1
		s.Seasons = []SonarrSeason{{SeasonNumber: 1, Monitored: true}}
		if s.TvdbID <= 0 && s.ImdbID != "" && h.resolver != nil {
			if res, err := h.resolver.ResolveIMDbID(ctx, s.ImdbID); err == nil && res != nil && res.TmdbID > 0 {
				s.TvdbID = res.TmdbID
				if res.Title != "" {
					s.Title = res.Title
				}
				s.PosterPath = res.PosterPath
				s.BackdropPath = res.BackdropPath
				h.resolver.PersistJITResult(s.ID, res)
			}
		}
		// Uniqueness fallback for Bazarr: tvdbId=0 would collide across
		// multiple stubs. When absent, use the unique internal ID instead.
		if s.TvdbID <= 0 {
			s.TvdbID = s.ID
		}
		// Mount TMDb image URLs into the images array.
		mountTMDBImages(s, s.PosterPath, s.BackdropPath)
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
	// JIT resolution: if tvdbId is zero and we have an imdbId, try to
	// resolve it synchronously from the TMDb API (with in-memory cache).
	if series.TvdbID <= 0 && series.ImdbID != "" && h.resolver != nil {
		if res, err := h.resolver.ResolveIMDbID(ctx, series.ImdbID); err == nil && res != nil && res.TmdbID > 0 {
			series.TvdbID = res.TmdbID
			if res.Title != "" {
				series.Title = res.Title
			}
			series.PosterPath = res.PosterPath
			series.BackdropPath = res.BackdropPath
			h.resolver.PersistJITResult(series.ID, res)
		}
	}
	// Uniqueness fallback for Bazarr: tvdbId=0 uses unique internal ID.
	if series.TvdbID <= 0 {
		series.TvdbID = series.ID
	}
	// Mount TMDb image URLs into the images array.
	mountTMDBImages(series, series.PosterPath, series.BackdropPath)
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
	for i := range eps {
		e := eps[i]
		e.HasFile = true
		e.Monitored = true
		e.EpisodeFileID = e.ID
		if e.EpisodeFile == nil && e.Path != "" {
			fullPath := e.Path
			basename := filepath.Base(fullPath)
			e.EpisodeFile = &SonarrEpisodeFile{
				ID:           e.ID,
				SeriesID:     e.SeriesID,
				SeasonNumber: e.SeasonNumber,
				RelativePath: basename,
				Path:         fullPath,
				Size:         e.Size,
				DateAdded:    time.Now().UTC().Format(time.RFC3339),
				Quality:      parseQualityFromFilename("", basename),
			}
		}
	}
	jsonResponse(w, http.StatusOK, eps)
}

func (h *Handler) handleEpisodeFilesBySeries(w http.ResponseWriter, r *http.Request) {
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

	files, err := h.store.GetEpisodeFilesBySeries(r.Context(), seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if files == nil {
		files = []*SonarrEpisodeFile{}
	}
	for _, f := range files {
		if f.RelativePath == "" && f.Path != "" {
			f.RelativePath = filepath.Base(f.Path)
		}
		if f.Quality.Quality.Name == "" {
			f.Quality = parseQualityFromFilename("", f.RelativePath)
		}
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
