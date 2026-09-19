package subprovider

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ---------- Engine: orchestrator ----------

// SubtitleEngine is the main orchestrator. It runs two provider pipelines
// (SubDB via video hash, then OpenSubtitles via IMDB ID) in sequence,
// each in its own goroutine, and sends the best result through the channel.
type SubtitleEngine struct {
	cfg     EngineConfig
	subdb   *SubDBProvider
	os      *OSProvider
	writer  *Writer
	rateMu  sync.Mutex
	rateTok int
	rateRef time.Time
}

// NewSubtitleEngine creates a new engine. Validates mandatory config.
func NewSubtitleEngine(cfg EngineConfig) (*SubtitleEngine, error) {
	// Apply defaults
	if cfg.PreferredLanguages == nil {
		cfg.PreferredLanguages = getDefaultConfig().PreferredLanguages
	}
	if cfg.MaxResultsPerProvider == 0 {
		cfg.MaxResultsPerProvider = getDefaultConfig().MaxResultsPerProvider
	}
	if cfg.SubDBTimeout == 0 {
		cfg.SubDBTimeout = getDefaultConfig().SubDBTimeout
	}
	if cfg.OSDownloadTimeout == 0 {
		cfg.OSDownloadTimeout = getDefaultConfig().OSDownloadTimeout
	}
	if cfg.OpenSubtitlesBaseURL == "" {
		cfg.OpenSubtitlesBaseURL = getDefaultConfig().OpenSubtitlesBaseURL
	}

	// Validate mandatory config
	if cfg.OpenSubtitlesKey == "" {
		return nil, fmt.Errorf("subprovider: OpenSubtitles API key is required (set subtitle.api_key in config.json)")
	}
	if cfg.FUSEMountPath == "" {
		return nil, fmt.Errorf("subprovider: FUSE mount path is required (set TIRAMISU_FUSE_MOUNT_PATH env var)")
	}

	return &SubtitleEngine{
		cfg:     cfg,
		subdb:   NewSubDBProvider(),
		os:      NewOSProvider(cfg.OpenSubtitlesKey, cfg.OpenSubtitlesBaseURL, cfg.OpenSubtitlesUser, cfg.OpenSubtitlesPass),
		writer:  NewWriter(cfg.FUSEMountPath),
		rateTok: 15,
		rateRef: time.Now(),
	}, nil
}

// SubtitleJob represents a request to download a subtitle.
type SubtitleJob struct {
	// Input (provided by caller)
	VideoPath     string      // full path to .mkv in FUSE mount
	TorrentName   string      // torrent/filename (e.g. "Interstellar.2014.1080p.BluRay.x264.YTS")
	VideoHash     string      // SubDB MD5 hash of first+last 64KB (may be empty)
	ImdbID        string      // IMDB ID (e.g. "tt0816692")
	PreferredLang LanguageTag // user's preferred language

	// Output (sent through resultCh — overwritten by Run() internally)
	ResultCh chan<- Result
}

