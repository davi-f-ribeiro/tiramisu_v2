package arr

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// mockStore is a lightweight in-memory store for testing.
type mockStore struct {
	movies  map[int64]*RadarrMovie
	series  map[int64]*SonarrSeries
	eps     map[int64][]*SonarrEpisode
	epFiles map[int64][]*SonarrEpisodeFile
}

func newMockStore() *mockStore {
	return &mockStore{
		movies:  make(map[int64]*RadarrMovie),
		series:  make(map[int64]*SonarrSeries),
		eps:     make(map[int64][]*SonarrEpisode),
		epFiles: make(map[int64][]*SonarrEpisodeFile),
	}
}

func (m *mockStore) GetMovies(ctx context.Context) ([]*RadarrMovie, error) {
	result := make([]*RadarrMovie, 0, len(m.movies))
	for _, v := range m.movies {
		result = append(result, v)
	}
	return result, nil
}

func (m *mockStore) GetMovieByID(ctx context.Context, id int64) (*RadarrMovie, error) {
	v, ok := m.movies[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	// Return a copy
	cp := *v
	return &cp, nil
}

func (m *mockStore) GetSeries(ctx context.Context) ([]*SonarrSeries, error) {
	result := make([]*SonarrSeries, 0, len(m.series))
	for _, v := range m.series {
		result = append(result, v)
	}
	return result, nil
}

func (m *mockStore) GetSeriesByID(ctx context.Context, id int64) (*SonarrSeries, error) {
	v, ok := m.series[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	cp := *v
	return &cp, nil
}

func (m *mockStore) GetEpisodesBySeries(ctx context.Context, seriesID int64) ([]*SonarrEpisode, error) {
	v, ok := m.eps[seriesID]
	if !ok {
		return []*SonarrEpisode{}, nil
	}
	result := make([]*SonarrEpisode, len(v))
	for i := range v {
		cp := *v[i]
		result[i] = &cp
	}
	return result, nil
}

func (m *mockStore) GetEpisodeFilesBySeries(ctx context.Context, seriesID int64) ([]*SonarrEpisodeFile, error) {
	v, ok := m.epFiles[seriesID]
	if !ok {
		return []*SonarrEpisodeFile{}, nil
	}
	result := make([]*SonarrEpisodeFile, len(v))
	for i := range v {
		cp := *v[i]
		result[i] = &cp
	}
	return result, nil
}

func newTestHandler() *Handler {
	store := newMockStore()
	return NewHandler(store)
}

// ---------------------------------------------------------------------------
// 1. System status
// ---------------------------------------------------------------------------

func TestSystemStatus(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/system/status", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.AppName != "Radarr" {
		t.Errorf("appName = %q, want Radarr", resp.AppName)
	}
	if resp.Version == "" {
		t.Error("version is empty")
	}
	if !resp.IsProduction {
		t.Error("isProduction should be true")
	}
	if resp.OsName != "linux" {
		t.Errorf("osName = %q, want linux", resp.OsName)
	}

	// Verify Content-Type header
	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}
}

func TestSystemStatusLegacy(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/system/status", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.AppName != "Radarr" {
		t.Errorf("appName = %q, want Radarr", resp.AppName)
	}
}

func TestSystemStatusWithAppQuery(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/system/status?app=sonarr", nil)
	mux.ServeHTTP(w, r)

	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.AppName != "Sonarr" {
		t.Errorf("appName = %q, want Sonarr (from query)", resp.AppName)
	}
}

func TestSystemStatusOnSonarrPort(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/system/status", nil)
	r.Host = "localhost:8989"
	mux.ServeHTTP(w, r)

	var resp SystemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if resp.AppName != "Sonarr" {
		t.Errorf("appName = %q, want Sonarr (from port 8989)", resp.AppName)
	}
}

// ---------------------------------------------------------------------------
// 2. Root folder, quality profile, tag — empty arrays
// ---------------------------------------------------------------------------

