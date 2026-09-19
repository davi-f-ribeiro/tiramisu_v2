package arr

import (
	"regexp"
	"strings"
)

var (
	reYear     = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reChannels = regexp.MustCompile(`(?i)\b(7\.1|5\.1|2\.0)\b|_([57]\.1)_`)
	reHash     = regexp.MustCompile(`_([a-f0-9]{8})\.mkv$`)
)

// ParseMediaInfoFromName deterministically derives the Servarr media-info
// fields from a release name and an optional known resolution.
func ParseMediaInfoFromName(name string, resolution string) MediaInfoResource {
	lower := strings.ToLower(name)
	info := MediaInfoResource{
		Resolution:    resolution,
		ScanType:      "Progressive",
		AudioCodec:    "AAC",
		AudioChannels: 2.0,
		VideoCodec:    "x264",
	}

	if info.Resolution == "" {
		switch {
		case strings.Contains(lower, "2160p") || strings.Contains(lower, "4k"):
			info.Resolution = "2160p"
		case strings.Contains(lower, "1080p"):
			info.Resolution = "1080p"
		case strings.Contains(lower, "720p"):
			info.Resolution = "720p"
		default:
			info.Resolution = "1080p"
		}
	}

	if strings.Contains(lower, "2160p") || strings.Contains(lower, "x265") || strings.Contains(lower, "hevc") || strings.Contains(lower, "h265") || strings.Contains(lower, "h.265") {
		info.VideoCodec = "x265"
	} else {
		info.VideoCodec = "x264"
	}

	if strings.Contains(lower, "dv") || strings.Contains(lower, "dolby_vision") || strings.Contains(lower, "dolbyvision") {
		info.VideoProfile = "Dolby Vision"
	} else if strings.Contains(lower, "hdr10+") || strings.Contains(lower, "hdr10plus") {
		info.VideoProfile = "HDR10+"
	} else if strings.Contains(lower, "hdr") {
		info.VideoProfile = "HDR"
	}

	switch {
	case strings.Contains(lower, "atmos"):
		if info.VideoCodec == "x265" || strings.Contains(lower, "remux") || strings.Contains(lower, "bluray") {
			info.AudioCodec = "TrueHD Atmos"
			info.AudioChannels = 7.1
		} else {
			info.AudioCodec = "EAC3 Atmos"
			info.AudioChannels = 5.1
		}
	case strings.Contains(lower, "truehd"):
		info.AudioCodec = "TrueHD"
		info.AudioChannels = 7.1
	case strings.Contains(lower, "dts-hd") || strings.Contains(lower, "dtshd") || strings.Contains(lower, "dts_hd"):
		info.AudioCodec = "DTS-HD MA"
		info.AudioChannels = 7.1
	case strings.Contains(lower, "dts"):
		info.AudioCodec = "DTS"
		info.AudioChannels = 5.1
	case strings.Contains(lower, "ddp") || strings.Contains(lower, "eac3") || strings.Contains(lower, "dd+"):
		info.AudioCodec = "EAC3"
		info.AudioChannels = 5.1
	case strings.Contains(lower, "5.1") || strings.Contains(lower, "ac3") || strings.Contains(lower, "dd5"):
		info.AudioCodec = "AC3"
		info.AudioChannels = 5.1
	case strings.Contains(lower, "7.1"):
		info.AudioCodec = "EAC3"
		info.AudioChannels = 7.1
	default:
		info.AudioCodec = "AAC"
		info.AudioChannels = 2.0
	}

	return info
}
