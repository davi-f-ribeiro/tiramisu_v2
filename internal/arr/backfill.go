package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BackfillStats carries counters from a single RunBackfill invocation.
type BackfillStats struct {
	MoviesIndexed   int   `json:"movies_indexed"`
	SeriesIndexed   int   `json:"series_indexed"`
	EpisodesIndexed int   `json:"episodes_indexed"`
	ErrorsCount     int   `json:"errors_count"`
	DurationMs      int64 `json:"duration_ms"`
}

// stubMeta is the minimal subset of MkvJSON the backfill needs.
type stubMeta struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Magnet    string `json:"magnet"`
	Imdb      string `json:"imdb,omitempty"`
	TMDBID    int    `json:"tmdb_id,omitempty"`
	TVDBID    int    `json:"tvdb_id,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Title     string `json:"title,omitempty"`
	Year      int    `json:"year,omitempty"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
}

// MediaStoreWriter defines the write operations needed by RunBackfill.
type MediaStoreWriter interface {
	UpsertARRMovie(ctx context.Context, tmdbID int64, imdbID, title string, year int, fullPath string, size int64) error
	UpsertARRSeries(ctx context.Context, tmdbID, tvdbID int64, imdbID, title, seriesDir string) (int64, error)
	UpsertARREpisode(ctx context.Context, seriesID int64, season, episode int, title, fullPath string, size int64) error
}

var (
	reSE          = regexp.MustCompile(`[Ss](\d{1,3})[Ee](\d{1,3})`)
	reYearInTitle = regexp.MustCompile(`[(\s](19\d{2}|20[0-2]\d)[)]`)
)

// RunBackfill scans existing stub files on disk and populates arr_media.
// Returns detailed stats and any accumulated error.
func RunBackfill(ctx context.Context, moviesDir, tvDir string, db MediaStoreWriter, logger *log.Logger) (BackfillStats, error) {
	start := time.Now()
	var stats BackfillStats
	var err error

	if db == nil {
		return stats, nil
	}

	if moviesDir != "" {
		e := backfillMovies(ctx, moviesDir, db, logger, &stats)
		if e != nil && err == nil {
			err = fmt.Errorf("backfill movies: %w", e)
		}
	}
	if tvDir != "" {
		e := backfillSeries(ctx, tvDir, db, logger, &stats)
		if e != nil && err == nil {
			err = fmt.Errorf("backfill series: %w", e)
		}
	}

	stats.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		stats.ErrorsCount++
		logger.Printf("[ARR backfill] completed with errors: %v", err)
	} else {
		logger.Printf("[ARR backfill] completed: %d movies, %d series, %d episodes (%dms)",
			stats.MoviesIndexed, stats.SeriesIndexed, stats.EpisodesIndexed, stats.DurationMs)
	}
	return stats, err
}

func parseStub(path string) (*stubMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "{") {
		return nil, fmt.Errorf("not JSON stub")
	}
	var m stubMeta
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return nil, err
	}
	// Validate: must have URL and valid size (stub signature)
	if m.URL == "" || m.Size < 100*1024*1024 {
		return nil, fmt.Errorf("invalid stub")
	}
	return &m, nil
}

func backfillMovies(ctx context.Context, dir string, db MediaStoreWriter, logger *log.Logger, stats *BackfillStats) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".mkv") {
			return nil
		}

		meta, err := parseStub(path)
		if err != nil {
			return nil // not a stub — skip silently
		}

		title := meta.Title
		if title == "" {
			title = strings.TrimSuffix(info.Name(), ".mkv")
		}
		imdbID := meta.Imdb
		year := meta.Year
		tmdbID := meta.TMDBID

		// Fallback: if no TMDB ID in JSON, try path
		if tmdbID == 0 {
			tmdbID = extractTMDBID(info.Name())
		}
		// Fallback: if no year in JSON, try filename
		if year == 0 {
			year = extractYear(info.Name())
		}

		if err := db.UpsertARRMovie(ctx, int64(tmdbID), imdbID, title, year, path, meta.Size); err != nil {
			logger.Printf("[ARR backfill] movie %s: %v", filepath.Base(path), err)
		} else {
			stats.MoviesIndexed++
		}
		return nil
	})
}

func backfillSeries(ctx context.Context, dir string, db MediaStoreWriter, logger *log.Logger, stats *BackfillStats) error {
	type epInfo struct {
		showDir string
		season  int
		ep      int
		title   string
		path    string
		size    int64
		imdbID  string
	}

	var episodes []epInfo

	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(path, ".mkv") {
			return nil
		}

		meta, err := parseStub(path)
		if err != nil {
			return nil
		}

		matches := reSE.FindStringSubmatch(info.Name())
		if len(matches) < 3 {
			return nil
		}
		season, _ := strconv.Atoi(matches[1])
		ep, _ := strconv.Atoi(matches[2])

		title := meta.Title
		if title == "" {
			title = fmt.Sprintf("Episode %d", ep)
		}

		episodes = append(episodes, epInfo{
			showDir: filepath.Dir(filepath.Dir(path)),
			season:  season,
			ep:      ep,
			title:   title,
			path:    path,
			size:    meta.Size,
			imdbID:  meta.Imdb,
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk tv dir: %w", err)
	}

	// Deduplicate by show directory
	seen := make(map[string]bool)
	var showDirs []string
	for _, ep := range episodes {
		if !seen[ep.showDir] {
			seen[ep.showDir] = true
			showDirs = append(showDirs, ep.showDir)
		}
	}

	// Register series parents + collect IDs
	type seriesEntry struct {
		id      int64
		showDir string
	}
	var seriesList []seriesEntry

	for _, sd := range showDirs {
		showName := filepath.Base(sd)
		var imdbID string
		for _, ep := range episodes {
			if ep.showDir == sd && ep.imdbID != "" {
				imdbID = ep.imdbID
				break
			}
		}

		id, err := db.UpsertARRSeries(ctx, 0, 0, imdbID, showName, sd)
		if err != nil {
			logger.Printf("[ARR backfill] series %s: %v", showName, err)
			stats.ErrorsCount++
			continue
		}
		stats.SeriesIndexed++
		seriesList = append(seriesList, seriesEntry{id: id, showDir: sd})
	}

	// Register episodes linked to their series
	for _, ep := range episodes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var found *seriesEntry
		for i := range seriesList {
			if seriesList[i].showDir == ep.showDir {
				found = &seriesList[i]
				break
			}
		}
		if found == nil {
			continue
		}

		if err := db.UpsertARREpisode(ctx, found.id, ep.season, ep.ep, ep.title, ep.path, ep.size); err != nil {
			logger.Printf("[ARR backfill] episode %s: %v", filepath.Base(ep.path), err)
			stats.ErrorsCount++
		} else {
			stats.EpisodesIndexed++
		}
	}

	return nil
}

func extractTMDBID(filename string) int {
	re := regexp.MustCompile(`[Tt]mdb[ _]?(\d+)`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) >= 2 {
		if id, err := strconv.Atoi(matches[1]); err == nil {
			return id
		}
	}
	return 0
}

func extractYear(filename string) int {
	matches := reYearInTitle.FindStringSubmatch(filename)
	if len(matches) >= 2 {
		if year, err := strconv.Atoi(matches[1]); err == nil {
			return year
		}
	}
	return 0
}
