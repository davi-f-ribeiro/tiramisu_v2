// Package syncer provides REST handlers for stub management.
package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tiramisu/internal/syncer/engines"
)

// StubsHandler manages stub lifecycle via REST API.
type StubsHandler struct {
	movieAPI  *engines.MovieStubAPI
	tvAPI     *engines.TVStubAPI
	moviesDir string
	tvDir     string
}

// NewStubsHandler creates a new stubs handler.
func NewStubsHandler(movieAPI *engines.MovieStubAPI, tvAPI *engines.TVStubAPI, moviesDir, tvDir string) *StubsHandler {
	return &StubsHandler{
		movieAPI:  movieAPI,
		tvAPI:     tvAPI,
		moviesDir: moviesDir,
		tvDir:     tvDir,
	}
}

// RegisterRoutes registers stub management HTTP routes.
func (h *StubsHandler) RegisterRoutes(mux *http.ServeMux) {
	// Flat routes used by stub_management.html
	mux.HandleFunc("/api/stubs", h.handleStubsRoot)
	mux.HandleFunc("/api/stubs/", h.handleStubsFlat)
	// GET /api/stubs/movies — list all movie stubs
	mux.HandleFunc("/api/stubs/movies", h.handleMoviesList)
	// POST /api/stubs/movies -> add manual movie
	mux.HandleFunc("/api/stubs/movies/", h.handleMovies())
	// GET /api/stubs/tv — list all tv stubs
	mux.HandleFunc("/api/stubs/tv", h.handleTVList)
	// Individual TV stub actions: /api/stubs/tv/{id}/{action}
	mux.HandleFunc("/api/stubs/tv/", h.handleTV())
}

// ========================= Movie Endpoints =========================

// handleMoviesList returns a GET endpoint for listing all movie stubs on disk.
func (h *StubsHandler) handleMoviesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	stubs, err := stubsListDetailed(h.moviesDir, "movie")
	if err != nil && !os.IsNotExist(err) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if stubs == nil {
		stubs = []map[string]interface{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stubs)
}

// handleTVList returns a GET endpoint for listing all TV stubs on disk.
func (h *StubsHandler) handleTVList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	stubs, err := stubsListDetailed(h.tvDir, "tv")
	if err != nil && !os.IsNotExist(err) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if stubs == nil {
		stubs = []map[string]interface{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stubs)
}

func (h *StubsHandler) handleMovies() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/stubs/movies/")
		parts := strings.SplitN(path, "/", 2)

		if len(parts) == 0 || parts[0] == "" {
			// POST /api/stubs/movies -> add manual
			if r.Method == http.MethodPost {
				h.postMovieManual(w, r)
			} else {
				http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			}
			return
		}

		id := parts[0]
		action := ""
		if len(parts) > 1 && parts[1] != "" {
			action = parts[1]
		}

		switch action {
		case "candidates":
			h.getMovieCandidates(w, r, id)
		case "source":
			h.putMovieSource(w, r, id)
		case "rehydrate":
			h.postMovieRehydrate(w, r, id)
		case "remove":
			h.deleteMovie(w, r, id)
		default:
			http.Error(w, fmt.Sprintf(`{"error":"unknown action %s"}`, action), http.StatusBadRequest)
		}
	}
}

type movieCandidatesReq struct {
	ImdbID string `json:"imdb_id"`
	Title  string `json:"title"`
	Year   int    `json:"year"`
}

func (h *StubsHandler) getMovieCandidates(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var req movieCandidatesReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.ImdbID == "" {
		req.ImdbID = id
	}
	if req.Title == "" {
		req.Title = id
	}

	candidates, hadRaw, err := h.movieAPI.ListCandidates(ctx, req.ImdbID, req.Title, req.Year)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if candidates == nil {
		candidates = []engines.Candidate{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"candidates": candidates,
		"had_raw":    hadRaw,
		"count":      len(candidates),
	})
}

type sourceChangeReq struct {
	CandidateID string `json:"candidate_id"`
	Magnet      string `json:"magnet"`
	StreamURL   string `json:"stream_url"`
}

