// Package ui is the sidebar: a Bubble Tea program running in the left pane.
package ui

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yudongnan/agent-tree/internal/claudedata"
	"github.com/yudongnan/agent-tree/internal/state"
	"github.com/yudongnan/agent-tree/internal/tmux"
	"github.com/yudongnan/agent-tree/internal/tree"
)

// Controller is everything the sidebar does outside itself.
type Controller interface {
	Snapshot() (tree.Input, error)
	Open(s tree.Session) error // show its pane, or resume it in a new one; then focus the slot
	New(cwd string) error      // start a new session in cwd; then focus the slot
	Close(paneID string) error
	EnsureSlot() error
	FocusSlot() error
	SaveState(*state.State) error
	Diff(dir string) error // open the diff popup for dir
}

const (
	refreshEvery = time.Second
	toastFor     = 4 * time.Second

	msgElsewhere        = "这个会话正在别的终端里运行（pid %d），请到那个窗口操作，或先在那边退出再从这里打开"
	msgArchiveElsewhere = "在别处运行的会话不能归档"
	msgProjectRunning   = "这个 project 还有正在运行的会话，请先关闭"
	msgAutoArchived     = "30 天内没有活动的 project 会自动归档，打开其中一个会话即可恢复"
	promptClose         = "关闭这个会话？(y/n)"
	promptArchive       = "归档会结束这个会话，继续？(y/n)"
)

type tickMsg struct{}
type toastExpiredMsg struct{ id int }

type confirmation struct {
	prompt string
	yes    func()
}

type Model struct {
	c       Controller
	in      tree.Input
	rows    []tree.Row
	cursor  int
	offset  int // first visible row
	focused bool
	width   int
	height  int

	toast   string
	toastID int
	confirm *confirmation
	modal   *newDirModal
}

// New builds the sidebar and loads the first snapshot.
func New(c Controller) tea.Model {
	m := &Model{c: c, width: tmux.SidebarWidth, height: 40}
	m.refresh()
	return m
}