func testEmptyEndpoint(t *testing.T, mux *http.ServeMux, path string) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	// Must be "[]" not "null"
	body := strings.TrimSpace(w.Body.String())
	if body != "[]" {
		// Allow trailing newline from Encode
		if body != "[]\n" {
			t.Errorf("body = %q, want []", body)
		}
	}
}

func TestRootFolderEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	testEmptyEndpoint(t, mux, "/api/v3/rootfolder")
}

func TestQualityProfileEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	testEmptyEndpoint(t, mux, "/api/v3/qualityprofile")
}

func TestTagEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	testEmptyEndpoint(t, mux, "/api/v3/tag")
}

// ---------------------------------------------------------------------------
// 3. Movie list and detail
// ---------------------------------------------------------------------------

func TestMovieList(t *testing.T) {
	store := newMockStore()
	store.movies[1] = &RadarrMovie{ID: 1, Title: "Movie A", Year: 2020, Path: "/data/movies/m1"}
	store.movies[2] = &RadarrMovie{ID: 2, Title: "Movie B", Year: 2021, Path: "/data/movies/m2"}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var movies []*RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movies); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(movies) != 2 {
		t.Fatalf("got %d movies, want 2", len(movies))
	}
	// Check both movies exist regardless of map iteration order
	foundA, foundB := false, false
	for _, m := range movies {
		if m.Title == "Movie A" {
			foundA = true
		}
		if m.Title == "Movie B" {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Errorf("expected both 'Movie A' and 'Movie B', got %v", movies)
	}
}

func TestMovieListEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := strings.TrimSpace(w.Body.String())
	if body != "[]" && body != "[]\n" {
		t.Errorf("empty movie list body = %q, want []", body)
	}
}

func TestMovieDetail(t *testing.T) {
	store := newMockStore()
	store.movies[42] = &RadarrMovie{
		ID: 42, Title: "The Movie", Year: 2019, Path: "/data/movies/the-movie",
		TmdbID: 12345, ImdbID: "tt1234567",
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie/42", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var movie RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movie); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if movie.ID != 42 {
		t.Errorf("id = %d, want 42", movie.ID)
	}
	if movie.Title != "The Movie" {
		t.Errorf("title = %q, want The Movie", movie.Title)
	}
	if movie.TmdbID != 12345 {
		t.Errorf("tmdbId = %d, want 12345", movie.TmdbID)
	}
}

func TestMovieDetail404(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie/99999", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 4. Series list and detail
// ---------------------------------------------------------------------------

func TestSeriesList(t *testing.T) {
	store := newMockStore()
	store.series[1] = &SonarrSeries{ID: 1, Title: "Show X", TvdbID: 555, Path: "/data/shows/x"}
	store.series[2] = &SonarrSeries{ID: 2, Title: "Show Y", TvdbID: 666, Path: "/data/shows/y"}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/series", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var series []*SonarrSeries
	if err := json.Unmarshal(w.Body.Bytes(), &series); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("got %d series, want 2", len(series))
	}
}

func TestSeriesDetail(t *testing.T) {
	store := newMockStore()
	store.series[7] = &SonarrSeries{
		ID: 7, Title: "The Series", TvdbID: 777, Path: "/data/shows/series",
		ImdbID: "tt7777777",
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/series/7", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var s SonarrSeries
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if s.ID != 7 || s.TvdbID != 777 || s.Title != "The Series" {
		t.Errorf("series = %+v", s)
	}
}

func TestSeriesDetail404(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/series/99999", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 5. Episodes filtered by seriesId
// ---------------------------------------------------------------------------

func TestEpisodesBySeries(t *testing.T) {
	store := newMockStore()
	store.series[10] = &SonarrSeries{ID: 10, Title: "Show Z", TvdbID: 888}
	store.eps[10] = []*SonarrEpisode{
		{ID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", HasFile: true},
		{ID: 2, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 2, Title: "Second", HasFile: true},
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/episode?seriesId=10", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var eps []*SonarrEpisode
	if err := json.Unmarshal(w.Body.Bytes(), &eps); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2", len(eps))
	}
	if eps[0].Title != "Pilot" {
		t.Errorf("first episode title = %q, want Pilot", eps[0].Title)
	}
}

func TestEpisodesEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// No seriesId param — should return []
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/episode", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != "[]" && body != "[]\n" {
		t.Errorf("no seriesId body = %q, want []", body)
	}
}

func TestEpisodeFilesBySeries(t *testing.T) {
	store := newMockStore()
	store.series[20] = &SonarrSeries{ID: 20, Title: "Show A", TvdbID: 999}
	store.epFiles[20] = []*SonarrEpisodeFile{
		{ID: 100, SeriesID: 20, SeasonNumber: 1, RelativePath: "s01e01.mkv", Path: "/data/shows/a/s01e01.mkv", Size: 1000},
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/episodefile?seriesId=20", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var files []*SonarrEpisodeFile
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d episode files, want 1", len(files))
	}
	if files[0].Path != "/data/shows/a/s01e01.mkv" {
		t.Errorf("path = %q, want /data/shows/a/s01e01.mkv", files[0].Path)
	}
}

func TestEpisodeFilesEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/episodefile", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != "[]" && body != "[]\n" {
		t.Errorf("no seriesId episodefile body = %q, want []", body)
	}
}

