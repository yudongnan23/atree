package diffui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(body), 0o644)
}

// repo: committed a.go e.go docs/readme.md sub/x.go
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	for _, f := range []string{"a.go", "e.go", "docs/readme.md", "sub/x.go"} {
		write(t, dir, f, "old\n")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func open(t *testing.T, dir string) *Model {
	t.Helper()
	m := New(dir).(*Model)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return m
}

func press(m *Model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd = m.Update(msg)
	}
	return cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestPopup_HeaderStats(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.go", "new\n")
	write(t, dir, "n.go", "x\n")
	v := open(t, dir).View()
	if !strings.Contains(v, filepath.Base(dir)+" · main · 1 个文件修改，1 个新增") || strings.Contains(v, "删除") {
		t.Errorf("header:\n%s", strings.SplitN(v, "\n", 2)[0])
	}
}

func TestPopup_NoChanges(t *testing.T) {
	if v := open(t, repo(t)).View(); !strings.Contains(v, "没有未提交的改动") {
		t.Errorf("view:\n%s", v)
	}
}

func TestPopup_SelectsFirstChange(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.go", "new\n")
	write(t, dir, "sub/x.go", "new\n")
	m := open(t, dir)
	if got := m.selectedPath(); got != "sub/x.go" {
		t.Fatalf("selected %q", got)
	}
	if !strings.Contains(m.View(), "+new") {
		t.Error("diff of the selected file not shown")
	}
}

func TestPopup_ChangedDirsExpanded(t *testing.T) {
	dir := repo(t)
	write(t, dir, "sub/x.go", "new\n")
	v := open(t, dir).View()
	for _, want := range []string{"▸ docs", "▾ sub  1", "M x.go"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q in\n%s", want, v)
		}
	}
	if strings.Contains(v, "readme.md") {
		t.Error("unchanged dir should be folded")
	}
}

func TestPopup_ToggleChangedOnly(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.go", "new\n")
	m := open(t, dir)
	if !strings.Contains(m.View(), "e.go") {
		t.Fatal("full tree should list e.go")
	}
	press(m, "c")
	if v := m.View(); strings.Contains(v, "e.go") || strings.Contains(v, "docs") || !strings.Contains(v, "a.go") {
		t.Errorf("changed-only view:\n%s", v)
	}
	press(m, "c")
	if !strings.Contains(m.View(), "e.go") {
		t.Error("c should toggle back")
	}
}

func TestPopup_TabScrollsDiff(t *testing.T) {
	dir := repo(t)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	write(t, dir, "big.go", b.String())
	m := open(t, dir)
	if m.selectedPath() != "big.go" {
		t.Fatalf("selected %q", m.selectedPath())
	}
	press(m, "j") // tree focus: moves selection, diff offset stays 0
	if m.diffOffset != 0 {
		t.Fatal("j in tree must not scroll the diff")
	}
	press(m, "k", "tab", "j", "j")
	if m.diffOffset != 2 || m.selectedPath() != "big.go" {
		t.Fatalf("offset %d selected %q", m.diffOffset, m.selectedPath())
	}
	press(m, "G")
	if m.diffOffset == 2 {
		t.Error("G should jump to the end")
	}
}

func TestPopup_NotRepo(t *testing.T) {
	m := open(t, t.TempDir())
	if !strings.Contains(m.View(), "这个目录不是 git 仓库") {
		t.Fatalf("view:\n%s", m.View())
	}
	if !isQuit(press(m, "x")) {
		t.Error("any key should close")
	}
}

func TestPopup_QuitKeys(t *testing.T) {
	dir := repo(t)
	for _, k := range []string{"q", "esc"} {
		if !isQuit(press(open(t, dir), k)) {
			t.Errorf("%s should quit", k)
		}
	}
}

func TestPopup_LinesFitWidth(t *testing.T) {
	dir := repo(t)
	write(t, dir, "sub/x.go", strings.Repeat("很长的一行", 60)+"\n")
	m := open(t, dir)
	for _, l := range strings.Split(m.View(), "\n") {
		if w := lipglossWidth(l); w > 120 {
			t.Fatalf("line width %d: %q", w, l)
		}
	}
	if n := len(strings.Split(m.View(), "\n")); n > 30 {
		t.Fatalf("%d lines for height 30", n)
	}
}

func lipglossWidth(s string) int { return lipgloss.Width(s) }

func TestPopup_EmptyListDoesNotPanic(t *testing.T) {
	empty := t.TempDir()
	git(t, empty, "init", "-q")
	m := open(t, empty)
	press(m, "j", "k", "down", "l")
	m.View()

	clean := open(t, repo(t))
	press(clean, "c", "j", "k")
	clean.View()
}

func TestPopup_DiffCacheBounded(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 100; i++ {
		write(t, dir, fmt.Sprintf("many/f%03d.go", i), "x\n")
	}
	m := open(t, dir)
	for i := 0; i < 110; i++ {
		press(m, "j")
	}
	if len(m.cache) > maxCached {
		t.Fatalf("cache holds %d entries", len(m.cache))
	}
}