// Run launches the subtitle download pipeline for a single job.
// Returns a channel that receives the Result when done (or after timeout).
// The caller owns the returned channel and must read from it.
func (e *SubtitleEngine) Run(job SubtitleJob) <-chan Result {
	ch := make(chan Result, 1)

	go func() {
		torrentName := job.TorrentName
		if torrentName == "" {
			torrentName = CleanFilename(job.VideoPath)
		}

		// Step 1: Check if .srt already exists on disk
		srtPath := e.writer.BuildSRTPath(job.VideoPath, job.PreferredLang)
		if e.writer.FileExists(srtPath) {
			logf("[sub] cache hit: %s", srtPath)
			ch <- Result{
				Content:  nil,
				Filename: srtPath,
				Provider: "cache",
				Language: string(job.PreferredLang),
				Error:    nil,
			}
			return
		}

		wantsBR := job.PreferredLang == LangPortugueseBR

		// ---- Phase 1: SubDB (hash-based) ----
		if job.VideoHash != "" {
			logf("[sub] trying phase 1: subdb (hash=%s, imdb=%s, lang=%s)",
				videoHashShort(job.VideoHash), job.ImdbID, job.PreferredLang)

			content, srtPath, lang, err := e.trySubDBDownload(job.VideoHash, job.PreferredLang, torrentName, job.VideoPath)
			if err == nil {
				// Detect variant to decide whether to accept SubDB result
				variant, confidence := DetectPortugueseVariant(content, 8)

				if wantsBR && variant == VariantPT && confidence > 0.7 {
					logf("[sub] subdb returned PT-PT (conf %.2f), BR preferred → falling back to OpenSubtitles", confidence)
				} else {
					logf("[sub] phase 1: subdb success → %s (lang=%s)", srtPath, lang)
					ch <- Result{
						Content:  content,
						Filename: srtPath,
						Provider: "subdb",
						Language: lang,
						Error:    nil,
					}
					return
				}
			} else {
				logf("[sub] phase 1: subdb failed: %v", err)
			}
		} else {
			logf("[sub] phase 1: subdb skipped (no video hash — imdb=%s)", job.ImdbID)
		}

		// ---- Phase 2: OpenSubtitles (IMDB ID, fallback) ----
		osLang := LangPortuguese
		if wantsBR {
			osLang = LangPortugueseBR
		}
		if job.ImdbID != "" {
			logf("[sub] trying phase 2: opensubtitles (imdb=%s, lang=%s)", job.ImdbID, osLang)

			content, srtPath, lang, err := e.tryOSDownload(job.ImdbID, osLang, torrentName, job.VideoPath)
			if err == nil {
				logf("[sub] phase 2: opensubtitles success → %s (lang=%s)", srtPath, lang)
				ch <- Result{
					Content:  content,
					Filename: srtPath,
					Provider: "opensubtitles",
					Language: lang,
					Error:    nil,
				}
				return
			} else {
				logf("[sub] phase 2: opensubtitles failed: %v", err)
			}
		} else {
			logf("[sub] phase 2: opensubtitles skipped (no imdb id)")
		}

		// Neither provider found a subtitle
		logf("[sub] no subtitle available for %s (subdb hash=%q, imdb=%q)", torrentName, job.VideoHash, job.ImdbID)
		ch <- Result{
			Content:  nil,
			Filename: "",
			Provider: "",
			Language: "",
			Error:    fmt.Errorf("no subtitle available for %s", torrentName),
		}
	}()

	// Set a deadline: total timeout = SubDBTimeout + OSDownloadTimeout, capped at 30s
	go func() {
		totalTimeout := e.cfg.SubDBTimeout + e.cfg.OSDownloadTimeout
		if totalTimeout > 30*time.Second {
			totalTimeout = 30 * time.Second
		}
		time.Sleep(totalTimeout)
		select {
		case ch <- Result{
			Content:  nil,
			Filename: "",
			Provider: "",
			Language: "",
			Error:    fmt.Errorf("subtitle download timed out after %v", totalTimeout),
		}:
		default:
		}
	}()

	return ch
}