// ---------------------------------------------------------------------------
// 6. Language profile — empty array (Sonarr)
// ---------------------------------------------------------------------------

func TestLanguageProfileEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	testEmptyEndpoint(t, mux, "/api/v3/languageprofile")
}

func TestLanguageProfileLegacyEmpty(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	testEmptyEndpoint(t, mux, "/api/languageprofile")
}

// ---------------------------------------------------------------------------
// 7. SignalR negotiate — both legacy and /api/v3/ paths
// ---------------------------------------------------------------------------

func TestSignalRNegotiateLegacy(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, path := range []string{"/signalr/negotiate", "/signalr"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		mux.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("path %q: status = %d, want 200", path, w.Code)
			continue
		}

		ct := w.Header().Get("Content-Type")
		if ct != "application/json; charset=utf-8" {
			t.Errorf("path %q: Content-Type = %q", path, ct)
		}

		var res map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("path %q: json decode: %v", path, err)
		}
		if res["negotiateVersion"] == nil {
			t.Errorf("path %q: missing negotiateVersion field", path)
		}
		if res["connectionId"] == nil {
			t.Errorf("path %q: missing connectionId field", path)
		}
		if avail, ok := res["availableTransports"].([]any); ok {
			// Must be present (even if empty slice)
			_ = avail
		} else {
			t.Errorf("path %q: availableTransports not []any", path)
		}
	}
}

func TestSignalRNegotiateV3(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, path := range []string{"/api/v3/signalr/negotiate", "/api/v3/signalr"} {
		reqURL := path
		if path == "/api/v3/signalr/negotiate" {
			reqURL = path + "?negotiateVersion=1"
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", reqURL, nil)
		mux.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("path %q: status = %d, want 200", path, w.Code)
			continue
		}

		var res map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("path %q: json decode: %v", path, err)
		}
		if nv, ok := res["negotiateVersion"].(float64); !ok || nv != 1 {
			t.Errorf("path %q: negotiateVersion = %v, want 1", path, res["negotiateVersion"])
		}
		if res["connectionId"] == nil {
			t.Errorf("path %q: missing connectionId", path)
		}
	}
}

// ---------------------------------------------------------------------------
// 8. Sonarr-specific: system status on port 8989 via /api/v3/ routes
// ---------------------------------------------------------------------------

func TestSonarrSystemStatusOnPort(t *testing.T) {
	h := NewHandler(newMockStore(), WithAppName("Sonarr"))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/system/status", nil)
	r.Host = "localhost:8989"
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
	if resp.Version == "" {
		t.Error("version should not be empty")
	}
}

func TestSonarrSeriesListOnPort(t *testing.T) {
	store := newMockStore()
	store.series[1] = &SonarrSeries{ID: 1, Title: "Show X", TvdbID: 555, Path: "/data/shows/x"}

	h := NewHandler(store, WithAppName("Sonarr"))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/series", nil)
	r.Host = "localhost:8989"
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var series []*SonarrSeries
	if err := json.Unmarshal(w.Body.Bytes(), &series); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(series) != 1 || series[0].Title != "Show X" {
		t.Errorf("series = %+v", series)
	}
}

