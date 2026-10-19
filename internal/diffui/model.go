// Package diffui is the read-only diff popup: the repo tree on the left with
// changed files coloured, the selected file's diff on the right.
package diffui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/yudongnan/agent-tree/internal/gitdiff"
)

const (
	msgNotRepo   = "这个目录不是 git 仓库"
	msgNoChanges = "没有未提交的改动"
	minTreeWidth = 28
	maxCached    = 64 // diffs kept in memory; each is capped at 1 MB by gitdiff
)

var (
	styleHeader   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Bold(true)
	styleDir      = lipgloss.NewStyle().Foreground(lipgloss.Color("#9aa5ce"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	styleKey      = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7"))
	styleCursor   = lipgloss.NewStyle().Reverse(true)
	styleBorder   = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	styleModified = lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))
	styleAdded    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a"))
	styleDeleted  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")).Strikethrough(true)
	stylePlus     = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a"))
	styleMinus    = lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e"))
	styleHunk     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7"))
)

type row struct {
	node  *gitdiff.Node
	depth int
}

type Model struct {
	repo    *gitdiff.Repo
	notRepo bool
	loadErr error
	root    *gitdiff.Node
	parent  map[*gitdiff.Node]*gitdiff.Node

	expanded    map[string]bool // dir path → open
	changedOnly bool
	rows        []row
	cursor      int
	treeOffset  int

	focusDiff  bool
	diff       []string // lines of the selected item's diff
	plain      bool     // diff holds file content, not a diff: don't colour +/-
	diffOffset int
	cache      map[string]string

	width, height int
}

// New loads the repository containing dir.
func New(dir string) tea.Model {
	m := &Model{width: 120, height: 40, expanded: map[string]bool{}, cache: map[string]string{}, parent: map[*gitdiff.Node]*gitdiff.Node{}}
	repo, err := gitdiff.Load(dir)
	switch {
	case errors.Is(err, gitdiff.ErrNotRepo):
		m.notRepo = true
		return m
	case err != nil:
		m.loadErr = err
		return m
	}
	m.repo = repo
	m.root = gitdiff.BuildTree(repo.Files)
	m.index(m.root)
	m.flatten()
	for i, r := range m.rows {
		if !r.node.Dir && r.node.Status != gitdiff.Unchanged {
			m.cursor = i
			break
		}
	}
	m.loadDiff()
	return m
}

// index records parents and opens every directory that contains changes.
func (m *Model) index(n *gitdiff.Node) {
	for _, c := range n.Children {
		m.parent[c] = n
		if c.Dir {
			if c.Changes > 0 {
				m.expanded[c.Path] = true
			}
			m.index(c)
		}
	}
}

// flatten lists the visible rows. Children of folded directories are never
// visited, which keeps large repositories cheap.
func (m *Model) flatten() {
	m.rows = m.rows[:0]
	var walk func(n *gitdiff.Node, depth int)
	walk = func(n *gitdiff.Node, depth int) {
		for _, c := range n.Children {
			if m.changedOnly && c.Changes == 0 && c.Status == gitdiff.Unchanged {
				continue
			}
			m.rows = append(m.rows, row{c, depth})
			if c.Dir && m.expanded[c.Path] {
				walk(c, depth+1)
			}
		}
	}
	walk(m.root, 0)
}

func (m *Model) selected() *gitdiff.Node {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor].node
	}
	return nil
}

func (m *Model) selectedPath() string {
	if n := m.selected(); n != nil {
		return n.Path
	}
	return ""
}

func (m *Model) loadDiff() {
	m.diffOffset = 0
	m.plain = false
	n := m.selected()
	switch {
	case n == nil:
		m.diff = nil
	case n.Dir:
		m.diff = changedUnder(n)
	default:
		text, ok := m.cache[n.Path]
		if !ok {
			var err error
			text, err = m.repo.Diff(gitdiff.File{Path: n.Path, Status: n.Status})
			if err != nil {
				text = "读取失败：" + err.Error()
			}
			if len(m.cache) >= maxCached {
				m.cache = map[string]string{}
			}
			m.cache[n.Path] = text
		}
		m.plain = n.Status == gitdiff.Unchanged
		m.diff = strings.Split(strings.TrimRight(text, "\n"), "\n")
	}
}

func changedUnder(n *gitdiff.Node) []string {
	var out []string
	var walk func(*gitdiff.Node)
	walk = func(n *gitdiff.Node) {
		for _, c := range n.Children {
			if c.Dir {
				walk(c)
			} else if c.Status != gitdiff.Unchanged {
				out = append(out, string(c.Status)+" "+c.Path)
			}
		}
	}
	walk(n)
	return out
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if m.repo == nil {
			return m, tea.Quit
		}
		k := msg.String()
		switch k {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.focusDiff = !m.focusDiff
			return m, nil
		case "c":
			m.toggleChangedOnly()
			return m, nil
		}
		if m.focusDiff {
			m.scrollDiff(k)
		} else {
			m.treeKey(k)
		}
	}
	return m, nil
}

func (m *Model) treeKey(k string) {
	n := m.selected()
	switch k {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "right", "l":
		if n != nil && n.Dir && !m.expanded[n.Path] {
			m.setExpanded(n, true)
		}
	case "left", "h":
		switch {
		case n == nil:
		case n.Dir && m.expanded[n.Path]:
			m.setExpanded(n, false)
		default:
			if p := m.parent[n]; p != nil && p != m.root {
				m.selectNode(p)
			}
		}
	case "enter":
		if n != nil && n.Dir {
			m.setExpanded(n, !m.expanded[n.Path])
		}
	}
}

func (m *Model) move(d int) {
	m.cursor += d
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.loadDiff()
}

