package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/yudongnan23/atree/internal/claudedata"
	"github.com/yudongnan23/atree/internal/state"
	"github.com/yudongnan23/atree/internal/tmux"
	"github.com/yudongnan23/atree/internal/tree"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)

const zone = "/Users/didi/project/zone"

type fakeCtl struct {
	snaps     []tree.Input // Snapshot returns these in order, then repeats the last
	st        *state.State
	opened    []tree.Session
	created   []string
	closed    []string
	ensure    int
	focusSlot int
	saves     int
	diffs     []string
}

func (f *fakeCtl) Snapshot() (tree.Input, error) {
	in := f.snaps[0]
	if len(f.snaps) > 1 {
		f.snaps = f.snaps[1:]
	}
	in.State = f.st
	return in, nil
}
func (f *fakeCtl) Open(s tree.Session) error    { f.opened = append(f.opened, s); return nil }
func (f *fakeCtl) New(cwd string) error         { f.created = append(f.created, cwd); return nil }
func (f *fakeCtl) Close(paneID string) error    { f.closed = append(f.closed, paneID); return nil }
func (f *fakeCtl) EnsureSlot() error            { f.ensure++; return nil }
func (f *fakeCtl) FocusSlot() error             { f.focusSlot++; return nil }
func (f *fakeCtl) Diff(dir string) error        { f.diffs = append(f.diffs, dir); return nil }
func (f *fakeCtl) SaveState(*state.State) error { f.saves++; return nil }

func newState() *state.State { s, _ := state.Load("/nonexistent-atree-test"); return s }

func hist(id, title string, ago time.Duration) claudedata.History {
	return claudedata.History{SessionID: id, Cwd: zone, Title: title, LastActive: t0.Add(-ago)}
}

// historyOnly: rows are [P zone, h1, h2].
func historyOnly() tree.Input {
	return tree.Input{History: []claudedata.History{hist("h1", "蜂巢重刷", time.Hour), hist("h2", "坐标系问题", 2*time.Hour)}, Now: t0}
}

func elsewhere(in tree.Input, id string) tree.Input {
	in.Running = append(in.Running, claudedata.Running{PID: 62386, SessionID: id, Cwd: zone, Status: claudedata.RunIdle, StatusUpdatedAt: t0.Add(-3 * time.Hour)})
	return in
}

func inAtree(in tree.Input, id, pane string) tree.Input {
	in.Running = append(in.Running, claudedata.Running{PID: 1, SessionID: id, Cwd: zone, Status: claudedata.RunIdle, StatusUpdatedAt: t0.Add(-3 * time.Hour)})
	in.Panes = append(in.Panes, tmux.Pane{ID: pane, SessionID: id, Cwd: zone, Role: tmux.RoleClaude})
	return in
}

func setup(snaps ...tree.Input) (*fakeCtl, *Model) {
	f := &fakeCtl{snaps: snaps, st: newState()}
	return f, New(f).(*Model)
}

func key(m *Model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func TestEnter_OpensHistory(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "down", "enter") // cursor on h1
	if len(f.opened) != 1 || f.opened[0].ID != "h1" {
		t.Fatalf("opened %+v", f.opened)
	}
	if _, ok := f.st.LastViewed["h1"]; !ok || f.saves == 0 {
		t.Error("LastViewed not recorded/saved")
	}
}

func TestEnter_ElsewhereShowsToast(t *testing.T) {
	f, m := setup(elsewhere(historyOnly(), "h1"))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30}) // keep the toast on one line
	key(m, "down", "enter")
	if len(f.opened) != 0 {
		t.Fatal("must not open a session running elsewhere")
	}
	if v := m.View(); !strings.Contains(v, "pid 62386") || !strings.Contains(v, "正在别的终端里运行") {
		t.Errorf("toast missing:\n%s", v)
	}
}

func TestEnter_RechecksBeforeSpawn(t *testing.T) {
	f, m := setup(historyOnly(), elsewhere(historyOnly(), "h1"))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	key(m, "down", "enter")
	if len(f.opened) != 0 {
		t.Fatal("re-snapshot showed it running elsewhere; must not open")
	}
	if !strings.Contains(m.View(), "pid 62386") {
		t.Error("toast missing")
	}
}

