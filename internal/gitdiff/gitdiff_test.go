package gitdiff

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo: committed a.go b.go c.go e.go ignored.log(.gitignore'd), on branch main.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, ".gitignore", "*.log\n")
	for _, f := range []string{"a.go", "b.go", "c.go", "e.go"} {
		write(t, dir, f, "package x\n// old\n")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func statusOf(r *Repo) map[string]Status {
	m := map[string]Status{}
	for _, f := range r.Files {
		m[f.Path] = f.Status
	}
	return m
}

func TestLoad_NotRepo(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoad_Statuses(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "package x\n// new\n")
	os.Remove(filepath.Join(dir, "b.go"))
	write(t, dir, "new dir/新.go", "package y\n")
	write(t, dir, "debug.log", "ignored")
	git(t, dir, "mv", "c.go", "d.go")

	r, err := Load(filepath.Join(dir, "new dir")) // from a subdirectory
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{".gitignore": Unchanged, "a.go": Modified, "b.go": Deleted, "c.go": Deleted,
		"d.go": Added, "e.go": Unchanged, "new dir/新.go": Added}
	got := statusOf(r)
	if len(got) != len(want) {
		t.Fatalf("files %v", got)
	}
	for p, s := range want {
		if got[p] != s {
			t.Errorf("%s: %q, want %q", p, got[p], s)
		}
	}
	if mod, add, del := r.Stats(); mod != 1 || add != 2 || del != 2 {
		t.Errorf("stats %d %d %d", mod, add, del)
	}
	if r.Branch != "main" || r.Name != filepath.Base(dir) || !r.HasHead {
		t.Errorf("repo %+v", r)
	}
	if real, _ := filepath.EvalSymlinks(dir); r.Root != real && r.Root != dir {
		t.Errorf("root %q", r.Root)
	}
}

func TestLoad_NoCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, "a.go", "line1\nline2\n")
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.HasHead || statusOf(r)["a.go"] != Added {
		t.Fatalf("repo %+v", r)
	}
	d, err := r.Diff(File{Path: "a.go", Status: Added})
	if err != nil || d != "+line1\n+line2" {
		t.Fatalf("diff %q %v", d, err)
	}
}

func TestLoad_DetachedBranch(t *testing.T) {
	dir := newRepo(t)
	h := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-q", h)
	r, err := Load(dir)
	if err != nil || r.Branch != h[:7] {
		t.Fatalf("branch %q %v", r.Branch, err)
	}
}

func TestDiff_Modified(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.go", "package x\n// new\n")
	r, _ := Load(dir)
	d, err := r.Diff(File{Path: "a.go", Status: Modified})
	if err != nil || !strings.Contains(d, "-// old") || !strings.Contains(d, "+// new") {
		t.Fatalf("diff %q %v", d, err)
	}
}

func TestDiff_Deleted(t *testing.T) {
	dir := newRepo(t)
	os.Remove(filepath.Join(dir, "b.go"))
	r, _ := Load(dir)
	d, _ := r.Diff(File{Path: "b.go", Status: Deleted})
	if !strings.Contains(d, "-// old") {
		t.Fatalf("diff %q", d)
	}
}

func TestDiff_Untracked(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "n.go", "a\nb\n")
	r, _ := Load(dir)
	d, _ := r.Diff(File{Path: "n.go", Status: Added})
	for _, l := range strings.Split(d, "\n") {
		if !strings.HasPrefix(l, "+") {
			t.Fatalf("line %q in %q", l, d)
		}
	}
}

func TestDiff_Unchanged(t *testing.T) {
	dir := newRepo(t)
	r, _ := Load(dir)
	d, _ := r.Diff(File{Path: "e.go"})
	if d != "package x\n// old\n" {
		t.Fatalf("content %q", d)
	}
}

