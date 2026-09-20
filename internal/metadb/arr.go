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
	ID            int64
	SeriesID      int64
	SeasonNumber  int
	EpisodeNumber int
	Title         string
	Path          string
	Size          int64
	UpdatedAt     time.Time
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

// UpsertARRMediaTMDB updates tmdb_id, year, title, poster_path and backdrop_path for a given
// arr_media row by ID.
func (d *DB) UpsertARRMediaTMDB(ctx context.Context, mediaID, tmdbID int64, year int, title, posterPath, backdropPath string) error {
	query := `UPDATE arr_media SET tmdb_id = $1, year = $2, title = $3, poster_path = $4, backdrop_path = $5, updated_at = datetime('now') WHERE id = $6`
	_, err := d.db.ExecContext(ctx, query, tmdbID, year, title, posterPath, backdropPath, mediaID)
	if err != nil {
		return fmt.Errorf("metadb: upsert ARR media tmdb: %w", err)
	}
	return nil
}

// ARRMediaForSubtitleDiscovery is a movie or episode that still needs a virtual subtitle row.
type ARRMediaForSubtitleDiscovery struct {
	ID        int64
	MediaType string
	Title     string
	Path      string
}

// VirtualSubtitle is the persisted virtual .srt selection for one media file.
type VirtualSubtitle struct {
	MediaPath  string
	Language   string
	SubtitleID string
	Provider   string
	Score      float64
}

// UpsertVirtualSubtitle inserts or updates the persisted virtual subtitle selection.
func (d *DB) UpsertVirtualSubtitle(ctx context.Context, rec VirtualSubtitle) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO virtual_subtitles
		(media_path, language, subtitle_id, provider, score, updated_at)
	VALUES ($1, $2, $3, $4, $5, datetime('now'))
	ON CONFLICT(media_path) DO UPDATE SET
		language = EXCLUDED.language,
		subtitle_id = EXCLUDED.subtitle_id,
		provider = EXCLUDED.provider,
		score = EXCLUDED.score,
		updated_at = datetime('now')`, rec.MediaPath, rec.Language, rec.SubtitleID, rec.Provider, rec.Score)
	if err != nil {
		return fmt.Errorf("metadb: upsert virtual subtitle: %w", err)
	}
	return nil
}

// GetVirtualSubtitle returns the persisted virtual subtitle selection for a media path/language.
func (d *DB) GetVirtualSubtitle(ctx context.Context, mediaPath, language string) (*VirtualSubtitle, error) {
	row := d.db.QueryRowContext(ctx, `SELECT media_path, language, subtitle_id, provider, score
		FROM virtual_subtitles WHERE media_path = $1 AND language = $2`, mediaPath, language)
	var rec VirtualSubtitle
	if err := row.Scan(&rec.MediaPath, &rec.Language, &rec.SubtitleID, &rec.Provider, &rec.Score); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("metadb: get virtual subtitle: %w", err)
	}
	return &rec, nil
}

// ListARRMediaMissingVirtualSubtitles returns movie/episode rows without a virtual subtitle record.
func (d *DB) ListARRMediaMissingVirtualSubtitles(ctx context.Context, language string) ([]ARRMediaForSubtitleDiscovery, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT a.id, a.media_type, a.title, a.path
		FROM arr_media a
		LEFT JOIN virtual_subtitles v ON v.media_path = a.path AND v.language = $1
		WHERE a.media_type IN ('movie', 'episode')
		  AND (v.media_path IS NULL OR (v.subtitle_id = '' AND v.updated_at <= datetime('now', '-24 hours')))
		ORDER BY a.id`, language)
	if err != nil {
		return nil, fmt.Errorf("metadb: list media missing virtual subtitles: %w", err)
	}
	defer rows.Close()
	var out []ARRMediaForSubtitleDiscovery
	for rows.Next() {
		var rec ARRMediaForSubtitleDiscovery
		if err := rows.Scan(&rec.ID, &rec.MediaType, &rec.Title, &rec.Path); err != nil {
			return nil, fmt.Errorf("metadb: scan media missing virtual subtitles: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ListVirtualSubtitles returns all persisted virtual subtitle selections for a language.
func (d *DB) ListVirtualSubtitles(ctx context.Context, language string) ([]VirtualSubtitle, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT media_path, language, subtitle_id, provider, score
		FROM virtual_subtitles WHERE language = $1 ORDER BY media_path`, language)
	if err != nil {
		return nil, fmt.Errorf("metadb: list virtual subtitles: %w", err)
	}
	defer rows.Close()
	var out []VirtualSubtitle
	for rows.Next() {
		var rec VirtualSubtitle
		if err := rows.Scan(&rec.MediaPath, &rec.Language, &rec.SubtitleID, &rec.Provider, &rec.Score); err != nil {
			return nil, fmt.Errorf("metadb: scan virtual subtitle: %w", err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteVirtualSubtitleMisses removes negative discovery cache rows.
func (d *DB) DeleteVirtualSubtitleMisses(ctx context.Context) (int64, error) {
	res, err := d.db.ExecContext(ctx, `DELETE FROM virtual_subtitles WHERE provider = 'none' AND subtitle_id = ''`)
	if err != nil {
		return 0, fmt.Errorf("metadb: delete virtual subtitle misses: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}
