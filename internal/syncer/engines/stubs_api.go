// Package engines exposes public methods for stub management via the REST API.
// These methods wrap the private engine methods (createMKV, removeStub, etc.)
// to allow manual control from the REST layer without duplicating logic.
package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tiramisu/internal/catalog/tmdb"
)

// ========================= MovieStubAPI =========================

// MovieStubAPI exposes movie stub management operations.
type MovieStubAPI struct {
	engine *MovieGoEngine
}

// NewMovieStubAPI creates a new MovieStubAPI from an existing MovieGoEngine.
func NewMovieStubAPI(engine *MovieGoEngine) *MovieStubAPI {
	return &MovieStubAPI{engine: engine}
}

// Candidate represents a discovered stream/torrent candidate for a movie.
type Candidate struct {
	Hash         string  `json:"hash"`
	Title        string  `json:"title"`
	Is4K         bool    `json:"is_4k"`
	QualityScore int     `json:"quality_score"`
	Seeders      int     `json:"seeders"`
	SizeGB       float64 `json:"size_gb"`
	Source       string  `json:"source"` // "prowlarr", "torrentio", or "manual"
	Magnet       string  `json:"magnet,omitempty"`
	StreamURL    string  `json:"stream_url,omitempty"`
}

// ListCandidates returns all discovered stream candidates for a movie by IMDB ID.
func (api *MovieStubAPI) ListCandidates(ctx context.Context, imdbID, title string, year int) ([]Candidate, bool, error) {
	streams, search, err := api.engine.getMovieStreams(ctx, imdbID, title, year)
	if err != nil {
		return nil, false, err
	}

	var candidates []Candidate
	for _, s := range streams {
		c := Candidate{
			Hash:         s.Hash,
			Title:        s.Title,
			Is4K:         s.Is4K,
			QualityScore: s.QualityScore,
			Seeders:      s.Seeders,
			SizeGB:       s.SizeGB,
			Source:       "discovered",
		}
		candidates = append(candidates, c)
	}
	return candidates, search.HadRaw, nil
}

// Remove removes a movie stub file and its associated torrent from GoStorm.
func (api *MovieStubAPI) Remove(ctx context.Context, stubPath string) error {
	// Read stub to get the hash before removing
	hash := readHashFromStub(ctx, stubPath)

	// Use the engine's internal removeStub
	api.engine.removeStub(ctx, stubPath, hash)

	return nil
}

// ChangeSource removes an existing movie stub and creates a new one with the
// candidate's magnet/stream_url. It delegates to Remove + AddManual.
func (api *MovieStubAPI) ChangeSource(ctx context.Context, stubPath string, candidate Candidate) error {
	// Read existing stub metadata before removing
	data, err := os.ReadFile(stubPath)
	if err != nil {
		return fmt.Errorf("failed to read stub %s: %w", stubPath, err)
	}

	var url, magnet, imdbID string
	var size float64
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err == nil {
			url, _ = obj["url"].(string)
			magnet, _ = obj["magnet"].(string)
			size, _ = obj["size"].(float64)
			imdbID, _ = obj["imdb"].(string)
		}
	}

	// Remove the old stub
	if err := api.Remove(ctx, stubPath); err != nil {
		return fmt.Errorf("failed to remove old stub: %w", err)
	}

	// Determine streamURL and magnet for the new stub
	newStreamURL := candidate.StreamURL
	newMagnet := candidate.Magnet
	if newStreamURL == "" && candidate.Hash != "" {
		// Hash might be a magnet link
		if strings.HasPrefix(candidate.Hash, "magnet:") {
			newMagnet = candidate.Hash
		} else {
			newStreamURL = candidate.Hash
		}
	}
	if newStreamURL == "" {
		newStreamURL = url
	}
	if newMagnet == "" {
		newMagnet = magnet
	}

	// Use engine's createMKV to create the new stub
	if !api.engine.createMKV(stubPath, newStreamURL, int64(size), newMagnet, imdbID) {
		return fmt.Errorf("failed to create new stub at %s", stubPath)
	}

	return nil
}

