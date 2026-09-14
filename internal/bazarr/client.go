package bazarr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ---------- Bazarr API client ----------

// Client talks to a Bazarr/Bazarr+ instance via its v3 (Radarr/Sonarr-compatible) API.
type Client struct {
	baseURL   string
	apiKey    string
	httpClient *http.Client
}

// NewClient creates a Bazarr client with sensible defaults.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// APIKey returns the API key for external inspection (logging, etc.).
func (c *Client) APIKey() string {
	return c.apiKey
}

// baseURL returns the configured base URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// request does a GET request to the Bazarr API with Api-Key auth.
// The caller is responsible for reading and closing resp.Body.
func (c *Client) request(ctx context.Context, path string, queryValues url.Values) (*http.Response, error) {
	url := c.baseURL + path
	if queryValues != nil {
		url += "?" + queryValues.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("bazarr request: %w", err)
	}

	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bazarr request to %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("bazarr request to %s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return resp, nil
}

// ---------- Movie search ----------

// SearchMoviesParams carries parameters for a movie subtitle search on Bazarr.
type SearchMoviesParams struct {	// IMDB ID of the movie (used by Bazarr to find the right movie).
	ImdbID string
	// Language codes (2 or 3 letter ISO codes, e.g. "por", "pt", "en").
	Languages []string
}

// MovieSearchResult represents a single subtitle offer for a movie, as returned by Bazarr's Providers Movies API.
type MovieSearchResult struct {
	ID           int64    `json:"id"`
	Language     Language `json:"language"`
	ProviderName string   `json:"providerName"`
	Series       *struct {
		Title string `json:"title"`
	} `json:"series,omitempty"`
}

// SearchMovies queries Bazarr's Providers Movies endpoint for available subtitle languages.
// Returns a list of MovieSearchResult, one per language provider entry Bazarr found.
func (c *Client) SearchMovies(ctx context.Context, params SearchMoviesParams) ([]MovieSearchResult, error) {
	if params.ImdbID == "" {
		return nil, fmt.Errorf("bazarr search movies: imdb_id is required")
	}

	values := url.Values{}
	values.Set("imdbId", params.ImdbID)
	if len(params.Languages) > 0 {
		values.Set("languages", strings.Join(params.Languages, ","))
	}

	resp, err := c.request(ctx, "/api/v1/providers/movies", values)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var results []MovieSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("bazarr search movies decode: %w", err)
	}

	return results, nil
}

// ---------- Movie subtitles download ----------

// DownloadMovieSubtitlesParams carries parameters for downloading a specific subtitle for a movie.
type DownloadMovieSubtitlesParams struct {
	MovieID    int64   // The Bazarr internal movie ID.
	LanguageID int64   // The language profile ID to download.
}

// DownloadMovieSubtitles triggers Bazarr to download the subtitle for the given movie and language.
// Returns the download job ID, or the ID of the already-completed download.
func (c *Client) DownloadMovieSubtitles(ctx context.Context, params DownloadMovieSubtitlesParams) (int64, error) {
	if params.MovieID == 0 {
		return 0, fmt.Errorf("bazarr download movie subtitles: movie_id is required")
	}

	values := url.Values{}
	values.Set("languages", fmt.Sprintf("%d", params.LanguageID))

	resp, err := c.request(ctx, fmt.Sprintf("/api/v1/subtitles/movie/%d", params.MovieID), values)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var downloadResp struct {
		MovieID        int64 `json:"movieId"`
		Status         string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&downloadResp); err != nil {
		return 0, fmt.Errorf("bazarr download movie subtitles decode: %w", err)
	}

	return downloadResp.MovieID, nil
}

// ---------- Search movies by title (for mapping filename → Bazarr movie ID) ----------

// SearchMovieByTitle searches Bazarr for a movie by title.
// Returns a list of matching movies with their Bazarr internal IDs.
type MovieInfo struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Year     int    `json:"year"`
	ImdbID   string `json:"imdbId"`
	TmdbID   int64  `json:"tmdbId"`
	Path     string `json:"path"`
}

// SearchMoviesByTitle queries Bazarr's movies endpoint to find a movie by title substring.
func (c *Client) SearchMoviesByTitle(ctx context.Context, title string) ([]MovieInfo, error) {
	if title == "" {
		return nil, fmt.Errorf("bazarr search movies by title: title is required")
	}

	// Bazarr's movies API supports a title filter via the standard API.
	// We use the /api/v1/movies endpoint which accepts query parameters.
	resp, err := c.request(ctx, "/api/v1/movies", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var movies []MovieInfo
	if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
		return nil, fmt.Errorf("bazarr search movies by title decode: %w", err)
	}

	// Filter by title substring (Bazarr doesn't support server-side filtering).
	var results []MovieInfo
	titleLower := strings.ToLower(title)
	for _, m := range movies {
		if strings.Contains(strings.ToLower(m.Title), titleLower) || strings.Contains(strings.ToLower(m.Path), titleLower) {
			results = append(results, m)
		}
	}

	return results, nil
}

// ---------- Language type ----------

// Language represents a language entry from Bazarr's API.
type Language struct {
	ID     int64  `json:"id"`
	ISO2   string `json:"iso2"`
	Name   string `json:"name"`
}

// SearchProviders queries Bazarr's Providers Movies endpoint for available subtitle languages.
func (c *Client) SearchProviders(ctx context.Context, movieID int64, languages []string) ([]ProviderResult, error) {
	values := url.Values{}
	if len(languages) > 0 {
		values.Set("languages", strings.Join(languages, ","))
	}

	resp, err := c.request(ctx, fmt.Sprintf("/api/v1/providers/movies/%d", movieID), values)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var results []ProviderResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("bazarr search providers decode: %w", err)
	}

	return results, nil
}

// ProviderResult represents a single subtitle offer from Bazarr's provider search.
type ProviderResult struct {
	ID           int64   `json:"id"`
	Language     Language `json:"language"`
	ProviderName string  `json:"providerName"`
	Score        int     `json:"score"`
	ReleaseInfo  string  `json:"releaseInfo"`
}
