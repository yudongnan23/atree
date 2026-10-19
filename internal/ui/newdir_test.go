package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func homeTree(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, d := range []string{"proj/new-thing", "proj/new-api", "proj/.hidden", "proj/zone"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	os.WriteFile(filepath.Join(home, "proj", "new-file.txt"), nil, 0o644)
	return home
}

func TestComplete_Unique(t *testing.T) {
	homeTree(t)
	got, cands := Complete("~/proj/new-thi")
	if got != "~/proj/new-thing/" || !reflect.DeepEqual(cands, []string{"new-thing/"}) {
		t.Fatalf("got %q %q", got, cands)
	}
}

func TestComplete_Ambiguous(t *testing.T) {
	homeTree(t)
	got, cands := Complete("~/proj/new-")
	if got != "~/proj/new-" || !reflect.DeepEqual(cands, []string{"new-api/", "new-thing/"}) {
		t.Fatalf("got %q %q", got, cands)
	}
	got, _ = Complete("~/proj/n")
	if got != "~/proj/new-" {
		t.Fatalf("common prefix: got %q", got)
	}
}

func TestComplete_FilesExcluded(t *testing.T) {
	homeTree(t)
	_, cands := Complete("~/proj/new-f")
	if len(cands) != 0 {
		t.Fatalf("files must not complete: %q", cands)
	}
}

func TestComplete_HiddenOnlyWhenAsked(t *testing.T) {
	homeTree(t)
	if _, c := Complete("~/proj/"); reflect.DeepEqual(c, nil) || strings.Contains(strings.Join(c, " "), ".hidden") {
		t.Fatalf("hidden dir listed: %q", c)
	}
	if got, _ := Complete("~/proj/.h"); got != "~/proj/.hidden/" {
		t.Fatalf("got %q", got)
	}
}

func TestComplete_AbsolutePath(t *testing.T) {
	home := homeTree(t)
	if got, _ := Complete(home + "/proj/zo"); got != home+"/proj/zone/" {
		t.Fatalf("got %q", got)
	}
}

func TestModal_EnterNonexistentShowsError(t *testing.T) {
	homeTree(t)
	f, m := setup(historyOnly())
	key(m, "N")
	if !strings.Contains(m.View(), "在新目录开会话") {
		t.Fatalf("modal not shown:\n%s", m.View())
	}
	key(m, "~/proj/nope", "enter")
	if len(f.created) != 0 || !strings.Contains(m.View(), "目录不存在") {
		t.Fatalf("created=%q view:\n%s", f.created, m.View())
	}
}

func TestModal_TabThenEnterCallsNew(t *testing.T) {
	home := homeTree(t)
	f, m := setup(historyOnly())
	key(m, "N", "~/proj/new-thi", "tab", "enter")
	if len(f.created) != 1 || f.created[0] != filepath.Join(home, "proj", "new-thing") {
		t.Fatalf("created %q", f.created)
	}
	if strings.Contains(m.View(), "在新目录开会话") {
		t.Error("modal should close")
	}
}

func TestModal_EscCancels(t *testing.T) {
	homeTree(t)
	f, m := setup(historyOnly())
	key(m, "N", "~/proj", "esc")
	if strings.Contains(m.View(), "在新目录开会话") || len(f.created) != 0 || f.focusSlot != 0 {
		t.Fatalf("esc should only close the modal")
	}
	key(m, "n") // keys go back to the tree
	if len(f.created) != 1 {
		t.Fatal("tree keys not restored")
	}
}
