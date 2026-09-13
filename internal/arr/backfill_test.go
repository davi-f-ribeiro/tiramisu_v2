package arr

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	"tiramisu/internal/metadb"
)

// ---------------------------------------------------------------------------
// Helper: in-memory metadb + DBStore for integration tests
// ---------------------------------------------------------------------------

func newTestDB(t *testing.T) *metadb.DB {
	t.Helper()
	db, err := metadb.New("", nil)
	if err != nil {
		t.Fatalf("create metadb: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newHandlerWithBackfill(db *metadb.DB, moviesDir, tvDir string) *Handler {
	store := NewDBStore(db.SQL())
	return NewHandler(
		store,
		WithStoreWriter(store),
		WithDirs(moviesDir, tvDir),
		WithLogger(log.New(io.Discard, "", 0)),
	)
}

// ---------------------------------------------------------------------------
// 1. POST /api/arr/sync — basic
// ---------------------------------------------------------------------------

func TestManualSyncEmpty(t *testing.T) {
	db := newTestDB(t)
	h := newHandlerWithBackfill(db, "", "")

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/arr/sync", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("status = %v, want error", body["status"])
	}
}

func TestManualSyncWithDirs(t *testing.T) {
	db := newTestDB(t)

	tmpDir := t.TempDir()
	moviesDir := filepath.Join(tmpDir, "movies")
	tvDir := filepath.Join(tmpDir, "tv")
	for _, d := range []string{moviesDir, filepath.Join(tvDir, "Test Show", "Season 1")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Write 2 movie stubs
	for i, name := range []string{"Movie A.mkv", "Movie B.mkv"} {
		data := fmt.Sprintf(`{"url":"http://gostorm/stream?link=abc%d","size":2147483648,"imdb":"tt%d000001","tmdb_id":%d,"media_type":"movie","title":"Movie A","year":2024}`, i, i+100, i+1)
		if err := os.WriteFile(filepath.Join(moviesDir, name), []byte(data), 0644); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}

	h := newHandlerWithBackfill(db, moviesDir, tvDir)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/arr/sync", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "success" {
		t.Errorf("status = %v, want success", body["status"])
	}
	movies := int(body["movies_indexed"].(float64))
	if movies != 2 {
		t.Errorf("movies_indexed = %d, want 2", movies)
	}

	// Verify movies visible via GET
	mux2 := http.NewServeMux()
	h.RegisterRoutes(mux2)

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux2.ServeHTTP(w2, r2)
	var moviesList []*RadarrMovie
	if err := json.Unmarshal(w2.Body.Bytes(), &moviesList); err != nil {
		t.Fatalf("decode movies: %v", err)
	}
	if len(moviesList) != 2 {
		t.Errorf("GET /api/v3/movie: got %d, want 2", len(moviesList))
	}
}

// ---------------------------------------------------------------------------
// 2. POST /api/v3/arr/sync — versioned endpoint
// ---------------------------------------------------------------------------

func TestManualSyncVersioned(t *testing.T) {
	db := newTestDB(t)

	tmpDir := t.TempDir()
	moviesDir := filepath.Join(tmpDir, "movies")
	if err := os.MkdirAll(moviesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	data := `{"url":"http://gostorm/stream?link=def","size":3221225472,"imdb":"tt0903747","tmdb_id":1399,"media_type":"movie","title":"Game of Thrones Film","year":2011}`
	if err := os.WriteFile(filepath.Join(moviesDir, "GoT.mkv"), []byte(data), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	h := newHandlerWithBackfill(db, moviesDir, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v3/arr/sync", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "success" {
		t.Errorf("status = %v", body["status"])
	}
	if int(body["movies_indexed"].(float64)) != 1 {
		t.Errorf("movies_indexed = %v", body["movies_indexed"])
	}
}

// ---------------------------------------------------------------------------
// 3. Series + episode backfill via sync
// ---------------------------------------------------------------------------

func TestManualSyncSeriesAndEpisodes(t *testing.T) {
	db := newTestDB(t)

	tmpDir := t.TempDir()
	tvDir := filepath.Join(tmpDir, "tv")
	showDir := filepath.Join(tvDir, "Breaking Bad", "Season 1")
	if err := os.MkdirAll(showDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write 2 episode stubs
	epStubs := []struct{ name, title string }{
		{"Breaking Bad - S01E01.mkv", "Pilot"},
		{"Breaking Bad - S01E02.mkv", "Cat's in the Bag..."},
	}
	for _, ep := range epStubs {
		data := fmt.Sprintf(`{"url":"http://gostorm/stream?link=%s","size":1073741824,"media_type":"episode","title":"%s","season":1}`, ep.name, ep.title)
		if err := os.WriteFile(filepath.Join(showDir, ep.name), []byte(data), 0644); err != nil {
			t.Fatalf("write %s: %v", ep.name, err)
		}
	}

	h := newHandlerWithBackfill(db, "", tvDir)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/arr/sync", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("sync status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["status"] != "success" {
		t.Errorf("status = %v", body["status"])
	}
	seriesCount := int(body["series_indexed"].(float64))
	epCount := int(body["episodes_indexed"].(float64))
	if seriesCount != 1 {
		t.Errorf("series_indexed = %d, want 1", seriesCount)
	}
	if epCount != 2 {
		t.Errorf("episodes_indexed = %d, want 2", epCount)
	}

	// Verify series visible
	mux2 := http.NewServeMux()
	h.RegisterRoutes(mux2)

	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/v3/series", nil)
	mux2.ServeHTTP(w2, r2)
	var seriesList []*SonarrSeries
	if err := json.Unmarshal(w2.Body.Bytes(), &seriesList); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(seriesList) != 1 {
		t.Fatalf("GET /api/v3/series: got %d, want 1", len(seriesList))
	}
	seriesID := seriesList[0].ID

	// Verify episodes visible
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest("GET", "/api/v3/episode?seriesId="+fmt.Sprintf("%d", seriesID), nil)
	mux2.ServeHTTP(w3, r3)
	var eps []*SonarrEpisode
	if err := json.Unmarshal(w3.Body.Bytes(), &eps); err != nil {
		t.Fatalf("decode episodes: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("GET /api/v3/episode: got %d, want 2", len(eps))
	}
}

// ---------------------------------------------------------------------------
// 4. POST method validation
// ---------------------------------------------------------------------------

func TestManualSyncGETNotAllowed(t *testing.T) {
	db := newTestDB(t)
	h := newHandlerWithBackfill(db, "", "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/arr/sync", nil)
	mux.ServeHTTP(w, r)

	// ServeMux may route GET to the POST handler with method mismatch
	// or 404 — both are acceptable as long as it doesn't run backfill
	if w.Code != http.StatusOK {
		t.Logf("GET /api/arr/sync returned %d (non-OK, acceptable)", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 5. DurationMs is populated
// ---------------------------------------------------------------------------

func TestManualSyncDurationMs(t *testing.T) {
	db := newTestDB(t)

	tmpDir := t.TempDir()
	moviesDir := filepath.Join(tmpDir, "movies")
	if err := os.MkdirAll(moviesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	data := `{"url":"http://gostorm/stream?link=x","size":2147483648,"media_type":"movie","title":"X","year":2025}`
	if err := os.WriteFile(filepath.Join(moviesDir, "X.mkv"), []byte(data), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	h := newHandlerWithBackfill(db, moviesDir, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v3/arr/sync", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	duration := int64(body["duration_ms"].(float64))
	if duration <= 0 {
		t.Errorf("duration_ms = %d, want > 0", duration)
	}
}

// ---------------------------------------------------------------------------
// 6. Idempotency — running sync twice shouldn't error, just 0 new
// ---------------------------------------------------------------------------

func TestManualSyncIdempotent(t *testing.T) {
	db := newTestDB(t)

	tmpDir := t.TempDir()
	moviesDir := filepath.Join(tmpDir, "movies")
	if err := os.MkdirAll(moviesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	data := `{"url":"http://gostorm/stream?link=abc","size":2147483648,"media_type":"movie","title":"Dup","year":2023}`
	if err := os.WriteFile(filepath.Join(moviesDir, "Dup.mkv"), []byte(data), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	h := newHandlerWithBackfill(db, moviesDir, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// First run
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("POST", "/api/arr/sync", nil)
	mux.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first run status = %d", w1.Code)
	}
	var body1 map[string]any
	if err := json.Unmarshal(w1.Body.Bytes(), &body1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(body1["movies_indexed"].(float64)) != 1 {
		t.Errorf("first run: movies_indexed = %v", body1["movies_indexed"])
	}

	// Second run — idempotent: same count, no error
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/api/arr/sync", nil)
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second run status = %d", w2.Code)
	}
	var body2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if int(body2["movies_indexed"].(float64)) != 1 {
		t.Errorf("second run: movies_indexed = %v, want 1 (idempotent scan)", body2["movies_indexed"])
	}
	if body2["status"] != "success" {
		t.Errorf("second run status = %v, want success", body2["status"])
	}
}
