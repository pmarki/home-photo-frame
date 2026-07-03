package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestHandleFavorite(t *testing.T) {
	t.Run("toggle on then off updates favorited flag and filter", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		seedFile(t, "b.jpg", makeTestJPEG(10, 10))

		// Baseline: neither file is favorited.
		rr := doRequest(handleImages, http.MethodGet, "/api/images", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("baseline images: status %d", rr.Code)
		}
		var resp ListResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, img := range resp.Images {
			if img.Favorited {
				t.Errorf("baseline: %s should not be favorited", img.Path)
			}
		}

		// Favorite a.jpg.
		rr = doRequest(handleFavorite, http.MethodPost, "/api/favorite/a.jpg",
			strings.NewReader(`{"favorited":true}`), "application/json")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("favorite POST: status %d body %s", rr.Code, rr.Body.String())
		}

		// The favorited filter should now return only a.jpg.
		rr = doRequest(handleImages, http.MethodGet, "/api/images?favorited=1", nil, "")
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode filtered: %v", err)
		}
		if resp.Total != 1 || len(resp.Images) != 1 || resp.Images[0].Path != "a.jpg" {
			t.Fatalf("filtered images = %+v, total = %d, want just a.jpg", resp.Images, resp.Total)
		}
		if !resp.Images[0].Favorited {
			t.Errorf("a.jpg.Favorited = false, want true")
		}

		// The unfiltered list should still show both, with the flag reflected.
		rr = doRequest(handleImages, http.MethodGet, "/api/images", nil, "")
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode unfiltered: %v", err)
		}
		if resp.Total != 2 {
			t.Errorf("unfiltered total = %d, want 2", resp.Total)
		}
		flags := map[string]bool{}
		for _, img := range resp.Images {
			flags[img.Path] = img.Favorited
		}
		if !flags["a.jpg"] || flags["b.jpg"] {
			t.Errorf("flags = %v, want a.jpg=true b.jpg=false", flags)
		}

		// Unfavorite a.jpg.
		rr = doRequest(handleFavorite, http.MethodPost, "/api/favorite/a.jpg",
			strings.NewReader(`{"favorited":false}`), "application/json")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("unfavorite POST: status %d", rr.Code)
		}
		rr = doRequest(handleImages, http.MethodGet, "/api/images?favorited=1", nil, "")
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode after unfavorite: %v", err)
		}
		if resp.Total != 0 || len(resp.Images) != 0 {
			t.Errorf("after unfavorite: total=%d images=%d, want empty", resp.Total, len(resp.Images))
		}
	})

	t.Run("re-favoriting is idempotent", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		for i := 0; i < 3; i++ {
			rr := doRequest(handleFavorite, http.MethodPost, "/api/favorite/a.jpg",
				strings.NewReader(`{"favorited":true}`), "application/json")
			if rr.Code != http.StatusNoContent {
				t.Fatalf("iter %d: status %d", i, rr.Code)
			}
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM file_favorites WHERE file_path = 'a.jpg'`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 1 {
			t.Errorf("file_favorites rows for a.jpg = %d, want 1", count)
		}
	})

	t.Run("rejects invalid path", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleFavorite, http.MethodPost, "/api/favorite/../etc/passwd",
			strings.NewReader(`{"favorited":true}`), "application/json")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("rejects non-POST", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleFavorite, http.MethodGet, "/api/favorite/a.jpg", nil, "")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", rr.Code)
		}
	})

	t.Run("deleteFile purges favorite row", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		if err := setFavorite("a.jpg", "", true); err != nil {
			t.Fatalf("setFavorite: %v", err)
		}
		deleteFile("a.jpg")
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM file_favorites WHERE file_path = 'a.jpg'`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("file_favorites rows after delete = %d, want 0", count)
		}
	})

	t.Run("per-user isolation", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, `users:
  - id: alice
    name: Alice
  - id: bob
    name: Bob
`)
		seedFile(t, "shared.jpg", makeTestJPEG(10, 10))

		// Alice favorites the file.
		rr := doRequestAs(handleFavorite, http.MethodPost, "/api/favorite/shared.jpg", "alice",
			strings.NewReader(`{"favorited":true}`), "application/json")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("alice favorite: status %d body %s", rr.Code, rr.Body.String())
		}

		// Alice's favorited listing includes it.
		rr = doRequestAs(handleImages, http.MethodGet, "/api/images?favorited=1", "alice", nil, "")
		var resp ListResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("alice decode: %v", err)
		}
		if resp.Total != 1 || !resp.Images[0].Favorited {
			t.Errorf("alice favorited listing = %+v, want 1 with Favorited=true", resp.Images)
		}

		// Bob's favorited listing is empty; the file shows Favorited=false in
		// the unfiltered listing.
		rr = doRequestAs(handleImages, http.MethodGet, "/api/images?favorited=1", "bob", nil, "")
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("bob decode filtered: %v", err)
		}
		if resp.Total != 0 {
			t.Errorf("bob filtered total = %d, want 0", resp.Total)
		}
		rr = doRequestAs(handleImages, http.MethodGet, "/api/images", "bob", nil, "")
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("bob decode unfiltered: %v", err)
		}
		if resp.Total != 1 || resp.Images[0].Favorited {
			t.Errorf("bob unfiltered: total=%d Favorited=%v, want 1 and false", resp.Total, resp.Images[0].Favorited)
		}
	})
}