func (h *StubsHandler) putMovieSource(w http.ResponseWriter, r *http.Request, id string) {
	var req sourceChangeReq
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if req.CandidateID == "" && req.Magnet == "" && req.StreamURL == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "provide candidate_id, magnet, or stream_url"})
		return
	}

	// Resolve existing stub path from ID
	log.Printf("[StubAPI] putMovieSource: moviesDir=%q id=%q", h.moviesDir, id)
	stubPath, found := findStubByPartialName(h.moviesDir, id)
	if !found {
		log.Printf("[StubAPI] putMovieSource: findStubByPartialName=%q returned found=false", id)
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("movie stub for %s not found", id)})
		return
	}
	log.Printf("[StubAPI] putMovieSource: resolved stubPath=%q", stubPath)

	// Extract IMDB ID from stub file content for candidate lookup.
	// This ensures the candidate search uses the actual IMDB ID stored in
	// the stub, not the raw filename or other identifier passed from frontend.
	imdbID := id
	if content, err := os.ReadFile(stubPath); err == nil {
		text := strings.TrimSpace(string(content))
		if !strings.HasPrefix(text, "{") {
			lines := strings.SplitN(text, "\n", 4)
			if len(lines) >= 4 {
				if extracted := strings.TrimSpace(lines[3]); extracted != "" {
					imdbID = extracted
				}
			}
		}
	}

	// Get candidates to find matching hash
	candidates, _, err := h.movieAPI.ListCandidates(ctx, imdbID, imdbID, 0)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to list candidates: %v", err)})
		return
	}

	var chosen *engines.Candidate
	if req.CandidateID != "" {
		for _, c := range candidates {
			if c.Hash == req.CandidateID {
				chosen = &c
				break
			}
		}
		if chosen == nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "candidate not found"})
			return
		}
	} else if req.Magnet != "" || req.StreamURL != "" {
		chosen = &engines.Candidate{
			StreamURL: req.StreamURL,
			Magnet:    req.Magnet,
			Source:    "manual",
		}
		if chosen.Hash == "" {
			chosen.Hash = req.Magnet
		}
	}

	if chosen == nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "no valid source specified"})
		return
	}

	// Execute change source
	if err := h.movieAPI.ChangeSource(ctx, stubPath, *chosen); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to change source: %v", err)})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"message":   fmt.Sprintf("source changed for %s", stubPath),
		"stub_path": stubPath,
		"chosen":    chosen,
	})
}

type rehydrateReq struct {
	StubPath string `json:"stub_path"`
}

func (h *StubsHandler) postMovieRehydrate(w http.ResponseWriter, r *http.Request, id string) {
	var req rehydrateReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	stubPath := req.StubPath
	if stubPath == "" {
		// Try to find stub by ID (partial name match)
		var found bool
		stubPath, found = findStubByPartialName(h.moviesDir, id)
		if !found {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("movie stub for %s not found", id)})
			return
		}
	}

	err := h.movieAPI.Rehydrate(ctx, stubPath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": fmt.Sprintf("rehydrated stub %s", stubPath),
	})
}

type manualMovieReq struct {
	StubPath  string `json:"stub_path"`
	StreamURL string `json:"stream_url"`
	Magnet    string `json:"magnet"`
	ImdbID    string `json:"imdb_id"`
}

func (h *StubsHandler) postMovieManual(w http.ResponseWriter, r *http.Request) {
	var req manualMovieReq
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
	}

	if req.StubPath == "" || req.StreamURL == "" || req.Magnet == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "stub_path, stream_url, and magnet are required",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	err := h.movieAPI.AddManual(ctx, req.StubPath, req.StreamURL, req.Magnet, req.ImdbID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": fmt.Sprintf("manual movie stub created at %s", req.StubPath),
	})
}

func (h *StubsHandler) deleteMovie(w http.ResponseWriter, r *http.Request, id string) {
	log.Printf("[StubAPI] deleteMovie: moviesDir=%q id=%q", h.moviesDir, id)
	stubPath, found := findStubByPartialName(h.moviesDir, id)
	if !found {
		log.Printf("[StubAPI] deleteMovie: findStubByPartialName=%q returned found=false", id)
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("movie stub for %s not found", id)})
		return
	}
	log.Printf("[StubAPI] deleteMovie: resolved stubPath=%q", stubPath)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	err := h.movieAPI.Remove(ctx, stubPath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": fmt.Sprintf("movie stub %s removed", stubPath),
	})
}

// ========================= TV Endpoints =========================

func (h *StubsHandler) handleTV() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/stubs/tv/")
		parts := strings.SplitN(path, "/", 2)

		if len(parts) == 0 || parts[0] == "" {
			http.Error(w, `{"error":"missing tv show id"}`, http.StatusBadRequest)
			return
		}

		id := parts[0]
		action := ""
		if len(parts) > 1 && parts[1] != "" {
			action = parts[1]
		}

		switch action {
		case "candidates":
			h.getTVCandidates(w, r, id)
		case "rehydrate":
			h.postTVRehydrate(w, r, id)
		case "remove":
			h.deleteTV(w, r, id)
		default:
			http.Error(w, fmt.Sprintf(`{"error":"unknown action %s"}`, action), http.StatusBadRequest)
		}
	}
}