// videoHashShort returns a truncated hash for log display (first 12 hex chars).
func videoHashShort(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

// writeSubtitleSidecar writes the downloaded subtitle to disk.
// Called inside trySubDBDownload / tryOSDownload so they have access
// to sub.Language for the WriteSidecar call.
func (e *SubtitleEngine) writeSubtitleSidecar(content []byte, videoPath string, lang LanguageTag) (string, error) {
	srtPath := e.writer.BuildSRTPath(videoPath, lang)
	written, err := e.writer.WriteSidecar(videoPath, content, lang)
	if err != nil {
		return "", err
	}
	_ = written
	return srtPath, nil
}

// trySubDBDownload searches and downloads a subtitle via SubDB.
// Returns (content, srtPath, languageString, error).
func (e *SubtitleEngine) trySubDBDownload(videoHash string, lang LanguageTag, torrentName, videoPath string) ([]byte, string, string, error) {
	// Rate limit check (15 tokens / 15 minutes)
	if !e.tryAcquireSubDBToken() {
		return nil, "", "", fmt.Errorf("subdb rate limited (15/15min, retry later)")
	}
	defer e.releaseSubDBToken()

	// Search for available languages
	results, err := e.subdb.Search(videoHash, "", torrentName, lang, e.cfg.MaxResultsPerProvider)
	if err != nil {
		return nil, "", "", fmt.Errorf("subdb search: %w", err)
	}
	if results == nil || len(results) == 0 {
		return nil, "", "", fmt.Errorf("subdb: no languages available for hash %s", videoHashShort(videoHash))
	}

	// Select best match using language preference + release profile
	bestIdx := SelectBestSubtitle(results, torrentName, e.cfg.PreferredLanguages)
	if bestIdx < 0 || bestIdx >= len(results) {
		bestIdx = 0
	}

	sub := results[bestIdx]

	// Download the subtitle
	content, err := e.subdb.Download(sub)
	if err != nil {
		return nil, "", "", fmt.Errorf("subdb download: %w", err)
	}

	// Write sidecar to disk (core fix: persist subtitle to FUSE mount)
	written, err := e.writeSubtitleSidecar(content, videoPath, sub.Language)
	if err != nil {
		logf("[sub] subdb: failed to write sidecar: %v", err)
		return nil, "", "", fmt.Errorf("subdb: write sidecar: %w", err)
	}

	return content, written, string(sub.Language), nil
}

// tryOSDownload searches and downloads a subtitle via OpenSubtitles.
// Returns (content, srtPath, languageString, error).
func (e *SubtitleEngine) tryOSDownload(imdbID string, lang LanguageTag, torrentName, videoPath string) ([]byte, string, string, error) {
	// Search for subtitles
	results, err := e.os.Search("", imdbID, torrentName, lang, e.cfg.MaxResultsPerProvider)
	if err != nil {
		return nil, "", "", fmt.Errorf("os search: %w", err)
	}
	if results == nil || len(results) == 0 {
		return nil, "", "", fmt.Errorf("os: no subtitles found for %s", imdbID)
	}

	// Select best match
	bestIdx := SelectBestSubtitle(results, torrentName, e.cfg.PreferredLanguages)
	if bestIdx < 0 || bestIdx >= len(results) {
		bestIdx = 0
	}

	sub := results[bestIdx]

	// Download
	content, err := e.os.Download(sub)
	if err != nil {
		return nil, "", "", fmt.Errorf("os download: %w", err)
	}

	// Write sidecar to disk
	written, err := e.writeSubtitleSidecar(content, videoPath, sub.Language)
	if err != nil {
		logf("[sub] os: failed to write sidecar: %v", err)
		return nil, "", "", fmt.Errorf("os: write sidecar: %w", err)
	}

	return content, written, string(sub.Language), nil
}

// tryAcquireSubDBToken implements the 15-token/15-minute sliding window.
func (e *SubtitleEngine) tryAcquireSubDBToken() bool {
	e.rateMu.Lock()
	defer e.rateMu.Unlock()

	now := time.Now()

	// Refill tokens if enough time has passed
	elapsed := now.Sub(e.rateRef)
	if elapsed >= 15*time.Minute {
		e.rateTok = 15
		e.rateRef = now
	} else if elapsed >= time.Minute {
		// Gradual refill: 1 token per minute
		tokensToAdd := int(elapsed.Minutes())
		if tokensToAdd > 0 && e.rateTok < 15 {
			e.rateTok += tokensToAdd
			if e.rateTok > 15 {
				e.rateTok = 15
			}
			e.rateRef = now
		}
	}

	if e.rateTok <= 0 {
		return false
	}

	e.rateTok--
	return true
}

// releaseSubDBToken releases a token (for error cases where we didn't actually download).
func (e *SubtitleEngine) releaseSubDBToken() {
	e.rateMu.Lock()
	defer e.rateMu.Unlock()
	if e.rateTok < 15 {
		e.rateTok++
	}
}

// Min helper
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// CleanFilename returns the base name of the file without extension.
func CleanFilename(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}

// FileExists checks if a file exists at the given path.
func (e *SubtitleEngine) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DeleteCachedSubtitle removes a cached .srt file and allows re-fetching.
// Called by the manual "resync subtitle" command.
func (e *SubtitleEngine) DeleteCachedSubtitle(videoPath string, lang LanguageTag) bool {
	srtPath := e.writer.BuildSRTPath(videoPath, lang)
	if e.FileExists(srtPath) {
		if err := os.Remove(srtPath); err != nil {
			logf("[sub] failed to delete cached subtitle %s: %v", srtPath, err)
			return false
		}
		logf("[sub] deleted cached subtitle: %s", srtPath)
		return true
	}
	return false
}

// GetWriter returns the engine's writer (exported for main.go to wire OnSidecarWritten).
func (e *SubtitleEngine) GetWriter() *Writer {
	return e.writer
}
