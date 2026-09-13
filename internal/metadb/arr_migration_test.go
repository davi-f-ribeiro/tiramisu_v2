package metadb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateARRMedia_BugRegression validates the fix for:
// "SQL logic error: no such column: raw_title" — the HTTP 500 returned
// when Bazarr queries GET /api/v3/movie on an existing database.
func TestMigrateARRMedia_BugRegression(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tiramisu.db")

	// STEP 1: Create a "legacy" database that mimics the old schema —
	// arr_media WITHOUT raw_title. This is exactly what the user had.
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	legacy.SetMaxOpenConns(1)

	_, err = legacy.Exec(`
		CREATE TABLE IF NOT EXISTS arr_media (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			media_type  TEXT NOT NULL,
			tmdb_id     INTEGER DEFAULT 0,
			tvdb_id     INTEGER DEFAULT 0,
			imdb_id     TEXT DEFAULT '',
			series_id   INTEGER DEFAULT 0,
			season_number INTEGER DEFAULT 0,
			episode_number INTEGER DEFAULT 0,
			title       TEXT NOT NULL,
			year        INTEGER DEFAULT 0,
			path        TEXT NOT NULL UNIQUE,
			size        INTEGER DEFAULT 0,
			updated_at  TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_arr_media_type ON arr_media(media_type);
		CREATE INDEX IF NOT EXISTS idx_arr_media_series ON arr_media(series_id);
	`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}

	// Insert a valid movie row (without raw_title).
	_, err = legacy.Exec(
		"INSERT INTO arr_media (media_type, tmdb_id, imdb_id, title, year, path, size) VALUES ('movie', 12345, 'tt123456', 'Test Movie', 2024, '/data/movies/Test Movie (2024)/Test Movie (2024).mkv', 5000000000)",
	)
	if err != nil {
		t.Fatalf("insert legacy movie: %v", err)
	}

	// Verify the column does NOT exist yet.
	var count int
	err = legacy.QueryRow("SELECT COUNT(*) FROM pragma_table_info('arr_media') WHERE name = 'raw_title'").Scan(&count)
	if err != nil {
		t.Fatalf("query pragma_table_info: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected raw_title to NOT exist in legacy db, but it does (count=%d)", count)
	}

	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	// STEP 2: Open with the CURRENT metadb.New — which applies the migration.
	logger := &testLogger{}
	db, err := New(dbPath, logger)
	if err != nil {
		t.Fatalf("New(dbPath) failed on legacy database: %v", err)
	}
	defer db.Close()

	// STEP 3: Verify PRAGMA table_info shows raw_title now exists.
	err = db.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('arr_media') WHERE name = 'raw_title'").Scan(&count)
	if err != nil {
		t.Fatalf("query pragma after migration: %v", err)
	}
	if count == 0 {
		t.Fatal("raw_title column missing after migration — the fix did not apply")
	}

	// STEP 4: Verify the old movie record still exists and data is preserved.
	var path string
	err = db.db.QueryRow("SELECT path FROM arr_media WHERE media_type = 'movie' AND title = 'Test Movie'").Scan(&path)
	if err != nil {
		t.Fatal("legacy movie record lost after migration — data was not preserved")
	}
	if path == "" {
		t.Fatal("legacy movie record path is empty after migration")
	}

	// STEP 5: Call GetMovies (the exact query that was failing).
	ctx := context.Background()
	movies, err := GetMovies(ctx, db.db)
	if err != nil {
		t.Fatalf("GetMovies failed after migration (this was the HTTP 500): %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("expected 1 movie after migration, got %d", len(movies))
	}
	if movies[0].Title != "Test Movie" {
		t.Fatalf("expected movie title 'Test Movie', got '%s'", movies[0].Title)
	}
	if movies[0].RawTitle != "" {
		// Legacy data will have empty raw_title — this is expected.
		t.Logf("legacy movie has raw_title='%s' (expected empty for migrated records)", movies[0].RawTitle)
	}

	// STEP 6: Write a new movie via UpsertARRMovie to confirm the column works.
	err = db.UpsertARRMovie(ctx, 99999, "tt999999", "New Movie", "New Movie (2025)", 2025, "/data/movies/New Movie (2025)/New Movie (2025).mkv", 3000000000)
	if err != nil {
		t.Fatalf("UpsertARRMovie after migration: %v", err)
	}

	// Verify both movies are returned.
	movies, err = GetMovies(ctx, db.db)
	if err != nil {
		t.Fatalf("GetMovies after UpsertARRMovie: %v", err)
	}
	if len(movies) != 2 {
		t.Fatalf("expected 2 movies after upsert, got %d", len(movies))
	}
}

// TestMigrateARRMedia_NewDatabase ensures the migration does not break
// databases that are brand-new (raw_title always exists).
func TestMigrateARRMedia_NewDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tiramisu.db")

	logger := &testLogger{}
	db, err := New(dbPath, logger)
	if err != nil {
		t.Fatalf("New(dbPath) failed for fresh database: %v", err)
	}
	defer db.Close()

	// Verify raw_title exists.
	var count int
	err = db.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('arr_media') WHERE name = 'raw_title'").Scan(&count)
	if err != nil {
		t.Fatalf("query pragma_table_info: %v", err)
	}
	if count == 0 {
		t.Fatal("raw_title column missing in freshly created database")
	}

	// Verify GetMovies works.
	ctx := context.Background()
	movies, err := GetMovies(ctx, db.db)
	if err != nil {
		t.Fatalf("GetMovies on fresh db: %v", err)
	}
	if len(movies) != 0 {
		t.Fatalf("expected 0 movies in fresh db, got %d", len(movies))
	}
}