func TestSonarrSignalRNegotiateV3(t *testing.T) {
	store := newMockStore()
	h := NewHandler(store, WithAppName("Sonarr"))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/signalr/negotiate?negotiateVersion=1", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if nv, ok := res["negotiateVersion"].(float64); !ok || nv != 1 {
		t.Errorf("negotiateVersion = %v, want 1", res["negotiateVersion"])
	}
}

// ---------------------------------------------------------------------------
// 9. movieFile and episodeFile subobjects (Bazarr subtitle indexation)
// ---------------------------------------------------------------------------

func TestMovieListWithMovieFile(t *testing.T) {
	store := newMockStore()
	store.movies[1] = &RadarrMovie{ID: 1, Title: "Movie A", Year: 2020, Path: "/data/movies/the.movie.2020.mkv"}
	store.movies[2] = &RadarrMovie{ID: 2, Title: "Movie B", Year: 2021, Path: "/data/movies/movie-b.mkv"}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var movies []*RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movies); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(movies) != 2 {
		t.Fatalf("got %d movies, want 2", len(movies))
	}
	// Every movie must have movieFile populated with relativePath != "".
	for _, m := range movies {
		if m.MovieFile == nil {
			t.Errorf("movie %q: movieFile is nil", m.Title)
			continue
		}
		if m.MovieFile.RelativePath == "" {
			t.Errorf("movie %q: movieFile.relativePath is empty", m.Title)
		}
		if m.MovieFile.Path == "" {
			t.Errorf("movie %q: movieFile.path is empty", m.Title)
		}
		if m.Path == "" {
			t.Errorf("movie %q: path (directory) is empty", m.Title)
		}
		if !m.HasFile || !m.Monitored {
			t.Errorf("movie %q: hasFile=%v, monitored=%v, want true,true", m.Title, m.HasFile, m.Monitored)
		}
		// Quality must be populated so Bazarr doesn't hit KeyError('quality').
		if m.MovieFile.Quality.Quality.Name == "" {
			t.Errorf("movie %q: movieFile.quality.quality.name is empty", m.Title)
		}
	}
}

func TestMovieDetailWithMovieFile(t *testing.T) {
	store := newMockStore()
	store.movies[42] = &RadarrMovie{
		ID: 42, Title: "The Movie", Year: 2019, Path: "/data/movies/the.movie.2019.mkv",
		TmdbID: 12345, ImdbID: "tt1234567",
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie/42", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var movie RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movie); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if movie.MovieFile == nil {
		t.Fatal("movie.movieFile is nil")
	}
	if movie.MovieFile.RelativePath == "" {
		t.Error("movieFile.relativePath is empty")
	}
	if movie.MovieFile.Path == "" {
		t.Error("movieFile.path is empty")
	}
	if movie.MovieFile.MovieID != 42 {
		t.Errorf("movieFile.movieId = %d, want 42", movie.MovieFile.MovieID)
	}
	if movie.MovieFile.ID != 42 {
		t.Errorf("movieFile.id = %d, want 42", movie.MovieFile.ID)
	}
	if movie.MovieFile.Quality.Quality.Name == "" {
		t.Error("movieFile.quality.quality.name is empty")
	} else if movie.MovieFile.Quality.Quality.Name != "WEBDL-1080p" {
		t.Errorf("movieFile.quality.quality.name = %q, want WEBDL-1080p", movie.MovieFile.Quality.Quality.Name)
	}
	if !movie.HasFile || !movie.Monitored {
		t.Errorf("hasFile=%v, monitored=%v, want true,true", movie.HasFile, movie.Monitored)
	}
	// movie.Path should be the directory now.
	if movie.Path == "" {
		t.Error("movie path (directory) is empty")
	}
}

