package metadb

import (
	"context"
	"path/filepath"
	"testing"
)

func TestVirtualSubtitlesPersistence(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "tiramisu.db"), &testLogger{})
	if err != nil {
		t.Fatalf("new db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	moviePath := "/data/movies/Test Movie (2024)/Test Movie (2024).mkv"
	if err := db.UpsertARRMovie(ctx, 12345, "tt1234567", "Test Movie", "Test Movie 2024", 2024, moviePath, 1000); err != nil {
		t.Fatalf("upsert arr movie: %v", err)
	}

	missing, err := db.ListARRMediaMissingVirtualSubtitles(ctx, "pt-BR")
	if err != nil {
		t.Fatalf("list missing before subtitle: %v", err)
	}
	if len(missing) != 1 || missing[0].Path != moviePath || missing[0].MediaType != "movie" || missing[0].ID == 0 {
		t.Fatalf("unexpected missing media before subtitle: %#v", missing)
	}

	rec := VirtualSubtitle{MediaPath: moviePath, Language: "pt-BR", SubtitleID: "sub-1", Provider: "Bazarr", Score: 88}
	if err := db.UpsertVirtualSubtitle(ctx, rec); err != nil {
		t.Fatalf("upsert virtual subtitle: %v", err)
	}

	got, err := db.GetVirtualSubtitle(ctx, moviePath, "pt-BR")
	if err != nil {
		t.Fatalf("get virtual subtitle: %v", err)
	}
	if got == nil || got.SubtitleID != "sub-1" || got.Score != 88 {
		t.Fatalf("unexpected virtual subtitle: %#v", got)
	}

	all, err := db.ListVirtualSubtitles(ctx, "pt-BR")
	if err != nil {
		t.Fatalf("list virtual subtitles: %v", err)
	}
	if len(all) != 1 || all[0].MediaPath != moviePath {
		t.Fatalf("unexpected virtual subtitles: %#v", all)
	}

	missing, err = db.ListARRMediaMissingVirtualSubtitles(ctx, "pt-BR")
	if err != nil {
		t.Fatalf("list missing after subtitle: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("expected no missing media after subtitle, got %#v", missing)
	}
}

func TestVirtualSubtitleNegativeCacheSuppressesRediscovery(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "tiramisu.db"), &testLogger{})
	if err != nil {
		t.Fatalf("new db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	moviePath := "/data/movies/Missing Movie (2024)/Missing Movie (2024).mkv"
	if err := db.UpsertARRMovie(ctx, 12345, "tt7654321", "Missing Movie", "Missing Movie 2024", 2024, moviePath, 1000); err != nil {
		t.Fatalf("upsert arr movie: %v", err)
	}
	if err := db.UpsertVirtualSubtitle(ctx, VirtualSubtitle{MediaPath: moviePath, Language: "pt-BR", Provider: "none", Score: 0}); err != nil {
		t.Fatalf("upsert negative virtual subtitle: %v", err)
	}

	missing, err := db.ListARRMediaMissingVirtualSubtitles(ctx, "pt-BR")
	if err != nil {
		t.Fatalf("list missing after negative cache: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("expected recent negative cache to suppress rediscovery, got %#v", missing)
	}
}
