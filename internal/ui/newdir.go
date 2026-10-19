package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

const maxCandidates = 5

// Complete completes the last path segment of input to directories. It
// returns the completed input (unchanged when nothing matches) and up to five
// matching directory names, each with a trailing "/".
func Complete(input string) (string, []string) {
	expanded := expandHome(input)
	dir, part := filepath.Split(expanded)
	readDir := dir
	if readDir == "" {
		readDir = "."
	}
	entries, err := os.ReadDir(readDir)
	if err != nil {
		return input, nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, part) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(part, ".")) {
			continue
		}
		if info, err := os.Stat(filepath.Join(readDir, name)); err == nil && info.IsDir() {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return input, nil
	}
	sort.Strings(names)
	completed := dir + commonPrefix(names)
	if len(names) == 1 {
		completed += "/"
	}
	cands := make([]string, 0, maxCandidates)
	for i, n := range names {
		if i == maxCandidates {
			break
		}
		cands = append(cands, n+"/")
	}
	if strings.HasPrefix(input, "~") {
		completed = abbreviateHome(completed)
	}
	return completed, cands
}

func commonPrefix(names []string) string {
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

func expandHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return home + p[1:]
	}
	return p
}

func abbreviateHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

type newDirModal struct {
	input      textinput.Model
	candidates []string
	err        string
}

func newNewDirModal(width int) *newDirModal {
	ti := textinput.New()
	ti.Prompt = "目录: "
	ti.Width = width - 7
	ti.Focus()
	return &newDirModal{input: ti}
}

func (d *newDirModal) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.modal = nil
		return nil
	case tea.KeyTab:
		completed, cands := Complete(d.input.Value())
		d.input.SetValue(completed)
		d.input.CursorEnd()
		d.candidates, d.err = cands, ""
		return nil
	case tea.KeyEnter:
		path := filepath.Clean(expandHome(strings.TrimSpace(d.input.Value())))
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			d.err = "目录不存在"
			return nil
		}
		m.modal = nil
		if err := m.c.New(path); err != nil {
			return m.showToast("新建失败：" + err.Error())
		}
		m.refresh()
		return nil
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	d.err = ""
	return cmd
}

func (d *newDirModal) lines(w int) []string {
	lines := []string{styleHeader.Render("在新目录开会话"), d.input.View()}
	for _, c := range d.candidates {
		lines = append(lines, styleDim.Render(truncate("  "+c, w)))
	}
	if d.err != "" {
		lines = append(lines, styleConfirm.Render(d.err))
	}
	k := func(key, label string) string { return styleKey.Render(key) + " " + label }
	return append(lines, strings.Join([]string{k("⏎", "确定"), k("tab", "补全"), k("esc", "取消")}, "  "))
}
