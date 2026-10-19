package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_Missing(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Archived == nil || s.LastViewed == nil || s.Collapsed == nil || s.ArchivedProjects == nil || s.ExpandedOlder == nil || s.ArchivedOpen == nil {
		t.Fatalf("maps must be non-nil: %+v", s)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "atree") // Save creates the dir
	s, _ := Load(dir)
	now := time.UnixMilli(1792414047032)
	s.Archived["a"] = true
	s.LastViewed["b"] = now
	s.Collapsed["/Users/didi/project/zone"] = true
	s.ArchivedGroupOpen = true
	s.ArchivedProjects["/p"] = now
	s.ExpandedOlder["/p"] = true
	s.ArchivedOpen["/q"] = true
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Archived["a"] || !got.LastViewed["b"].Equal(now) || !got.Collapsed["/Users/didi/project/zone"] || !got.ArchivedGroupOpen ||
		!got.ArchivedProjects["/p"].Equal(now) || !got.ExpandedOlder["/p"] || !got.ArchivedOpen["/q"] {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestLoad_Corrupt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("{garbage"), 0o644)
	s, err := Load(dir)
	if err != nil || s.Archived == nil {
		t.Fatalf("corrupt file should give empty state, got %+v, %v", s, err)
	}
}

func TestSave_Atomic(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("unexpected files: %v", entries)
	}
}

func TestMigrateLegacy_CopiesOldState(t *testing.T) {
	old, dst := t.TempDir(), filepath.Join(t.TempDir(), "atree")
	os.WriteFile(filepath.Join(old, "state.json"), []byte(`{"archived":{"s1":true}}`), 0o644)
	if err := MigrateLegacy(old, dst); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(dst)
	if !s.Archived["s1"] {
		t.Fatal("old state not migrated")
	}
}

func TestMigrateLegacy_KeepsExistingState(t *testing.T) {
	old, dst := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(old, "state.json"), []byte(`{"archived":{"old":true}}`), 0o644)
	os.WriteFile(filepath.Join(dst, "state.json"), []byte(`{"archived":{"new":true}}`), 0o644)
	MigrateLegacy(old, dst)
	s, _ := Load(dst)
	if s.Archived["old"] || !s.Archived["new"] {
		t.Fatalf("existing state overwritten: %v", s.Archived)
	}
}

func TestMigrateLegacy_NoOldState(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "atree")
	if err := MigrateLegacy(filepath.Join(t.TempDir(), "missing"), dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "state.json")); !os.IsNotExist(err) {
		t.Fatal("nothing to migrate: no file should be created")
	}
}
