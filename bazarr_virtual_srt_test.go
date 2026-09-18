package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tiramisu/internal/subprovider"
)

type mockSubtitleProvider struct {
	enabled     bool
	candidates  []subprovider.SubtitleCandidate
	searchMedia int
	searchTitle string
}

func (m *mockSubtitleProvider) Search(ctx context.Context, mediaID int, title string, language string) ([]subprovider.SubtitleCandidate, error) {
	m.searchMedia = mediaID
	m.searchTitle = title
	return m.candidates, nil
}

func (m *mockSubtitleProvider) Download(ctx context.Context, subtitleID string, destPath string) error {
	return os.WriteFile(destPath, []byte("1\n00:00:00,000 --> 00:00:01,000\nmock\n\n"), 0644)
}

func (m *mockSubtitleProvider) IsEnabled() bool { return m.enabled }

func TestBazarrVirtualSRTEntriesSynthesizesSubtitleNode(t *testing.T) {
	dir := t.TempDir()
	mkvPath := filepath.Join(dir, "Movie.Title.2024-GROUP.mkv")
	metadata := `{"url":"http://example.invalid/movie.mkv","size":104857600,"imdb":"tt1234567","radarr_id":42}`
	if err := os.WriteFile(mkvPath, []byte(metadata), 0644); err != nil {
		t.Fatal(err)
	}

	provider := &mockSubtitleProvider{
		enabled: true,
		candidates: []subprovider.SubtitleCandidate{{
			ID:           "sub-1",
			Title:        "Movie Title",
			ReleaseGroup: "GROUP",
			Score:        97,
			Language:     "pt-BR",
		}},
	}

	entries := bazarrVirtualSRTEntries(dir, 0, provider)
	if len(entries) != 1 {
		t.Fatalf("expected 1 virtual subtitle entry, got %d", len(entries))
	}
	wantName := "Movie.Title.GROUP-97%.pt-BR.srt"
	if entries[0].Name != wantName {
		t.Fatalf("expected virtual subtitle name %q, got %q", wantName, entries[0].Name)
	}
	if provider.searchMedia != 42 {
		t.Fatalf("expected provider search to receive radarr id 42, got %d", provider.searchMedia)
	}

	entry, ok := lookupBazarrVirtualSRT(dir, wantName, provider)
	if !ok || entry == nil {
		t.Fatalf("expected lookup to resolve synthesized subtitle")
	}
}

func TestVirtualSRTHandleReturnsDummyBeforeDownloadCompletes(t *testing.T) {
	entry := &bazarrVirtualSubtitle{
		Name:      "dummy.srt",
		Dir:       t.TempDir(),
		Candidate: subprovider.SubtitleCandidate{ID: "sub-1"},
		Provider:  &mockSubtitleProvider{enabled: false},
		done:      make(chan struct{}),
	}
	h := &VirtualSRTHandle{entry: entry}
	buf := make([]byte, 1024)
	res, errno := h.Read(context.Background(), buf, 0)
	if errno != 0 {
		t.Fatalf("unexpected errno: %v", errno)
	}
	if res == nil || res.Size() == 0 {
		t.Fatalf("expected dummy subtitle bytes")
	}
}