// Rehydrate attempts to re-add a torrent for a specific stub file.
func (api *MovieStubAPI) Rehydrate(ctx context.Context, stubPath string) error {
	data, err := os.ReadFile(stubPath)
	if err != nil {
		return fmt.Errorf("failed to read stub %s: %w", stubPath, err)
	}

	var url, magnet, imdbID string
	var size float64
	content := strings.TrimSpace(string(data))

	if strings.HasPrefix(content, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("failed to parse stub JSON: %w", err)
		}
		url, _ = obj["url"].(string)
		magnet, _ = obj["magnet"].(string)
		size, _ = obj["size"].(float64)
		imdbID, _ = obj["imdb"].(string)
	} else {
		lines := strings.SplitN(content, "\n", 4)
		if len(lines) < 3 {
			return fmt.Errorf("stub file has insufficient content")
		}
		url = strings.TrimSpace(lines[0])
		magnet = strings.TrimSpace(lines[2])
		if len(lines) > 1 {
			size, _ = strconv.ParseFloat(strings.TrimSpace(lines[1]), 64)
		}
		if len(lines) >= 4 {
			imdbID = strings.TrimSpace(lines[3])
		}
	}

	// Extract hash from URL
	reMHashURL := regexp.MustCompile(`link=([a-f0-9]{40})`)
	m := reMHashURL.FindStringSubmatch(url)
	if len(m) < 2 {
		return fmt.Errorf("cannot extract hash from stub URL")
	}
	hash := m[1]

	// Check if torrent is already active
	torrents, err := api.engine.gostorm.ListTorrents(ctx)
	if err != nil {
		return fmt.Errorf("failed to list torrents: %w", err)
	}
	for _, t := range torrents {
		if t.Hash == hash {
			return fmt.Errorf("torrent %s is already active", hash)
		}
	}

	// Rebuild magnet if needed
	if !strings.HasPrefix(magnet, "magnet:?") {
		return fmt.Errorf("stub has no valid magnet")
	}
	freshMagnet := BuildMagnet(hash, filepath.Base(stubPath), DefaultTrackers())

	// Add to GoStorm
	if _, err := api.engine.gostorm.AddTorrent(ctx, freshMagnet, filepath.Base(stubPath)); err != nil {
		return fmt.Errorf("failed to add torrent: %w", err)
	}

	// Update stub with fresh magnet
	api.engine.createMKV(stubPath, url, int64(size), freshMagnet, imdbID)

	return nil
}

// AddManual creates a new movie stub from an external magnet/stream URL.
func (api *MovieStubAPI) AddManual(ctx context.Context, stubPath, streamURL, magnet, imdbID string) error {
	// Validate stub path is within the movies directory
	baseDir := api.engine.moviesDir
	absStub, err := filepath.Abs(stubPath)
	if err != nil {
		return fmt.Errorf("invalid stub path: %w", err)
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("invalid movies dir: %w", err)
	}
	if !strings.HasPrefix(absStub, absBase+"/") {
		return fmt.Errorf("stub path must be within %s", baseDir)
	}

	// Check if stub already exists
	if _, err := os.Stat(stubPath); err == nil {
		return fmt.Errorf("stub already exists at %s", stubPath)
	}

	// Use the engine's createMKV
	if !api.engine.createMKV(stubPath, streamURL, 0, magnet, imdbID) {
		return fmt.Errorf("failed to create stub at %s", stubPath)
	}
	return nil
}

// Create creates a new movie stub with the given source URL or magnet.
func (api *MovieStubAPI) Create(ctx context.Context, stubPath, source string) error {
	// Validate stub path is within the movies directory
	baseDir := api.engine.moviesDir
	absStub, err := filepath.Abs(stubPath)
	if err != nil {
		return fmt.Errorf("invalid stub path: %w", err)
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("invalid movies dir: %w", err)
	}
	if !strings.HasPrefix(absStub, absBase+"/") {
		return fmt.Errorf("stub path must be within %s", baseDir)
	}

	// Check if stub already exists
	if _, err := os.Stat(stubPath); err == nil {
		return fmt.Errorf("stub already exists at %s", stubPath)
	}

	magnet := ""
	streamURL := ""
	if strings.HasPrefix(source, "magnet:") {
		magnet = source
	} else {
		streamURL = source
	}

	if !api.engine.createMKV(stubPath, streamURL, 0, magnet, "") {
		return fmt.Errorf("failed to create stub at %s", stubPath)
	}
	return nil
}

// ========================= TVStubAPI =========================

// TVStubAPI exposes TV stub management operations.
type TVStubAPI struct {
	engine *TVGoEngine
}

// NewTVStubAPI creates a new TVStubAPI from an existing TVGoEngine.
func NewTVStubAPI(engine *TVGoEngine) *TVStubAPI {
	return &TVStubAPI{engine: engine}
}

// TVCandidate represents a discovered stream/torrent candidate for a TV show.
type TVCandidate struct {
	Hash          string  `json:"hash"`
	Title         string  `json:"title"`
	IsFullpack    bool    `json:"is_fullpack"`
	IsPartialPack bool    `json:"is_partial_pack"`
	QualityScore  int     `json:"quality_score"`
	Season        int     `json:"season"`
	EpisodeNum    int     `json:"episode_num"`
	Seeders       int     `json:"seeders"`
	SizeGB        float64 `json:"size_gb"`
	Priority      int     `json:"priority"`
	Source        string  `json:"source"`
}

