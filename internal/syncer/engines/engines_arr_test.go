package engines

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"tiramisu/internal/metadb"
)

// mockARRDB is a lightweight mock that mirrors metadb.DB operations using
// an in-memory SQLite database for integration testing.
type mockARRDB struct {
	db *sql.DB
}

func newMockARRDB(t *testing.T) *mockARRDB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	// Initialize the arr_media table
	_, err = db.Exec(`
		CREATE TABLE arr_media (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			media_type TEXT NOT NULL,
			tmdb_id INTEGER DEFAULT 0,
			tvdb_id INTEGER DEFAULT 0,
			imdb_id TEXT DEFAULT '',
			raw_title TEXT DEFAULT '',
			series_id INTEGER DEFAULT 0,
			season_number INTEGER DEFAULT 0,
			episode_number INTEGER DEFAULT 0,
			title TEXT NOT NULL,
			year INTEGER DEFAULT 0,
			path TEXT NOT NULL UNIQUE,
			size INTEGER DEFAULT 0,
			updated_at TEXT DEFAULT (datetime('now'))
		);
		CREATE INDEX IF NOT EXISTS idx_arr_media_type ON arr_media(media_type);
		CREATE INDEX IF NOT EXISTS idx_arr_media_series ON arr_media(series_id);
	`)
	if err != nil {
		t.Fatalf("create arr_media table: %v", err)
	}
	return &mockARRDB{db: db}
}

func (m *mockARRDB) UpsertARRMovie(ctx context.Context, tmdbID int64, imdbID, title, rawTitle string, year int, fullPath string, size int64) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO arr_media (media_type, tmdb_id, imdb_id, raw_title, series_id, season_number, episode_number, title, year, path, size)
		 VALUES ('movie', $1, $2, $3, 0, 0, 0, $4, $5, $6, $7)
		 ON CONFLICT(path) DO UPDATE SET
		 tmdb_id=EXCLUDED.tmdb_id, imdb_id=EXCLUDED.imdb_id, raw_title=EXCLUDED.raw_title, series_id=0,
		 season_number=0, episode_number=0, title=EXCLUDED.title,
		 year=EXCLUDED.year, size=EXCLUDED.size, updated_at=datetime('now')`,
		tmdbID, imdbID, rawTitle, title, year, fullPath, size)
	return err
}

func (m *mockARRDB) UpsertARRSeries(ctx context.Context, tmdbID, tvdbID int64, imdbID, title, seriesDir string) (int64, error) {
	var id int64
	err := m.db.QueryRowContext(ctx,
		`INSERT INTO arr_media (media_type, tmdb_id, tvdb_id, imdb_id, series_id, season_number, episode_number, title, path)
		 VALUES ('series', $1, $2, $3, 0, 0, 0, $4, $5)
		 ON CONFLICT(path) DO UPDATE SET
			 tmdb_id=EXCLUDED.tmdb_id, tvdb_id=EXCLUDED.tvdb_id, imdb_id=EXCLUDED.imdb_id,
			 title=EXCLUDED.title, updated_at=datetime('now')
		 RETURNING id`,
		tmdbID, tvdbID, imdbID, title, seriesDir).Scan(&id)
	return id, err
}

func (m *mockARRDB) UpsertARREpisode(ctx context.Context, seriesID int64, season, episode int, title, fullPath string, size int64) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO arr_media (media_type, series_id, season_number, episode_number, title, path, size)
		 VALUES ('episode', $1, $2, $3, $4, $5, $6)
		 ON CONFLICT(path) DO UPDATE SET
			 series_id=EXCLUDED.series_id, season_number=EXCLUDED.season_number,
			 episode_number=EXCLUDED.episode_number, title=EXCLUDED.title,
			 size=EXCLUDED.size, updated_at=datetime('now')`,
		seriesID, season, episode, title, fullPath, size)
	return err
}

