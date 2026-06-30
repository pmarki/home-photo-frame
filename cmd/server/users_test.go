package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testUsersYAML = `
users:
  - id: alice
    name: Alice
    folders: [vacation]
  - id: bob
    name: Bob
    folders: [work]
`

// seedFolderFile inserts a file at "folder/name" (or "name" if folder=="") into
// the DB and writes the bytes to disk under photosDir.
func seedFolderFile(t *testing.T, folder, name string) {
	t.Helper()
	imgPath := name
	if folder != "" {
		if err := os.MkdirAll(filepath.Join(photosDir, filepath.FromSlash(folder)), 0o755); err != nil {
			t.Fatalf("seedFolderFile mkdir: %v", err)
		}
		imgPath = folder + "/" + name
	}
	seedFile(t, imgPath, makeTestJPEG(10, 10))
	// seedFile inserts with folder='', so fix the row's folder column.
	if folder != "" {
		if _, err := db.Exec(`UPDATE files SET folder = ? WHERE path = ?`, folder, imgPath); err != nil {
			t.Fatalf("seedFolderFile update folder: %v", err)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("missing file returns nil config without error", func(t *testing.T) {
		setupTestEnv(t)
		cfg, err := loadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if cfg != nil {
			t.Errorf("cfg = %+v, want nil", cfg)
		}
		if appConfig != nil {
			t.Errorf("appConfig = %+v, want nil", appConfig)
		}
	})

	t.Run("missing users key disables feature", func(t *testing.T) {
		setupTestEnv(t)
		path := filepath.Join(t.TempDir(), "c.yaml")
		if err := os.WriteFile(path, []byte("# no users key\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if appConfig != nil {
			t.Errorf("appConfig = %+v, want nil", appConfig)
		}
	})

	t.Run("empty users list disables feature", func(t *testing.T) {
		setupTestEnv(t)
		path := filepath.Join(t.TempDir(), "c.yaml")
		if err := os.WriteFile(path, []byte("users: []\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if appConfig != nil {
			t.Errorf("appConfig = %+v, want nil", appConfig)
		}
	})

	t.Run("duplicate user id is an error", func(t *testing.T) {
		setupTestEnv(t)
		yaml := "users:\n  - id: a\n    name: A\n  - id: a\n    name: A2\n"
		path := filepath.Join(t.TempDir(), "c.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "duplicate user id") {
			t.Errorf("err = %v, want duplicate user id", err)
		}
	})

	t.Run("nested folder is rejected", func(t *testing.T) {
		setupTestEnv(t)
		yaml := "users:\n  - id: a\n    folders: [a/b]\n"
		path := filepath.Join(t.TempDir(), "c.yaml")
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "invalid folder") {
			t.Errorf("err = %v, want invalid folder", err)
		}
	})

	t.Run("name defaults to id when missing", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, "users:\n  - id: alice\n")
		if usersByID["alice"].Name != "alice" {
			t.Errorf("name = %q, want %q (fallback to id)", usersByID["alice"].Name, "alice")
		}
	})
}

func TestUserCanAccessPath(t *testing.T) {
	setupTestEnv(t)
	setupUsers(t, testUsersYAML)

	cases := []struct {
		user string
		path string
		want bool
	}{
		{"alice", "vacation/a.jpg", true},
		{"alice", "vacation/2024/a.jpg", true},
		{"alice", "work/a.jpg", false},
		{"alice", "garden/a.jpg", true}, // unassigned = public
		{"alice", "root.jpg", true},
		{"bob", "work/a.jpg", true},
		{"bob", "vacation/a.jpg", false},
		{"bob", "garden/a.jpg", true},
	}
	for _, c := range cases {
		u := usersByID[c.user]
		if got := userCanAccessPath(u, c.path); got != c.want {
			t.Errorf("userCanAccessPath(%s, %q) = %v, want %v", c.user, c.path, got, c.want)
		}
	}
}

func TestAuthMiddleware(t *testing.T) {
	pass := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("no config: passes through with any/no X-User", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequestAs(pass, http.MethodGet, "/api/images", "", nil, "")
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("with config: missing X-User returns 401", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, testUsersYAML)
		rr := doRequestAs(pass, http.MethodGet, "/api/images", "", nil, "")
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rr.Code)
		}
	})

	t.Run("with config: unknown X-User returns 401", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, testUsersYAML)
		rr := doRequestAs(pass, http.MethodGet, "/api/images", "carol", nil, "")
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rr.Code)
		}
	})

	t.Run("with config: /api/config is exempt", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, testUsersYAML)
		rr := doRequestAs(handleConfig, http.MethodGet, "/api/config", "", nil, "")
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rr.Code)
		}
	})

	t.Run("with config: /api/users is exempt", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, testUsersYAML)
		rr := doRequestAs(handleUsers, http.MethodGet, "/api/users", "", nil, "")
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rr.Code)
		}
	})
}

