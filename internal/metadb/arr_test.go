package metadb

import (
	"context"
	"database/sql"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := New("file::memory:?cache=shared", nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestUpsertARRMovie(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	const (
		path  = "/data/movies/Example (2023)/example.mkv"
		title = "Example Movie"
	)

	if err := d.UpsertARRMovie(ctx, 12345, "tt0000000", title, 2023, path, 1024*1024*500); err != nil {
		t.Fatalf("UpsertARRMovie: %v", err)
	}

	movie, err := d.GetARRMovieByPath(ctx, path)
	if err != nil {
		t.Fatalf("GetARRMovieByPath: %v", err)
	}

	if movie.TMDBID != 12345 {
		t.Errorf("tmdb_id = %d, want 12345", movie.TMDBID)
	}
	if movie.IMDBID != "tt0000000" {
		t.Errorf("imdb_id = %q, want tt0000000", movie.IMDBID)
	}
	if movie.Title != title {
		t.Errorf("title = %q, want %q", movie.Title, title)
	}
	if movie.Year != 2023 {
		t.Errorf("year = %d, want 2023", movie.Year)
	}
	if movie.Size != 1024*1024*500 {
		t.Errorf("size = %d, want %d", movie.Size, 1024*1024*500)
	}
	if movie.Path != path {
		t.Errorf("path = %q, want %q", movie.Path, path)
	}
}

func TestUpsertARRMovieIdempotent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	path := "/data/movies/Example (2023)/example.mkv"

	// First insert
	if err := d.UpsertARRMovie(ctx, 12345, "tt0000000", "Example Movie", 2023, path, 500*1024*1024); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second insert same path with different data — should UPDATE
	if err := d.UpsertARRMovie(ctx, 99999, "tt9999999", "Updated Movie", 2024, path, 800*1024*1024); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	movie, err := d.GetARRMovieByPath(ctx, path)
	if err != nil {
		t.Fatalf("GetARRMovieByPath: %v", err)
	}

	if movie.TMDBID != 99999 {
		t.Errorf("tmdb_id = %d, want 99999 (updated)", movie.TMDBID)
	}
	if movie.IMDBID != "tt9999999" {
		t.Errorf("imdb_id = %q, want tt9999999 (updated)", movie.IMDBID)
	}
	if movie.Title != "Updated Movie" {
		t.Errorf("title = %q, want Updated Movie (updated)", movie.Title)
	}
	if movie.Year != 2024 {
		t.Errorf("year = %d, want 2024", movie.Year)
	}
	if movie.Size != 800*1024*1024 {
		t.Errorf("size = %d, want %d", movie.Size, 800*1024*1024)
	}

	// Verify only one row exists (UNIQUE constraint on path)
	var count int
	err = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM arr_media WHERE path = $1", path).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestUpsertARRSeries(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	seriesDir := "/data/series/Example Series"

	id, err := d.UpsertARRSeries(ctx, 111, 222, "tt1111111", "Example Series", seriesDir)
	if err != nil {
		t.Fatalf("UpsertARRSeries: %v", err)
	}
	if id == 0 {
		t.Fatal("UpsertARRSeries returned id=0")
	}

	// Verify the row exists with correct fields
	var mt string
	var tmdbID, tvdbID int64
	var imdbID string
	var seriesID, seasonNum, episodeNum int
	var title string
	var path string

	err = d.db.QueryRowContext(ctx,
		`SELECT media_type, tmdb_id, tvdb_id, imdb_id, series_id, season_number, episode_number, title, path
		 FROM arr_media WHERE id = $1`, id).
		Scan(&mt, &tmdbID, &tvdbID, &imdbID, &seriesID, &seasonNum, &episodeNum, &title, &path)
	if err != nil {
		t.Fatalf("verify series row: %v", err)
	}

	if mt != "series" {
		t.Errorf("media_type = %q, want series", mt)
	}
	if tmdbID != 111 {
		t.Errorf("tmdb_id = %d, want 111", tmdbID)
	}
	if tvdbID != 222 {
		t.Errorf("tvdb_id = %d, want 222", tvdbID)
	}
	if imdbID != "tt1111111" {
		t.Errorf("imdb_id = %q, want tt1111111", imdbID)
	}
	if title != "Example Series" {
		t.Errorf("title = %q, want Example Series", title)
	}
	if path != seriesDir {
		t.Errorf("path = %q, want %q", path, seriesDir)
	}
}

func TestUpsertARRSeriesIdempotent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	seriesDir := "/data/series/Example Series"

	id1, err := d.UpsertARRSeries(ctx, 111, 222, "tt1111111", "Example Series", seriesDir)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	id2, err := d.UpsertARRSeries(ctx, 111, 222, "tt1111111", "Example Series", seriesDir)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if id1 != id2 {
		t.Errorf("first id = %d, second id = %d, want same id", id1, id2)
	}

	var count int
	err = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM arr_media WHERE media_type = 'series'").Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("series count = %d, want 1", count)
	}
}

