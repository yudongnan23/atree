// Package tree merges Claude's session data, the tmux panes and atree's state
// into the rows the sidebar renders. It is pure: no I/O.
package tree

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/yudongnan/agent-tree/internal/claudedata"
	"github.com/yudongnan/agent-tree/internal/state"
	"github.com/yudongnan/agent-tree/internal/tmux"
)

type Status int

const (
	Busy      Status = iota // ● working
	Waiting                 // ◆ finished a turn you haven't looked at
	Idle                    // ○ open in atree
	Elsewhere               // ↗ running outside atree
	History                 // · not running
)

const newSessionTitle = "新会话"

type Session struct {
	ID, Cwd, Title string
	Status         Status
	LastActive     time.Time
	PaneID         string // non-empty iff running in atree
	PID            int    // for Elsewhere
	Archived       bool
}

func (s *Session) running() bool { return s.Status != History }

type RowKind int

const (
	ProjectRow RowKind = iota
	SessionRow
	ArchivedHeaderRow
	OlderRow // "… 更早的 N 个会话" fold inside a project
)

const (
	// Sessions idle longer than this fold under the project's OlderRow.
	RecentWindow = 7 * 24 * time.Hour
	// Projects idle longer than this (and with nothing running) are archived.
	AutoArchiveAfter = 30 * 24 * time.Hour
	// Activity this soon after a manual project archive doesn't bring the
	// project back: Claude may still write the transcript of a just-closed session.
	ArchiveSlack = 10 * time.Second
)

type Row struct {
	Kind         RowKind
	Project      string // display name
	Cwd          string
	Count        int // ProjectRow: sessions; OlderRow: folded sessions; header: archived projects
	Collapsed    bool
	Session      *Session
	InSlot       bool
	Archived     bool // ProjectRow / SessionRow inside the 已归档 group
	AutoArchived bool // ProjectRow archived by the 30-day rule
	Running      bool // ProjectRow: some session in it is running
}

type Input struct {
	Running    []claudedata.Running
	History    []claudedata.History
	Panes      []tmux.Pane
	SlotPaneID string
	State      *state.State
	Now        time.Time
	// Parents maps pid → parent pid, used to find which pane a claude process
	// runs in when it is not the pane's own process. May be nil.
	Parents map[int]int
}

type project struct {
	cwd      string
	sessions []*Session
	latest   time.Time
	running  bool
}

func (p *project) autoArchived(now time.Time) bool {
	return !p.running && p.latest.Before(now.Add(-AutoArchiveAfter))
}

func (p *project) manuallyArchived(st *state.State) bool {
	at, ok := st.ArchivedProjects[p.cwd]
	return ok && !p.latest.After(at.Add(ArchiveSlack))
}

func (p *project) archived(in Input) bool {
	return p.autoArchived(in.Now) || p.manuallyArchived(in.State)
}

func groupProjects(sessions []*Session) []*project {
	byCwd := map[string]*project{}
	for _, s := range sessions {
		p := byCwd[s.Cwd]
		if p == nil {
			p = &project{cwd: s.Cwd}
			byCwd[s.Cwd] = p
		}
		p.sessions = append(p.sessions, s)
		if s.LastActive.After(p.latest) {
			p.latest = s.LastActive
		}
		if s.running() {
			p.running = true
		}
	}
	projects := make([]*project, 0, len(byCwd))
	for _, p := range byCwd {
		sort.SliceStable(p.sessions, func(i, j int) bool {
			a, b := p.sessions[i], p.sessions[j]
			if a.running() != b.running() {
				return a.running()
			}
			return newer(a, b)
		})
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		if !projects[i].latest.Equal(projects[j].latest) {
			return projects[i].latest.After(projects[j].latest)
		}
		return projects[i].cwd < projects[j].cwd
	})
	return projects
}

// ReturnedArchives lists manually archived projects that have had new activity
// since they were archived; the caller should drop their archive records.
func ReturnedArchives(in Input) []string {
	var out []string
	for _, p := range groupProjects(collect(in)) {
		if _, ok := in.State.ArchivedProjects[p.cwd]; ok && !p.manuallyArchived(in.State) {
			out = append(out, p.cwd)
		}
	}
	return out
}