func (m *Model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// refresh reloads everything, keeping the cursor on the same item.
func (m *Model) refresh() {
	_ = m.c.EnsureSlot()
	in, err := m.c.Snapshot()
	if err != nil {
		m.showToast("刷新失败：" + err.Error())
		return
	}
	keep := m.cursorKey()
	m.in = in
	if returned := tree.ReturnedArchives(in); len(returned) > 0 {
		for _, cwd := range returned {
			delete(in.State.ArchivedProjects, cwd)
		}
		m.save()
	}
	m.rows = tree.Build(in)
	m.markSlotViewed()
	// Follow the same item; if it's gone (archived, folded away), stay at the
	// same position so the cursor lands on its neighbour.
	for i, r := range m.rows {
		if rowKey(r) == keep {
			m.cursor = i
			break
		}
	}
	m.clampCursor()
}

// markSlotViewed records that whatever is in the slot has been seen, so it
// doesn't turn ◆ the moment you switch away from it.
func (m *Model) markSlotViewed() {
	var id string
	for _, r := range m.rows {
		if r.InSlot {
			id = r.Session.ID
		}
	}
	if id == "" {
		return
	}
	for _, r := range m.in.Running {
		if r.SessionID == id && r.StatusUpdatedAt.After(m.in.State.LastViewed[id]) {
			m.in.State.LastViewed[id] = m.now()
			m.save()
			return
		}
	}
}

func (m *Model) now() time.Time {
	if !m.in.Now.IsZero() {
		return m.in.Now
	}
	return time.Now()
}

func rowKey(r tree.Row) string {
	switch r.Kind {
	case tree.SessionRow:
		return "s:" + r.Session.ID
	case tree.ProjectRow:
		if r.Archived {
			return "ap:" + r.Cwd
		}
		return "p:" + r.Cwd
	case tree.OlderRow:
		return "o:" + r.Cwd
	}
	return "archived"
}

func (m *Model) cursorKey() string {
	if m.cursor < len(m.rows) {
		return rowKey(m.rows[m.cursor])
	}
	return ""
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) save() {
	if err := m.c.SaveState(m.in.State); err != nil {
		m.showToast("保存状态失败：" + err.Error())
	}
}

func (m *Model) showToast(s string) tea.Cmd {
	m.toast = s
	m.toastID++
	id := m.toastID
	return tea.Tick(toastFor, func(time.Time) tea.Msg { return toastExpiredMsg{id} })
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.refresh()
		return m, tick()
	case toastExpiredMsg:
		if msg.id == m.toastID {
			m.toast = ""
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.FocusMsg:
		m.focused = true
	case tea.BlurMsg:
		m.focused = false
	case tea.MouseMsg:
		// Only clicks on visible list rows count: not the header, footer,
		// toast, or anything while a prompt is open.
		inList := msg.Y >= headerLines && msg.Y < headerLines+m.listHeight(len(m.bottomLines()))
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && inList && m.modal == nil && m.confirm == nil {
			if i := m.offset + msg.Y - headerLines; i < len(m.rows) {
				m.cursor = i
				return m, m.activate()
			}
		}
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.modal != nil {
		return m.modal.update(m, msg)
	}
	m.toast = ""
	if c := m.confirm; c != nil {
		m.confirm = nil
		if msg.String() == "y" {
			c.yes()
			m.refresh()
		}
		return nil
	}
	switch msg.String() {
	case "up", "k":
		m.cursor--
		m.clampCursor()
	case "down", "j":
		m.cursor++
		m.clampCursor()
	case "enter":
		return m.activate()
	case "left":
		m.collapse()
	case "right":
		m.expand()
	case "n":
		if r, ok := m.current(); ok && r.Kind != tree.ArchivedHeaderRow {
			if err := m.c.New(r.Cwd); err != nil {
				return m.showToast("新建失败：" + err.Error())
			}
			m.refresh()
		}
	case "N":
		m.modal = newNewDirModal(m.width)
	case "x":
		if r, ok := m.current(); ok && r.Kind == tree.SessionRow && r.Session.PaneID != "" {
			pane := r.Session.PaneID
			m.confirm = &confirmation{prompt: promptClose, yes: func() { m.closePane(pane) }}
		}
	case "a":
		return m.archive()
	case "d":
		if r, ok := m.current(); ok && r.Kind != tree.ArchivedHeaderRow {
			if err := m.c.Diff(r.Cwd); err != nil {
				return m.showToast("打开 diff 失败：" + err.Error())
			}
		}
	case "esc":
		_ = m.c.FocusSlot()
	}
	return nil
}

func (m *Model) current() (tree.Row, bool) {
	if m.cursor < len(m.rows) {
		return m.rows[m.cursor], true
	}
	return tree.Row{}, false
}

func (m *Model) closePane(pane string) {
	if err := m.c.Close(pane); err != nil {
		m.showToast("关闭失败：" + err.Error())
	}
}

func (m *Model) activate() tea.Cmd {
	r, ok := m.current()
	if !ok {
		return nil
	}
	switch r.Kind {
	case tree.ProjectRow:
		if r.Archived {
			m.in.State.ArchivedOpen[r.Cwd] = r.Collapsed
		} else {
			m.in.State.Collapsed[r.Cwd] = !r.Collapsed
		}
		m.save()
		m.refresh()
		return nil
	case tree.OlderRow:
		m.in.State.ExpandedOlder[r.Cwd] = r.Collapsed
		m.save()
		m.refresh()
		return nil
	case tree.ArchivedHeaderRow:
		m.in.State.ArchivedGroupOpen = !m.in.State.ArchivedGroupOpen
		m.save()
		m.refresh()
		return nil
	}
	if r.Session.Status == tree.Elsewhere {
		return m.showToast(fmt.Sprintf(msgElsewhere, r.Session.PID))
	}
	// Re-read before spawning: it may have started elsewhere since the last tick.
	id := r.Session.ID
	m.refresh()
	s := m.findSession(id)
	if s == nil {
		return nil
	}
	if s.Status == tree.Elsewhere {
		return m.showToast(fmt.Sprintf(msgElsewhere, s.PID))
	}
	if err := m.c.Open(*s); err != nil {
		return m.showToast("打开失败：" + err.Error())
	}
	m.in.State.LastViewed[id] = m.now()
	delete(m.in.State.Archived, id)
	if r.Archived { // opened from 已归档: the project comes back
		delete(m.in.State.ArchivedProjects, r.Cwd)
	}
	m.save()
	m.refresh()
	return nil
}

func (m *Model) findSession(id string) *tree.Session {
	for _, r := range m.rows {
		if r.Kind == tree.SessionRow && r.Session.ID == id {
			return r.Session
		}
	}
	return nil
}

func (m *Model) collapse() {
	r, ok := m.current()
	if !ok {
		return
	}
	st := m.in.State
	land := "" // row key to put the cursor on afterwards
	switch r.Kind {
	case tree.ArchivedHeaderRow:
		st.ArchivedGroupOpen = false
	case tree.ProjectRow:
		if r.Archived {
			st.ArchivedOpen[r.Cwd] = false
		} else {
			st.Collapsed[r.Cwd] = true
		}
	case tree.OlderRow:
		st.ExpandedOlder[r.Cwd] = false
	case tree.SessionRow:
		switch parent := m.parentRow(m.cursor); {
		case r.Archived:
			st.ArchivedOpen[r.Cwd] = false
			land = "ap:" + r.Cwd
		case parent.Kind == tree.OlderRow:
			st.ExpandedOlder[r.Cwd] = false
			land = "o:" + r.Cwd
		default:
			st.Collapsed[r.Cwd] = true
			land = "p:" + r.Cwd
		}
	}
	m.save()
	m.refresh()
	for i, row := range m.rows {
		if land != "" && rowKey(row) == land {
			m.cursor = i
		}
	}
}

// parentRow is the nearest non-session row above i: the project or the
// "更早的" fold a session row is listed under.
func (m *Model) parentRow(i int) tree.Row {
	for j := i - 1; j >= 0; j-- {
		if m.rows[j].Kind != tree.SessionRow {
			return m.rows[j]
		}
	}
	return tree.Row{}
}

func (m *Model) expand() {
	r, ok := m.current()
	if !ok {
		return
	}
	st := m.in.State
	switch {
	case r.Kind == tree.ProjectRow && r.Archived:
		st.ArchivedOpen[r.Cwd] = true
	case r.Kind == tree.ProjectRow:
		delete(st.Collapsed, r.Cwd)
	case r.Kind == tree.OlderRow:
		st.ExpandedOlder[r.Cwd] = true
	case r.Kind == tree.ArchivedHeaderRow:
		st.ArchivedGroupOpen = true
	default:
		return
	}
	m.save()
	m.refresh()
}

func (m *Model) archive() tea.Cmd {
	r, ok := m.current()
	if !ok {
		return nil
	}
	if r.Kind == tree.ProjectRow {
		return m.archiveProject(r)
	}
	if r.Kind != tree.SessionRow {
		return nil
	}
	s := r.Session
	switch {
	case s.Status == tree.Elsewhere:
		return m.showToast(msgArchiveElsewhere)
	case s.Archived:
		delete(m.in.State.Archived, s.ID)
	case s.PaneID != "":
		id, pane := s.ID, s.PaneID
		m.confirm = &confirmation{prompt: promptArchive, yes: func() {
			m.closePane(pane)
			m.in.State.Archived[id] = true
			m.save()
		}}
		return nil
	default:
		m.in.State.Archived[s.ID] = true
	}
	m.save()
	m.refresh()
	return nil
}

func (m *Model) archiveProject(r tree.Row) tea.Cmd {
	st := m.in.State
	switch {
	case r.Archived && r.AutoArchived:
		return m.showToast(msgAutoArchived)
	case r.Archived:
		delete(st.ArchivedProjects, r.Cwd)
	case r.Running:
		return m.showToast(msgProjectRunning)
	default:
		st.ArchivedProjects[r.Cwd] = m.now()
	}
	m.save()
	m.refresh()
	return nil
}

// RealController wires the sidebar to tmux and Claude's files.
func RealController(client *tmux.Client, hist *claudedata.HistoryScanner, root, cfgDir string) Controller {
	st, err := state.Load(cfgDir)
	if err != nil {
		// Unreadable state file: run with empty state rather than not at all.
		st = state.New()
	}
	return &realController{client: client, hist: hist, root: root, cfgDir: cfgDir, st: st}
}

type realController struct {
	client *tmux.Client
	hist   *claudedata.HistoryScanner
	root   string
	cfgDir string
	st     *state.State
}

func (r *realController) Snapshot() (tree.Input, error) {
	running, err := claudedata.LoadRunning(r.root, claudedata.PIDAlive)
	if err != nil {
		return tree.Input{}, err
	}
	history, err := r.hist.Scan()
	if err != nil {
		return tree.Input{}, err
	}
	panes, err := r.client.Panes()
	if err != nil {
		return tree.Input{}, err
	}
	in := tree.Input{Running: running, History: history, Panes: panes, State: r.st, Now: time.Now()}
	if out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output(); err == nil {
		in.Parents = parseParents(string(out))
	}
	if slot, err := r.client.SlotPane(); err == nil {
		in.SlotPaneID = slot.ID
	} else if !errors.Is(err, tmux.ErrNoSlot) {
		return tree.Input{}, err
	}
	return in, nil
}

func (r *realController) Open(s tree.Session) error {
	pane := s.PaneID
	if pane == "" {
		if err := checkDir(s.Cwd); err != nil {
			return err
		}
		var err error
		if pane, err = r.client.NewClaude(s.ID, s.Cwd, true); err != nil {
			return err
		}
	}
	if err := r.client.Show(pane); err != nil {
		return err
	}
	return r.client.FocusSlot()
}

func (r *realController) New(cwd string) error {
	if err := checkDir(cwd); err != nil {
		return err
	}
	pane, err := r.client.NewClaude(newUUID(), cwd, false)
	if err != nil {
		return err
	}
	if err := r.client.Show(pane); err != nil {
		return err
	}
	return r.client.FocusSlot()
}

func (r *realController) Close(paneID string) error { return r.client.Kill(paneID) }
func (r *realController) EnsureSlot() error         { return r.client.EnsureSlot() }
func (r *realController) Diff(dir string) error {
	if err := checkDir(dir); err != nil {
		return err
	}
	return r.client.Popup(dir)
}
func (r *realController) FocusSlot() error               { return r.client.FocusSlot() }
func (r *realController) SaveState(s *state.State) error { return s.Save(r.cfgDir) }

// checkDir rejects a project directory that no longer exists; tmux would
// otherwise silently start claude in the home directory.
func checkDir(dir string) error {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("目录不存在：%s", dir)
	}
	return nil
}

// parseParents parses `ps -o pid=,ppid=` output into pid → parent pid.
func parseParents(out string) map[int]int {
	parents := map[int]int{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			parents[pid] = ppid
		}
	}
	return parents
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
