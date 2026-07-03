package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// countAndLast returns the view_count and last_viewed_at for (path, userID).
// Returns 0, 0 if no row exists.
func countAndLast(t *testing.T, path, userID string) (int, int64) {
	t.Helper()
	var c int
	var last int64
	err := db.QueryRow(
		`SELECT view_count, last_viewed_at FROM file_views WHERE file_path = ? AND user_id = ?`,
		path, userID,
	).Scan(&c, &last)
	if err != nil {
		return 0, 0
	}
	return c, last
}

func TestTrackView(t *testing.T) {
	t.Run("first call inserts row with count=1", func(t *testing.T) {
		setupTestEnv(t)
		trackView("a.jpg", nil)
		c, last := countAndLast(t, "a.jpg", "")
		if c != 1 {
			t.Errorf("count = %d, want 1", c)
		}
		if last == 0 {
			t.Errorf("last_viewed_at not set")
		}
	})

	t.Run("second call increments count and updates timestamp", func(t *testing.T) {
		setupTestEnv(t)
		trackView("a.jpg", nil)
		_, first := countAndLast(t, "a.jpg", "")
		time.Sleep(2 * time.Millisecond)
		trackView("a.jpg", nil)
		c, second := countAndLast(t, "a.jpg", "")
		if c != 2 {
			t.Errorf("count = %d, want 2", c)
		}
		if second <= first {
			t.Errorf("timestamp did not advance: first=%d second=%d", first, second)
		}
	})

	t.Run("attributes to user id when user provided", func(t *testing.T) {
		setupTestEnv(t)
		u := &ConfigUser{ID: "piotrek", Name: "Piotr"}
		trackView("a.jpg", u)
		if c, _ := countAndLast(t, "a.jpg", "piotrek"); c != 1 {
			t.Errorf("piotrek count = %d, want 1", c)
		}
		if c, _ := countAndLast(t, "a.jpg", ""); c != 0 {
			t.Errorf("anonymous count = %d, want 0 (user was set)", c)
		}
	})

	t.Run("keeps per-user rows separate", func(t *testing.T) {
		setupTestEnv(t)
		u1 := &ConfigUser{ID: "a", Name: "A"}
		u2 := &ConfigUser{ID: "b", Name: "B"}
		trackView("photo.jpg", u1)
		trackView("photo.jpg", u2)
		trackView("photo.jpg", u2)
		if c, _ := countAndLast(t, "photo.jpg", "a"); c != 1 {
			t.Errorf("a count = %d, want 1", c)
		}
		if c, _ := countAndLast(t, "photo.jpg", "b"); c != 2 {
			t.Errorf("b count = %d, want 2", c)
		}
	})
}

func TestDeleteFileCascadesViews(t *testing.T) {
	setupTestEnv(t)
	seedFile(t, "a.jpg", makeTestJPEG(10, 10))
	trackView("a.jpg", nil)
	if c, _ := countAndLast(t, "a.jpg", ""); c != 1 {
		t.Fatalf("precondition failed: count = %d, want 1", c)
	}
	deleteFile("a.jpg")
	if c, _ := countAndLast(t, "a.jpg", ""); c != 0 {
		t.Errorf("file_views row still present after deleteFile: count = %d", c)
	}
}

func TestQueryFilesViewedMode(t *testing.T) {
	t.Run("recent sorts by last_viewed_at desc and hides unviewed", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		seedFile(t, "b.jpg", makeTestJPEG(10, 10))
		seedFile(t, "c.jpg", makeTestJPEG(10, 10)) // unviewed

		trackView("a.jpg", nil)
		time.Sleep(2 * time.Millisecond)
		trackView("b.jpg", nil) // b is more recent

		imgs, total, err := queryFiles(queryParams{viewedMode: "recent"})
		if err != nil {
			t.Fatalf("queryFiles: %v", err)
		}
		if total != 2 || len(imgs) != 2 {
			t.Fatalf("total=%d len=%d, want 2 (unviewed hidden)", total, len(imgs))
		}
		if imgs[0].Path != "b.jpg" || imgs[1].Path != "a.jpg" {
			t.Errorf("order = %s,%s, want b.jpg,a.jpg", imgs[0].Path, imgs[1].Path)
		}
	})

	t.Run("most sorts by view_count desc", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		seedFile(t, "b.jpg", makeTestJPEG(10, 10))

		trackView("a.jpg", nil)
		trackView("a.jpg", nil)
		trackView("a.jpg", nil) // a: 3 views
		trackView("b.jpg", nil) // b: 1 view

		imgs, _, err := queryFiles(queryParams{viewedMode: "most"})
		if err != nil {
			t.Fatalf("queryFiles: %v", err)
		}
		if len(imgs) != 2 || imgs[0].Path != "a.jpg" || imgs[1].Path != "b.jpg" {
			t.Errorf("order = %v, want [a.jpg b.jpg]", []string{imgs[0].Path, imgs[1].Path})
		}
	})

	t.Run("scopes views to current user", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		seedFile(t, "b.jpg", makeTestJPEG(10, 10))

		trackView("a.jpg", &ConfigUser{ID: "alice"})
		trackView("b.jpg", &ConfigUser{ID: "bob"})

		imgs, total, err := queryFiles(queryParams{viewedMode: "recent", userID: "alice"})
		if err != nil {
			t.Fatalf("queryFiles: %v", err)
		}
		if total != 1 || len(imgs) != 1 || imgs[0].Path != "a.jpg" {
			t.Errorf("alice view = %v (total=%d), want [a.jpg]", imgs, total)
		}
	})

	t.Run("combines with year filter", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		seedFile(t, "b.jpg", makeTestJPEG(10, 10))

		trackView("a.jpg", nil)
		trackView("b.jpg", nil)

		// Force a's date_taken into 2020 and b's into 2021.
		y2020 := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC).UnixNano()
		y2021 := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC).UnixNano()
		if _, err := db.Exec(`UPDATE files SET date_taken = ? WHERE path = ?`, y2020, "a.jpg"); err != nil {
			t.Fatalf("update: %v", err)
		}
		if _, err := db.Exec(`UPDATE files SET date_taken = ? WHERE path = ?`, y2021, "b.jpg"); err != nil {
			t.Fatalf("update: %v", err)
		}

		imgs, total, err := queryFiles(queryParams{viewedMode: "recent", year: 2020})
		if err != nil {
			t.Fatalf("queryFiles: %v", err)
		}
		if total != 1 || len(imgs) != 1 || imgs[0].Path != "a.jpg" {
			t.Errorf("filtered = %v total=%d, want [a.jpg]", imgs, total)
		}
	})
}

func TestHandleImagesViewedParam(t *testing.T) {
	t.Run("rejects invalid viewed value", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleImages, http.MethodGet, "/api/images?viewed=bogus", nil, "")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("recent returns only viewed files for the current user", func(t *testing.T) {
		setupTestEnv(t)
		seedFile(t, "a.jpg", makeTestJPEG(10, 10))
		seedFile(t, "b.jpg", makeTestJPEG(10, 10))
		trackView("a.jpg", nil)

		rr := doRequest(handleImages, http.MethodGet, "/api/images?viewed=recent", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var resp ListResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Total != 1 || len(resp.Images) != 1 || resp.Images[0].Path != "a.jpg" {
			t.Errorf("response = %+v, want single a.jpg", resp)
		}
	})
}