func TestEnter_ProjectToggles(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "enter")
	if !f.st.Collapsed[zone] {
		t.Fatal("enter on project should collapse it")
	}
	key(m, "enter")
	if f.st.Collapsed[zone] {
		t.Fatal("second enter should expand")
	}
}

func TestLeftRight_Collapse(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "down", "down", "left") // on h2 → collapses project, cursor to project
	if !f.st.Collapsed[zone] || m.cursor != 0 {
		t.Fatalf("collapsed=%v cursor=%d", f.st.Collapsed[zone], m.cursor)
	}
	key(m, "right")
	if f.st.Collapsed[zone] {
		t.Fatal("right should expand")
	}
}

func TestNew_UsesProjectCwd(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "down", "n")
	if len(f.created) != 1 || f.created[0] != zone {
		t.Fatalf("created %q", f.created)
	}
}

func TestClose_RequiresConfirm(t *testing.T) {
	f, m := setup(inAtree(historyOnly(), "h1", "%3"))
	key(m, "down") // running session sorts first
	key(m, "x")
	if !strings.Contains(m.View(), "关闭这个会话？(y/n)") {
		t.Fatalf("no prompt:\n%s", m.View())
	}
	key(m, "n")
	if len(f.closed) != 0 {
		t.Fatal("closed without confirmation")
	}
	key(m, "x", "y")
	if len(f.closed) != 1 || f.closed[0] != "%3" {
		t.Fatalf("closed %q", f.closed)
	}
}

func TestClose_IgnoredForHistory(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "down", "x", "y")
	if len(f.closed) != 0 {
		t.Fatal("history session has nothing to close")
	}
}

func TestArchive_Toggle(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "down", "a")
	if !f.st.Archived["h1"] || f.saves == 0 {
		t.Fatal("not archived")
	}
	f.st.ExpandedOlder[zone] = true
	m.refresh()
	// rows: P zone, h2, O(1), h1
	m.cursor = 3
	key(m, "a")
	if f.st.Archived["h1"] {
		t.Fatal("not unarchived")
	}
}

func TestArchive_RunningConfirmsAndCloses(t *testing.T) {
	f, m := setup(inAtree(historyOnly(), "h1", "%3"))
	key(m, "down", "a")
	if !strings.Contains(m.View(), "归档会结束这个会话，继续？(y/n)") {
		t.Fatalf("no prompt:\n%s", m.View())
	}
	key(m, "y")
	if len(f.closed) != 1 || !f.st.Archived["h1"] {
		t.Fatalf("closed=%q archived=%v", f.closed, f.st.Archived["h1"])
	}
}

func TestArchive_ElsewhereRefused(t *testing.T) {
	f, m := setup(elsewhere(historyOnly(), "h1"))
	key(m, "down", "a")
	if f.st.Archived["h1"] {
		t.Fatal("must not archive a session running elsewhere")
	}
	if !strings.Contains(m.View(), "在别处运行的会话不能归档") {
		t.Error("toast missing")
	}
}

func TestEnter_ArchivedSessionUnarchives(t *testing.T) {
	f, m := setup(historyOnly())
	f.st.Archived["h1"] = true
	f.st.ExpandedOlder[zone] = true
	m.refresh()
	// rows: P zone, h2, O(1), h1
	m.cursor = 3
	key(m, "enter")
	if len(f.opened) != 1 || f.opened[0].ID != "h1" || f.st.Archived["h1"] {
		t.Fatalf("opened=%+v archived=%v", f.opened, f.st.Archived["h1"])
	}
}

const day = 24 * time.Hour

const tree_ = "/Users/didi/project/claude-tree"

// withOld: rows are [P zone, h1, h2, O(1)] (+ old when expanded).
func withOld() tree.Input {
	in := historyOnly()
	in.History = append(in.History, hist("old", "旧会话", 10*day))
	return in
}