func TestHandleUsers(t *testing.T) {
	t.Run("returns empty list when no config", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleUsers, http.MethodGet, "/api/users", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		var resp struct {
			Users []struct{ ID, Name string } `json:"users"`
		}
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if len(resp.Users) != 0 {
			t.Errorf("users = %+v, want empty", resp.Users)
		}
	})

	t.Run("returns id+name for each configured user", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, testUsersYAML)
		rr := doRequestAs(handleUsers, http.MethodGet, "/api/users", "", nil, "")
		var resp struct {
			Users []struct{ ID, Name string } `json:"users"`
		}
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		if len(resp.Users) != 2 || resp.Users[0].ID != "alice" || resp.Users[1].ID != "bob" {
			t.Errorf("users = %+v", resp.Users)
		}
	})
}

func TestHandleConfigUsersEnabled(t *testing.T) {
	t.Run("false when no config", func(t *testing.T) {
		setupTestEnv(t)
		rr := doRequest(handleConfig, http.MethodGet, "/api/config", nil, "")
		var m map[string]any
		json.NewDecoder(rr.Body).Decode(&m) //nolint:errcheck
		if m["usersEnabled"] != false {
			t.Errorf("usersEnabled = %v, want false", m["usersEnabled"])
		}
	})

	t.Run("true when config loaded", func(t *testing.T) {
		setupTestEnv(t)
		setupUsers(t, testUsersYAML)
		rr := doRequestAs(handleConfig, http.MethodGet, "/api/config", "", nil, "")
		var m map[string]any
		json.NewDecoder(rr.Body).Decode(&m) //nolint:errcheck
		if m["usersEnabled"] != true {
			t.Errorf("usersEnabled = %v, want true", m["usersEnabled"])
		}
	})
}

const testUsersScopeYAML = `
users:
  - id: alice
    name: Alice
    folders: [vacation, work]
  - id: bob
    name: Bob
    folders: [work]
  - id: guest
    name: Guest
`

func TestHandleFoldersUserScoped(t *testing.T) {
	setupTestEnv(t)
	setupUsers(t, testUsersScopeYAML)
	now := time.Now().UnixNano()
	for _, r := range []struct{ path, folder string }{
		{"vacation/a.jpg", "vacation"},
		{"vacation/hawaii/b.jpg", "vacation/hawaii"},
		{"work/c.jpg", "work"},
		{"work/2026/d.jpg", "work/2026"},
		{"garden/e.jpg", "garden"},
	} {
		db.Exec(`INSERT INTO files (path,filename,folder,file_type,width,height,size,file_mtime,date_taken,indexed_at) VALUES (?,?,?,'image',0,0,0,?,?,?)`, //nolint:errcheck
			r.path, filepath.Base(r.path), r.folder, now, now, now)
	}

	check := func(who string, want map[string]apiFolderEntry) {
		t.Helper()
		rr := doRequestAs(handleFolders, http.MethodGet, "/api/folders", who, nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", who, rr.Code)
		}
		entries := decodeFolders(t, rr)
		if len(entries) != len(want) {
			t.Errorf("%s: got %d entries (%v), want %d (%v)", who, len(entries), entries, len(want), want)
		}
		for _, got := range entries {
			exp, ok := want[got.Path]
			if !ok {
				t.Errorf("%s: unexpected folder %q in response", who, got.Path)
				continue
			}
			if got.Scope != exp.Scope {
				t.Errorf("%s: folder %q scope = %q, want %q", who, got.Path, got.Scope, exp.Scope)
			}
			if !equalStrings(got.SharedWith, exp.SharedWith) {
				t.Errorf("%s: folder %q sharedWith = %v, want %v", who, got.Path, got.SharedWith, exp.SharedWith)
			}
		}
	}

	check("alice", map[string]apiFolderEntry{
		"garden":           {Scope: "public"},
		"vacation":         {Scope: "private"},
		"vacation/hawaii":  {Scope: "private"},
		"work":             {Scope: "shared", SharedWith: []string{"bob"}},
		"work/2026":        {Scope: "shared", SharedWith: []string{"bob"}},
	})
	check("bob", map[string]apiFolderEntry{
		"garden":    {Scope: "public"},
		"work":      {Scope: "shared", SharedWith: []string{"alice"}},
		"work/2026": {Scope: "shared", SharedWith: []string{"alice"}},
	})
	check("guest", map[string]apiFolderEntry{
		"garden": {Scope: "public"},
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestClassifyTopFolder(t *testing.T) {
	setupTestEnv(t)
	setupUsers(t, testUsersScopeYAML)
	alice := usersByID["alice"]
	bob := usersByID["bob"]

	tests := []struct {
		name       string
		top        string
		user       *ConfigUser
		wantScope  string
		wantShared []string
	}{
		{"alice/vacation", "vacation", alice, "private", nil},
		{"alice/work", "work", alice, "shared", []string{"bob"}},
		{"alice/garden", "garden", alice, "public", nil},
		{"bob/work", "work", bob, "shared", []string{"alice"}},
		{"bob/vacation (denied)", "vacation", bob, "public", nil}, // bob lacks access — helper degrades gracefully
		{"nil user / unassigned", "garden", nil, "public", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotScope, gotShared := classifyTopFolder(tc.top, tc.user)
			if gotScope != tc.wantScope {
				t.Errorf("scope = %q, want %q", gotScope, tc.wantScope)
			}
			if !equalStrings(gotShared, tc.wantShared) {
				t.Errorf("sharedWith = %v, want %v", gotShared, tc.wantShared)
			}
		})
	}

	t.Run("feature disabled returns public", func(t *testing.T) {
		setupTestEnv(t) // re-init: appConfig stays nil
		scope, shared := classifyTopFolder("vacation", nil)
		if scope != "public" || shared != nil {
			t.Errorf("got (%q, %v), want (public, nil)", scope, shared)
		}
	})
}

func TestHandleImagesUserScoped(t *testing.T) {
	setupTestEnv(t)
	setupUsers(t, testUsersYAML)
	for _, r := range []struct{ folder, name string }{
		{"vacation", "a.jpg"},
		{"vacation/hawaii", "b.jpg"},
		{"work", "c.jpg"},
		{"garden", "d.jpg"},
		{"", "root.jpg"},
	} {
		seedFolderFile(t, r.folder, r.name)
	}

	t.Run("alice listing all", func(t *testing.T) {
		rr := doRequestAs(handleImages, http.MethodGet, "/api/images", "alice", nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		var resp ListResponse
		json.NewDecoder(rr.Body).Decode(&resp) //nolint:errcheck
		// expects: vacation/a, vacation/hawaii/b, garden/d, root.jpg → 4
		if resp.Total != 4 {
			t.Errorf("total = %d, want 4", resp.Total)
		}
	})

	t.Run("alice requesting work folder gets 403", func(t *testing.T) {
		rr := doRequestAs(handleImages, http.MethodGet, "/api/images?folder=work", "alice", nil, "")
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rr.Code)
		}
	})

	t.Run("bob requesting work folder ok", func(t *testing.T) {
		rr := doRequestAs(handleImages, http.MethodGet, "/api/images?folder=work", "bob", nil, "")
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rr.Code)
		}
	})
}

