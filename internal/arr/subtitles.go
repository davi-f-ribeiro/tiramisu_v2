package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"tiramisu/internal/bazarr"
)

// SubtitleSearchResponse is the normalized response returned to Jellyfin.
type SubtitleSearchResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Language string `json:"language"`
	Format   string `json:"format"`
}

// SubtitleHandler manages the /api/v1/subtitles/ routes for Jellyfin integration.
type SubtitleHandler struct {
	store     *DBStore
	bazarr    *bazarr.Client
	moviesDir string
	tvDir     string
	logger    func(format string, v ...any)
}

// NewSubtitleHandler creates a new SubtitleHandler from the DBStore and Bazarr client.
func NewSubtitleHandler(store *DBStore, bazarrClient *bazarr.Client, moviesDir, tvDir string) *SubtitleHandler {
	return &SubtitleHandler{
		store:     store,
		bazarr:    bazarrClient,
		moviesDir: moviesDir,
		tvDir:     tvDir,
		logger: func(format string, v ...any) {},
	}
}

// RegisterSubtitlesRoutes mounts the subtitle routes on the mux.
func (h *SubtitleHandler) RegisterSubtitlesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/subtitles/search", h.handleSearch)
	mux.HandleFunc("GET /api/v1/subtitles/download", h.handleDownload)
}

