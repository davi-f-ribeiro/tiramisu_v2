package arr

// SystemStatusResponse mirrors the Servarr /api/v3/system/status response.
type SystemStatusResponse struct {
	Version          string `json:"version"`
	BuildTime        string `json:"buildTime"`
	IsDebug          bool   `json:"isDebug"`
	IsProduction     bool   `json:"isProduction"`
	IsAdmin          bool   `json:"isAdmin"`
	IsUserInteractive bool  `json:"isUserInteractive"`
	OsName           string `json:"osName"`
	OsVersion        string `json:"osVersion"`
	IsLinux          bool   `json:"isLinux"`
	IsDocker         bool   `json:"isDocker"`
	Mode             string `json:"mode"`
	Authentication   string `json:"authentication"`
	UrlBase          string `json:"urlBase"`
	PackageVersion   string `json:"packageVersion"`
	AppName          string `json:"appName"`
	InstanceName     string `json:"instanceName"`
}

// RadarrMovieFile mirrors Radarr's movie file response.
type RadarrMovieFile struct {
	ID               int64  `json:"id"`
	MovieID          int64  `json:"movieId"`
	RelativePath     string `json:"relativePath"`
	Path             string `json:"path"`
	Size             int64  `json:"size"`
	DateAdded        string `json:"dateAdded"`
	QualityCutoffNot bool   `json:"qualityCutoffNotMet"`
}

// RadarrMovie mirrors Radarr's movie response.
type RadarrMovie struct {
	ID              int64             `json:"id"`
	Title           string            `json:"title"`
	OriginalTitle   string            `json:"originalTitle"`
	Year            int               `json:"year"`
	Path            string            `json:"path"`
	Monitored       bool              `json:"monitored"`
	HasFile         bool              `json:"hasFile"`
	IsAvailable     bool              `json:"isAvailable"`
	ImdbID          string            `json:"imdbId"`
	TmdbID          int64             `json:"tmdbId"`
	Size            int64             `json:"size,omitempty"`
	MovieFile       *RadarrMovieFile  `json:"movieFile,omitempty"`
}

// SonarrSeries mirrors Sonarr's series response.
type SonarrSeries struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Path      string `json:"path"`
	Monitored bool   `json:"monitored"`
	TvdbID    int64  `json:"tvdbId"`
	ImdbID    string `json:"imdbId"`
}

// SonarrEpisode mirrors Sonarr's episode response.
type SonarrEpisode struct {
	ID            int64              `json:"id"`
	SeriesID      int64              `json:"seriesId"`
	EpisodeFileID int64              `json:"episodeFileId"`
	SeasonNumber  int                `json:"seasonNumber"`
	EpisodeNumber int                `json:"episodeNumber"`
	Title         string             `json:"title"`
	Path          string             `json:"path"`
	HasFile       bool               `json:"hasFile"`
	Monitored     bool               `json:"monitored"`
	Size          int64              `json:"size,omitempty"`
	EpisodeFile   *SonarrEpisodeFile `json:"episodeFile,omitempty"`
}

// SonarrEpisodeFile mirrors Sonarr's episode file response.
type SonarrEpisodeFile struct {
	ID           int64  `json:"id"`
	SeriesID     int64  `json:"seriesId"`
	SeasonNumber int    `json:"seasonNumber"`
	RelativePath string `json:"relativePath"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	DateAdded    string `json:"dateAdded"`
}
