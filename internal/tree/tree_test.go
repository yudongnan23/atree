package tree

import (
	"fmt"
	"testing"
	"time"

	"github.com/yudongnan23/atree/internal/claudedata"
	"github.com/yudongnan23/atree/internal/state"
	"github.com/yudongnan23/atree/internal/tmux"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)

func ago(d time.Duration) time.Time { return t0.Add(-d) }

func emptyState() *state.State {
	s, _ := state.Load("/nonexistent-atree-test")
	return s
}

func hist(id, cwd, title string, at time.Time) claudedata.History {
	return claudedata.History{SessionID: id, Cwd: cwd, Title: title, LastActive: at}
}

func pane(id, session, cwd string) tmux.Pane {
	return tmux.Pane{ID: id, SessionID: session, Cwd: cwd, Role: tmux.RoleClaude, Window: "bg"}
}

// rowsText renders rows compactly for comparison.
func rowsText(rows []Row) []string {
	var out []string
	for _, r := range rows {
		switch r.Kind {
		case ProjectRow:
			s := "P " + r.Project
			if r.Archived {
				s += " [archived]"
			}
			if r.AutoArchived {
				s += " [auto]"
			}
			if r.Collapsed {
				s += " (collapsed)"
			}
			out = append(out, s)
		case SessionRow:
			out = append(out, "  "+r.Session.ID)
		case OlderRow:
			out = append(out, fmt.Sprintf("  O(%d)", r.Count))
		case ArchivedHeaderRow:
			out = append(out, "A")
		}
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %q\nwant %q", got, want)
		}
	}
}

func find(rows []Row, id string) *Session {
	for _, r := range rows {
		if r.Kind == SessionRow && r.Session.ID == id {
			return r.Session
		}
	}
	return nil
}

const zone, tree = "/Users/didi/project/zone", "/Users/didi/project/claude-tree"

func TestBuild_GroupsAndOrders(t *testing.T) {
	in := Input{
		History: []claudedata.History{
			hist("z-old", zone, "蜂巢重刷", ago(72*time.Hour)),
			hist("z-new", zone, "坐标系问题", ago(48*time.Hour)),
			hist("z-run", zone, "围栏判定排查", ago(96*time.Hour)),
			hist("t1", tree, "侧边栏", ago(time.Hour)),
		},
		Running: []claudedata.Running{{PID: 1, SessionID: "z-run", Cwd: zone, Status: claudedata.RunBusy, StatusUpdatedAt: ago(96 * time.Hour)}},
		Panes:   []tmux.Pane{pane("%3", "z-run", zone)},
		State:   emptyState(), Now: t0,
	}
	equal(t, rowsText(Build(in)), []string{
		"P claude-tree", "  t1",
		"P zone", "  z-run", "  z-new", "  z-old",
	})
}

func TestBuild_StatusMapping(t *testing.T) {
	st := emptyState()
	st.LastViewed["idle"] = ago(time.Minute)      // viewed after it went idle
	st.LastViewed["wait"] = ago(10 * time.Minute) // went idle after last view
	st.LastViewed["slot"] = ago(10 * time.Minute)
	run := func(id string, s claudedata.RunStatus, at time.Time) claudedata.Running {
		return claudedata.Running{PID: 100, SessionID: id, Cwd: zone, Status: s, StatusUpdatedAt: at}
	}
	in := Input{
		History: []claudedata.History{hist("hist", zone, "h", ago(time.Hour))},
		Running: []claudedata.Running{
			run("busy", claudedata.RunBusy, ago(time.Minute)),
			run("idle", claudedata.RunIdle, ago(5*time.Minute)),
			run("wait", claudedata.RunIdle, ago(5*time.Minute)),
			run("slot", claudedata.RunIdle, ago(5*time.Minute)),
			run("other", claudedata.RunOther, ago(5*time.Minute)),
			{PID: 62386, SessionID: "else", Cwd: zone, Status: claudedata.RunIdle, StatusUpdatedAt: ago(time.Minute)},
		},
		Panes: []tmux.Pane{
			pane("%1", "busy", zone), pane("%2", "idle", zone), pane("%3", "wait", zone),
			pane("%4", "slot", zone), pane("%5", "other", zone),
		},
		SlotPaneID: "%4",
		State:      st, Now: t0,
	}
	rows := Build(in)
	want := map[string]Status{"busy": Busy, "idle": Idle, "wait": Waiting, "slot": Idle, "other": Waiting, "else": Elsewhere, "hist": History}
	for id, s := range want {
		got := find(rows, id)
		if got == nil {
			t.Fatalf("%s missing", id)
		}
		if got.Status != s {
			t.Errorf("%s: status %v, want %v", id, got.Status, s)
		}
	}
	if find(rows, "else").PID != 62386 || find(rows, "else").PaneID != "" {
		t.Errorf("elsewhere session: %+v", find(rows, "else"))
	}
	if find(rows, "busy").PaneID != "%1" {
		t.Error("PaneID not set")
	}
	for _, r := range rows {
		if r.Kind == SessionRow && r.InSlot != (r.Session.ID == "slot") {
			t.Errorf("InSlot wrong on %s", r.Session.ID)
		}
	}
}