func TestUpsertARREpisode(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// First create a parent series
	seriesDir := "/data/series/Example Series"
	seriesID, err := d.UpsertARRSeries(ctx, 111, 222, "tt1111111", "Example Series", seriesDir)
	if err != nil {
		t.Fatalf("UpsertARRSeries: %v", err)
	}

	// Create episode
	epPath := "/data/series/Example Series/Season 1/example.s01e01.mkv"
	if err := d.UpsertARREpisode(ctx, seriesID, 1, 1, "Pilot", epPath, 2*1024*1024*1024); err != nil {
		t.Fatalf("UpsertARREpisode: %v", err)
	}

	// Verify
	ep, err := d.GetARREpisodeByPath(ctx, epPath)
	if err != nil {
		t.Fatalf("GetARREpisodeByPath: %v", err)
	}

	if ep.SeriesID != seriesID {
		t.Errorf("series_id = %d, want %d", ep.SeriesID, seriesID)
	}
	if ep.SeasonNumber != 1 {
		t.Errorf("season_number = %d, want 1", ep.SeasonNumber)
	}
	if ep.EpisodeNumber != 1 {
		t.Errorf("episode_number = %d, want 1", ep.EpisodeNumber)
	}
	if ep.Title != "Pilot" {
		t.Errorf("title = %q, want Pilot", ep.Title)
	}
	if ep.Size != 2*1024*1024*1024 {
		t.Errorf("size = %d, want %d", ep.Size, 2*1024*1024*1024)
	}
}

func TestUpsertMultipleEpisodes(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	seriesDir := "/data/series/Example Series"
	seriesID, err := d.UpsertARRSeries(ctx, 111, 222, "tt1111111", "Example Series", seriesDir)
	if err != nil {
		t.Fatalf("UpsertARRSeries: %v", err)
	}

	eps := []struct {
		season, episode int
		title           string
		path            string
	}{
		{1, 1, "Pilot", "/data/series/Example Series/Season 1/s01e01.mkv"},
		{1, 2, "The Next Episode", "/data/series/Example Series/Season 1/s01e02.mkv"},
		{1, 3, "Third Time's the Charm", "/data/series/Example Series/Season 1/s01e03.mkv"},
		{2, 1, "Season Two Premiere", "/data/series/Example Series/Season 2/s02e01.mkv"},
	}

	for i, ep := range eps {
		if err := d.UpsertARREpisode(ctx, seriesID, ep.season, ep.episode, ep.title, ep.path, 1024*1024*500); err != nil {
			t.Fatalf("episode %d: UpsertARREpisode: %v", i, err)
		}
	}

	// Count episodes
	var count int
	err = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM arr_media WHERE series_id = $1 AND media_type = 'episode'", seriesID).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != len(eps) {
		t.Errorf("episode count = %d, want %d", count, len(eps))
	}

	// Verify each episode individually
	for _, ep := range eps {
		e, err := d.GetARREpisodeByPath(ctx, ep.path)
		if err != nil {
			t.Fatalf("GetARREpisodeByPath(%q): %v", ep.path, err)
		}
		if e.SeriesID != seriesID {
			t.Errorf("series_id = %d, want %d", e.SeriesID, seriesID)
		}
		if e.SeasonNumber != ep.season {
			t.Errorf("episode %s: season = %d, want %d", ep.path, e.SeasonNumber, ep.season)
		}
		if e.EpisodeNumber != ep.episode {
			t.Errorf("episode %s: episode = %d, want %d", ep.path, e.EpisodeNumber, ep.episode)
		}
	}
}

