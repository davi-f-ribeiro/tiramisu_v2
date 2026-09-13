package arr

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"fmt"

	_ "modernc.org/sqlite"
	"tiramisu/internal/metadb"
)

// ---------------------------------------------------------------------------
// Helper: create a real *metadb.DB in memory for the E2E test
func newTestDBE2E(t *testing.T) *metadb.DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	d, err := metadb.New(dbPath, nil)
	if err != nil {
		t.Fatalf("metadb.New: %v", err)
	}
	return d
}

func newTestHandlerWithDB(t *testing.T, db *metadb.DB) *Handler {
	t.Helper()
	store := NewDBStore(db.SQL())
	return NewHandler(store)
}

// ---------------------------------------------------------------------------
// RADARR SCENARIO — Bazarr query sequence for movies
// ---------------------------------------------------------------------------

func TestE2ERadarrSystemStatus(t *testing.T) {
	db := newTestDBE2E(t)
	h := newTestHandlerWithDB(t, db)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/system/status", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.AppName != "Radarr" {
		t.Errorf("appName = %q, want Radarr", resp.AppName)
	}
	if resp.IsProduction != true {
		t.Error("isProduction should be true")
	}
}

func TestE2ERadarrRootFolderQualityTagEmpty(t *testing.T) {
	db := newTestDBE2E(t)
	h := newTestHandlerWithDB(t, db)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, path := range []string{"/api/v3/rootfolder", "/api/v3/qualityprofile", "/api/v3/tag"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		mux.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, w.Code)
		}
		body := strings.TrimSpace(w.Body.String())
		if body != "[]" && body != "[]\n" {
			t.Errorf("%s: body = %q, want []", path, body)
		}
	}
}

func TestE2ERadarrMovieListAndDetail(t *testing.T) {
	db := newTestDBE2E(t)

	// Insert a movie via direct SQL
	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO arr_media (media_type, tmdb_id, imdb_id, title, year, path, size)
		 VALUES ('movie', 550, 'tt0137523', 'Fight Club', 1999, '/data/movies/Fight Club (1999).mkv', 2147483648)`)
	if err != nil {
		t.Fatalf("insert movie: %v", err)
	}

	h := newTestHandlerWithDB(t, db)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// GET /api/v3/movie — list
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("movie list: status = %d, want 200", w.Code)
	}
	var movies []*RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movies); err != nil {
		t.Fatalf("decode movie list: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("got %d movies, want 1", len(movies))
	}
	if movies[0].Title != "Fight Club" {
		t.Errorf("title = %q, want Fight Club", movies[0].Title)
	}
	if movies[0].TmdbID != 550 {
		t.Errorf("tmdbId = %d, want 550", movies[0].TmdbID)
	}
	if !movies[0].Monitored {
		t.Error("monitored should be true")
	}
	if !movies[0].HasFile {
		t.Error("hasFile should be true")
	}

	// GET /api/v3/movie/{id} — detail
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/v3/movie/1", nil)
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("movie detail: status = %d, want 200", w2.Code)
	}
	var movie RadarrMovie
	if err := json.Unmarshal(w2.Body.Bytes(), &movie); err != nil {
		t.Fatalf("decode movie detail: %v", err)
	}
	if movie.ID != 1 {
		t.Errorf("id = %d, want 1", movie.ID)
	}
	if movie.Title != "Fight Club" {
		t.Errorf("title = %q, want Fight Club", movie.Title)
	}
	if movie.ImdbID != "tt0137523" {
		t.Errorf("imdbId = %q, want tt0137523", movie.ImdbID)
	}
}

// ---------------------------------------------------------------------------
// SONARR SCENARIO — Bazarr query sequence for series/episodes
// ---------------------------------------------------------------------------

func TestE2ESonarrSystemStatus(t *testing.T) {
	db := newTestDBE2E(t)
	h := newTestHandlerWithDB(t, db)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/system/status?app=sonarr", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.AppName != "Sonarr" {
		t.Errorf("appName = %q, want Sonarr", resp.AppName)
	}
}

func TestE2ESonarrSeriesListAndDetail(t *testing.T) {
	db := newTestDBE2E(t)

	// Insert a series via direct SQL
	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO arr_media (media_type, tmdb_id, tvdb_id, imdb_id, title, path)
		 VALUES ('series', 1402, 81181, 'tt2356777', 'The Walking Dead', '/data/series/The Walking Dead')`)
	if err != nil {
		t.Fatalf("insert series: %v", err)
	}

	h := newTestHandlerWithDB(t, db)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// GET /api/v3/series
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/series", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("series list: status = %d, want 200", w.Code)
	}
	var series []*SonarrSeries
	if err := json.Unmarshal(w.Body.Bytes(), &series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("got %d series, want 1", len(series))
	}
	if series[0].Title != "The Walking Dead" {
		t.Errorf("title = %q, want The Walking Dead", series[0].Title)
	}
	if series[0].TvdbID != 81181 {
		t.Errorf("tvdbId = %d, want 81181", series[0].TvdbID)
	}
	if !series[0].Monitored {
		t.Error("monitored should be true")
	}

	// GET /api/v3/series/1
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/v3/series/1", nil)
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("series detail: status = %d, want 200", w2.Code)
	}
	var s SonarrSeries
	if err := json.Unmarshal(w2.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode series detail: %v", err)
	}
	if s.ID != 1 || s.TvdbID != 81181 {
		t.Errorf("series = %+v", s)
	}
}