func TestBuild_NewPaneWithoutFile(t *testing.T) {
	in := Input{Panes: []tmux.Pane{pane("%9", "fresh", "/Users/didi/project/new-thing")}, State: emptyState(), Now: t0}
	rows := Build(in)
	equal(t, rowsText(rows), []string{"P new-thing", "  fresh"})
	s := find(rows, "fresh")
	if s.Title != "新会话" || s.Status != Idle || s.Cwd != "/Users/didi/project/new-thing" {
		t.Errorf("%+v", s)
	}
}

func TestBuild_DuplicateBasenames(t *testing.T) {
	in := Input{History: []claudedata.History{
		hist("1", "/Users/didi/a/api", "x", ago(time.Hour)),
		hist("2", "/Users/didi/b/api", "y", ago(2*time.Hour)),
		hist("3", zone, "z", ago(3*time.Hour)),
	}, State: emptyState(), Now: t0}
	equal(t, rowsText(Build(in)), []string{"P a/api", "  1", "P b/api", "  2", "P zone", "  3"})
}

const day = 24 * time.Hour

func TestBuild_ManualSessionArchiveGoesToOlder(t *testing.T) {
	st := emptyState()
	st.Archived["z2"] = true
	in := Input{History: []claudedata.History{
		hist("z1", zone, "a", ago(time.Hour)),
		hist("z2", zone, "b", ago(2*time.Hour)),
	}, State: st, Now: t0}
	equal(t, rowsText(Build(in)), []string{"P zone", "  z1", "  O(1)"})
	st.ExpandedOlder[zone] = true
	rows := Build(in)
	equal(t, rowsText(rows), []string{"P zone", "  z1", "  O(1)", "  z2"})
	if !rows[3].Session.Archived {
		t.Error("archived flag lost")
	}
}

func TestBuild_OlderFold(t *testing.T) {
	st := emptyState()
	in := Input{History: []claudedata.History{
		hist("d1", zone, "", ago(1*day)), hist("d3", zone, "", ago(3*day)),
		hist("d10", zone, "", ago(10*day)), hist("d20", zone, "", ago(20*day)),
	}, State: st, Now: t0}
	equal(t, rowsText(Build(in)), []string{"P zone", "  d1", "  d3", "  O(2)"})
	st.ExpandedOlder[zone] = true
	rows := Build(in)
	equal(t, rowsText(rows), []string{"P zone", "  d1", "  d3", "  O(2)", "  d10", "  d20"})
	if rows[0].Count != 4 || rows[3].Cwd != zone {
		t.Errorf("project count %d, older cwd %q", rows[0].Count, rows[3].Cwd)
	}
}

func TestBuild_RunningOldSessionStaysVisible(t *testing.T) {
	in := Input{
		History: []claudedata.History{hist("old", zone, "", ago(15*day)), hist("new", zone, "", ago(day))},
		Running: []claudedata.Running{{PID: 9, SessionID: "old", Cwd: zone, Status: claudedata.RunIdle, StatusUpdatedAt: ago(15 * day)}},
		State:   emptyState(), Now: t0,
	}
	equal(t, rowsText(Build(in)), []string{"P zone", "  old", "  new"})
}

func TestBuild_OnlyOlder(t *testing.T) {
	in := Input{History: []claudedata.History{hist("d10", zone, "", ago(10*day))}, State: emptyState(), Now: t0}
	equal(t, rowsText(Build(in)), []string{"P zone", "  O(1)"})
}

func TestBuild_AutoArchiveProject(t *testing.T) {
	st := emptyState()
	in := Input{History: []claudedata.History{
		hist("t1", tree, "", ago(day)),
		hist("z1", zone, "", ago(31*day)), hist("z2", zone, "", ago(40*day)),
	}, State: st, Now: t0}
	rows := Build(in)
	equal(t, rowsText(rows), []string{"P claude-tree", "  t1", "A"})
	if rows[2].Count != 1 || !rows[2].Collapsed {
		t.Errorf("header %+v", rows[2])
	}
	st.ArchivedGroupOpen = true
	equal(t, rowsText(Build(in)), []string{"P claude-tree", "  t1", "A", "P zone [archived] [auto] (collapsed)"})
	st.ArchivedOpen[zone] = true
	rows = Build(in)
	equal(t, rowsText(rows), []string{"P claude-tree", "  t1", "A", "P zone [archived] [auto]", "  z1", "  z2"})
	if !rows[4].Archived {
		t.Error("session rows under 已归档 must be flagged Archived")
	}
}

