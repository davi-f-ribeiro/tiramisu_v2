package bazarrvfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tiramisu/internal/metadb"
)

type mockSubtitleProvider struct {
	enabled         bool
	candidates      []SubtitleCandidate
	searchErr       error
	searchMedia     int
	searchTitle     string
	movieSearches   int
	episodeSearches int
}

func (m *mockSubtitleProvider) Search(ctx context.Context, mediaID int, title string, language string) ([]SubtitleCandidate, error) {
	m.searchMedia = mediaID
	m.searchTitle = title
	return m.candidates, nil
}

func (m *mockSubtitleProvider) SearchMovie(ctx context.Context, radarrID int, language string) ([]SubtitleCandidate, error) {
	m.movieSearches++
	m.searchMedia = radarrID
	return m.candidates, m.searchErr
}

func (m *mockSubtitleProvider) SearchEpisode(ctx context.Context, episodeID int, language string) ([]SubtitleCandidate, error) {
	m.episodeSearches++
	m.searchMedia = episodeID
	return m.candidates, m.searchErr
}

func (m *mockSubtitleProvider) Download(ctx context.Context, subtitleID string, destPath string) error {
	return os.WriteFile(destPath, []byte("1\n00:00:00,000 --> 00:00:01,000\nmock\n\n"), 0644)
}

func (m *mockSubtitleProvider) IsEnabled() bool { return m.enabled }

func (m *mockSubtitleProvider) UpdateConfig(BazarrConfig) {}

func TestEntriesUsesOnlyVirtualSRTCache(t *testing.T) {
	ClearCache()
	dir := t.TempDir()
	provider := &mockSubtitleProvider{enabled: true, candidates: []SubtitleCandidate{{ID: "sub-1", Score: 97}}}
	opts := Options{Provider: provider, InodeForPath: func(string) uint64 { return 123 }}

	if entries := Entries(dir, 0, opts); len(entries) != 0 {
		t.Fatalf("expected no entries without cached subtitle, got %d", len(entries))
	}
	if provider.movieSearches != 0 || provider.episodeSearches != 0 || provider.searchMedia != 0 {
		t.Fatalf("Entries must not search provider: %#v", provider)
	}

	videoPath := filepath.Join(dir, "Movie.Title.2024-GROUP.mkv")
	cacheSubtitleForMedia(videoPath, SubtitleCandidate{ID: "sub-1", Title: "Movie Title", ReleaseGroup: "GROUP", Score: 97, Language: "pt-BR"}, provider, opts)

	entries := Entries(dir, 0, opts)
	if len(entries) != 1 {
		t.Fatalf("expected 1 cached virtual subtitle entry, got %d", len(entries))
	}
	wantName := "Movie.Title.GROUP-97%.pt-BR.srt"
	if entries[0].Name != wantName {
		t.Fatalf("expected virtual subtitle name %q, got %q", wantName, entries[0].Name)
	}
	if provider.movieSearches != 0 || provider.episodeSearches != 0 {
		t.Fatalf("Entries must not search provider, movie=%d episode=%d", provider.movieSearches, provider.episodeSearches)
	}

	entry, ok := Lookup(dir, wantName, opts)
	if !ok || entry == nil {
		t.Fatalf("expected lookup to resolve cached subtitle")
	}
}

func TestHandleReturnsDummyBeforeDownloadCompletes(t *testing.T) {
	entry := &Subtitle{
		Name:      "dummy.srt",
		Dir:       t.TempDir(),
		Candidate: SubtitleCandidate{ID: "sub-1"},
		Provider:  &mockSubtitleProvider{enabled: false},
		done:      make(chan struct{}),
	}
	h := &Handle{entry: entry}
	buf := make([]byte, 1024)
	res, errno := h.Read(context.Background(), buf, 0)
	if errno != 0 {
		t.Fatalf("unexpected errno: %v", errno)
	}
	if res == nil || res.Size() == 0 {
		t.Fatalf("expected dummy subtitle bytes")
	}
}

func TestDiscoverAbortsOnProviderErrorWithoutNegativeCache(t *testing.T) {
	ClearCache()
	db, err := metadb.New(filepath.Join(t.TempDir(), "tiramisu.db"), nil)
	if err != nil {
		t.Fatalf("new db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	firstPath := filepath.Join(t.TempDir(), "One.mkv")
	secondPath := filepath.Join(t.TempDir(), "Two.mkv")
	if err := db.UpsertARRMovie(ctx, 1, "tt0000001", "One", "One", 2024, firstPath, 1000); err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	if err := db.UpsertARRMovie(ctx, 2, "tt0000002", "Two", "Two", 2024, secondPath, 1000); err != nil {
		t.Fatalf("upsert second: %v", err)
	}

	provider := &mockSubtitleProvider{enabled: true, searchErr: errors.New("connection refused")}
	DiscoverVirtualSubtitles(ctx, db, provider, Options{Provider: provider})

	if provider.movieSearches != 1 {
		t.Fatalf("expected discovery to abort after first provider error, got %d movie searches", provider.movieSearches)
	}
	misses, err := db.ListVirtualSubtitles(ctx, VirtualLanguage)
	if err != nil {
		t.Fatalf("list virtual subtitles: %v", err)
	}
	if len(misses) != 0 {
		t.Fatalf("provider error must not persist negative cache, got %#v", misses)
	}
}