func TestOlderRow_EnterExpands(t *testing.T) {
	f, m := setup(withOld())
	m.cursor = 3
	key(m, "enter")
	if !f.st.ExpandedOlder[zone] || len(m.rows) != 5 || m.rows[4].Session.ID != "old" {
		t.Fatalf("expanded=%v rows=%d", f.st.ExpandedOlder[zone], len(m.rows))
	}
	key(m, "enter")
	if f.st.ExpandedOlder[zone] || len(m.rows) != 4 {
		t.Fatal("second enter should fold")
	}
	key(m, "right")
	if !f.st.ExpandedOlder[zone] {
		t.Fatal("right should expand")
	}
}

func TestOlderRow_LeftFromChildCollapses(t *testing.T) {
	f, m := setup(withOld())
	f.st.ExpandedOlder[zone] = true
	m.refresh()
	m.cursor = 4 // old
	key(m, "left")
	if f.st.ExpandedOlder[zone] || m.cursor != 3 || m.rows[3].Kind != tree.OlderRow {
		t.Fatalf("expanded=%v cursor=%d", f.st.ExpandedOlder[zone], m.cursor)
	}
	if f.st.Collapsed[zone] {
		t.Fatal("project must stay expanded")
	}
}

func TestProjectArchive_Manual(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "a") // cursor on the project row
	if _, ok := f.st.ArchivedProjects[zone]; !ok || f.saves == 0 {
		t.Fatal("project not archived")
	}
	if m.rows[0].Kind != tree.ArchivedHeaderRow {
		t.Fatalf("project should have moved to 已归档: %+v", m.rows[0])
	}
}

func TestProjectArchive_RefusedWhenRunning(t *testing.T) {
	f, m := setup(inAtree(historyOnly(), "h1", "%3"))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	key(m, "a")
	if _, ok := f.st.ArchivedProjects[zone]; ok {
		t.Fatal("must not archive a project with running sessions")
	}
	if !strings.Contains(m.View(), "这个 project 还有正在运行的会话，请先关闭") {
		t.Errorf("toast missing:\n%s", m.View())
	}
}

func idleZone() tree.Input {
	return tree.Input{History: []claudedata.History{
		{SessionID: "t1", Cwd: tree_, Title: "t", LastActive: t0.Add(-day)},
		hist("z1", "z", 40*day),
	}, Now: t0}
}

func TestProjectArchive_AutoToast(t *testing.T) {
	f, m := setup(idleZone())
	f.st.ArchivedGroupOpen = true
	m.refresh()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	// rows: P claude-tree, t1, A, P zone (auto)
	m.cursor = 3
	key(m, "a")
	if !strings.Contains(m.View(), "30 天内没有活动的 project 会自动归档，打开其中一个会话即可恢复") {
		t.Errorf("toast missing:\n%s", m.View())
	}
}

func TestProjectArchive_Unarchive(t *testing.T) {
	f, m := setup(historyOnly())
	f.st.ArchivedProjects[zone] = t0.Add(-30 * time.Minute)
	f.st.ArchivedGroupOpen = true
	m.refresh()
	// rows: A, P zone (archived)
	m.cursor = 1
	key(m, "a")
	if _, ok := f.st.ArchivedProjects[zone]; ok {
		t.Fatal("not unarchived")
	}
	if m.rows[0].Kind != tree.ProjectRow || m.rows[0].Archived {
		t.Fatalf("project should be back: %+v", m.rows[0])
	}
}

func TestArchivedProject_EnterToggles(t *testing.T) {
	f, m := setup(historyOnly())
	f.st.ArchivedProjects[zone] = t0.Add(-30 * time.Minute)
	f.st.ArchivedGroupOpen = true
	m.refresh()
	m.cursor = 1
	key(m, "enter")
	if !f.st.ArchivedOpen[zone] || len(m.rows) != 4 {
		t.Fatalf("open=%v rows=%d", f.st.ArchivedOpen[zone], len(m.rows))
	}
	if f.st.Collapsed[zone] {
		t.Fatal("must not touch the main-list collapse state")
	}
}

func TestRefresh_ClearsReturnedArchive(t *testing.T) {
	st := newState()
	st.ArchivedProjects[zone] = t0.Add(-3 * time.Hour) // h1 is 1h old: new activity since
	f := &fakeCtl{snaps: []tree.Input{historyOnly()}, st: st}
	m := New(f).(*Model)
	if _, ok := st.ArchivedProjects[zone]; ok || f.saves == 0 {
		t.Fatal("returned archive not cleared/saved")
	}
	if m.rows[0].Kind != tree.ProjectRow {
		t.Fatalf("rows %+v", m.rows[0])
	}
}

