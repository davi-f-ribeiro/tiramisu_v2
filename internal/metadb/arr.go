package metadb

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UpsertARRMovie inserts or updates a movie record in arr_media.
// media_type = 'movie', series_id = 0, season/episode = 0.
func (d *DB) UpsertARRMovie(ctx context.Context, tmdbID int64, imdbID, title, rawTitle string, year int, fullPath string, size int64) error {
	query := `INSERT INTO arr_media
		(media_type, tmdb_id, imdb_id, raw_title, series_id, season_number, episode_number, title, year, path, size)
	VALUES ('movie', $1, $2, $3, 0, 0, 0, $4, $5, $6, $7)
	ON CONFLICT(path) DO UPDATE SET
		tmdb_id       = EXCLUDED.tmdb_id,
		imdb_id       = EXCLUDED.imdb_id,
		raw_title     = EXCLUDED.raw_title,
		series_id     = 0,
		season_number = 0,
		episode_number = 0,
		title         = EXCLUDED.title,
		year          = EXCLUDED.year,
		size          = EXCLUDED.size,
		updated_at    = datetime('now')`

	_, err := d.db.ExecContext(ctx, query, tmdbID, imdbID, rawTitle, title, year, fullPath, size)
	if err != nil {
		return fmt.Errorf("metadb: upsert ARR movie: %w", err)
	}
	return nil
}

// UpsertARRSeries inserts or updates a TV series parent record in arr_media.
// media_type = 'series' and returns the row id (existing or newly inserted).
func (d *DB) UpsertARRSeries(ctx context.Context, tmdbID, tvdbID int64, imdbID, title, seriesDir string) (int64, error) {
	query := `INSERT INTO arr_media
		(media_type, tmdb_id, tvdb_id, imdb_id, series_id, season_number, episode_number, title, path)
	VALUES ('series', $1, $2, $3, 0, 0, 0, $4, $5)
	ON CONFLICT(path) DO UPDATE SET
		tmdb_id       = EXCLUDED.tmdb_id,
		tvdb_id       = EXCLUDED.tvdb_id,
		imdb_id       = EXCLUDED.imdb_id,
		title         = EXCLUDED.title,
		updated_at    = datetime('now')
	RETURNING id`

	var id int64
	err := d.db.QueryRowContext(ctx, query, tmdbID, tvdbID, imdbID, title, seriesDir).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("metadb: upsert ARR series: %w", err)
	}
	return id, nil
}

// UpsertARREpisode inserts or updates an episode record in arr_media,
// linked to a series via series_id.
func (d *DB) UpsertARREpisode(ctx context.Context, seriesID int64, season, episode int, title, fullPath string, size int64) error {
	query := `INSERT INTO arr_media
		(media_type, series_id, season_number, episode_number, title, path, size)
	VALUES ('episode', $1, $2, $3, $4, $5, $6)
	ON CONFLICT(path) DO UPDATE SET
		series_id     = EXCLUDED.series_id,
		season_number = EXCLUDED.season_number,
		episode_number = EXCLUDED.episode_number,
		title         = EXCLUDED.title,
		size          = EXCLUDED.size,
		updated_at    = datetime('now')`

	_, err := d.db.ExecContext(ctx, query, seriesID, season, episode, title, fullPath, size)
	if err != nil {
		return fmt.Errorf("metadb: upsert ARR episode: %w", err)
	}
	return nil
}

// DeleteARRMediaByPath removes the record matching the given full path.
func (d *DB) DeleteARRMediaByPath(ctx context.Context, fullPath string) error {
	result, err := d.db.ExecContext(ctx,
		"DELETE FROM arr_media WHERE path = $1", fullPath)
	if err != nil {
		return fmt.Errorf("metadb: delete ARR media: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("metadb: delete ARR media: no row found for path %q", fullPath)
	}
	return nil
}

// ARRMovie represents a movie row from arr_media.
type ARRMovie struct {
	ID        int64
	TMDBID    int64
	IMDBID    string
	RawTitle  string
	Title     string
	Year      int
	Size      int64
	Path      string
	UpdatedAt time.Time
}

// GetARRMovieByPath reads a movie record by its full path.
func (d *DB) GetARRMovieByPath(ctx context.Context, path string) (*ARRMovie, error) {
	row := d.db.QueryRowContext(ctx,
		"SELECT id, tmdb_id, imdb_id, raw_title, title, year, size, path, updated_at FROM arr_media WHERE path = $1 AND media_type = 'movie'", path)

	var m ARRMovie
	var updatedAt string
	err := row.Scan(&m.ID, &m.TMDBID, &m.IMDBID, &m.RawTitle, &m.Title, &m.Year, &m.Size, &m.Path, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("metadb: arr movie not found: %q", path)
	}
	if err != nil {
		return nil, fmt.Errorf("metadb: get ARR movie: %w", err)
	}
	m.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &m, nil
}

// ARREpisode represents an episode row from arr_media.
type ARREpisode struct {
	ID           int64
	SeriesID     int64
	SeasonNumber int
	EpisodeNumber int
	Title        string
	Path         string
	Size         int64
	UpdatedAt    time.Time
}

// GetARREpisodeByPath reads an episode record by its full path.
func (d *DB) GetARREpisodeByPath(ctx context.Context, path string) (*ARREpisode, error) {
	row := d.db.QueryRowContext(ctx,
		"SELECT id, series_id, season_number, episode_number, title, path, size, updated_at FROM arr_media WHERE path = $1 AND media_type = 'episode'", path)

	var e ARREpisode
	var updatedAt string
	err := row.Scan(&e.ID, &e.SeriesID, &e.SeasonNumber, &e.EpisodeNumber, &e.Title, &e.Path, &e.Size, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("metadb: arr episode not found: %q", path)
	}
	if err != nil {
		return nil, fmt.Errorf("metadb: get ARR episode: %w", err)
	}
	e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &e, nil
}

// UnresolvedMedia represents a movie or series row that has imdb_id but no tmdb_id.
type UnresolvedMedia struct {
	ID        int64
	MediaType string
	IMDBID    string
}

// GetUnresolvedARRMedia returns media records where tmdb_id is 0 and imdb_id is not empty.
func (d *DB) GetUnresolvedARRMedia(ctx context.Context) ([]UnresolvedMedia, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, media_type, imdb_id FROM arr_media
		 WHERE tmdb_id = 0 AND imdb_id != '' AND media_type IN ('movie', 'series')`)
	if err != nil {
		return nil, fmt.Errorf("metadb: get unresolved ARR media: %w", err)
	}
	defer rows.Close()

	var result []UnresolvedMedia
	for rows.Next() {
		var m UnresolvedMedia
		if err := rows.Scan(&m.ID, &m.MediaType, &m.IMDBID); err != nil {
			return nil, fmt.Errorf("metadb: scan unresolved media: %w", err)
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// UpsertARRMediaTMDB updates tmdb_id and year for a given arr_media row by ID.
func (d *DB) UpsertARRMediaTMDB(ctx context.Context, mediaID, tmdbID int64, year int) error {
	query := `UPDATE arr_media SET tmdb_id = $1, year = $2, updated_at = datetime('now') WHERE id = $3`
	_, err := d.db.ExecContext(ctx, query, tmdbID, year, mediaID)
	if err != nil {
		return fmt.Errorf("metadb: upsert ARR media tmdb: %w", err)
	}
	return nil
}
