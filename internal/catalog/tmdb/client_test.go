package tmdb

import (
	"encoding/json"
	"testing"
)

func TestFindResult_ParseMovieResult(t *testing.T) {
	raw := `{
		"movie_results": [{"id": 603, "release_date": "1999-03-31"}],
		"tv_results": []
	}`
	var result FindResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.MovieResults) != 1 {
		t.Fatalf("expected 1 movie result, got %d", len(result.MovieResults))
	}
	if result.MovieResults[0].ID != 603 {
		t.Errorf("MovieResults[0].ID = %d, want 603", result.MovieResults[0].ID)
	}
	if result.MovieResults[0].ReleaseDate != "1999-03-31" {
		t.Errorf("MovieResults[0].ReleaseDate = %q, want %q", result.MovieResults[0].ReleaseDate, "1999-03-31")
	}
}

func TestFindResult_ParseTVResult(t *testing.T) {
	raw := `{
		"movie_results": [],
		"tv_results": [{"id": 1396, "first_air_date": "2005-03-20"}]
	}`
	var result FindResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.TVResults) != 1 {
		t.Fatalf("expected 1 tv result, got %d", len(result.TVResults))
	}
	if result.TVResults[0].ID != 1396 {
		t.Errorf("TVResults[0].ID = %d, want 1396", result.TVResults[0].ID)
	}
	if result.TVResults[0].FirstAirDate != "2005-03-20" {
		t.Errorf("TVResults[0].FirstAirDate = %q, want %q", result.TVResults[0].FirstAirDate, "2005-03-20")
	}
}

func TestFindResult_ParseEmpty(t *testing.T) {
	raw := `{"movie_results": [], "tv_results": []}`
	var result FindResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.MovieResults) != 0 {
		t.Errorf("expected 0 movie results, got %d", len(result.MovieResults))
	}
	if len(result.TVResults) != 0 {
		t.Errorf("expected 0 tv results, got %d", len(result.TVResults))
	}
}
