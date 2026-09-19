package arr

// SystemStatusResponse mirrors the Servarr /api/v3/system/status response.
type SystemStatusResponse struct {
	Version           string `json:"version"`
	BuildTime         string `json:"buildTime"`
	IsDebug           bool   `json:"isDebug"`
	IsProduction      bool   `json:"isProduction"`
	IsAdmin           bool   `json:"isAdmin"`
	IsUserInteractive bool   `json:"isUserInteractive"`
	OsName            string `json:"osName"`
	OsVersion         string `json:"osVersion"`
	IsLinux           bool   `json:"isLinux"`
	IsDocker          bool   `json:"isDocker"`
	Mode              string `json:"mode"`
	Authentication    string `json:"authentication"`
	UrlBase           string `json:"urlBase"`
	PackageVersion    string `json:"packageVersion"`
	AppName           string `json:"appName"`
	InstanceName      string `json:"instanceName"`
}

// QualityDetail mirrors Servarr's quality profile detail.
type QualityDetail struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Resolution int    `json:"resolution"`
}

// RevisionDetail mirrors Servarr's revision info.
type RevisionDetail struct {
	Version  int  `json:"version"`
	Real     int  `json:"real"`
	IsRepack bool `json:"isRepack"`
}

// QualityModel wraps quality detail + revision for file objects.
type QualityModel struct {
	Quality  QualityDetail  `json:"quality"`
	Revision RevisionDetail `json:"revision"`
}

// RadarrMovieFile mirrors Radarr's movie file response.
type RadarrMovieFile struct {
	ID               int64        `json:"id"`
	MovieID          int64        `json:"movieId"`
	RelativePath     string       `json:"relativePath"`
	Path             string       `json:"path"`
	Size             int64        `json:"size"`
	DateAdded        string       `json:"dateAdded"`
	Quality          QualityModel `json:"quality"`
	QualityCutoffNot bool         `json:"qualityCutoffNotMet"`
}

// AlternativeTitle mirrors Servarr's alternative title response.
type AlternativeTitle struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// RadarrMovie mirrors Radarr's movie response.
type RadarrMovie struct {
	ID                int64              `json:"id"`
	Title             string             `json:"title"`
	OriginalTitle     string             `json:"originalTitle"`
	RawTitle          string             `json:"rawTitle,omitempty"`
	SortTitle         string             `json:"sortTitle"`
	CleanTitle        string             `json:"cleanTitle"`
	TitleSlug         string             `json:"titleSlug"`
	Status            string             `json:"status"`
	Year              int                `json:"year"`
	Path              string             `json:"path"`
	Monitored         bool               `json:"monitored"`
	HasFile           bool               `json:"hasFile"`
	IsAvailable       bool               `json:"isAvailable"`
	ImdbID            string             `json:"imdbId"`
	TmdbID            int64              `json:"tmdbId"`
	Size              int64              `json:"size,omitempty"`
	MovieFile         *RadarrMovieFile   `json:"movieFile,omitempty"`
	AlternateTitles   []AlternativeTitle `json:"alternateTitles"`
	AlternativeTitles []AlternativeTitle `json:"alternativeTitles"`
	Genres            []string           `json:"genres"`
	Tags              []int              `json:"tags"`
	Images            []MediaImage       `json:"images"`
	PosterPath        string             `json:"-"`
	BackdropPath      string             `json:"-"`
}

// SonarrSeries mirrors Sonarr's series response.
type SonarrSeries struct {
	ID                int64              `json:"id"`
	Title             string             `json:"title"`
	OriginalTitle     string             `json:"originalTitle"`
	SortTitle         string             `json:"sortTitle"`
	SeasonCount       int                `json:"seasonCount"`
	Year              int                `json:"year"`
	Path              string             `json:"path"`
	Monitored         bool               `json:"monitored"`
	TvdbID            int64              `json:"tvdbId"`
	ImdbID            string             `json:"imdbId"`
	TmdbID            int64              `json:"tmdbId"`
	Seasons           []SonarrSeason     `json:"seasons"`
	AlternateTitles   []AlternativeTitle `json:"alternateTitles"`
	AlternativeTitles []AlternativeTitle `json:"alternativeTitles"`
	Genres            []string           `json:"genres"`
	Tags              []int              `json:"tags"`
	Images            []MediaImage       `json:"images"`
	PosterPath        string             `json:"-"`
	BackdropPath      string             `json:"-"`
}

// SonarrSeason mirrors Sonarr's season response.
type SonarrSeason struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// MediaImage mirrors Servarr's cover image (poster or fanart/backdrop).
type MediaImage struct {
	CoverType string `json:"coverType"` // "poster", "fanart"
	URL       string `json:"url"`
	RemoteURL string `json:"remoteUrl"`
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
	ID           int64        `json:"id"`
	SeriesID     int64        `json:"seriesId"`
	SeasonNumber int          `json:"seasonNumber"`
	RelativePath string       `json:"relativePath"`
	Path         string       `json:"path"`
	Size         int64        `json:"size"`
	DateAdded    string       `json:"dateAdded"`
	Quality      QualityModel `json:"quality"`
}
