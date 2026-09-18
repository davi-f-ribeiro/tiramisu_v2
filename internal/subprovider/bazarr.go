package subprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// BazarrConfig holds the Bazarr connection configuration.
type BazarrConfig struct {
	Enabled        bool
	URL            string
	APIKey         string
	TimeoutSeconds int
	MaxResults     int
}

// BazarrClient is a small HTTP client for Bazarr-compatible APIs.
type BazarrClient struct {
	cfg  BazarrConfig
	http *http.Client
}

var _ SubtitleProvider = (*BazarrClient)(nil)

// NewBazarrClient creates a Bazarr client. Empty timeout/max values get safe defaults.
func NewBazarrClient(cfg BazarrConfig) *BazarrClient {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 5
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &BazarrClient{
		cfg:  cfg,
		http: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
	}
}

func (c *BazarrClient) IsEnabled() bool {
	return c != nil && c.cfg.Enabled && c.cfg.URL != ""
}

// Search implements SubtitleProvider. mediaID is interpreted as Radarr/Sonarr ID when present;
// title is used as a provider-side fallback query for future metadata that has no numeric ID.
func (c *BazarrClient) Search(ctx context.Context, mediaID int, title string, language string) ([]SubtitleCandidate, error) {
	if !c.IsEnabled() {
		return nil, nil
	}
	q := url.Values{}
	if mediaID > 0 {
		q.Set("radarrId", strconv.Itoa(mediaID))
		q.Set("sonarrEpisodeId", strconv.Itoa(mediaID))
		q.Set("episodeId", strconv.Itoa(mediaID))
	}
	if title != "" {
		q.Set("title", title)
		q.Set("query", title)
	}
	q.Set("language", language)
	q.Set("languages", language)
	q.Set("maxResults", strconv.Itoa(c.cfg.MaxResults))
	return c.getSubtitles(ctx, "/api/v1/subtitles/search?"+q.Encode())
}

// SearchByIMDb searches cached/available subtitles by IMDb ID and language.
func (c *BazarrClient) SearchByIMDb(ctx context.Context, imdbID, language string) ([]SubtitleCandidate, error) {
	if !c.IsEnabled() || imdbID == "" {
		return nil, nil
	}
	q := url.Values{}
	q.Set("imdbId", imdbID)
	q.Set("imdb_id", imdbID)
	q.Set("language", language)
	q.Set("languages", language)
	q.Set("maxResults", strconv.Itoa(c.cfg.MaxResults))
	return c.getSubtitles(ctx, "/api/v1/subtitles/search?"+q.Encode())
}

func (c *BazarrClient) Download(ctx context.Context, subtitleID string, destPath string) error {
	if !c.IsEnabled() {
		return fmt.Errorf("bazarr disabled")
	}
	if subtitleID == "" {
		return fmt.Errorf("missing subtitle id")
	}
	paths := []string{
		"/api/v1/subtitles/" + url.PathEscape(subtitleID) + "/download",
		"/api/v1/subtitles/download/" + url.PathEscape(subtitleID),
	}
	var lastErr error
	for _, p := range paths {
		body, err := c.getBytes(ctx, p)
		if err == nil && len(body) > 0 {
			return os.WriteFile(destPath, body, 0644)
		}
		lastErr = err
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("empty subtitle download for %q", subtitleID)
}

func (c *BazarrClient) getSubtitles(ctx context.Context, path string) ([]SubtitleCandidate, error) {
	body, err := c.getBytes(ctx, path)
	if err != nil {
		return nil, err
	}
	return parseBazarrSubtitles(body)
}

func (c *BazarrClient) getBytes(ctx context.Context, path string) ([]byte, error) {
	u := path
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = c.cfg.URL + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("X-Api-Key", c.cfg.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bazarr status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func parseBazarrSubtitles(body []byte) ([]SubtitleCandidate, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	items := unwrapBazarrList(raw)
	out := make([]SubtitleCandidate, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sub := SubtitleCandidate{
			ID:           firstString(m, "id", "subtitleId", "subtitle_id", "providerId"),
			Title:        firstString(m, "movieName", "movie_name", "title", "name"),
			ReleaseGroup: firstString(m, "releaseGroup", "release_group", "release", "provider"),
			Language:     firstString(m, "language", "languageCode", "language_code"),
			DownloadURL:  firstString(m, "downloadUrl", "download_url", "url"),
			Score:        firstInt(m, "score", "matchScore", "match_score"),
		}
		if sub.ID == "" && sub.DownloadURL != "" {
			sub.ID = sub.DownloadURL
		}
		if sub.ID == "" {
			continue
		}
		out = append(out, sub)
	}
	return out, nil
}

func unwrapBazarrList(raw any) []any {
	switch v := raw.(type) {
	case []any:
		return v
	case map[string]any:
		for _, key := range []string{"data", "results", "subtitles"} {
			if arr, ok := v[key].([]any); ok {
				return arr
			}
			if nested, ok := v[key].(map[string]any); ok {
				if arr := unwrapBazarrList(nested); len(arr) > 0 {
					return arr
				}
			}
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := m[key].(type) {
		case string:
			return v
		case float64:
			return strconv.FormatInt(int64(v), 10)
		}
	}
	return ""
}

func firstInt(m map[string]any, keys ...string) int {
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return int(v)
		case string:
			if n, err := strconv.Atoi(strings.TrimSuffix(v, "%")); err == nil {
				return n
			}
		}
	}
	return 0
}