func TestBuild_AutoArchiveSkippedWhenRunning(t *testing.T) {
	in := Input{
		History: []claudedata.History{hist("z1", zone, "", ago(40*day))},
		Running: []claudedata.Running{{PID: 9, SessionID: "z1", Cwd: zone, Status: claudedata.RunIdle, StatusUpdatedAt: ago(40 * day)}},
		State:   emptyState(), Now: t0,
	}
	rows := Build(in)
	equal(t, rowsText(rows), []string{"P zone", "  z1"})
	if !rows[0].Running {
		t.Error("project Running flag not set")
	}
}

func TestBuild_ManualProjectArchive(t *testing.T) {
	st := emptyState()
	st.ArchivedProjects[zone] = ago(time.Hour)
	mk := func(at time.Time) Input {
		return Input{History: []claudedata.History{hist("t1", tree, "", ago(day)), hist("z1", zone, "", at)}, State: st, Now: t0}
	}
	equal(t, rowsText(Build(mk(ago(2*time.Hour)))), []string{"P claude-tree", "  t1", "A"})
	if got := ReturnedArchives(mk(ago(2 * time.Hour))); len(got) != 0 {
		t.Fatalf("returned %v", got)
	}
	// Claude writes the transcript a few seconds after the archive: still archived.
	late := mk(ago(time.Hour).Add(5 * time.Second))
	equal(t, rowsText(Build(late)), []string{"P claude-tree", "  t1", "A"})
	st.ArchivedGroupOpen = true
	equal(t, rowsText(Build(late)), []string{"P claude-tree", "  t1", "A", "P zone [archived] (collapsed)"})
	// A real new chat afterwards brings it back.
	back := mk(ago(30 * time.Minute))
	equal(t, rowsText(Build(back)), []string{"P zone", "  z1", "P claude-tree", "  t1"})
	if got := ReturnedArchives(back); len(got) != 1 || got[0] != zone {
		t.Fatalf("returned %v", got)
	}
}

func TestBuild_ProjectsSortedByActivityInclArchivedSessions(t *testing.T) {
	st := emptyState()
	st.Archived["z1"] = true
	in := Input{History: []claudedata.History{
		hist("t1", tree, "", ago(2*time.Hour)),
		hist("z1", zone, "", ago(time.Hour)),
	}, State: st, Now: t0}
	equal(t, rowsText(Build(in)), []string{"P zone", "  O(1)", "P claude-tree", "  t1"})
}

func TestBuild_Collapsed(t *testing.T) {
	st := emptyState()
	st.Collapsed[zone] = true
	in := Input{History: []claudedata.History{
		hist("z1", zone, "a", ago(time.Hour)), hist("z2", zone, "b", ago(2*time.Hour)),
	}, State: st, Now: t0}
	rows := Build(in)
	equal(t, rowsText(rows), []string{"P zone (collapsed)"})
	if rows[0].Count != 2 || rows[0].Cwd != zone {
		t.Errorf("%+v", rows[0])
	}
}

// After /clear Claude switches the live process to a new session id while the
// pane keeps its original tag. The pane belongs to whatever its process runs.
func TestBuild_PaneMatchedByPID(t *testing.T) {
	p := pane("%3", "s1", zone)
	p.PID = 500
	in := Input{
		History: []claudedata.History{hist("s1", zone, "old", ago(time.Hour))},
		Running: []claudedata.Running{{PID: 500, SessionID: "s2", Cwd: zone, Status: claudedata.RunIdle, StatusUpdatedAt: ago(time.Minute)}},
		Panes:   []tmux.Pane{p}, SlotPaneID: "%3",
		State: emptyState(), Now: t0,
	}
	rows := Build(in)
	if s := find(rows, "s2"); s == nil || s.Status != Idle || s.PaneID != "%3" {
		t.Fatalf("s2 should be in atree: %+v", s)
	}
	if s := find(rows, "s1"); s == nil || s.Status != History || s.PaneID != "" {
		t.Fatalf("s1 should be history: %+v", s)
	}
}

func TestBuild_PaneMatchedByDescendantPID(t *testing.T) {
	p := pane("%3", "s1", zone)
	p.PID = 400
	in := Input{
		Running: []claudedata.Running{{PID: 502, SessionID: "s1", Cwd: zone, Status: claudedata.RunBusy}},
		Panes:   []tmux.Pane{p},
		Parents: map[int]int{502: 501, 501: 400, 400: 1},
		State:   emptyState(), Now: t0,
	}
	if s := find(Build(in), "s1"); s == nil || s.Status != Busy || s.PaneID != "%3" {
		t.Fatalf("got %+v", s)
	}
}