func (m *Model) setExpanded(n *gitdiff.Node, open bool) {
	m.expanded[n.Path] = open
	m.flatten()
	m.selectNode(n)
}

func (m *Model) selectNode(n *gitdiff.Node) {
	for i, r := range m.rows {
		if r.node == n {
			m.cursor = i
			break
		}
	}
	m.loadDiff()
}

func (m *Model) toggleChangedOnly() {
	cur := m.selected()
	m.changedOnly = !m.changedOnly
	m.flatten()
	m.cursor = 0
	for i, r := range m.rows {
		if r.node == cur {
			m.cursor = i
		}
	}
	m.loadDiff()
}

func (m *Model) bodyHeight() int {
	if h := m.height - 2; h > 1 {
		return h
	}
	return 1
}

func (m *Model) scrollDiff(k string) {
	h := m.bodyHeight()
	maxOff := len(m.diff) - h
	if maxOff < 0 {
		maxOff = 0
	}
	switch k {
	case "up", "k":
		m.diffOffset--
	case "down", "j":
		m.diffOffset++
	case "pgup":
		m.diffOffset -= h
	case "pgdown", " ":
		m.diffOffset += h
	case "g":
		m.diffOffset = 0
	case "G":
		m.diffOffset = maxOff
	}
	if m.diffOffset > maxOff {
		m.diffOffset = maxOff
	}
	if m.diffOffset < 0 {
		m.diffOffset = 0
	}
}

func (m *Model) View() string {
	w := m.width
	switch {
	case m.notRepo:
		return msgNotRepo + "\n" + styleDim.Render("按任意键关闭")
	case m.loadErr != nil:
		return "读取失败：" + m.loadErr.Error() + "\n" + styleDim.Render("按任意键关闭")
	}
	lines := []string{styleHeader.Render(truncate(" "+m.header(), w))}

	h := m.bodyHeight()
	tw := w * 2 / 5
	if tw < minTreeWidth {
		tw = minTreeWidth
	}
	dw := w - tw - 1
	if m.cursor < m.treeOffset {
		m.treeOffset = m.cursor
	}
	if m.cursor >= m.treeOffset+h {
		m.treeOffset = m.cursor - h + 1
	}
	sep := styleBorder.Render("│")
	for i := 0; i < h; i++ {
		left := ""
		if j := m.treeOffset + i; j < len(m.rows) {
			left = m.renderRow(m.rows[j], j == m.cursor, tw)
		}
		right := ""
		if j := m.diffOffset + i; j < len(m.diff) {
			right = m.renderDiffLine(m.diff[j], dw)
		}
		lines = append(lines, pad(left, tw)+sep+right)
	}
	lines = append(lines, m.footer(w))
	return strings.Join(lines, "\n")
}

func (m *Model) header() string {
	parts := []string{m.repo.Name}
	if m.repo.Branch != "" {
		parts = append(parts, m.repo.Branch)
	}
	mod, add, del := m.repo.Stats()
	var stats []string
	if mod > 0 {
		stats = append(stats, fmt.Sprintf("%d 个文件修改", mod))
	}
	if add > 0 {
		stats = append(stats, fmt.Sprintf("%d 个新增", add))
	}
	if del > 0 {
		stats = append(stats, fmt.Sprintf("%d 个删除", del))
	}
	if len(stats) == 0 {
		parts = append(parts, msgNoChanges)
	} else {
		parts = append(parts, strings.Join(stats, "，"))
	}
	return strings.Join(parts, " · ")
}

func (m *Model) renderRow(r row, cursor bool, w int) string {
	n := r.node
	indent := strings.Repeat("  ", r.depth)
	var text string
	style := lipgloss.NewStyle()
	if n.Dir {
		arrow := "▸ "
		if m.expanded[n.Path] {
			arrow = "▾ "
		}
		text = indent + arrow + n.Name
		if n.Changes > 0 {
			text += fmt.Sprintf("  %d", n.Changes)
		}
		style = styleDir
	} else {
		switch n.Status {
		case gitdiff.Modified:
			text, style = indent+"M "+n.Name, styleModified
		case gitdiff.Added:
			text, style = indent+"A "+n.Name, styleAdded
		case gitdiff.Deleted:
			text, style = indent+"D "+n.Name, styleDeleted
		default:
			text = indent + "  " + n.Name
		}
	}
	text = truncate(text, w)
	if cursor {
		if m.focusDiff {
			return styleDim.Reverse(true).Render(text)
		}
		return styleCursor.Render(text)
	}
	return style.Render(text)
}

func (m *Model) renderDiffLine(l string, w int) string {
	l = truncate(strings.ReplaceAll(l, "\t", "    "), w)
	if m.plain {
		return l
	}
	switch {
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"),
		strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "), strings.HasPrefix(l, "@@"):
		return styleHunk.Render(l)
	case strings.HasPrefix(l, "+"):
		return stylePlus.Render(l)
	case strings.HasPrefix(l, "-"):
		return styleMinus.Render(l)
	}
	return l
}

func (m *Model) footer(w int) string {
	k := func(key, label string) string { return styleKey.Render(key) + " " + label }
	var items []string
	if m.focusDiff {
		items = []string{k("↑↓", "滚动"), k("PgUp/PgDn", "翻页"), k("g/G", "首/尾"), k("tab", "回到目录")}
	} else {
		items = []string{k("↑↓", "选文件"), k("←→", "折叠"), k("tab", "切到 diff 滚动")}
	}
	only := "只看改动"
	if m.changedOnly {
		only = "完整目录"
	}
	items = append(items, k("c", only), k("q", "关闭"))
	return " " + strings.Join(items, "  ")
}

func truncate(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