func TestHandleDeleteUserScoped(t *testing.T) {
	setupTestEnv(t)
	setupUsers(t, testUsersYAML)
	seedFolderFile(t, "vacation", "a.jpg")
	seedFolderFile(t, "work", "b.jpg")

	body, _ := json.Marshal(map[string]any{"paths": []string{"vacation/a.jpg", "work/b.jpg"}})
	rr := doRequestAs(handleDelete, http.MethodPost, "/api/delete", "alice", bytes.NewReader(body), "application/json")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	// Neither file should be deleted after the rejected batch.
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n) //nolint:errcheck
	if n != 2 {
		t.Errorf("files remaining = %d, want 2", n)
	}
}

func TestHandleCropUserScoped(t *testing.T) {
	setupTestEnv(t)
	setupUsers(t, testUsersYAML)
	seedFolderFile(t, "work", "photo.jpg")

	body, _ := json.Marshal(map[string]int{"x": 0, "y": 0, "width": 5, "height": 5})
	rr := doRequestAs(handleCrop, http.MethodPost, "/api/crop/work/photo.jpg", "alice", bytes.NewReader(body), "application/json")
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}

	rr2 := doRequestAs(handleCrop, http.MethodPost, "/api/crop/work/photo.jpg", "bob", bytes.NewReader(body), "application/json")
	if rr2.Code != http.StatusOK {
		t.Errorf("bob status = %d, want 200; body: %s", rr2.Code, rr2.Body.String())
	}
}

func TestHandleUploadUserScoped(t *testing.T) {
	setupTestEnv(t)
	setupUsers(t, testUsersYAML)

	body, ct := multipartFileWithFolder(t, "p.jpg", makeTestJPEG(10, 10), "work")
	rr := doRequestAs(handleUpload, http.MethodPost, "/api/upload", "alice", body, ct)
	if rr.Code != http.StatusForbidden {
		t.Errorf("alice status = %d, want 403", rr.Code)
	}

	body2, ct2 := multipartFileWithFolder(t, "p.jpg", makeTestJPEG(10, 10), "work")
	rr2 := doRequestAs(handleUpload, http.MethodPost, "/api/upload", "bob", body2, ct2)
	if rr2.Code != http.StatusOK {
		t.Errorf("bob status = %d, want 200; body: %s", rr2.Code, rr2.Body.String())
	}
}