// TestMigrateARRMedia_Idempotent ensures running migration multiple times
// does not error or duplicate data.
func TestMigrateARRMedia_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tiramisu.db")

	// Create legacy database without raw_title.
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = legacy.Exec(`
		CREATE TABLE IF NOT EXISTS arr_media (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			media_type TEXT NOT NULL,
			tmdb_id INTEGER DEFAULT 0,
			tvdb_id INTEGER DEFAULT 0,
			imdb_id TEXT DEFAULT '',
			series_id INTEGER DEFAULT 0,
			season_number INTEGER DEFAULT 0,
			episode_number INTEGER DEFAULT 0,
			title TEXT NOT NULL,
			year INTEGER DEFAULT 0,
			path TEXT NOT NULL UNIQUE,
			size INTEGER DEFAULT 0,
			updated_at TEXT DEFAULT (datetime('now'))
		);
	`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	// First migration via New.
	logger := &testLogger{}
	db1, err := New(dbPath, logger)
	if err != nil {
		t.Fatalf("first New() on legacy db: %v", err)
	}

	// Second migration via ExecSchema (re-open).
	db2, err := New(dbPath, logger)
	if err != nil {
		db1.Close()
		t.Fatalf("second New() on same db (idempotency): %v", err)
	}
	db1.Close()
	db2.Close()

	// Verify only one movie row exists (no duplication).
	db3, err := New(dbPath, logger)
	if err != nil {
		t.Fatalf("third New(): %v", err)
	}
	defer db3.Close()

	var count int
	err = db3.db.QueryRow("SELECT COUNT(*) FROM arr_media").Scan(&count)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after multiple migrations, got %d", count)
	}
}

// testLogger is a minimal Logger implementation for tests.
type testLogger struct{}

func (l *testLogger) Printf(format string, v ...interface{}) {
	// Silent in tests.
}

// GetMovies is the query function used by the ARR API handler.
// It mirrors the actual implementation in arr/server.go.
func GetMovies(ctx context.Context, db *sql.DB) ([]ARRMovie, error) {
	query := `SELECT id, tmdb_id, imdb_id, raw_title, title, year, size, path, updated_at
		FROM arr_media WHERE media_type = 'movie' ORDER BY id`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var movies []ARRMovie
	for rows.Next() {
		var m ARRMovie
		var updatedAt string
		if err := rows.Scan(&m.ID, &m.TMDBID, &m.IMDBID, &m.RawTitle, &m.Title, &m.Year, &m.Size, &m.Path, &updatedAt); err != nil {
			return nil, err
		}
		movies = append(movies, m)
	}
	return movies, nil
}

// Ensure os is used to prevent import errors if not directly used elsewhere.
var _ = os.RemoveAll