func TestEpisodesWithEpisodeFile(t *testing.T) {
	store := newMockStore()
	store.series[10] = &SonarrSeries{ID: 10, Title: "Show Z", TvdbID: 888, Path: "/data/shows/show-z"}
	store.eps[10] = []*SonarrEpisode{
		{ID: 1, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", Path: "/data/shows/show-z/S01E01.mkv", Size: 500},
		{ID: 2, SeriesID: 10, SeasonNumber: 1, EpisodeNumber: 2, Title: "Second", Path: "/data/shows/show-z/S01E02.mkv", Size: 550},
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/episode?seriesId=10", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var eps []*SonarrEpisode
	if err := json.Unmarshal(w.Body.Bytes(), &eps); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2", len(eps))
	}
	// Each episode must have episodeFile populated.
	for _, e := range eps {
		if e.EpisodeFile == nil {
			t.Errorf("episode %q: episodeFile is nil", e.Title)
			continue
		}
		if e.EpisodeFile.RelativePath == "" {
			t.Errorf("episode %q: episodeFile.relativePath is empty", e.Title)
		}
		if e.EpisodeFile.Path == "" {
			t.Errorf("episode %q: episodeFile.path is empty", e.Title)
		}
		if e.EpisodeFile.SeriesID != e.SeriesID {
			t.Errorf("episode %q: episodeFile.seriesId = %d, want %d", e.Title, e.EpisodeFile.SeriesID, e.SeriesID)
		}
		if e.EpisodeFileID == 0 {
			t.Errorf("episode %q: episodeFileId = 0, want %d", e.Title, e.ID)
		}
		if !e.HasFile || !e.Monitored {
			t.Errorf("episode %q: hasFile=%v, monitored=%v, want true,true", e.Title, e.HasFile, e.Monitored)
		}
	}
	if eps[0].Title != "Pilot" {
		t.Errorf("first episode title = %q, want Pilot", eps[0].Title)
	}
}

func TestEpisodeFilesWithRelativePath(t *testing.T) {
	store := newMockStore()
	store.series[20] = &SonarrSeries{ID: 20, Title: "Show A", TvdbID: 999}
	store.epFiles[20] = []*SonarrEpisodeFile{
		{ID: 100, SeriesID: 20, SeasonNumber: 1, RelativePath: "S01E01.mkv", Path: "/data/shows/a/S01E01.mkv", Size: 1000},
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/episodefile?seriesId=20", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var files []*SonarrEpisodeFile
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d episode files, want 1", len(files))
	}
	if files[0].RelativePath == "" {
		t.Error("episodeFile.relativePath is empty")
	}
	if files[0].Path == "" {
		t.Error("episodeFile.path is empty")
	}
}

// ---------------------------------------------------------------------------
// 10. parseQualityFromFilename unit tests
// ---------------------------------------------------------------------------

func TestParseQualityFromFilename_WEBDL_1080p(t *testing.T) {
	q := parseQualityFromFilename("", "72_HOURS_2026_1080p_5.1_2301055d.mkv")
	if q.Quality.Resolution != 1080 {
		t.Errorf("resolution = %d, want 1080", q.Quality.Resolution)
	}
	if q.Quality.Name != "WEBDL-1080p" {
		t.Errorf("quality.name = %q, want WEBDL-1080p", q.Quality.Name)
	}
	if q.Quality.Source != "webdl" {
		t.Errorf("quality.source = %q, want webdl", q.Quality.Source)
	}
}

func TestParseQualityFromFilename_2160p(t *testing.T) {
	q := parseQualityFromFilename("", "Movie.Name.2024.2160p.WEBRip.x265.mkv")
	if q.Quality.Resolution != 2160 {
		t.Errorf("resolution = %d, want 2160", q.Quality.Resolution)
	}
	if q.Quality.Name != "WEBRip-2160p" {
		t.Errorf("quality.name = %q, want WEBRip-2160p", q.Quality.Name)
	}
	if q.Quality.Source != "webrip" {
		t.Errorf("quality.source = %q, want webrip", q.Quality.Source)
	}
}

func TestParseQualityFromFilename_Bluray(t *testing.T) {
	q := parseQualityFromFilename("", "Show.S01E01.720p.BluRay.x264.mkv")
	if q.Quality.Resolution != 720 {
		t.Errorf("resolution = %d, want 720", q.Quality.Resolution)
	}
	if q.Quality.Name != "Bluray-720p" {
		t.Errorf("quality.name = %q, want Bluray-720p", q.Quality.Name)
	}
}

func TestParseQualityFromFilename_HDTV(t *testing.T) {
	q := parseQualityFromFilename("", "Series.S01E05.480p.HDTV.x264.mkv")
	if q.Quality.Resolution != 480 {
		t.Errorf("resolution = %d, want 480", q.Quality.Resolution)
	}
	if q.Quality.Name != "HDTV-480p" {
		t.Errorf("quality.name = %q, want HDTV-480p", q.Quality.Name)
	}
}

func TestParseQualityFromFilename_4k(t *testing.T) {
	q := parseQualityFromFilename("", "Movie.4K.Remux.mkv")
	if q.Quality.Resolution != 2160 {
		t.Errorf("resolution = %d, want 2160", q.Quality.Resolution)
	}
	if q.Quality.Source != "bluray" {
		t.Errorf("quality.source = %q, want bluray (remux maps to bluray)", q.Quality.Source)
	}
}

func TestParseQualityFromFilename_Default(t *testing.T) {
	q := parseQualityFromFilename("", "Unknown.File.mp4")
	if q.Quality.Resolution != 1080 {
		t.Errorf("resolution = %d, want 1080 (default)", q.Quality.Resolution)
	}
	if q.Quality.Source != "webdl" {
		t.Errorf("quality.source = %q, want webdl (default)", q.Quality.Source)
	}
}

// ---------------------------------------------------------------------------
// 11. sortTitle and cleanTitle assertions
// ---------------------------------------------------------------------------

func TestMovieSortTitle(t *testing.T) {
	store := newMockStore()
	store.movies[1] = &RadarrMovie{ID: 1, Title: "Some_Movie_2020", Year: 2020, Path: "/data/movies/some.movie.2020.mkv"}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var movies []*RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movies); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("got %d movies, want 1", len(movies))
	}
	m := movies[0]
	if m.SortTitle != "some movie 2020" {
		t.Errorf("sortTitle = %q, want some movie 2020", m.SortTitle)
	}
	if m.CleanTitle != "Some Movie 2020" {
		t.Errorf("cleanTitle = %q, want Some Movie 2020", m.CleanTitle)
	}
	if m.TitleSlug != "some-movie-2020" {
		t.Errorf("titleSlug = %q, want some-movie-2020", m.TitleSlug)
	}
	if m.Status != "released" {
		t.Errorf("status = %q, want released", m.Status)
	}
}

