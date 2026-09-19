package arr

import "testing"

func TestParseMediaInfoFromName(t *testing.T) {
	tests := []struct {
		name           string
		resolution     string
		wantResolution string
		wantVideo      string
		wantProfile    string
		wantAudio      string
		wantChannels   float64
	}{
		{
			name:           "Movie.2025.2160p.WEB-DL.DV.HDR10+.EAC3.5.1",
			wantResolution: "2160p",
			wantVideo:      "x265",
			wantProfile:    "Dolby Vision",
			wantAudio:      "EAC3",
			wantChannels:   5.1,
		},
		{
			name:           "Movie.2025.1080p.BluRay.TrueHD.Atmos",
			wantResolution: "1080p",
			wantVideo:      "x264",
			wantAudio:      "TrueHD Atmos",
			wantChannels:   7.1,
		},
		{
			name:           "Show.S01E01.1080p.WEB-DL.H264.DDP5.1",
			wantResolution: "1080p",
			wantVideo:      "x264",
			wantAudio:      "EAC3",
			wantChannels:   5.1,
		},
	}

	for _, tt := range tests {
		info := ParseMediaInfoFromName(tt.name, tt.resolution)
		if info.Resolution != tt.wantResolution || info.VideoCodec != tt.wantVideo || info.VideoProfile != tt.wantProfile || info.AudioCodec != tt.wantAudio || info.AudioChannels != tt.wantChannels {
			t.Errorf("ParseMediaInfoFromName(%q) = %+v", tt.name, info)
		}
	}
}

func TestParseMediaInfoFromNameDefaultsResolution(t *testing.T) {
	info := ParseMediaInfoFromName("Show.S01E01.720p.WEB.AAC", "")
	if info.Resolution != "720p" || info.ScanType != "Progressive" || info.AudioCodec != "AAC" || info.AudioChannels != 2.0 {
		t.Fatalf("media info defaults = %+v", info)
	}
}