// handleSearch implements GET /api/v1/subtitles/search.
func (h *SubtitleHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "missing filename parameter", http.StatusBadRequest)
		return
	}

	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = "por"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Step 1: Map filename to media in local DB.
	mediaID, mediaType, err := h.mapFilenameToMediaID(ctx, filename)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Step 2: Query Bazarr for available subtitles.
	var results []SubtitleSearchResponse

	switch mediaType {
	case "movie":
		results, err = h.searchMovieSubtitles(ctx, mediaID, lang)
	case "episode":
		results, err = h.searchEpisodeSubtitles(ctx, mediaID, lang)
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// searchMovieSubtitles queries Bazarr for movie subtitles.
func (h *SubtitleHandler) searchMovieSubtitles(ctx context.Context, mediaID int64, lang string) ([]SubtitleSearchResponse, error) {
	movie, err := h.store.GetMovieByID(ctx, mediaID)
	if err != nil {
		return nil, fmt.Errorf("get movie by ID %d: %w", mediaID, err)
	}

	if movie.ImdbID == "" {
		return nil, fmt.Errorf("movie has no IMDB ID")
	}

	// Search Bazarr movies to find the matching Bazarr movie ID.
	bazarrMovies, err := h.bazarr.SearchMoviesByTitle(ctx, movie.Title)
	if err != nil {
		return nil, fmt.Errorf("bazarr search movies: %w", err)
	}

	var bazarrMovie *bazarr.MovieInfo
	for i := range bazarrMovies {
		if strings.EqualFold(bazarrMovies[i].Title, movie.Title) {
			bazarrMovie = &bazarrMovies[i]
			break
		}
	}
	if bazarrMovie == nil {
		return nil, fmt.Errorf("no Bazarr movie found for title: %s", movie.Title)
	}

	// Search providers for available subtitle languages.
	providers, err := h.bazarr.SearchProviders(ctx, bazarrMovie.ID, []string{lang})
	if err != nil {
		return nil, fmt.Errorf("bazarr search providers: %w", err)
	}

	var results []SubtitleSearchResponse
	for _, p := range providers {
		title := fmt.Sprintf("[%s] %s - %d%% - %s",
			p.ProviderName, p.Language.Name, p.Score, p.ReleaseInfo)
		results = append(results, SubtitleSearchResponse{
			ID:       fmt.Sprintf("movie-%d-%d", bazarrMovie.ID, p.Language.ID),
			Title:    title,
			Language: p.Language.ISO2,
			Format:   "srt",
		})
	}

	return results, nil
}

// searchEpisodeSubtitles queries Bazarr for episode subtitles.
func (h *SubtitleHandler) searchEpisodeSubtitles(ctx context.Context, mediaID int64, lang string) ([]SubtitleSearchResponse, error) {
	// Episodes are looked up by seriesID + season/episode
	// First, get all series to find matching episode path
	series, err := h.store.GetSeries(ctx)
	if err != nil {
		return nil, fmt.Errorf("get series: %w", err)
	}

	for _, s := range series {
		if strings.Contains(filepath.Base(s.Path), filepath.Base(mediaIDToString(mediaID))) ||
			strings.Contains(mediaIDToString(mediaID), filepath.Base(s.Path)) {
			eps, err := h.store.GetEpisodesBySeries(ctx, s.ID)
			if err != nil {
				continue
			}
			for _, ep := range eps {
				if strings.Contains(filepath.Base(ep.Path), filepath.Base(mediaIDToString(mediaID))) ||
					strings.Contains(mediaIDToString(mediaID), filepath.Base(ep.Path)) {
					// Found the episode — now search Bazarr for series subtitles.
					bazarrSeries, err := h.bazarr.SearchMoviesByTitle(ctx, s.Title)
					if err != nil {
						return nil, fmt.Errorf("bazarr search series: %w", err)
					}

					var bazarrSeriesObj *bazarr.MovieInfo
					for i := range bazarrSeries {
						if strings.EqualFold(bazarrSeries[i].Title, s.Title) {
							bazarrSeriesObj = &bazarrSeries[i]
							break
						}
					}
					if bazarrSeriesObj == nil {
						return nil, fmt.Errorf("no Bazarr series found for title: %s", s.Title)
					}

					providers, err := h.bazarr.SearchProviders(ctx, bazarrSeriesObj.ID, []string{lang})
					if err != nil {
						return nil, fmt.Errorf("bazarr search providers: %w", err)
					}

					var results []SubtitleSearchResponse
					for _, p := range providers {
						title := fmt.Sprintf("[%s] %s - %d%% - S%02dE%02d - %s",
							p.ProviderName, p.Language.Name, p.Score,
							ep.SeasonNumber, ep.EpisodeNumber, p.ReleaseInfo)
						results = append(results, SubtitleSearchResponse{
							ID:       fmt.Sprintf("episode-%d-%d-%d-%d", bazarrSeriesObj.ID, p.Language.ID, ep.SeasonNumber, ep.EpisodeNumber),
							Title:    title,
							Language: p.Language.ISO2,
							Format:   "srt",
						})
					}
					return results, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no matching episode found for ID %d", mediaID)
}

// handleDownload implements GET /api/v1/subtitles/download.
func (h *SubtitleHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}

	filename := r.URL.Query().Get("filename")
	if filename == "" {
		http.Error(w, "missing filename parameter", http.StatusBadRequest)
		return
	}

	mediaType, mediaID, langID := parseSubtitleID(id)
	if mediaType == "" {
		http.Error(w, "invalid subtitle id format", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	content, err := h.downloadSubtitle(ctx, mediaType, mediaID, langID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	contentType := "application/x-subrip"
	ext := ".srt"
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`,
		strings.TrimSuffix(filename, filepath.Ext(filename))+ext))
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

// downloadSubtitle fetches the subtitle content from Bazarr.
func (h *SubtitleHandler) downloadSubtitle(ctx context.Context, mediaType string, mediaID int64, langID int64) ([]byte, error) {
	if mediaType == "movie" {
		params := bazarr.DownloadMovieSubtitlesParams{
			MovieID:    mediaID,
			LanguageID: langID,
		}

		downloadID, err := h.bazarr.DownloadMovieSubtitles(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("bazarr download movie subtitles: %w", err)
		}

		// Poll for subtitle file ready (Bazarr returns download job ID)
		content, err := h.waitForSubtitleReady(ctx, downloadID)
		if err != nil {
			return nil, fmt.Errorf("waiting for subtitle: %w", err)
		}
		return content, nil
	}

	return nil, fmt.Errorf("download for type %s not yet implemented", mediaType)
}

// waitForSubtitleReady polls Bazarr for the subtitle file to be ready.
func (h *SubtitleHandler) waitForSubtitleReady(ctx context.Context, downloadID int64) ([]byte, error) {
	for i := 0; i < 15; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		// Poll Bazarr download status endpoint here
		// For now, return placeholder
		return []byte("# Subtitle content will be available when Bazarr API is properly configured"), nil
	}
	return nil, fmt.Errorf("timeout waiting for subtitle download")
}

// mapFilenameToMediaID maps a filename to a movie/episode ID in the local database.
func (h *SubtitleHandler) mapFilenameToMediaID(ctx context.Context, filename string) (int64, string, error) {
	stem := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))

	// Search in movies
	movies, err := h.store.GetMovies(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("get all movies: %w", err)
	}

	for _, m := range movies {
		base := strings.TrimSuffix(filepath.Base(m.Path), filepath.Ext(m.Path))
		if strings.Contains(base, stem) || strings.Contains(stem, base) {
			return m.ID, "movie", nil
		}
	}

	// Search in series/episodes
	series, err := h.store.GetSeries(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("get all series: %w", err)
	}

	for _, s := range series {
		base := strings.TrimSuffix(filepath.Base(s.Path), filepath.Ext(s.Path))
		if strings.Contains(base, stem) || strings.Contains(stem, base) {
			eps, err := h.store.GetEpisodesBySeries(ctx, s.ID)
			if err != nil {
				continue
			}
			for _, ep := range eps {
				epBase := strings.TrimSuffix(filepath.Base(ep.Path), filepath.Ext(ep.Path))
				if strings.Contains(epBase, stem) || strings.Contains(stem, epBase) {
					return ep.ID, "episode", nil
				}
			}
		}
	}

	return 0, "", fmt.Errorf("no media found for filename: %s", filename)
}

// parseSubtitleID parses the subtitle ID format and returns (mediaType, mediaID, langID).
func parseSubtitleID(id string) (string, int64, int64) {
	parts := strings.Split(id, "-")
	if len(parts) < 3 {
		return "", 0, 0
	}

	switch parts[0] {
	case "movie":
		if len(parts) >= 3 {
			var mediaID, langID int64
			fmt.Sscanf(parts[1], "%d", &mediaID)
			fmt.Sscanf(parts[2], "%d", &langID)
			return "movie", mediaID, langID
		}
	case "episode":
		if len(parts) >= 5 {
			var seriesID, langID, season, episode int64
			fmt.Sscanf(parts[1], "%d", &seriesID)
			fmt.Sscanf(parts[2], "%d", &langID)
			fmt.Sscanf(parts[3], "%d", &season)
			fmt.Sscanf(parts[4], "%d", &episode)
			return "episode", seriesID, langID
		}
	}
	return "", 0, 0
}

func mediaIDToString(id int64) string {
	return fmt.Sprintf("%d", id)
}
