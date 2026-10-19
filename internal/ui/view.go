package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/yudongnan23/atree/internal/tree"
)

const (
	headerLines  = 1
	olderRowText = "  … 更早的 %d 个会话"
) // rows start below the "Agent Tree" header

var (
	styleHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Bold(true)
	styleProject = lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68"))

	styleArchivedProject = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a7a5a"))
	styleDim             = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	styleInSlot          = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true)
	styleCursor          = lipgloss.NewStyle().Reverse(true)
	styleKey             = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7"))
	styleToast           = lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7"))
	styleConfirm         = lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")).Bold(true)

	glyphs = map[tree.Status]string{
		tree.Busy:      lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")).Render("●"),
		tree.Waiting:   lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")).Render("◆"),
		tree.Idle:      lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")).Render("○"),
		tree.Elsewhere: lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Render("↗"),
		tree.History:   styleDim.Render("·"),
	}
	plainGlyphs = map[tree.Status]string{tree.Busy: "●", tree.Waiting: "◆", tree.Idle: "○", tree.Elsewhere: "↗", tree.History: "·"}
)

func (m *Model) View() string {
	w := m.width
	bottom := m.bottomLines()
	visible := m.listHeight(len(bottom))
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}

	lines := []string{styleHeader.Render(truncate("Agent Tree", w))}
	for i := m.offset; i < len(m.rows) && i < m.offset+visible; i++ {
		lines = append(lines, m.renderRow(m.rows[i], i == m.cursor && m.focused, w))
	}
	for len(lines) < headerLines+visible {
		lines = append(lines, "")
	}
	return strings.Join(append(lines, bottom...), "\n")
}

// bottomLines is everything under the list: modal, prompt or toast, and the footer.
func (m *Model) bottomLines() []string {
	w := m.width
	var bottom []string
	if m.modal != nil {
		bottom = m.modal.lines(w)
	} else if m.confirm != nil {
		bottom = append(bottom, wrap(styleConfirm, m.confirm.prompt, w)...)
	} else if m.toast != "" {
		bottom = append(bottom, wrap(styleToast, m.toast, w)...)
	}
	if m.modal == nil {
		bottom = append(bottom, m.footer()...)
	}
	return bottom
}

// listHeight is how many rows fit between the header and the bottom lines.
func (m *Model) listHeight(bottom int) int {
	if v := m.height - headerLines - bottom; v > 1 {
		return v
	}
	return 1
}

func (m *Model) footer() []string {
	if !m.focused {
		return []string{styleDim.Render("⌥← 回到侧栏  ⌃q 退出")}
	}
	k := func(key, label string) string { return styleKey.Render(key) + " " + label }
	return []string{
		strings.Join([]string{k("↑↓", "移动"), k("⏎", "打开"), k("←→", "折叠")}, "  "),
		strings.Join([]string{k("n", "新建"), k("N", "新目录"), k("x", "关闭")}, "  "),
		strings.Join([]string{k("a", "归档"), k("d", "diff"), k("esc", "返回")}, "  "),
	}
}

func (m *Model) renderRow(r tree.Row, cursor bool, w int) string {
	var left, right, glyph string
	var base lipgloss.Style
	switch r.Kind {
	case tree.ProjectRow, tree.ArchivedHeaderRow:
		arrow := "▾ "
		if r.Collapsed {
			arrow = "▸ "
		}
		name := r.Project
		base = styleProject
		if r.Archived {
			base = styleArchivedProject
		}
		if r.Kind == tree.ArchivedHeaderRow {
			name, base = "已归档", styleDim
		}
		left = arrow + name
		if r.Collapsed {
			right = fmt.Sprint(r.Count)
		}
	case tree.OlderRow:
		left = fmt.Sprintf(olderRowText, r.Count)
		base = styleDim
	case tree.SessionRow:
		s := r.Session
		left = "  " + plainGlyphs[s.Status] + " " + s.Title
		glyph = glyphs[s.Status]
		switch {
		case r.Archived:
			right = s.LastActive.Format("01-02")
		case s.Status == tree.History:
			right = s.LastActive.Format("01-02")
		}
		base = lipgloss.NewStyle()
		switch {
		case r.InSlot:
			base = styleInSlot
		case s.Archived:
			base = styleDim
		}
	}
	line := fit(left, right, w)
	if cursor {
		return styleCursor.Render(line)
	}
	if glyph == "" {
		return base.Render(line)
	}
	// Colour the glyph separately from the text: "  <glyph> <title>…".
	rest := strings.TrimPrefix(line, "  "+plainGlyphs[r.Session.Status])
	if right != "" && strings.HasSuffix(rest, right) {
		rest = strings.TrimSuffix(rest, right)
		return "  " + glyph + base.Render(rest) + styleDim.Render(right)
	}
	return "  " + glyph + base.Render(rest)
}

// fit lays out left and right on one line of width w, truncating left.
func fit(left, right string, w int) string {
	if right == "" {
		return truncate(left, w)
	}
	room := w - runewidth.StringWidth(right) - 1
	if room < 4 {
		return truncate(left, w)
	}
	left = truncate(left, room)
	pad := w - runewidth.StringWidth(left) - runewidth.StringWidth(right)
	return left + strings.Repeat(" ", pad) + right
}

func truncate(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}

// wrap breaks s into lines no wider than w, by display width.
func wrap(style lipgloss.Style, s string, w int) []string {
	var lines []string
	var cur strings.Builder
	width := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if width+rw > w {
			lines = append(lines, style.Render(cur.String()))
			cur.Reset()
			width = 0
		}
		cur.WriteRune(r)
		width += rw
	}
	if cur.Len() > 0 {
		lines = append(lines, style.Render(cur.String()))
	}
	return lines
}
