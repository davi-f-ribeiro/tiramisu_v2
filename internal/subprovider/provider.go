package subprovider

import (
	"sync"
)

// LanguageTag is an ISO 639-2/B language code.
type LanguageTag string

const (
	LangPortuguese   LanguageTag = "por"
	LangPortugueseBR LanguageTag = "pob"
	LangEnglish      LanguageTag = "eng"
	LangMulti        LanguageTag = "multi"
)

// Subtitle is the common payload returned by a provider's Search method.
type Subtitle struct {
	ID           string      // provider-specific identifier (SubDB hash / OS file_id)
	ProviderName string      // "subdb" or "opensubtitles"
	Language     LanguageTag // ISO 639-2/B code
	Format       string      // "srt"
	Filename     string      // suggested sidecar filename (e.g. "Interstellar.default.por.srt")
	ReleaseInfo  string      // release string from provider (YTS, BLURAY, WEB-DL …)
	VideoHash    string      // SubDB MD5 hash of video
	Score        int         // 0–100, higher = better match
}

// Result is what the Engine returns through its channel after searching + downloading.
type Result struct {
	Content  []byte // raw .srt bytes
	Filename string // full sidecar filename
	Provider string // "subdb" or "opensubtitles"
	Language string
	Error    error // non-nil if no subtitle was found or download failed
}

// Provider is the contract every subtitle source must implement.
type Provider interface {
	// Name returns a human-readable provider name (for logging / metrics).
	Name() string

	// CanMatch reports whether this provider can operate with the data
	// currently available (video hash, IMDB ID, torrent metadata).
	CanMatch(videoHash string, imdbID string, torrentName string) bool

	// Search finds subtitles matching the given identifiers, returns
	// at most limit results sorted by relevance (highest Score first).
	Search(videoHash, imdbID, torrentName string, lang LanguageTag, limit int) ([]Subtitle, error)

	// Download fetches the subtitle content for the given payload.
	Download(sub Subtitle) ([]byte, error)
}

// Logger is the interface the subprovider package uses for logging.
type Logger interface {
	Printf(format string, v ...any)
}

// SetLogger sets the logger used by the subprovider package.
var (
	globalLogMu sync.Mutex
	globalLog   Logger = nil
)

func SetLogger(l Logger) {
	globalLogMu.Lock()
	defer globalLogMu.Unlock()
	globalLog = l
}

func logf(format string, v ...any) {
	globalLogMu.Lock()
	l := globalLog
	globalLogMu.Unlock()
	if l != nil {
		l.Printf("[subprovider] "+format, v...)
	}
}

// AppVersionOverride allows setting the version from external code.
// This is set by main.go at startup before using subprovider.
var AppVersionOverride string = ""

// getAppVersion returns the application version.
// Uses AppVersionOverride if set, otherwise returns "dev".
func getAppVersion() string {
	if AppVersionOverride != "" {
		return AppVersionOverride
	}
	return "dev"
}

// SetAppVersion sets the application version for the subprovider package.
func SetAppVersion(version string) {
	AppVersionOverride = version
}

// Version returns the current application version.
func Version() string {
	return getAppVersion()
}
