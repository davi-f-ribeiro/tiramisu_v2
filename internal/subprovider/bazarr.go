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
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	return &BazarrClient{cfg: cfg, http: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}}
}

func (c *BazarrClient) IsEnabled() bool { return c != nil && c.cfg.Enabled && c.cfg.URL != "" }

// Search implements SubtitleProvider. mediaID is a Bazarr/Radarr movie id or a
// Bazarr/Sonarr episode id. If mediaID is missing and title contains an IMDb id,
// the client resolves the movie id from /api/movies before searching providers.
func (c *BazarrClient) Search(ctx context.Context, mediaID int, title string, language string) ([]SubtitleCandidate, error) {
	if !c.IsEnabled() {
		return nil, nil
	}
	var lastErr error
	if mediaID > 0 {
		for _, path := range []string{
			"/api/providers/movies?radarrid=" + url.QueryEscape(strconv.Itoa(mediaID)),
			"/api/providers/episodes?episodeid=" + url.QueryEscape(strconv.Itoa(mediaID)),
		} {
			subs, err := c.getProviderSubtitles(ctx, path, language)
			if err != nil {
				lastErr = err
				continue
			}
			if len(subs) > 0 {
				return limitCandidates(subs, c.cfg.MaxResults), nil
			}
		}
	}
	if imdbID := normalizeIMDbID(title); imdbID != "" {
		radarrID, err := c.resolveMovieRadarrID(ctx, imdbID)
		if err != nil {
			lastErr = err
		} else if radarrID > 0 {
			subs, err := c.getProviderSubtitles(ctx, "/api/providers/movies?radarrid="+url.QueryEscape(strconv.Itoa(radarrID)), language)
			if err != nil {
				lastErr = err
			} else if len(subs) > 0 {
				return limitCandidates(subs, c.cfg.MaxResults), nil
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// SearchByIMDb resolves the movie in Bazarr and searches configured providers.
func (c *BazarrClient) SearchByIMDb(ctx context.Context, imdbID, language string) ([]SubtitleCandidate, error) {
	return c.Search(ctx, 0, imdbID, language)
}

func (c *BazarrClient) Download(ctx context.Context, subtitleID string, destPath string) error {
	if !c.IsEnabled() {
		return fmt.Errorf("bazarr disabled")
	}
	if subtitleID == "" {
		return fmt.Errorf("missing subtitle id")
	}
	paths := []string{subtitleID}
	if !strings.HasPrefix(subtitleID, "http://") && !strings.HasPrefix(subtitleID, "https://") && !strings.HasPrefix(subtitleID, "/") {
		escaped := url.PathEscape(subtitleID)
		paths = []string{"/api/subtitles/" + escaped + "/download", "/api/subtitles/download/" + escaped}
	}
	var lastErr error
	for _, p := range paths {
		body, err := c.getBytes(ctx, p, false)
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

func (c *BazarrClient) getProviderSubtitles(ctx context.Context, path, language string) ([]SubtitleCandidate, error) {
	body, err := c.getBytes(ctx, path, true)
	if err != nil {
		return nil, err
	}
	subs, err := parseBazarrSubtitles(body)
	if err != nil {
		return nil, err
	}
	return filterLanguage(subs, language), nil
}

func (c *BazarrClient) resolveMovieRadarrID(ctx context.Context, imdbID string) (int, error) {
	body, err := c.getBytes(ctx, "/api/movies", true)
	if err != nil {
		return 0, err
	}
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, err
	}
	for _, item := range unwrapBazarrList(raw) {
		m, ok := item.(map[string]any)
		if !ok || normalizeIMDbID(firstString(m, "imdbId", "imdb_id", "imdb")) != imdbID {
			continue
		}
		return firstInt(m, "radarrId", "radarr_id", "id", "movieId", "movie_id"), nil
	}
	return 0, nil
}

func (c *BazarrClient) getBytes(ctx context.Context, path string, requireJSON bool) ([]byte, error) {
	u := path
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = c.cfg.URL + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("X-API-KEY", c.cfg.APIKey)
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
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bazarr status %d for %s: %s", resp.StatusCode, u, strings.TrimSpace(string(body)))
	}
	if requireJSON && !strings.Contains(contentType, "application/json") {
		if strings.Contains(contentType, "text/html") || strings.Contains(strings.ToLower(string(body[:bazarrMin(len(body), 128)])), "<html") {
			return nil, fmt.Errorf("bazarr returned HTML instead of JSON for %s; route is likely invalid or fell back to SPA index", u)
		}
		return nil, fmt.Errorf("bazarr returned non-JSON content-type %q for %s", contentType, u)
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
		sub := SubtitleCandidate{ID: firstString(m, "id", "subtitleId", "subtitle_id", "providerId", "provider_id"), Title: firstString(m, "movieName", "movie_name", "seriesTitle", "title", "name"), ReleaseGroup: firstString(m, "releaseGroup", "release_group", "release", "provider", "provider_name"), Language: firstLanguage(m), DownloadURL: firstString(m, "downloadUrl", "download_url", "url"), Score: firstInt(m, "score", "matchScore", "match_score")}
		if sub.ID == "" && sub.DownloadURL != "" {
			sub.ID = sub.DownloadURL
		}
		if sub.ID != "" {
			out = append(out, sub)
		}
	}
	return out, nil
}

func unwrapBazarrList(raw any) []any {
	switch v := raw.(type) {
	case []any:
		return v
	case map[string]any:
		for _, key := range []string{"data", "results", "subtitles", "movies", "episodes", "providers"} {
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
		case map[string]any:
			if s := firstString(v, "code", "name", "language"); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstLanguage(m map[string]any) string {
	if s := firstString(m, "language", "languageCode", "language_code", "languageName", "language_name"); s != "" {
		return s
	}
	if v, ok := m["languages"].([]any); ok && len(v) > 0 {
		if mm, ok := v[0].(map[string]any); ok {
			return firstString(mm, "code", "name", "language")
		}
		if s, ok := v[0].(string); ok {
			return s
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

func filterLanguage(subs []SubtitleCandidate, language string) []SubtitleCandidate {
	want := normalizeLang(language)
	out := subs[:0]
	for _, sub := range subs {
		if want == "" || normalizeLang(sub.Language) == want || normalizeLang(sub.Language) == "por" {
			out = append(out, sub)
		}
	}
	return out
}

func normalizeLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	if s == "pt-br" || s == "ptbr" || s == "pb" || s == "portuguese-br" || s == "brazilian portuguese" {
		return "por"
	}
	if s == "por" || s == "pt" || strings.Contains(s, "portuguese") {
		return "por"
	}
	return s
}

func normalizeIMDbID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "tt") {
		return s
	}
	return ""
}

func limitCandidates(subs []SubtitleCandidate, n int) []SubtitleCandidate {
	if n > 0 && len(subs) > n {
		return subs[:n]
	}
	return subs
}

func bazarrMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