type tvCandidatesReq struct {
	ImdbID   string `json:"imdb_id"`
	TmdbID   int    `json:"tmdb_id"`
	ShowName string `json:"show_name"`
}

func (h *StubsHandler) getTVCandidates(w http.ResponseWriter, r *http.Request, id string) {
	// ctx reserved for when tmdb lookup is integrated
	_ = r.Context()

	var req tvCandidatesReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.ShowName == "" {
		req.ShowName = id
	}
	if req.ImdbID == "" {
		req.ImdbID = id
	}

	// TV candidates require more context (tmdbID, details)
	// For simplicity, we return a placeholder until full integration
	json.NewEncoder(w).Encode(map[string]interface{}{
		"candidates": []engines.TVCandidate{},
		"count":      0,
		"show_name":  req.ShowName,
		"imdb_id":    req.ImdbID,
		"tmdb_id":    req.TmdbID,
		"note":       "TV candidate lookup requires tmdb details integration",
		"message":    "use the discover endpoint or provide full show details",
	})
}

type tvRehydrateReq struct {
	StubPath string `json:"stub_path"`
}

func (h *StubsHandler) postTVRehydrate(w http.ResponseWriter, r *http.Request, id string) {
	var req tvRehydrateReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	stubPath := req.StubPath
	if stubPath == "" {
		var found bool
		stubPath, found = findStubByPartialName(h.tvDir, id)
		if !found {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("tv stub for %s not found", id)})
			return
		}
	}

	err := h.tvAPI.Rehydrate(ctx, stubPath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": fmt.Sprintf("rehydrated tv stub %s", stubPath),
	})
}

func (h *StubsHandler) deleteTV(w http.ResponseWriter, r *http.Request, id string) {
	stubPath, found := findStubByPartialName(h.tvDir, id)
	if !found {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("tv stub for %s not found", id)})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	err := h.tvAPI.Remove(ctx, stubPath)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": fmt.Sprintf("tv stub %s removed", stubPath),
	})
}

// ========================= Utility =========================

// findStubByPartialName searches a directory for a .mkv file whose name contains the given partial name.
func findStubByPartialName(dir, partialName string) (string, bool) {
	log.Printf("[StubAPI] findStubByPartialName: dir=%q partial=%q", dir, partialName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("[StubAPI] findStubByPartialName: ReadDir err=%v", err)
		return "", false
	}
	foundCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".mkv") {
			continue
		}
		foundCount++
		log.Printf("[StubAPI] findStubByPartialName: candidate=%q", name)
		if strings.Contains(name, partialName) || strings.Contains(partialName, name) {
			log.Printf("[StubAPI] findStubByPartialName: MATCH candidate=%q", name)
			return filepath.Join(dir, name), true
		}
	}
	log.Printf("[StubAPI] findStubByPartialName: scanned=%d .mkv entries, no match", foundCount)
	return "", false
}

// stubsListDetailed returns full stub info including parsed metadata from stub file content.
func stubsListDetailed(dir, stubType string) ([]map[string]interface{}, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	var stubs []map[string]interface{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".mkv") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		stubInfo := map[string]interface{}{
			"type":   stubType,
			"status": "unknown",
		}

		// Try to read stub metadata
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			content := strings.TrimSpace(string(data))

			// Extract hash from content
			var url string
			if strings.HasPrefix(content, "{") {
				var obj map[string]interface{}
				if err := json.Unmarshal(data, &obj); err == nil {
					if u, ok := obj["url"].(string); ok {
						url = u
					}
					stubInfo["source"] = "auto"
				}
			} else {
				lines := strings.SplitN(content, "\n", 4)
				if len(lines) >= 1 {
					url = strings.TrimSpace(lines[0])
				}
				if len(lines) >= 4 {
					imdb := strings.TrimSpace(lines[3])
					stubInfo["imdb_id"] = imdb
				}
			}

			// Extract hash from URL to check torrent status
			if url != "" {
				reHash := regexp.MustCompile(`link=([a-f0-9]{40})`)
				m := reHash.FindStringSubmatch(url)
				if len(m) >= 2 {
					stubInfo["hash"] = m[1]
				}
			}
			stubInfo["hash_8"] = name[len(name)-8:] // last 8 chars of filename
		}

		stubInfo["filename"] = name
		stubInfo["path"] = filepath.Join(dir, name)
		stubInfo["size"] = info.Size()
		stubInfo["name"] = strings.TrimSuffix(name, filepath.Ext(name))

		stubs = append(stubs, stubInfo)
	}
	return stubs, nil
}

// stubsList is a helper to expose all stubs on disk for the dashboard.
func stubsList(dir string) ([]map[string]interface{}, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	var stubs []map[string]interface{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".mkv") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stubs = append(stubs, map[string]interface{}{
			"filename": name,
			"size":     info.Size(),
		})
	}
	return stubs, nil
}

