package subprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OSProvider implements Provider for the OpenSubtitles REST API.
type OSProvider struct {
	apiKey    string
	baseURL   string
	user      string
	password  string
	token     string
	userAgent string
	// tokenExpiry is the absolute time when the current token expires.
	// A nil/zero value means the token has not been fetched yet.
	tokenExpiry time.Time
	client      *http.Client
	mu          sync.Mutex // protects token and tokenExpiry
}

// NewOSProvider creates an OpenSubtitles REST API provider.
func NewOSProvider(apiKey, baseURL, user, password string) *OSProvider {
	if baseURL == "" {
		baseURL = "https://api.opensubtitles.com"
	}
	version := getAppVersion()
	if version == "" {
		version = "0.0.0"
	}
	return &OSProvider{
		apiKey:    apiKey,
		baseURL:   strings.TrimRight(baseURL, "/"),
		user:      user,
		password:  password,
		userAgent: "Tiramisu/" + version + " (https://github.com/MrRobotoGit/tiramisu)",
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (p *OSProvider) Name() string { return "opensubtitles" }

// ensureToken fetches a JWT token via the /api/v1/login endpoint if one is not
// already available and not expired.  OpenSubtitles v2 tokens expire after ~24h
// so a single call per playback session is sufficient.
func (p *OSProvider) ensureToken() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// If we have a non-expired token, skip login.
	if p.token != "" && time.Now().Before(p.tokenExpiry) {
		return nil
	}

	// No credentials configured — nothing to do. The API key alone works for
	// basic searches on the free tier; Bearer is required for downloads only.
	if p.user == "" || p.password == "" {
		logf("[osprovider] login skipped: no username/password configured")
		return nil
	}

	payload, err := json.Marshal(map[string]string{
		"username": p.user,
		"password": p.password,
	})
	if err != nil {
		return fmt.Errorf("os login marshal: %w", err)
	}

	url := p.baseURL + "/api/v1/login"
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("os login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", p.apiKey)
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("os login POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("os login: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var loginResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("os login parse: %w", err)
	}
	if loginResp.Data.Token == "" {
		return fmt.Errorf("os login: empty token in response")
	}

	p.token = loginResp.Data.Token
	// Tokens expire after ~24h; set expiry to 23h to leave headroom.
	p.tokenExpiry = time.Now().Add(23 * time.Hour)
	logf("[osprovider] login successful, token valid until %s", p.tokenExpiry.Format(time.Kitchen))
	return nil
}

// CanMatch: true when we have an IMDB ID.
func (p *OSProvider) CanMatch(videoHash, imdbID, torrentName string) bool {
	return imdbID != ""
}

// Search finds subtitles for the given IMDB ID.
func (p *OSProvider) Search(videoHash, imdbID, torrentName string, lang LanguageTag, limit int) ([]Subtitle, error) {
	// Ensure we have a valid token (lazy login on first search).
	if err := p.ensureToken(); err != nil {
		logf("[osprovider] login failed: %v", err)
		// Don't fail the search entirely — the free tier may still accept Api-Key alone.
	}

	url := fmt.Sprintf(
		"%s/api/v1/subtitles?imdb_id=%s&languages=%s",
		p.baseURL, imdbID, lang,
	)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Api-Key", p.apiKey)
	req.Header.Set("User-Agent", p.userAgent)
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("os search GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Token may have expired during request — retry with a fresh login.
		if p.token != "" {
			p.mu.Lock()
			p.token = ""
			p.tokenExpiry = time.Time{}
			p.mu.Unlock()
			logf("[osprovider] token expired, will retry with fresh login")
			if err := p.ensureToken(); err == nil {
				return p.Search(videoHash, imdbID, torrentName, lang, limit)
			}
		}
		return nil, fmt.Errorf("os search: unauthorized (check Api-Key)")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("os search: rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("os search: HTTP %d", resp.StatusCode)
	}

	var searchResp struct {
		Data []struct {
			Type  string `json:"type"`
			ID    int64  `json:"id"`
			Attrs struct {
				Language string   `json:"language"`
				Release  string   `json:"release"`
				Files    []struct {
					ID       int64  `json:"id"`
					FileName string   `json:"file_name"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("os search parse: %w", err)
	}

	var results []Subtitle
	for _, item := range searchResp.Data {
		if len(item.Attrs.Files) == 0 {
			continue
		}

		langCode := item.Attrs.Language
		if langCode == "" {
			continue
		}

		score := 100
		if langCode == string(lang) {
			score = 100
		} else if langCode == "por" || langCode == "pt" {
			score = 90
		} else if langCode == "multi" {
			score = 80
		}

		fileName := item.Attrs.Files[0].FileName

		results = append(results, Subtitle{
			ID:           fmt.Sprintf("%d", item.Attrs.Files[0].ID),
			ProviderName: "opensubtitles",
			Language:     LanguageTag(langCode),
			Format:       "srt",
			Filename:     fileName,
			ReleaseInfo:  item.Attrs.Release,
			Score:        score,
		})
	}

	return results, nil
}

// Download obtains a subtitle from OpenSubtitles.
func (p *OSProvider) Download(sub Subtitle) ([]byte, error) {
	// Ensure token is present (it should be from Search, but guard defensively).
	if err := p.ensureToken(); err != nil {
		return nil, fmt.Errorf("os download: login failed: %w", err)
	}

	// Extract file_id from sub.ID
	var fileID int64
	fmt.Sscanf(sub.ID, "%d", &fileID)
	if fileID == 0 {
		return nil, fmt.Errorf("os download: invalid file_id %q", sub.ID)
	}

	downloadURL := p.baseURL + "/api/v1/download"

	downloadPayload, _ := json.Marshal(map[string]int64{
		"file_id": fileID,
	})

	req, _ := http.NewRequest("POST", downloadURL, bytes.NewReader(downloadPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Api-Key", p.apiKey)
	req.Header.Set("User-Agent", p.userAgent)
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("os download POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Retry once with a fresh login in case the token expired.
		if p.token != "" {
			p.mu.Lock()
			p.token = ""
			p.tokenExpiry = time.Time{}
			p.mu.Unlock()
			if err := p.ensureToken(); err == nil {
				return p.Download(sub)
			}
		}
		return nil, fmt.Errorf("os download: unauthorized")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("os download: rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("os download: HTTP %d", resp.StatusCode)
	}

	var dlResp struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dlResp); err != nil {
		return nil, fmt.Errorf("os download parse: %w", err)
	}
	if dlResp.Link == "" {
		return nil, fmt.Errorf("os download: empty link")
	}

	// GET the signed link
	resp2, err := p.client.Get(dlResp.Link)
	if err != nil {
		return nil, fmt.Errorf("os download GET: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("os download: GET HTTP %d", resp2.StatusCode)
	}

	return io.ReadAll(resp2.Body)
}