func TestDiff_Binary(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "img.bin", "\x00\x01\x02")
	git(t, dir, "add", "img.bin")
	git(t, dir, "commit", "-q", "-m", "bin")
	write(t, dir, "img.bin", "\x00\x03")
	write(t, dir, "new.bin", "\x00\x04")
	r, _ := Load(dir)
	for _, f := range []File{{"img.bin", Modified}, {"new.bin", Added}} {
		if d, _ := r.Diff(f); d != "二进制文件" {
			t.Errorf("%s: %q", f.Path, d)
		}
	}
}

func TestBuildTree_OrderAndCounts(t *testing.T) {
	root := BuildTree([]File{
		{"z.go", Unchanged}, {"cmd/main.go", Modified}, {"a.go", Added},
		{"internal/x/b.go", Deleted}, {"internal/x/c.go", Unchanged}, {"internal/y.go", Unchanged},
	})
	var names []string
	for _, c := range root.Children {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "cmd,internal,a.go,z.go" {
		t.Fatalf("order %v", names)
	}
	if root.Changes != 3 || root.Children[1].Changes != 1 || root.Children[1].Children[0].Name != "x" {
		t.Fatalf("counts root=%d internal=%d", root.Changes, root.Children[1].Changes)
	}
	if c := root.Children[0].Children[0]; c.Path != "cmd/main.go" || c.Status != Modified || c.Dir {
		t.Fatalf("leaf %+v", c)
	}
}

func TestLoad_LargeRepoFast(t *testing.T) {
	if testing.Short() {
		t.Skip("slow setup")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	for i := 0; i < 50000; i++ {
		write(t, dir, fmt.Sprintf("d%03d/f%05d.txt", i%500, i), "x\n")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "big")
	write(t, dir, "d000/f00000.txt", "y\n")

	start := time.Now()
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	root := BuildTree(r.Files)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Load+BuildTree took %v", d)
	}
	if len(r.Files) != 50000 || root.Changes != 1 {
		t.Fatalf("files %d changes %d", len(r.Files), root.Changes)
	}
}

// Claude may be running git add/commit in the same repo; the popup must not
// take .git/index.lock or rewrite the index.
func TestLoad_DoesNotWriteIndex(t *testing.T) {
	dir := newRepo(t)
	index := filepath.Join(dir, ".git", "index")
	old := time.Now().Add(-time.Hour)
	os.Chtimes(index, old, old)
	later := time.Now().Add(time.Minute) // stat-dirty but content-identical file
	os.Chtimes(filepath.Join(dir, "a.go"), later, later)
	before, _ := os.Stat(index)
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(index)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("index rewritten: %v → %v", before.ModTime(), after.ModTime())
	}
}

func TestDiff_LargeFileTruncated(t *testing.T) {
	dir := newRepo(t)
	big := strings.Repeat("0123456789abcdef\n", 1<<17) // 2 MiB, 131072 lines
	write(t, dir, "big.geojson", big)
	git(t, dir, "add", "big.geojson")
	git(t, dir, "commit", "-q", "-m", "big")
	write(t, dir, "new.geojson", big)
	r, _ := Load(dir)
	for _, f := range []File{{"big.geojson", Unchanged}, {"new.geojson", Added}} {
		d, err := r.Diff(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(d) > maxRead*11/10 || !strings.HasSuffix(d, truncatedNote) { // added files gain a "+" per line
			t.Errorf("%s: %d bytes, suffix %q", f.Path, len(d), d[len(d)-40:])
		}
	}
}

func TestDiff_SpecialFiles(t *testing.T) {
	dir := newRepo(t)
	os.Symlink("/etc/passwd", filepath.Join(dir, "link"))
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	r, _ := Load(dir)
	if d, _ := r.Diff(File{"link", Added}); d != "符号链接 → /etc/passwd" {
		t.Errorf("symlink: %q", d)
	}
	if d, _ := r.Diff(File{"sub", Unchanged}); d != "子模块或目录" {
		t.Errorf("dir: %q", d)
	}
}
