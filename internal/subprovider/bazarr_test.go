package subprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBazarrSearchUsesProvidersMoviesNoSubtitlesEndpoint(t *testing.T) {
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.String())
		if r.URL.Path == "/api/subtitles" {
			t.Fatalf("/api/subtitles must not be used for provider search")
		}
		if r.Header.Get("X-API-KEY") != "secret" {
			t.Fatalf("missing X-API-KEY header")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/providers/movies" || r.URL.Query().Get("radarrid") != "123" {
			t.Fatalf("unexpected route: %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`[{"id":"sub-1","title":"Movie","releaseGroup":"RG","language":"pt-BR","score":96}]`))
	}))
	defer srv.Close()

	client := NewBazarrClient(BazarrConfig{Enabled: true, URL: srv.URL, APIKey: "secret", MaxResults: 5})
	subs, err := client.Search(context.Background(), 123, "Movie", "pt-BR")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].ID != "sub-1" {
		t.Fatalf("unexpected subtitles: %#v", subs)
	}
	if len(requested) != 1 || requested[0] != "/api/providers/movies?radarrid=123" {
		t.Fatalf("unexpected requests: %#v", requested)
	}
}

func TestBazarrSearchByIMDbResolvesRadarrIDThenUsesProviders(t *testing.T) {
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.String())
		if r.URL.Path == "/api/subtitles" {
			t.Fatalf("/api/subtitles must not be used for imdb provider search")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/movies":
			_, _ = w.Write([]byte(`[{"imdbId":"tt1234567","radarrId":321}]`))
		case "/api/providers/movies":
			if r.URL.Query().Get("radarrid") != "321" {
				t.Fatalf("unexpected radarrid: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"sub-2","title":"Movie","releaseGroup":"RG","language":{"code":"por"},"score":88}]}`))
		default:
			t.Fatalf("unexpected route: %s", r.URL.String())
		}
	}))
	defer srv.Close()

	client := NewBazarrClient(BazarrConfig{Enabled: true, URL: srv.URL, MaxResults: 5})
	subs, err := client.SearchByIMDb(context.Background(), "tt1234567", "pt-BR")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].ID != "sub-2" {
		t.Fatalf("unexpected subtitles: %#v", subs)
	}
	want := []string{"/api/movies", "/api/providers/movies?radarrid=321"}
	if strings.Join(requested, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected requests: %#v", requested)
	}
}

func TestBazarrSearchRejectsHTMLFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>Bazarr SPA</body></html>`))
	}))
	defer srv.Close()

	client := NewBazarrClient(BazarrConfig{Enabled: true, URL: srv.URL, MaxResults: 1})
	_, err := client.Search(context.Background(), 123, "Movie", "pt-BR")
	if err == nil {
		t.Fatal("expected HTML fallback error")
	}
	if !strings.Contains(err.Error(), "HTML instead of JSON") {
		t.Fatalf("expected explicit HTML fallback error, got: %v", err)
	}
}