func Build(in Input) []Row {
	sessions := collect(in)
	names := displayNames(sessions)
	projects := groupProjects(sessions)

	sessionRow := func(s *Session, archived bool) Row {
		return Row{Kind: SessionRow, Project: names[s.Cwd], Cwd: s.Cwd, Session: s, Archived: archived,
			InSlot: s.PaneID != "" && s.PaneID == in.SlotPaneID}
	}
	recentSince := in.Now.Add(-RecentWindow)

	var rows []Row
	var archived []*project
	for _, p := range projects {
		if p.archived(in) {
			archived = append(archived, p)
			continue
		}
		collapsed := in.State.Collapsed[p.cwd]
		rows = append(rows, Row{Kind: ProjectRow, Project: names[p.cwd], Cwd: p.cwd,
			Count: len(p.sessions), Collapsed: collapsed, Running: p.running})
		if collapsed {
			continue
		}
		var older []*Session
		for _, s := range p.sessions {
			if s.running() || (!s.Archived && !s.LastActive.Before(recentSince)) {
				rows = append(rows, sessionRow(s, false))
			} else {
				older = append(older, s)
			}
		}
		if len(older) == 0 {
			continue
		}
		expanded := in.State.ExpandedOlder[p.cwd]
		rows = append(rows, Row{Kind: OlderRow, Project: names[p.cwd], Cwd: p.cwd, Count: len(older), Collapsed: !expanded})
		if expanded {
			for _, s := range older {
				rows = append(rows, sessionRow(s, false))
			}
		}
	}
	if len(archived) > 0 {
		rows = append(rows, Row{Kind: ArchivedHeaderRow, Count: len(archived), Collapsed: !in.State.ArchivedGroupOpen})
		if in.State.ArchivedGroupOpen {
			for _, p := range archived {
				open := in.State.ArchivedOpen[p.cwd]
				rows = append(rows, Row{Kind: ProjectRow, Project: names[p.cwd], Cwd: p.cwd, Count: len(p.sessions),
					Collapsed: !open, Archived: true, AutoArchived: p.autoArchived(in.Now), Running: p.running})
				if open {
					for _, s := range p.sessions {
						rows = append(rows, sessionRow(s, true))
					}
				}
			}
		}
	}
	return rows
}

func newer(a, b *Session) bool {
	if !a.LastActive.Equal(b.LastActive) {
		return a.LastActive.After(b.LastActive)
	}
	return a.ID < b.ID
}

// collect unions history, running sessions and atree's claude panes by session ID.
func collect(in Input) []*Session {
	byID := map[string]*Session{}
	get := func(id, cwd string) *Session {
		s := byID[id]
		if s == nil {
			s = &Session{ID: id, Cwd: cwd, Title: newSessionTitle}
			byID[id] = s
		}
		if s.Cwd == "" {
			s.Cwd = cwd
		}
		return s
	}
	for _, h := range in.History {
		s := get(h.SessionID, h.Cwd)
		s.Title, s.LastActive = h.Title, h.LastActive
	}
	running := map[string]claudedata.Running{}
	for _, r := range in.Running {
		running[r.SessionID] = r
		s := get(r.SessionID, r.Cwd)
		if r.StatusUpdatedAt.After(s.LastActive) {
			s.LastActive = r.StatusUpdatedAt
		}
	}
	panes := map[string]tmux.Pane{} // session ID → the atree pane running it
	for _, p := range in.Panes {
		if p.Role != tmux.RoleClaude {
			continue
		}
		// A pane runs whatever session its process currently has (the id
		// changes on /clear); the tag is only a fallback before Claude has
		// written its sessions file.
		id := p.SessionID
		if r, ok := runningInPane(p, in); ok {
			id = r.SessionID
		}
		if id == "" {
			continue
		}
		panes[id] = p
		s := get(id, p.Cwd)
		if s.LastActive.IsZero() {
			s.LastActive = in.Now
		}
	}

	out := make([]*Session, 0, len(byID))
	for id, s := range byID {
		s.Archived = in.State.Archived[id]
		r, isRunning := running[id]
		p, inAtree := panes[id]
		switch {
		case inAtree:
			s.PaneID = p.ID
			inSlot := p.ID == in.SlotPaneID
			switch {
			case !isRunning:
				s.Status = Idle
			case r.Status == claudedata.RunBusy:
				s.Status = Busy
			case !inSlot && r.StatusUpdatedAt.After(in.State.LastViewed[id]):
				s.Status = Waiting
			default:
				s.Status = Idle
			}
		case isRunning:
			s.Status, s.PID = Elsewhere, r.PID
		default:
			s.Status = History
		}
		out = append(out, s)
	}
	return out
}

// runningInPane finds the live session whose process is the pane's process
// or one of its descendants.
func runningInPane(p tmux.Pane, in Input) (claudedata.Running, bool) {
	if p.PID == 0 {
		return claudedata.Running{}, false
	}
	for _, r := range in.Running {
		pid := r.PID
		for depth := 0; pid > 1 && depth < 32; depth++ {
			if pid == p.PID {
				return r, true
			}
			pid = in.Parents[pid]
		}
	}
	return claudedata.Running{}, false
}

// displayNames maps each cwd to its basename, or parent/basename when two
// projects share a basename.
func displayNames(sessions []*Session) map[string]string {
	count := map[string]int{}
	cwds := map[string]bool{}
	for _, s := range sessions {
		if !cwds[s.Cwd] {
			cwds[s.Cwd] = true
			count[filepath.Base(s.Cwd)]++
		}
	}
	names := map[string]string{}
	for cwd := range cwds {
		base := filepath.Base(cwd)
		if count[base] > 1 {
			base = filepath.Join(filepath.Base(filepath.Dir(cwd)), base)
		}
		names[cwd] = base
	}
	return names
}