func TestMovieDetailSortTitle(t *testing.T) {
	store := newMockStore()
	store.movies[42] = &RadarrMovie{
		ID: 42, Title: "Movie_Name_2019", Year: 2019, Path: "/data/movies/movie.name.2019.mkv",
	}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie/42", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var movie RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movie); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if movie.SortTitle != "movie name 2019" {
		t.Errorf("sortTitle = %q, want movie name 2019", movie.SortTitle)
	}
	if movie.TitleSlug != "movie-name-2019" {
		t.Errorf("titleSlug = %q, want movie-name-2019", movie.TitleSlug)
	}
	if movie.Status != "released" {
		t.Errorf("status = %q, want released", movie.Status)
	}
}

// ---------------------------------------------------------------------------
// 12. Fallback de IDs externos para evitar UNIQUE constraint no Bazarr
// ---------------------------------------------------------------------------

func TestMovieTmdbIdFallbackDistinct(t *testing.T) {
	// Cria dois filmes SEM TmdbID explícito (zero).
	store := newMockStore()
	store.movies[10] = &RadarrMovie{ID: 10, Title: "Movie NoTMDB", Year: 2020, Path: "/data/movies/movie-no-tmdb.mkv", TmdbID: 0}
	store.movies[11] = &RadarrMovie{ID: 11, Title: "Movie AlsoNoTMDB", Year: 2021, Path: "/data/movies/movie-also-no-tmdb.mkv", TmdbID: 0}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/movie", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var movies []*RadarrMovie
	if err := json.Unmarshal(w.Body.Bytes(), &movies); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(movies) != 2 {
		t.Fatalf("got %d movies, want 2", len(movies))
	}

	tmdbMap := make(map[int64]bool)
	for _, m := range movies {
		if m.TmdbID <= 0 {
			t.Errorf("movie %q: tmdbId = %d, deve ser > 0 (fallback para ID interno)", m.Title, m.TmdbID)
		}
		if tmdbMap[m.TmdbID] {
			t.Errorf("duplicação: tmdbId = %d compartilhado entre filmes — causará UNIQUE constraint no Bazarr", m.TmdbID)
		}
		tmdbMap[m.TmdbID] = true
	}
	if len(tmdbMap) != 2 {
		t.Errorf("tmdbMap tem %d entradas, esperado 2 (IDs distintos)", len(tmdbMap))
	}
}

