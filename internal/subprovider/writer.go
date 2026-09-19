package subprovider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---------- Writer: sidecar file creation ----------

// Writer handles writing subtitle sidecar files to the FUSE mount.
type Writer struct {
	fuseMountPath    string
	OnSidecarWritten func(dirPath string) // optional, may be nil
}

// NewWriter creates a new Writer for the given FUSE mount path.
func NewWriter(fuseMountPath string) *Writer {
	return &Writer{
		fuseMountPath: fuseMountPath,
	}
}

// BuildSRTPath constructs the full .srt sidecar path for a video file.
//
// Convention:
//
//	Input:  "/mnt/tiramisu-mkv-virtual/movies/Interstellar/default.mkv"
//	Output: "/mnt/tiramisu-mkv-virtual/movies/Interstellar/default.por.srt"
//	Output: "/mnt/tiramisu-mkv-virtual/movies/Interstellar/default.multi.srt"
func (w *Writer) BuildSRTPath(videoPath string, lang LanguageTag) string {
	// Strip .mkv extension
	ext := filepath.Ext(videoPath)
	if !strings.EqualFold(ext, ".mkv") {
		// If not .mkv, append language.srt to the original name
		return videoPath + "." + string(lang) + ".srt"
	}
	name := videoPath[:len(videoPath)-len(ext)]

	// Append language + .srt
	return name + "." + string(lang) + ".srt"
}

// FileExists checks if a file exists at the given path.
func (w *Writer) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WriteSidecar writes subtitle content to a .srt file next to the video.
// Returns the path written, or an error.
func (w *Writer) WriteSidecar(videoPath string, content []byte, lang LanguageTag) (string, error) {
	srtPath := w.BuildSRTPath(videoPath, lang)

	// Ensure the directory exists (it should, but be safe)
	dir := filepath.Dir(srtPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("subprovider: mkdir %s: %w", dir, err)
	}

	// Write the file (exclusive: fail if it already exists)
	if _, err := os.Stat(srtPath); err == nil {
		// Already exists — skip (cache policy D4)
		logf("WriteSidecar: skipping, file exists: %s", srtPath)
		return srtPath, nil
	}

	// Write .srt.tmp first, then rename for atomicity
	tmpPath := srtPath + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		os.Remove(tmpPath) // clean up
		return "", fmt.Errorf("subprovider: write %s: %w", tmpPath, err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, srtPath); err != nil {
		os.Remove(tmpPath) // clean up
		return "", fmt.Errorf("subprovider: rename %s → %s: %w", tmpPath, srtPath, err)
	}

	if w.OnSidecarWritten != nil {
		w.OnSidecarWritten(filepath.Dir(srtPath))
	}

	logf("WriteSidecar: wrote %d bytes → %s", len(content), srtPath)
	return srtPath, nil
}

// GetMountPath returns the configured FUSE mount path.
func (w *Writer) GetMountPath() string {
	return w.fuseMountPath
}

// DeleteCachedSubtitle removes the cached subtitle file for the given video and language.
// Used by the manual resync endpoint.
func (w *Writer) DeleteCachedSubtitle(videoPath string, lang LanguageTag) error {
	srtPath := w.BuildSRTPath(videoPath, lang)
	if err := os.Remove(srtPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("subprovider: delete cached subtitle %s: %w", srtPath, err)
	}
	return nil
}