func (m *mockARRDB) DeleteARRMediaByPath(ctx context.Context, fullPath string) error {
	result, err := m.db.ExecContext(ctx, "DELETE FROM arr_media WHERE path = $1", fullPath)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil // idempotent for tests
	}
	return nil
}

func (m *mockARRDB) GetARRMovieByPath(ctx context.Context, path string) (*metadb.ARRMovie, error) {
	row := m.db.QueryRowContext(ctx,
		"SELECT id, tmdb_id, imdb_id, title, year, size, path, updated_at FROM arr_media WHERE path = $1 AND media_type = 'movie'", path)
	var m2 metadb.ARRMovie
	var updatedAt string
	err := row.Scan(&m2.ID, &m2.TMDBID, &m2.IMDBID, &m2.Title, &m2.Year, &m2.Size, &m2.Path, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &m2, err
}

func (m *mockARRDB) GetARREpisodeByPath(ctx context.Context, path string) (*metadb.ARREpisode, error) {
	row := m.db.QueryRowContext(ctx,
		"SELECT id, series_id, season_number, episode_number, title, path, size, updated_at FROM arr_media WHERE path = $1 AND media_type = 'episode'", path)
	var e metadb.ARREpisode
	var updatedAt string
	err := row.Scan(&e.ID, &e.SeriesID, &e.SeasonNumber, &e.EpisodeNumber, &e.Title, &e.Path, &e.Size, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &e, err
}

func (m *mockARRDB) Close() error {
	return m.db.Close()
}

// TestMovieARRLifecycle verifies the full lifecycle of a movie in the ARR catalog:
// creation via UpsertARRMovie followed by removal via DeleteARRMediaByPath.
func TestMovieARRLifecycle(t *testing.T) {
	ctx := context.Background()
	mock := newMockARRDB(t)
	defer mock.Close()

	// 1. Inserção de filme
	err := mock.UpsertARRMovie(ctx, 12345, "tt0111111", "The Shawshank Redemption", "The Shawshank Redemption (1994)", 1994, "/data/movies/The Shawshank Redemption (1994).mkv", 2147483648)
	if err != nil {
		t.Fatalf("UpsertARRMovie failed: %v", err)
	}

	// 2. Verificar persistência dos campos
	record, err := mock.GetARRMovieByPath(ctx, "/data/movies/The Shawshank Redemption (1994).mkv")
	if err != nil {
		t.Fatalf("GetARRMovieByPath failed: %v", err)
	}
	if record == nil {
		t.Fatal("expected movie record, got nil")
	}
	if record.TMDBID != 12345 {
		t.Errorf("expected TMDBID 12345, got %d", record.TMDBID)
	}
	if record.IMDBID != "tt0111111" {
		t.Errorf("expected IMDBID tt0111111, got %s", record.IMDBID)
	}
	if record.Title != "The Shawshank Redemption" {
		t.Errorf("expected title 'The Shawshank Redemption', got %s", record.Title)
	}
	if record.Year != 1994 {
		t.Errorf("expected year 1994, got %d", record.Year)
	}
	if record.Size != 2147483648 {
		t.Errorf("expected size 2147483648, got %d", record.Size)
	}

	// 3. Update idempotente pelo mesmo path
	err = mock.UpsertARRMovie(ctx, 12345, "tt0111111", "The Shawshank Redemption", "The Shawshank Redemption (1994)", 1994, "/data/movies/The Shawshank Redemption (1994).mkv", 3221225472)
	if err != nil {
		t.Fatalf("second UpsertARRMovie failed: %v", err)
	}

	// Verificar que o tamanho foi atualizado
	record2, err := mock.GetARRMovieByPath(ctx, "/data/movies/The Shawshank Redemption (1994).mkv")
	if err != nil {
		t.Fatalf("GetARRMovieByPath after update failed: %v", err)
	}
	if record2.Size != 3221225472 {
		t.Errorf("expected size 3221225472 after update, got %d", record2.Size)
	}

	// 4. Remoção por path
	err = mock.DeleteARRMediaByPath(ctx, "/data/movies/The Shawshank Redemption (1994).mkv")
	if err != nil {
		t.Fatalf("DeleteARRMediaByPath failed: %v", err)
	}

	// Verificar que o registro foi removido
	recordDeleted, err := mock.GetARRMovieByPath(ctx, "/data/movies/The Shawshank Redemption (1994).mkv")
	if err != nil {
		t.Logf("GetARRMovieByPath returned error after delete (acceptable): %v", err)
	} else if recordDeleted != nil {
		t.Error("expected nil movie record after delete, got non-nil")
	}
}

// TestSeriesARRLifecycle verifica o ciclo de vida de uma série e episódios:
// a série pai gera um arrSeriesID válido e os episódios são vinculados com a chave estrangeira correta.
func TestSeriesARRLifecycle(t *testing.T) {
	ctx := context.Background()
	mock := newMockARRDB(t)
	defer mock.Close()

	// 1. Inserção de série pai
	seriesID, err := mock.UpsertARRSeries(ctx, 67890, 12345, "tt0944947", "Game of Thrones", "/data/series/Game of Thrones")
	if err != nil {
		t.Fatalf("UpsertARRSeries failed: %v", err)
	}
	if seriesID <= 0 {
		t.Fatalf("expected positive seriesID, got %d", seriesID)
	}

	// Verificar registro da série
	var count int
	err = mock.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM arr_media WHERE media_type = 'series' AND id = $1", seriesID).Scan(&count)
	if err != nil {
		t.Fatalf("query series count failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 series record, got %d", count)
	}

	// 2. Inserção de múltiplos episódios atrelados à série
	episodes := []struct {
		season int
		episode int
		title  string
		path   string
		size   int64
	}{
		{1, 1, "Winter Is Coming", "/data/series/Game of Thrones/Season 1/Game of Thrones - S01E01 - Winter Is Coming.mkv", 1073741824},
		{1, 2, "The Kingsroad", "/data/series/Game of Thrones/Season 1/Game of Thrones - S01E02 - The Kingsroad.mkv", 1073741824},
		{1, 3, "Lord Snow", "/data/series/Game of Thrones/Season 1/Game of Thrones - S01E03 - Lord Snow.mkv", 1073741824},
	}

	for _, ep := range episodes {
		err := mock.UpsertARREpisode(ctx, seriesID, ep.season, ep.episode, ep.title, ep.path, ep.size)
		if err != nil {
			t.Fatalf("UpsertARREpisode S%02dE%02d failed: %v", ep.season, ep.episode, err)
		}
	}

	// 3. Verificar vínculo com chave estrangeira
	var epCount int
	err = mock.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM arr_media WHERE media_type = 'episode' AND series_id = $1", seriesID).Scan(&epCount)
	if err != nil {
		t.Fatalf("query episode count failed: %v", err)
	}
	if epCount != 3 {
		t.Errorf("expected 3 episodes linked to series %d, got %d", seriesID, epCount)
	}

	// 4. Verificar campos de episódio individual
	epRecord, err := mock.GetARREpisodeByPath(ctx, "/data/series/Game of Thrones/Season 1/Game of Thrones - S01E01 - Winter Is Coming.mkv")
	if err != nil {
		t.Fatalf("GetARREpisodeByPath failed: %v", err)
	}
	if epRecord == nil {
		t.Fatal("expected episode record, got nil")
	}
	if epRecord.SeriesID != seriesID {
		t.Errorf("expected series_id %d, got %d", seriesID, epRecord.SeriesID)
	}
	if epRecord.SeasonNumber != 1 {
		t.Errorf("expected season 1, got %d", epRecord.SeasonNumber)
	}
	if epRecord.EpisodeNumber != 1 {
		t.Errorf("expected episode 1, got %d", epRecord.EpisodeNumber)
	}
	if epRecord.Title != "Winter Is Coming" {
		t.Errorf("expected title 'Winter Is Coming', got %s", epRecord.Title)
	}
	if epRecord.Size != 1073741824 {
		t.Errorf("expected size 1073741824, got %d", epRecord.Size)
	}

	// 5. Remoção de um episódio
	err = mock.DeleteARRMediaByPath(ctx, "/data/series/Game of Thrones/Season 1/Game of Thrones - S01E01 - Winter Is Coming.mkv")
	if err != nil {
		t.Fatalf("DeleteARRMediaByPath for episode failed: %v", err)
	}

	err = mock.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM arr_media WHERE media_type = 'episode' AND series_id = $1", seriesID).Scan(&epCount)
	if err != nil {
		t.Fatalf("query episode count after delete failed: %v", err)
	}
	if epCount != 2 {
		t.Errorf("expected 2 episodes after delete, got %d", epCount)
	}
}

// TestARRFieldsIntegrity verifica se campos essenciais (tmdb_id, tvdb_id, imdb_id, path, size)
// permanecem íntegros após inserção e recuperação.
func TestARRFieldsIntegrity(t *testing.T) {
	ctx := context.Background()
	mock := newMockARRDB(t)
	defer mock.Close()

	// Inserir filme com todos os campos
	err := mock.UpsertARRMovie(ctx, 550, "tt0944947", "Game of Thrones", "Game of Thrones", 2011, "/data/movies/Game of Thrones.mkv", 4294967296)
	if err != nil {
		t.Fatalf("UpsertARRMovie failed: %v", err)
	}

	record, err := mock.GetARRMovieByPath(ctx, "/data/movies/Game of Thrones.mkv")
	if err != nil {
		t.Fatalf("GetARRMovieByPath failed: %v", err)
	}
	if record == nil {
		t.Fatal("expected movie record, got nil")
	}

	// Verificar cada campo individualmente
	fields := []struct {
		name  string
		got   any
		want  any
	}{
		{"tmdb_id", record.TMDBID, int64(550)},
		{"imdb_id", record.IMDBID, "tt0944947"},
		{"title", record.Title, "Game of Thrones"},
		{"year", record.Year, 2011},
		{"size", record.Size, int64(4294967296)},
		{"path", record.Path, "/data/movies/Game of Thrones.mkv"},
	}

	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("field %s: expected %v, got %v", f.name, f.want, f.got)
		}
	}

	// Inserir série com tvdb_id
	seriesID, err := mock.UpsertARRSeries(ctx, 1402, 121361, "tt0944947", "The Walking Dead", "/data/series/The Walking Dead")
	if err != nil {
		t.Fatalf("UpsertARRSeries failed: %v", err)
	}
	if seriesID <= 0 {
		t.Fatalf("expected positive seriesID, got %d", seriesID)
	}

	// Inserir episódio vinculado
	err = mock.UpsertARREpisode(ctx, seriesID, 1, 1, "Days Gone Bye", "/data/series/The Walking Dead/Season 1/The Walking Dead - S01E01.mkv", 2147483648)
	if err != nil {
		t.Fatalf("UpsertARREpisode failed: %v", err)
	}

	epRecord, err := mock.GetARREpisodeByPath(ctx, "/data/series/The Walking Dead/Season 1/The Walking Dead - S01E01.mkv")
	if err != nil {
		t.Fatalf("GetARREpisodeByPath failed: %v", err)
	}
	if epRecord == nil {
		t.Fatal("expected episode record, got nil")
	}

	epFields := []struct {
		name  string
		got   any
		want  any
	}{
		{"series_id", epRecord.SeriesID, seriesID},
		{"season_number", epRecord.SeasonNumber, 1},
		{"episode_number", epRecord.EpisodeNumber, 1},
		{"title", epRecord.Title, "Days Gone Bye"},
		{"size", epRecord.Size, int64(2147483648)},
	}

	for _, f := range epFields {
		if f.got != f.want {
			t.Errorf("episode field %s: expected %v, got %v", f.name, f.want, f.got)
		}
	}
}