// ListCandidates returns all discovered stream candidates for a TV show.
func (api *TVStubAPI) ListCandidates(ctx context.Context, imdbID string, tmdbID int, showName string, tmdbDetails *tmdb.TVDetail) ([]TVCandidate, error) {
	streams, _ := api.engine.getStreams(ctx, imdbID, tmdbID, showName, tmdbDetails)

	var candidates []TVCandidate
	for _, s := range streams {
		c := TVCandidate{
			Hash:          s.Hash,
			Title:         s.Title,
			IsFullpack:    s.IsFullpack,
			IsPartialPack: s.IsPartialPack,
			QualityScore:  s.QualityScore,
			Season:        s.Season,
			EpisodeNum:    s.EpisodeNum,
			Seeders:       s.Seeders,
			SizeGB:        s.SizeGB,
			Priority:      s.Priority,
			Source:        "discovered",
		}
		candidates = append(candidates, c)
	}
	return candidates, nil
}

// Remove removes a TV stub file and its associated torrent from GoStorm.
func (api *TVStubAPI) Remove(ctx context.Context, stubPath string) error {
	hash := readHashFromStub(ctx, stubPath)
	api.engine.removeStub(ctx, stubPath, hash)
	return nil
}

// Rehydrate attempts to re-add a torrent for a specific TV stub file.
func (api *TVStubAPI) Rehydrate(ctx context.Context, stubPath string) error {
	data, err := os.ReadFile(stubPath)
	if err != nil {
		return fmt.Errorf("failed to read stub %s: %w", stubPath, err)
	}

	var url, magnet string
	var size float64
	content := strings.TrimSpace(string(data))

	if strings.HasPrefix(content, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("failed to parse stub JSON: %w", err)
		}
		url, _ = obj["url"].(string)
		magnet, _ = obj["magnet"].(string)
		size, _ = obj["size"].(float64)
	} else {
		lines := strings.SplitN(content, "\n", 4)
		if len(lines) < 3 {
			return fmt.Errorf("stub file has insufficient content")
		}
		url = strings.TrimSpace(lines[0])
		magnet = strings.TrimSpace(lines[2])
		if len(lines) > 1 {
			size, _ = strconv.ParseFloat(strings.TrimSpace(lines[1]), 64)
		}
	}

	reTVHashURL := regexp.MustCompile(`link=([a-f0-9]{40})`)
	m := reTVHashURL.FindStringSubmatch(url)
	if len(m) < 2 {
		return fmt.Errorf("cannot extract hash from stub URL")
	}
	hash := m[1]

	torrents, err := api.engine.gostorm.ListTorrents(ctx)
	if err != nil {
		return fmt.Errorf("failed to list torrents: %w", err)
	}
	for _, t := range torrents {
		if t.Hash == hash {
			return fmt.Errorf("torrent %s is already active", hash)
		}
	}

	if !strings.HasPrefix(magnet, "magnet:?") {
		return fmt.Errorf("stub has no valid magnet")
	}
	freshMagnet := BuildMagnet(hash, filepath.Base(stubPath), DefaultTrackers())

	if _, err := api.engine.gostorm.AddTorrent(ctx, freshMagnet, filepath.Base(stubPath)); err != nil {
		return fmt.Errorf("failed to add torrent: %w", err)
	}

	if !api.engine.createMKV(stubPath, url, int64(size), freshMagnet) {
		return fmt.Errorf("failed to create stub at %s", stubPath)
	}
	return nil
}

// Create creates a new TV stub with the given source URL or magnet.
func (api *TVStubAPI) Create(ctx context.Context, stubPath, source string) error {
	baseDir := api.engine.tvDir
	absStub, err := filepath.Abs(stubPath)
	if err != nil {
		return fmt.Errorf("invalid stub path: %w", err)
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("invalid tv dir: %w", err)
	}
	if !strings.HasPrefix(absStub, absBase+"/") {
		return fmt.Errorf("stub path must be within %s", baseDir)
	}

	if _, err := os.Stat(stubPath); err == nil {
		return fmt.Errorf("stub already exists at %s", stubPath)
	}

	magnet := ""
	streamURL := ""
	if strings.HasPrefix(source, "magnet:") {
		magnet = source
	} else {
		streamURL = source
	}

	if !api.engine.createMKV(stubPath, streamURL, 0, magnet) {
		return fmt.Errorf("failed to create stub at %s", stubPath)
	}
	return nil
}

// ========================= Helpers =========================

// readHashFromStub extracts the torrent hash from a stub file.
func readHashFromStub(ctx context.Context, stubPath string) string {
	data, err := os.ReadFile(stubPath)
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal(data, &obj); err != nil {
			return ""
		}
		if streamURL, ok := obj["url"].(string); ok {
			reMHashURL := regexp.MustCompile(`link=([a-f0-9]{40})`)
			m := reMHashURL.FindStringSubmatch(streamURL)
			if len(m) >= 2 {
				return m[1]
			}
		}
		return ""
	}

	lines := strings.SplitN(content, "\n", 4)
	if len(lines) < 1 {
		return ""
	}
	url := strings.TrimSpace(lines[0])
	reMHashURL := regexp.MustCompile(`link=([a-f0-9]{40})`)
	m := reMHashURL.FindStringSubmatch(url)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}