func TestE2ESonarrEpisodesAndEpisodeFiles(t *testing.T) {
	db := newTestDBE2E(t)

	// Insert series + episodes
	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO arr_media (media_type, tmdb_id, tvdb_id, imdb_id, title, path)
		 VALUES ('series', 1402, 81181, 'tt2356777', 'The Walking Dead', '/data/series/The Walking Dead')`)
	if err != nil {
		t.Fatalf("insert series: %v", err)
	}
	_, err = db.SQL().ExecContext(context.Background(),
		`INSERT INTO arr_media (media_type, series_id, season_number, episode_number, title, path, size)
		 VALUES ('episode', 1, 1, 1, 'Days Gone Bye', '/data/series/The Walking Dead/Season 1/TWD - S01E01.mkv', 2147483648)`)
	if err != nil {
		t.Fatalf("insert episode: %v", err)
	}
	_, err = db.SQL().ExecContext(context.Background(),
		`INSERT INTO arr_media (media_type, series_id, season_number, episode_number, title, path, size)
		 VALUES ('episode', 1, 1, 2, 'Cell', '/data/series/The Walking Dead/Season 1/TWD - S01E02.mkv', 2147483648)`)
	if err != nil {
		t.Fatalf("insert episode 2: %v", err)
	}

	h := newTestHandlerWithDB(t, db)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// GET /api/v3/episode?seriesId=1
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/episode?seriesId=1", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("episodes: status = %d, want 200", w.Code)
	}
	var eps []*SonarrEpisode
	if err := json.Unmarshal(w.Body.Bytes(), &eps); err != nil {
		t.Fatalf("decode episodes: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2", len(eps))
	}
	if !eps[0].HasFile {
		t.Error("hasFile should be true")
	}
	if eps[0].EpisodeFileID == 0 {
		// EpisodeFileID is 0 by default in DBStore; Bazarr may or may not check it
		t.Log("EpisodeFileID is 0 (expected for direct DBStore without explicit ID)")
	}

	// GET /api/v3/episodefile?seriesId=1
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/v3/episodefile?seriesId=1", nil)
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("episodefiles: status = %d, want 200", w2.Code)
	}
	var files []*SonarrEpisodeFile
	if err := json.Unmarshal(w2.Body.Bytes(), &files); err != nil {
		t.Fatalf("decode episodefiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d episode files, want 2", len(files))
	}
	if files[0].Path == "" {
		t.Error("episode file path should not be empty")
	}
	if files[0].RelativePath == "" {
		t.Error("episode file relativePath should not be empty")
	}
}

// ---------------------------------------------------------------------------
// BACKFILL SCENARIO — stub files on disk appear in API
// ---------------------------------------------------------------------------

func TestE2EBuildAndBackfill(t *testing.T) {
	// 1. Build a minimal temp directory tree with stub files
	tmpDir := t.TempDir()
	moviesDir := filepath.Join(tmpDir, "movies")
	tvDir := filepath.Join(tmpDir, "tv")
	for _, d := range []string{moviesDir, filepath.Join(tvDir, "Breaking Bad", "Season 1")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Movie stub with JSON metadata
	movieStub := `{
		"url": "http://gostorm/stream?link=abc123",
		"size": 2147483648,
		"magnet": "magnet:?xt=urn:btih:abc123",
		"imdb": "tt0111111",
		"tmdb_id": 278,
		"media_type": "movie",
		"title": "The Shawshank Redemption",
		"year": 1994
	}`
	moviePath := filepath.Join(moviesDir, "The Shawshank Redemption (1994).mkv")
	if err := os.WriteFile(moviePath, []byte(movieStub), 0644); err != nil {
		t.Fatalf("write movie stub: %v", err)
	}

	// Series episode stub
	epStub := `{
		"url": "http://gostorm/stream?link=def456",
		"size": 1073741824,
		"magnet": "magnet:?xt=urn:btih:def456",
		"imdb": "tt0903747",
		"media_type": "episode",
		"title": "Pilot",
		"season": 1,
		"episode": 1
	}`
	epPath := filepath.Join(tvDir, "Breaking Bad", "Season 1", "Breaking Bad - S01E01 - Pilot.mkv")
	if err := os.WriteFile(epPath, []byte(epStub), 0644); err != nil {
		t.Fatalf("write episode stub: %v", err)
	}

	// 2. Create in-memory metadb and run backfill
	db := newTestDBE2E(t)
	store := NewDBStore(db.SQL())

	logger := log.New(io.Discard, "", 0)
	stats, err := RunBackfill(context.Background(), moviesDir, tvDir, store, logger)
	if err != nil {
		t.Fatalf("backfill error: %v", err)
	}
	if stats.MoviesIndexed != 1 {
		t.Errorf("movies_indexed = %d, want 1", stats.MoviesIndexed)
	}
	if stats.EpisodesIndexed != 1 {
		t.Errorf("episodes_indexed = %d, want 1", stats.EpisodesIndexed)
	}

	// 3. Verify movie appears in API
	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("movie list: status = %d, want 200", w.Code)
	}
	var movies []*RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movies); err != nil {
		t.Fatalf("decode movies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("backfill: got %d movies, want 1", len(movies))
	}
	if movies[0].Title != "The Shawshank Redemption" {
		t.Errorf("movie title = %q, want The Shawshank Redemption", movies[0].Title)
	}
	if movies[0].TmdbID != 278 {
		t.Errorf("movie tmdbId = %d, want 278", movies[0].TmdbID)
	}
	if movies[0].ImdbID != "tt0111111" {
		t.Errorf("movie imdbId = %q, want tt0111111", movies[0].ImdbID)
	}

	// 4. Verify series appears in API
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/v3/series", nil)
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("series list: status = %d, want 200", w2.Code)
	}
	var series []*SonarrSeries
	if err := json.Unmarshal(w2.Body.Bytes(), &series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("backfill: got %d series, want 1", len(series))
	}
	if series[0].Title != "Breaking Bad" {
		t.Errorf("series title = %q, want Breaking Bad", series[0].Title)
	}

	// 5. Verify episode appears with seriesId (fetch real seriesId first)
	var firstSeries []*SonarrSeries
	w0 := httptest.NewRecorder()
	r0 := httptest.NewRequest("GET", "/api/v3/series", nil)
	mux.ServeHTTP(w0, r0)
	if w0.Code != http.StatusOK {
		t.Fatalf("series list: status = %d", w0.Code)
	}
	if err := json.Unmarshal(w0.Body.Bytes(), &firstSeries); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(firstSeries) == 0 {
		t.Fatal("no series found after backfill")
	}
	seriesID := firstSeries[0].ID

	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest("GET", "/api/v3/episode?seriesId="+fmt.Sprintf("%d", seriesID), nil)
	mux.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("episodes: status = %d, want 200", w3.Code)
	}
	var eps []*SonarrEpisode
	if err := json.Unmarshal(w3.Body.Bytes(), &eps); err != nil {
		t.Fatalf("decode episodes: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("backfill: got %d episodes, want 1", len(eps))
	}
	if eps[0].Title != "Pilot" {
		t.Errorf("episode title = %q, want Pilot", eps[0].Title)
	}
	if eps[0].SeasonNumber != 1 || eps[0].EpisodeNumber != 1 {
		t.Errorf("season/episode = %d/%d, want 1/1", eps[0].SeasonNumber, eps[0].EpisodeNumber)
	}
}

func TestE2EBuildBackfillSkipsNonStubs(t *testing.T) {
	tmpDir := t.TempDir()
	moviesDir := filepath.Join(tmpDir, "movies")
	if err := os.MkdirAll(moviesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write a real binary file (not a stub) — should be silently skipped
	if err := os.WriteFile(filepath.Join(moviesDir, "not-a-stub.mkv"), []byte{0x00, 0x01, 0x02, 0x03}, 0644); err != nil {
		t.Fatalf("write non-stub: %v", err)
	}

	db := newTestDBE2E(t)
	store := NewDBStore(db.SQL())
	logL := log.New(io.Discard, "", 0)

	stats, err := RunBackfill(context.Background(), moviesDir, "", store, logL)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.MoviesIndexed != 0 {
		t.Errorf("movies_indexed = %d, want 0 (non-stub skipped)", stats.MoviesIndexed)
	}

	// Movie list should be empty
	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)

	var movies []*RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movies); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(movies) != 0 {
		t.Errorf("got %d movies, want 0 (non-stub should be skipped)", len(movies))
	}
}

// ---------------------------------------------------------------------------
// Minimal logger for tests
// ---------------------------------------------------------------------------

type testLogger struct {
	buf strings.Builder
}

func (l *testLogger) Printf(format string, v ...interface{}) {
	fmt.Fprintf(&l.buf, format, v...)
}

// Logger returns a *log.Logger that writes to a discard writer, for use with RunBackfill.
func newTestLog() *log.Logger {
	return log.New(os.Stderr, "", 0)
}
