package claudedata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const zoneDir = "-Users-didi-project-zone"

func writeHist(t *testing.T, root, proj, name string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, "projects", proj)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const cwdLine = `{"type":"user","cwd":"/Users/didi/project/zone","message":{"role":"user","content":"hi"}}`

func scanOne(t *testing.T, root string) []History {
	t.Helper()
	got, err := NewHistoryScanner(root).Scan()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestScan_TitlePrefersCustomTitle(t *testing.T) {
	root := t.TempDir()
	writeHist(t, root, zoneDir, "e858.jsonl",
		`{"type":"queue-operation","operation":"enqueue"}`,
		cwdLine,
		`{"type":"last-prompt","lastPrompt":"你确定我现在的桌面版就是最新的吗"}`,
		`{"type":"custom-title","customTitle":"旧"}`,
		`{"type":"custom-title","customTitle":"你好"}`,
		`{"type":"mode","mode":"normal"}`)
	got := scanOne(t, root)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	h := got[0]
	if h.SessionID != "e858" || h.Title != "你好" || h.Cwd != "/Users/didi/project/zone" {
		t.Errorf("got %+v", h)
	}
	if h.LastActive.IsZero() {
		t.Error("LastActive not set")
	}
}

func TestScan_TitleFallsBackToLastPrompt(t *testing.T) {
	root := t.TempDir()
	writeHist(t, root, zoneDir, "a.jsonl", cwdLine,
		`{"type":"last-prompt","lastPrompt":"first"}`,
		`{"type":"last-prompt","lastPrompt":"  看一下\n  设计文档  "}`)
	if got := scanOne(t, root)[0].Title; got != "看一下 设计文档" {
		t.Errorf("Title = %q", got)
	}
}

func TestScan_UntitledFallback(t *testing.T) {
	root := t.TempDir()
	writeHist(t, root, zoneDir, "a.jsonl", cwdLine)
	if got := scanOne(t, root)[0].Title; got != "(无标题)" {
		t.Errorf("Title = %q", got)
	}
}

func TestScan_TruncatesLongTitle(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("蜂", 60)
	writeHist(t, root, zoneDir, "a.jsonl", cwdLine, `{"type":"custom-title","customTitle":"`+long+`"}`)
	want := strings.Repeat("蜂", 40) + "…"
	if got := scanOne(t, root)[0].Title; got != want {
		t.Errorf("Title = %q", got)
	}
}

func TestScan_SkipsNonSessionFiles(t *testing.T) {
	root := t.TempDir()
	writeHist(t, root, zoneDir, "ok.jsonl", cwdLine)
	writeHist(t, root, zoneDir, "x.desktop-released.json", `{"a":1}`)
	writeHist(t, root, zoneDir, "memory/a.md", "notes")
	writeHist(t, root, zoneDir, "ok/sub.jsonl", cwdLine)
	writeHist(t, root, zoneDir, "nocwd.jsonl", `{"type":"mode","mode":"normal"}`)
	got := scanOne(t, root)
	if len(got) != 1 || got[0].SessionID != "ok" {
		t.Fatalf("got %+v", got)
	}
}

func TestScan_TolerantOfBadLines(t *testing.T) {
	root := t.TempDir()
	big := `{"type":"assistant","text":"` + strings.Repeat("x", 200*1024) + `"}`
	writeHist(t, root, zoneDir, "a.jsonl",
		big, "not json at all", cwdLine,
		`{"type":"custom-title","customTitle":"好"}`,
		`{"type":"custom-title","customTitle":"截断`)
	got := scanOne(t, root)
	if len(got) != 1 || got[0].Title != "好" || got[0].Cwd != "/Users/didi/project/zone" {
		t.Fatalf("got %+v", got)
	}
}

func TestScan_UsesCache(t *testing.T) {
	root := t.TempDir()
	p := writeHist(t, root, zoneDir, "a.jsonl", cwdLine, `{"type":"custom-title","customTitle":"AA"}`)
	mtime := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(p, mtime, mtime)
	s := NewHistoryScanner(root)
	if got, _ := s.Scan(); got[0].Title != "AA" {
		t.Fatalf("first scan %+v", got)
	}
	// Same size and mtime: cached result is used.
	os.WriteFile(p, []byte(cwdLine+"\n"+`{"type":"custom-title","customTitle":"BB"}`), 0o644)
	os.Chtimes(p, mtime, mtime)
	if got, _ := s.Scan(); got[0].Title != "AA" {
		t.Fatalf("expected cached title, got %+v", got)
	}
	later := mtime.Add(time.Minute)
	os.Chtimes(p, later, later)
	if got, _ := s.Scan(); got[0].Title != "BB" {
		t.Fatalf("expected rescan, got %+v", got)
	}
}