func TestSeriesTvdbIdFallbackDistinct(t *testing.T) {
	store := newMockStore()
	store.series[30] = &SonarrSeries{ID: 30, Title: "Show NoTvdb", TvdbID: 0, Path: "/data/shows/no-tvdb"}
	store.series[31] = &SonarrSeries{ID: 31, Title: "Show AlsoNoTvdb", TvdbID: 0, Path: "/data/shows/also-no-tvdb"}

	h := NewHandler(store)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v3/series", nil)
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var series []*SonarrSeries
	if err := json.Unmarshal(w.Body.Bytes(), &series); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("got %d series, want 2", len(series))
	}

	tvdbMap := make(map[int64]bool)
	for _, s := range series {
		if s.TvdbID <= 0 {
			t.Errorf("series %q: tvdbId = %d, deve ser > 0 (fallback para ID interno)", s.Title, s.TvdbID)
		}
		if tvdbMap[s.TvdbID] {
			t.Errorf("duplicação: tvdbId = %d compartilhado entre séries", s.TvdbID)
		}
		tvdbMap[s.TvdbID] = true
	}
	if len(tvdbMap) != 2 {
		t.Errorf("tvdbMap tem %d entradas, esperado 2 (IDs distintos)", len(tvdbMap))
	}
}

func TestParseQualityFromFilename_RawTitle_Prioritized(t *testing.T) {
	// rawTitle has Remux, fileName has no quality — should use rawTitle
	q := parseQualityFromFilename("Movie.Name.2025.UHD.BluRay.2160p.Remux", "file.mkv")
	if q.Quality.Source != "bluray" {
		t.Errorf("source = %q, want bluray (remux from rawTitle)", q.Quality.Source)
	}
	if q.Quality.Name != "Remux-2160p" {
		t.Errorf("quality.name = %q, want Remux-2160p", q.Quality.Name)
	}
	if q.Quality.Resolution != 2160 {
		t.Errorf("resolution = %d, want 2160", q.Quality.Resolution)
	}
}

func TestParseQualityFromFilename_RawTitle_WebRip(t *testing.T) {
	q := parseQualityFromFilename("Series.2024.S01E01.1080p.WEBRip.x265", "episode.mkv")
	if q.Quality.Source != "webrip" {
		t.Errorf("source = %q, want webrip", q.Quality.Source)
	}
	if q.Quality.Name != "WEBRip-1080p" {
		t.Errorf("quality.name = %q, want WEBRip-1080p", q.Quality.Name)
	}
	if q.Quality.Resolution != 1080 {
		t.Errorf("resolution = %d, want 1080", q.Quality.Resolution)
	}
}

func TestParseQualityFromFilename_RawTitle_Bdrip(t *testing.T) {
	q := parseQualityFromFilename("Movie.2023.720p.BDRip.x264", "file.mkv")
	if q.Quality.Source != "bluray" {
		t.Errorf("source = %q, want bluray (bdrip maps to bluray)", q.Quality.Source)
	}
	if q.Quality.Name != "Bluray-720p" {
		t.Errorf("quality.name = %q, want Bluray-720p", q.Quality.Name)
	}
}