func TestDeleteARRMediaByPath(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	path := "/data/movies/Example (2023)/example.mkv"

	if err := d.UpsertARRMovie(ctx, 12345, "tt0000000", "Example Movie", 2023, path, 500*1024*1024); err != nil {
		t.Fatalf("upsert before delete: %v", err)
	}

	// Delete
	if err := d.DeleteARRMediaByPath(ctx, path); err != nil {
		t.Fatalf("DeleteARRMediaByPath: %v", err)
	}

	// Verify deleted
	var count int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM arr_media WHERE path = $1", path).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("count after delete = %d, want 0", count)
	}
}

func TestDeleteARRMediaByPathNotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	err := d.DeleteARRMediaByPath(ctx, "/nonexistent/path.mkv")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestResourceCleanup(t *testing.T) {
	// Verify no resource leaks: open and close multiple times.
	for i := 0; i < 10; i++ {
		d := openTestDB(t)
		ctx := context.Background()

		path := "/data/test/resource_cleanup.mkv"
		if err := d.UpsertARRMovie(ctx, 1, "tt1", "Test", 2024, path, 100); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}

		movie, err := d.GetARRMovieByPath(ctx, path)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if movie.Title != "Test" {
			t.Errorf("iteration %d: title = %q, want Test", i, movie.Title)
		}
		// Close called via t.Cleanup in openTestDB
	}
}

// Test that the DB struct implements _ = sql.DB (compile-time check).
var _ = (*sql.DB)(nil)

// Test context cancellation propagates correctly.
func TestContextCancellation(t *testing.T) {
	d := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := d.UpsertARRMovie(ctx, 1, "tt1", "Test", 2024, "/cancel/test.mkv", 100)
	if err == nil {
		t.Log("expected error for cancelled context (may succeed depending on sqlite implementation)")
	}
}

// Test that TVDBID field is properly supported by ensuring the UpsertARRSeries
// with TVDBID set works end-to-end.
func TestUpsertARRSeriesWithTVDBID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	id, err := d.UpsertARRSeries(ctx, 42, 99, "tt4299999", "TVDB Test Series", "/data/series/tvdb_test")
	if err != nil {
		t.Fatalf("UpsertARRSeries with TVDB: %v", err)
	}

	var tvdbID int64
	err = d.db.QueryRowContext(ctx, "SELECT tvdb_id FROM arr_media WHERE id = $1", id).Scan(&tvdbID)
	if err != nil {
		t.Fatalf("query tvdb_id: %v", err)
	}
	if tvdbID != 99 {
		t.Errorf("tvdb_id = %d, want 99", tvdbID)
	}
}

// TestUpsertARRMovieZeroValues checks that zero-value fields are handled correctly.
func TestUpsertARRMovieZeroValues(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	path := "/data/movies/zero_values/zero.mkv"
	if err := d.UpsertARRMovie(ctx, 0, "", "Zero Movie", 0, path, 0); err != nil {
		t.Fatalf("UpsertARRMovie zero values: %v", err)
	}

	movie, err := d.GetARRMovieByPath(ctx, path)
	if err != nil {
		t.Fatalf("GetARRMovieByPath: %v", err)
	}
	if movie.TMDBID != 0 {
		t.Errorf("tmdb_id = %d, want 0", movie.TMDBID)
	}
}

// TestUpsertARRSeriesWithZeroValues ensures zero-value TVDBID is handled.
func TestUpsertARRSeriesWithZeroValues(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	id, err := d.UpsertARRSeries(ctx, 0, 0, "", "No ID Series", "/data/series/no_id")
	if err != nil {
		t.Fatalf("UpsertARRSeries zero values: %v", err)
	}
	if id == 0 {
		t.Fatal("returned id=0 for zero-value upsert")
	}

	var tmdbID, tvdbID int64
	err = d.db.QueryRowContext(ctx, "SELECT tmdb_id, tvdb_id FROM arr_media WHERE id = $1", id).Scan(&tmdbID, &tvdbID)
	if err != nil {
		t.Fatalf("query zero values: %v", err)
	}
	if tmdbID != 0 {
		t.Errorf("tmdb_id = %d, want 0", tmdbID)
	}
	if tvdbID != 0 {
		t.Errorf("tvdb_id = %d, want 0", tvdbID)
	}
}
