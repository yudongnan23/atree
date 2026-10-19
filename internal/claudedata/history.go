package claudedata

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// History is a session transcript under ~/.claude/projects/<dir>/<id>.jsonl.
type History struct {
	SessionID  string
	Cwd        string
	Title      string
	LastActive time.Time // file mtime
}

const (
	untitled      = "(无标题)"
	maxTitleRunes = 40
)

type cacheEntry struct {
	size  int64
	mtime time.Time
	h     History
	ok    bool // false: file is not a usable session (no cwd)
}

// HistoryScanner lists history sessions, re-reading only files whose size or
// mtime changed since the previous Scan.
type HistoryScanner struct {
	root  string
	cache map[string]cacheEntry
}

func NewHistoryScanner(root string) *HistoryScanner {
	return &HistoryScanner{root: root, cache: map[string]cacheEntry{}}
}

func (s *HistoryScanner) Scan() ([]History, error) {
	projects := filepath.Join(s.root, "projects")
	dirs, err := os.ReadDir(projects)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []History
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projects, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(projects, d.Name(), f.Name())
			info, err := f.Info()
			if err != nil {
				continue
			}
			seen[path] = true
			e, hit := s.cache[path]
			if !hit || e.size != info.Size() || !e.mtime.Equal(info.ModTime()) {
				e = cacheEntry{size: info.Size(), mtime: info.ModTime()}
				e.h, e.ok = readHistory(path)
				e.h.SessionID = strings.TrimSuffix(f.Name(), ".jsonl")
				e.h.LastActive = info.ModTime()
				s.cache[path] = e
			}
			if e.ok {
				out = append(out, e.h)
			}
		}
	}
	for p := range s.cache {
		if !seen[p] {
			delete(s.cache, p)
		}
	}
	return out, nil
}

var (
	prefixCustomTitle = []byte(`{"type":"custom-title"`)
	prefixLastPrompt  = []byte(`{"type":"last-prompt"`)
	cwdMarker         = []byte(`"cwd":"`)
)

// readHistory streams a transcript, decoding only the lines it needs. Lines
// can be many megabytes, so it reads with bufio.Reader rather than Scanner.
func readHistory(path string) (History, bool) {
	f, err := os.Open(path)
	if err != nil {
		return History{}, false
	}
	defer f.Close()
	var h History
	var customTitle, lastPrompt string
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		switch {
		case bytes.HasPrefix(line, prefixCustomTitle):
			var v struct {
				CustomTitle string `json:"customTitle"`
			}
			if json.Unmarshal(line, &v) == nil && v.CustomTitle != "" {
				customTitle = v.CustomTitle
			}
		case bytes.HasPrefix(line, prefixLastPrompt):
			var v struct {
				LastPrompt string `json:"lastPrompt"`
			}
			if json.Unmarshal(line, &v) == nil && v.LastPrompt != "" {
				lastPrompt = v.LastPrompt
			}
		case h.Cwd == "" && bytes.Contains(line, cwdMarker):
			var v struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(line, &v) == nil {
				h.Cwd = v.Cwd
			}
		}
		if err != nil {
			if err != io.EOF {
				return History{}, false
			}
			break
		}
	}
	if h.Cwd == "" {
		return History{}, false
	}
	h.Title = makeTitle(customTitle, lastPrompt)
	return h, true
}

func makeTitle(customTitle, lastPrompt string) string {
	t := customTitle
	if t == "" {
		t = lastPrompt
	}
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		return untitled
	}
	if r := []rune(t); len(r) > maxTitleRunes {
		return string(r[:maxTitleRunes]) + "…"
	}
	return t
}
