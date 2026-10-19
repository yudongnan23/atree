// Package state holds atree's own persistent state. It never touches Claude's files.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Archived          map[string]bool      `json:"archived"`   // sessionID
	LastViewed        map[string]time.Time `json:"lastViewed"` // sessionID
	Collapsed         map[string]bool      `json:"collapsed"`  // project cwd
	ArchivedGroupOpen bool                 `json:"archivedGroupOpen"`
	ArchivedProjects  map[string]time.Time `json:"archivedProjects"` // project cwd → when archived by hand
	ExpandedOlder     map[string]bool      `json:"expandedOlder"`    // project cwd → "更早的会话" fold open
	ArchivedOpen      map[string]bool      `json:"archivedOpen"`     // project cwd → expanded inside 已归档
}

const fileName = "state.json"

// Dir is atree's config directory: $ATREE_CONFIG_DIR or ~/.config/atree.
func Dir() string {
	if d := os.Getenv("ATREE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "atree")
}

// LegacyDir is where ctree (atree's former name) kept its state.
func LegacyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ctree")
}

// MigrateLegacy copies state.json from oldDir to newDir when newDir has none,
// so archives and folds survive the rename. An existing new state wins.
func MigrateLegacy(oldDir, newDir string) error {
	if _, err := os.Stat(filepath.Join(newDir, fileName)); err == nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(oldDir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	s := &State{}
	if json.Unmarshal(data, s) != nil {
		return nil // unreadable old state: start fresh
	}
	s.fill()
	return s.Save(newDir)
}

// New returns an empty State with all maps allocated.
func New() *State {
	s := &State{}
	s.fill()
	return s
}

// Load reads the state file. A missing or corrupt file yields an empty State,
// so a bad file never blocks the sidebar.
func Load(dir string) (*State, error) {
	s := &State{}
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && json.Unmarshal(data, s) != nil {
		s = &State{}
	}
	s.fill()
	return s, nil
}

func (s *State) fill() {
	if s.Archived == nil {
		s.Archived = map[string]bool{}
	}
	if s.LastViewed == nil {
		s.LastViewed = map[string]time.Time{}
	}
	if s.Collapsed == nil {
		s.Collapsed = map[string]bool{}
	}
	if s.ArchivedProjects == nil {
		s.ArchivedProjects = map[string]time.Time{}
	}
	if s.ExpandedOlder == nil {
		s.ExpandedOlder = map[string]bool{}
	}
	if s.ArchivedOpen == nil {
		s.ArchivedOpen = map[string]bool{}
	}
}

// Save writes the state atomically (temp file + rename).
func (s *State) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, fileName+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, fileName))
}
