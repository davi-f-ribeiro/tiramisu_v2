package subprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBazarrSearchUsesNativeAPINoV1(t *testing.T) {
	var requested string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.String()
		if r.Header.Get("X-API-KEY") != "secret" {
			t.Fatalf("missing X-API-KEY header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"sub-1","title":"Movie","releaseGroup":"RG","language":"pt-BR","score":96}]}`))
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
	if strings.HasPrefix(requested, "/api/v1/") {
		t.Fatalf("client used legacy v1 route: %s", requested)
	}
	if !strings.HasPrefix(requested, "/api/subtitles?") {
		t.Fatalf("client did not use native subtitles route: %s", requested)
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