func TestOpenFromArchivedProjectClearsArchive(t *testing.T) {
	f, m := setup(historyOnly())
	f.st.ArchivedProjects[zone] = t0.Add(-30 * time.Minute)
	f.st.ArchivedGroupOpen = true
	f.st.ArchivedOpen[zone] = true
	m.refresh()
	// rows: A, P zone, h1, h2
	m.cursor = 2
	key(m, "enter")
	if len(f.opened) != 1 || f.opened[0].ID != "h1" {
		t.Fatalf("opened %+v", f.opened)
	}
	if _, ok := f.st.ArchivedProjects[zone]; ok {
		t.Fatal("archive record should be cleared on open")
	}
}

func TestView_OlderRowText(t *testing.T) {
	_, m := setup(withOld())
	if !strings.Contains(m.View(), "… 更早的 1 个会话") {
		t.Errorf("view:\n%s", m.View())
	}
}

func TestCursorStableAcrossRefresh(t *testing.T) {
	more := historyOnly()
	more.History = append(more.History, hist("h0", "新的", time.Minute))
	_, m := setup(historyOnly(), historyOnly(), more)
	key(m, "down", "down") // on h2
	m.Update(tickMsg{})
	if r := m.rows[m.cursor]; r.Session == nil || r.Session.ID != "h2" {
		t.Fatalf("cursor moved to %+v", r)
	}
}

func TestRefresh_CallsEnsureSlot(t *testing.T) {
	f, m := setup(historyOnly())
	n := f.ensure
	m.Update(tickMsg{})
	if f.ensure != n+1 {
		t.Fatalf("EnsureSlot calls %d → %d", n, f.ensure)
	}
}

func TestRefresh_MarksSlotSessionViewed(t *testing.T) {
	in := inAtree(historyOnly(), "h1", "%3")
	in.SlotPaneID = "%3"
	f, m := setup(in)
	m.Update(tickMsg{})
	if !f.st.LastViewed["h1"].After(t0.Add(-3 * time.Hour)) {
		t.Fatalf("LastViewed = %v", f.st.LastViewed["h1"])
	}
}

func TestEsc_FocusesSlot(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "esc")
	if f.focusSlot != 1 {
		t.Fatal("esc should focus the slot")
	}
}

func TestView_Glyphs(t *testing.T) {
	in := historyOnly()
	in = elsewhere(in, "h2")
	in = inAtree(in, "r1", "%3")
	in.Running = append(in.Running, claudedata.Running{PID: 2, SessionID: "b1", Cwd: zone, Status: claudedata.RunBusy, StatusUpdatedAt: t0})
	in.Panes = append(in.Panes, tmux.Pane{ID: "%4", SessionID: "b1", Cwd: zone, Role: tmux.RoleClaude})
	_, m := setup(in)
	m.Update(tea.WindowSizeMsg{Width: 34, Height: 30})
	v := m.View()
	for _, g := range []string{"●", "◆", "↗", "·", "▾ zone"} {
		if !strings.Contains(v, g) {
			t.Errorf("missing %q in\n%s", g, v)
		}
	}
	if !strings.Contains(v, "⌥← 回到侧栏") {
		t.Error("blurred footer missing")
	}
	m.Update(tea.FocusMsg{})
	if v := m.View(); !strings.Contains(v, "n 新建") || strings.Contains(v, "⌥← 回到侧栏") {
		t.Errorf("focused footer wrong:\n%s", v)
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if w := lipglossWidth(line); w > 34 {
			t.Errorf("line wider than pane (%d): %q", w, line)
		}
	}
}