// stubsGetList retrieves stub files from both movies and tv directories.
func stubsGetList(moviesDir, tvDir string) ([]map[string]interface{}, error) {
	var allStubs []map[string]interface{}

	movies, err := stubsList(moviesDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list movies: %w", err)
	}
	for _, s := range movies {
		s["type"] = "movie"
		allStubs = append(allStubs, s)
	}

	tv, err := stubsList(tvDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list tv: %w", err)
	}
	for _, s := range tv {
		s["type"] = "tv"
		allStubs = append(allStubs, s)
	}

	return allStubs, nil
}

// addStubReq is the request body for adding a new stub via POST /api/stubs.
type addStubReq struct {
	Type       string `json:"type"`       // "movie" or "tv"
	ID         string `json:"id"`         // TMDB ID or movie name
	SourceType string `json:"sourceType"` // "url" or "magnet"
	Source     string `json:"source"`     // URL or magnet link
}

// handleStubsRoot dispatches POST /api/stubs to add a new stub.
func (h *StubsHandler) handleStubsRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req addStubReq
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
	}

	if req.Type == "" || req.ID == "" || req.Source == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "type, id, and source are required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Generate filename based on type and id
	// Sanitize the ID to create a safe filename
	filename := sanitizeFilename(req.ID) + ".mkv"
	stubPath := filepath.Join(func() string {
		if req.Type == "movie" {
			return h.moviesDir
		}
		return h.tvDir
	}(), filename)

	var err error
	if req.Type == "movie" {
		err = h.movieAPI.Create(ctx, stubPath, req.Source)
	} else if req.Type == "tv" {
		err = h.tvAPI.Create(ctx, stubPath, req.Source)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "type must be 'movie' or 'tv'"})
		return
	}

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"stub_path": stubPath,
	})
}

// stubsDir returns the appropriate stubs directory based on type.
func (h *StubsHandler) stubsDir(isMovie bool) string {
	if isMovie {
		return h.moviesDir
	}
	return h.tvDir
}

// sanitizeFilename strips unsafe characters from a string to produce a safe
// filename component (letters, digits, dots, hyphens, underscores and spaces).
func sanitizeFilename(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9._\\- ]+`)
	return re.ReplaceAllString(s, "_")
}

// handleStubsFlat handles /api/stubs/<type>/ requests (backwards compat).
func (h *StubsHandler) handleStubsFlat(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/stubs/")
	if path == "" || path == "movies" || path == "tv" {
		http.NotFound(w, r)
		return
	}

	// Try to match as a stub ID — dispatch to the right handler.
	// Format: /api/stubs/<id>/action  or  /api/stubs/<id>
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// First try movies, then tv
	if _, found := findStubByPartialName(h.moviesDir, id); found {
		h.dispatchMovieAction(w, r, id, action)
		return
	}
	if _, found := findStubByPartialName(h.tvDir, id); found {
		h.dispatchTVAction(w, r, id, action)
		return
	}

	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"error": "stub not found"})
}

// dispatchMovieAction forwards an action to the appropriate movie handler.
func (h *StubsHandler) dispatchMovieAction(w http.ResponseWriter, r *http.Request, id, action string) {
	w.Header().Set("Content-Type", "application/json")
	switch action {
	case "candidates":
		h.getMovieCandidates(w, r, id)
	case "source":
		h.putMovieSource(w, r, id)
	case "rehydrate":
		h.postMovieRehydrate(w, r, id)
	case "remove":
		h.deleteMovie(w, r, id)
	default:
		if action == "" {
			// Just GET on the stub ID — return stub detail
			stubPath, _ := findStubByPartialName(h.moviesDir, id)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":      "movie",
				"id":        id,
				"stub_path": stubPath,
			})
		} else {
			http.Error(w, fmt.Sprintf(`{"error":"unknown action %s"}`, action), http.StatusBadRequest)
		}
	}
}

// dispatchTVAction forwards an action to the appropriate TV handler.
func (h *StubsHandler) dispatchTVAction(w http.ResponseWriter, r *http.Request, id, action string) {
	w.Header().Set("Content-Type", "application/json")
	switch action {
	case "candidates":
		h.getTVCandidates(w, r, id)
	case "rehydrate":
		h.postTVRehydrate(w, r, id)
	case "remove":
		h.deleteTV(w, r, id)
	default:
		if action == "" {
			stubPath, _ := findStubByPartialName(h.tvDir, id)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":      "tv",
				"id":        id,
				"stub_path": stubPath,
			})
		} else {
			http.Error(w, fmt.Sprintf(`{"error":"unknown action %s"}`, action), http.StatusBadRequest)
		}
	}
}
