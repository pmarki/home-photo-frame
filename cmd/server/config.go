package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the YAML config file shape. New top-level sections may be added
// later (display, slideshow, etc.) without breaking existing files.
type Config struct {
	Users []ConfigUser `yaml:"users"`
}

// ConfigUser is one entry under `users:` in the YAML config.
type ConfigUser struct {
	ID      string   `yaml:"id"`
	Name    string   `yaml:"name"`
	Folders []string `yaml:"folders"`
}

var (
	appConfig          *Config
	usersByID          map[string]*ConfigUser
	assignedTopFolders map[string]struct{}
)

// loadConfig parses the YAML file at path and installs the result in the
// package-level globals. A missing file returns (nil, nil) so the feature
// stays disabled; parse errors and duplicate user ids are fatal.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(cfg.Users) == 0 {
		// No users defined (missing or empty users: key) — feature disabled,
		// same as if the file were absent.
		return nil, nil
	}

	byID := make(map[string]*ConfigUser, len(cfg.Users))
	assigned := make(map[string]struct{})
	for i := range cfg.Users {
		u := &cfg.Users[i]
		if u.ID == "" {
			return nil, fmt.Errorf("config: user at index %d has empty id", i)
		}
		if _, dup := byID[u.ID]; dup {
			return nil, fmt.Errorf("config: duplicate user id %q", u.ID)
		}
		if u.Name == "" {
			u.Name = u.ID
		}
		byID[u.ID] = u
		for _, f := range u.Folders {
			if f == "" || strings.ContainsAny(f, "/\\") {
				return nil, fmt.Errorf("config: user %q has invalid folder %q (top-level names only, no separators)", u.ID, f)
			}
			assigned[f] = struct{}{}
		}
	}

	usersByID = byID
	assignedTopFolders = assigned
	appConfig = &cfg
	return &cfg, nil
}

// deniedTopFoldersFor returns the top folders u cannot see: folders that are
// assigned to someone (in assignedTopFolders) but not to u. Returns nil when
// the users feature is disabled or u is nil.
func deniedTopFoldersFor(u *ConfigUser) []string {
	if appConfig == nil || u == nil {
		return nil
	}
	allowed := make(map[string]struct{}, len(u.Folders))
	for _, f := range u.Folders {
		allowed[f] = struct{}{}
	}
	denied := make([]string, 0)
	for f := range assignedTopFolders {
		if _, ok := allowed[f]; !ok {
			denied = append(denied, f)
		}
	}
	return denied
}

// userCanAccessPath reports whether u may read or write the file at imgPath.
// imgPath is the forward-slash relative path used throughout the app. When the
// users feature is disabled (appConfig == nil) or u is nil, returns true.
func userCanAccessPath(u *ConfigUser, imgPath string) bool {
	if appConfig == nil || u == nil {
		return true
	}
	top := imgPath
	if i := strings.IndexByte(imgPath, '/'); i >= 0 {
		top = imgPath[:i]
	}
	if _, assigned := assignedTopFolders[top]; !assigned {
		return true // unassigned top folder is public
	}
	for _, f := range u.Folders {
		if f == top {
			return true
		}
	}
	return false
}

// userCanAccessFolder is like userCanAccessPath but for a folder reference
// (e.g. the `folder` form field on upload). Empty folder = root, always allowed.
func userCanAccessFolder(u *ConfigUser, folder string) bool {
	if folder == "" {
		return true
	}
	return userCanAccessPath(u, folder+"/")
}