func TestMouseClickOpens(t *testing.T) {
	f, m := setup(historyOnly())
	m.Update(tea.WindowSizeMsg{Width: 34, Height: 30})
	// line 0 is the header, line 1 the project, line 2 = h1
	m.Update(tea.MouseMsg{X: 5, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if len(f.opened) != 1 || f.opened[0].ID != "h1" {
		t.Fatalf("opened %+v", f.opened)
	}
}

func lipglossWidth(s string) int { return lipgloss.Width(s) }

func TestParseParents(t *testing.T) {
	got := parseParents("    1     0\n  400     1\n  501   400\nbad line\n")
	if len(got) != 3 || got[501] != 400 || got[400] != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestRefresh_MarksSlotSessionViewedAfterClear(t *testing.T) {
	in := historyOnly()
	in.Running = append(in.Running, claudedata.Running{PID: 500, SessionID: "s2", Cwd: zone, Status: claudedata.RunIdle, StatusUpdatedAt: t0.Add(-time.Hour)})
	in.Panes = append(in.Panes, tmux.Pane{ID: "%3", PID: 500, SessionID: "h1", Cwd: zone, Role: tmux.RoleClaude})
	in.SlotPaneID = "%3"
	f, m := setup(in)
	m.Update(tickMsg{})
	if !f.st.LastViewed["s2"].After(t0.Add(-time.Hour)) {
		t.Fatalf("live session in slot not marked viewed: %v", f.st.LastViewed)
	}
}

func manyRows() tree.Input {
	in := tree.Input{Now: t0}
	for i := 0; i < 30; i++ {
		in.History = append(in.History, hist(fmt.Sprintf("h%02d", i), "t", time.Duration(i+1)*time.Hour))
	}
	return in
}

func click(m *Model, y int) {
	m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func TestMouseClick_OutsideRowsIgnored(t *testing.T) {
	f, m := setup(manyRows())
	m.Update(tea.WindowSizeMsg{Width: 34, Height: 12})
	m.Update(tea.FocusMsg{})
	m.View()
	for y := 9; y < 14; y++ { // header is line 0, rows 1-8 (12 - 1 header - 3 footer), footer 9-11
		click(m, y)
	}
	click(m, 0) // header
	if len(f.opened) != 0 {
		t.Fatalf("clicks outside the list opened %+v", f.opened)
	}
}

func TestMouseClick_HeaderWhenScrolledIgnored(t *testing.T) {
	f, m := setup(manyRows())
	m.Update(tea.WindowSizeMsg{Width: 34, Height: 12})
	for i := 0; i < 20; i++ {
		key(m, "down")
	}
	m.View()
	click(m, 0)
	if len(f.opened) != 0 {
		t.Fatalf("header click opened %+v", f.opened)
	}
}

func TestMouseClick_IgnoredWhileConfirming(t *testing.T) {
	f, m := setup(inAtree(historyOnly(), "h1", "%3"))
	key(m, "down", "x")
	m.View()
	click(m, 2)
	if len(f.opened) != 0 || m.confirm == nil {
		t.Fatalf("click during confirm: opened=%+v", f.opened)
	}
}

func TestDiffKey_CallsControllerWithCwd(t *testing.T) {
	f, m := setup(historyOnly())
	key(m, "d") // project row
	key(m, "down", "d")
	if len(f.diffs) != 2 || f.diffs[0] != zone || f.diffs[1] != zone {
		t.Fatalf("diffs %q", f.diffs)
	}
}

func TestArchive_CursorStaysInPlace(t *testing.T) {
	_, m := setup(historyOnly())
	key(m, "down", "a") // h1 moves into the closed fold: rows P zone, h2, O(1)
	if m.cursor != 1 {
		t.Fatalf("cursor %d, want 1 (now on h2)", m.cursor)
	}
}

func TestProjectArchive_CursorStaysInPlace(t *testing.T) {
	in := historyOnly()
	in.History = append(in.History, claudedata.History{SessionID: "t1", Cwd: tree_, Title: "t", LastActive: t0.Add(-3 * time.Hour)})
	_, m := setup(in)
	// rows: P zone, h1, h2, P claude-tree, t1
	m.cursor = 3
	key(m, "a")
	// rows: P zone, h1, h2, A
	if m.cursor != 3 {
		t.Fatalf("cursor %d, want 3", m.cursor)
	}
}

func TestView_BlurredFooterShowsQuit(t *testing.T) {
	_, m := setup(historyOnly())
	if v := m.View(); !strings.Contains(v, "⌃q 退出") {
		t.Errorf("view:\n%s", v)
	}
}
