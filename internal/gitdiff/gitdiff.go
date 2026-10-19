// Package gitdiff reads a repository's uncommitted changes for the diff popup.
// It only ever runs read-only git commands.
package gitdiff

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Status byte

const (
	Unchanged Status = 0
	Modified  Status = 'M'
	Added     Status = 'A'
	Deleted   Status = 'D'
)

// File is a repo-relative, '/'-separated path and its change status.
type File struct {
	Path   string
	Status Status
}

type Repo struct {
	Root    string
	Name    string // basename of Root
	Branch  string // branch name, or short hash when detached
	Files   []File // tracked + untracked (not ignored) + deleted, sorted by path
	HasHead bool
}

var ErrNotRepo = errors.New("not a git repository")

const (
	binaryText    = "二进制文件"
	maxRead       = 1 << 20 // bytes of a file or diff shown in the popup
	truncatedNote = "… 文件过大，只显示前 1 MB"
)

func run(dir string, args ...string) ([]byte, error) {
	// --no-optional-locks: don't refresh .git/index, so a concurrent
	// `git add`/`commit` (e.g. by Claude) never hits index.lock.
	cmd := exec.Command("git", append([]string{"--no-optional-locks", "-c", "core.quotepath=off"}, args...)...)
	cmd.Dir = dir
	return cmd.Output()
}

// Load reads the repository containing dir.
func Load(dir string) (*Repo, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, ErrNotRepo
	}
	r := &Repo{Root: strings.TrimSpace(string(out))}
	r.Name = filepath.Base(r.Root)
	_, err = run(r.Root, "rev-parse", "--verify", "-q", "HEAD")
	r.HasHead = err == nil
	if out, err := run(r.Root, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		r.Branch = strings.TrimSpace(string(out))
	} else if out, err := run(r.Root, "rev-parse", "--short=7", "HEAD"); err == nil {
		r.Branch = strings.TrimSpace(string(out))
	}

	tracked, err := run(r.Root, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	status, err := run(r.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	files := map[string]Status{}
	for _, p := range splitZ(tracked) {
		files[p] = Unchanged
	}
	for p, s := range parseStatus(status) {
		files[p] = s
	}
	if !r.HasHead {
		for p := range files {
			if files[p] != Deleted {
				files[p] = Added
			}
		}
	}
	r.Files = make([]File, 0, len(files))
	for p, s := range files {
		r.Files = append(r.Files, File{Path: p, Status: s})
	}
	sort.Slice(r.Files, func(i, j int) bool { return r.Files[i].Path < r.Files[j].Path })
	return r, nil
}

func splitZ(b []byte) []string {
	var out []string
	for _, f := range bytes.Split(b, []byte{0}) {
		if len(f) > 0 {
			out = append(out, string(f))
		}
	}
	return out
}

// parseStatus parses `git status --porcelain=v1 -z`. Renames and copies are
// followed by a second NUL-separated field holding the original path.
func parseStatus(b []byte) map[string]Status {
	out := map[string]Status{}
	fields := splitZ(b)
	for i := 0; i < len(fields); i++ {
		e := fields[i]
		if len(e) < 4 {
			continue
		}
		x, y, path := e[0], e[1], e[3:]
		switch {
		case x == 'R' || x == 'C':
			out[path] = Added
			if i+1 < len(fields) {
				i++
				if x == 'R' {
					out[fields[i]] = Deleted
				}
			}
		case x == '?' || x == 'A':
			out[path] = Added
		case x == 'D' || y == 'D':
			out[path] = Deleted
		case strings.ContainsAny(string([]byte{x, y}), "MTU"):
			out[path] = Modified
		}
	}
	return out
}

func (r *Repo) Stats() (modified, added, deleted int) {
	for _, f := range r.Files {
		switch f.Status {
		case Modified:
			modified++
		case Added:
			added++
		case Deleted:
			deleted++
		}
	}
	return
}

// Diff returns the change for one file: a unified diff against HEAD for
// modified and deleted files, the whole file as "+" lines for added ones, and
// the plain content for unchanged ones.
func (r *Repo) Diff(f File) (string, error) {
	switch f.Status {
	case Modified, Deleted:
		out, err := run(r.Root, "diff", "--no-color", "HEAD", "--", f.Path)
		if err != nil {
			return "", err
		}
		if bytes.Contains(out, []byte("Binary files")) && !bytes.Contains(out, []byte("\n@@")) {
			return binaryText, nil
		}
		out, cut := capBytes(out)
		return withNote(strings.TrimRight(string(out), "\n"), cut), nil
	case Added:
		data, cut, special, err := readCapped(filepath.Join(r.Root, f.Path))
		if err != nil || special != "" {
			return special, err
		}
		if isBinary(data) {
			return binaryText, nil
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		for i, l := range lines {
			lines[i] = "+" + l
		}
		return withNote(strings.Join(lines, "\n"), cut), nil
	default:
		data, cut, special, err := readCapped(filepath.Join(r.Root, f.Path))
		if err != nil || special != "" {
			return special, err
		}
		if isBinary(data) {
			return binaryText, nil
		}
		return withNote(string(data), cut), nil
	}
}

// readCapped reads at most maxRead bytes of a regular file. Symlinks,
// directories (submodules) and other special files are described instead of
// read, so a link can't point the popup outside the repo or at a FIFO.
func readCapped(path string) (data []byte, cut bool, special string, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, false, "", err
	}
	switch mode := info.Mode(); {
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return nil, false, "", err
		}
		return nil, false, "符号链接 → " + target, nil
	case mode.IsDir():
		return nil, false, "子模块或目录", nil
	case !mode.IsRegular():
		return nil, false, "特殊文件", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, "", err
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, maxRead+1))
	if err != nil {
		return nil, false, "", err
	}
	data, cut = capBytes(data)
	return data, cut, "", nil
}

// capBytes cuts b to at most maxRead bytes, at a line boundary when possible.
func capBytes(b []byte) ([]byte, bool) {
	if len(b) <= maxRead {
		return b, false
	}
	b = b[:maxRead]
	if i := bytes.LastIndexByte(b, '\n'); i > 0 {
		b = b[:i]
	}
	return b, true
}

func withNote(s string, cut bool) string {
	if !cut {
		return s
	}
	return strings.TrimRight(s, "\n") + "\n" + truncatedNote
}

func isBinary(data []byte) bool {
	if len(data) > 8000 {
		data = data[:8000]
	}
	return bytes.IndexByte(data, 0) >= 0
}
