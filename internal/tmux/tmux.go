// Package tmux drives atree's private tmux server.
//
// Window "main" holds two panes: the sidebar (index 0, left) and the slot
// (index 1, right). Every Claude session is its own pane, parked in hidden
// "bg" windows; showing one swaps it into the slot.
package tmux

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

//go:embed tmux.conf
var Conf string

// ConfFor is Conf with the atree binary path filled in for key bindings that
// run atree (⌥d).
func ConfFor(self string) string { return strings.ReplaceAll(Conf, "@ATREE@", self) }

const (
	Session      = "atree"
	mainWindow   = Session + ":main"
	SidebarWidth = 34

	RoleSidebar     = "sidebar"
	RolePlaceholder = "placeholder"
	RoleClaude      = "claude"
)

// Runner runs tmux with the given args (after "tmux -L <socket>") and returns stdout.
type Runner interface {
	Run(args ...string) (string, error)
}

type execRunner struct{ socket string }

// NewExecRunner returns a Runner for socket $ATREE_SOCKET or "atree".
func NewExecRunner() Runner {
	s := os.Getenv("ATREE_SOCKET")
	if s == "" {
		s = "atree"
	}
	return execRunner{socket: s}
}

// Socket is the tmux socket name the exec runner uses.
func Socket() string { return NewExecRunner().(execRunner).socket }

func (r execRunner) Run(args ...string) (string, error) {
	cmd := exec.Command("tmux", append([]string{"-L", r.socket}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

type Pane struct {
	ID          string
	SessionID   string
	Cwd         string
	PID         int
	Role        string
	Window      string
	CurrentPath string // the pane process's working directory
}

type Client struct {
	R      Runner
	Self   string // path to the atree binary
	Claude string // claude binary; "claude" unless overridden in tests
}

var ErrNoSlot = errors.New("atree: main window has no slot pane")

const paneFormat = "#{pane_id}\t#{pane_pid}\t#{@atree_role}\t#{@atree_session}\t#{@atree_cwd}\t#{window_name}\t#{pane_current_path}"

func parsePanes(out string) []Pane {
	var ps []Pane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 6 || f[0] == "" {
			continue
		}
		pid, _ := strconv.Atoi(f[1])
		p := Pane{ID: f[0], PID: pid, Role: f[2], SessionID: f[3], Cwd: f[4], Window: f[5]}
		if len(f) > 6 {
			p.CurrentPath = f[6]
		}
		ps = append(ps, p)
	}
	return ps
}

func (c *Client) HasServer() bool {
	_, err := c.R.Run("has-session", "-t", Session)
	return err == nil
}

// Bootstrap starts the server with a placeholder slot and the sidebar to its
// left. The slot is created first: the sidebar calls EnsureSlot as soon as it
// starts and would otherwise add a second placeholder.
func (c *Client) Bootstrap(confPath string, width, height int) error {
	slot, err := c.R.Run("-f", confPath, "new-session", "-d", "-s", Session, "-n", "main",
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height), "-P", "-F", "#{pane_id}", c.Self, "placeholder")
	if err != nil {
		return err
	}
	if err := c.tag(slot, RolePlaceholder, "", ""); err != nil {
		return err
	}
	return c.splitSidebar(slot)
}

// EnsureSidebar gives the main window a sidebar running the current atree
// binary: an existing one is restarted (it may be an older version; its state
// lives on disk), a missing one (e.g. crashed) is re-created. Sessions are
// never touched.
func (c *Client) EnsureSidebar() error {
	ps, err := c.mainPanes()
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Role == RoleSidebar {
			_, err := c.R.Run("respawn-pane", "-k", "-t", p.ID, c.Self, "sidebar")
			return err
		}
	}
	if len(ps) == 0 {
		return ErrNoSlot
	}
	return c.splitSidebar(ps[0].ID)
}

func (c *Client) splitSidebar(slot string) error {
	sidebar, err := c.R.Run("split-window", "-h", "-b", "-l", strconv.Itoa(SidebarWidth), "-t", slot,
		"-P", "-F", "#{pane_id}", c.Self, "sidebar")
	if err != nil {
		return err
	}
	return c.tag(sidebar, RoleSidebar, "", "")
}

// Panes lists every pane on the server.
func (c *Client) Panes() ([]Pane, error) {
	out, err := c.R.Run("list-panes", "-a", "-F", paneFormat)
	if err != nil {
		return nil, err
	}
	return parsePanes(out), nil
}

func (c *Client) mainPanes() ([]Pane, error) {
	out, err := c.R.Run("list-panes", "-t", mainWindow, "-F", paneFormat)
	if err != nil {
		return nil, err
	}
	return parsePanes(out), nil
}

// SlotPane is the pane shown on the right of the main window.
func (c *Client) SlotPane() (Pane, error) {
	ps, err := c.mainPanes()
	if err != nil {
		return Pane{}, err
	}
	for _, p := range ps {
		if p.Role != RoleSidebar {
			return p, nil
		}
	}
	return Pane{}, ErrNoSlot
}

// NewClaude starts claude in a hidden window. resume=true resumes sessionID;
// otherwise a new session is created with that id.
func (c *Client) NewClaude(sessionID, cwd string, resume bool) (string, error) {
	flag := "--session-id"
	if resume {
		flag = "--resume"
	}
	id, err := c.R.Run("new-window", "-d", "-t", Session+":", "-n", "bg", "-c", cwd,
		"-P", "-F", "#{pane_id}", c.Claude, flag, sessionID)
	if err != nil {
		return "", err
	}
	return id, c.tag(id, RoleClaude, sessionID, cwd)
}

// Show swaps paneID into the slot.
func (c *Client) Show(paneID string) error {
	slot, err := c.SlotPane()
	if err != nil {
		return err
	}
	if slot.ID == paneID {
		return nil
	}
	_, err = c.R.Run("swap-pane", "-d", "-s", paneID, "-t", slot.ID)
	return err
}

// Kill ends a pane. If it is in the slot, a placeholder takes its place first.
func (c *Client) Kill(paneID string) error {
	slot, err := c.SlotPane()
	if err != nil && !errors.Is(err, ErrNoSlot) {
		return err
	}
	if err == nil && slot.ID == paneID {
		ph, err := c.placeholder()
		if err != nil {
			return err
		}
		if err := c.Show(ph); err != nil {
			return err
		}
	}
	_, err = c.R.Run("kill-pane", "-t", paneID)
	return err
}

// EnsureSlot refills the slot when the pane in it has exited.
func (c *Client) EnsureSlot() error {
	ps, err := c.mainPanes()
	if err != nil {
		return err
	}
	if len(ps) != 1 {
		return nil
	}
	sidebar := ps[0].ID
	all, err := c.Panes()
	if err != nil {
		return err
	}
	for _, p := range all {
		if p.Role == RolePlaceholder {
			if _, err := c.R.Run("join-pane", "-h", "-d", "-s", p.ID, "-t", sidebar); err != nil {
				return err
			}
			return c.resizeSidebar(sidebar)
		}
	}
	return c.splitPlaceholder(sidebar)
}

// ReloadConf applies a rewritten config to a running server, so key bindings
// added in newer atree versions take effect without restarting sessions.
func (c *Client) ReloadConf(confPath string) error {
	_, err := c.R.Run("source-file", confPath)
	return err
}

// Popup opens the diff popup for dir over the current client.
func (c *Client) Popup(dir string) error {
	_, err := c.R.Run("display-popup", "-E", "-w", "90%", "-h", "90%", "-d", dir, c.Self, "diff", dir)
	return err
}

func (c *Client) FocusSlot() error {
	slot, err := c.SlotPane()
	if err != nil {
		return err
	}
	_, err = c.R.Run("select-pane", "-t", slot.ID)
	return err
}

// placeholder returns a parked placeholder pane, creating one if needed.
func (c *Client) placeholder() (string, error) {
	all, err := c.Panes()
	if err != nil {
		return "", err
	}
	for _, p := range all {
		if p.Role == RolePlaceholder {
			return p.ID, nil
		}
	}
	id, err := c.R.Run("new-window", "-d", "-t", Session+":", "-n", "bg", "-P", "-F", "#{pane_id}", c.Self, "placeholder")
	if err != nil {
		return "", err
	}
	return id, c.tag(id, RolePlaceholder, "", "")
}

func (c *Client) splitPlaceholder(sidebar string) error {
	id, err := c.R.Run("split-window", "-h", "-d", "-t", sidebar, "-P", "-F", "#{pane_id}", c.Self, "placeholder")
	if err != nil {
		return err
	}
	if err := c.tag(id, RolePlaceholder, "", ""); err != nil {
		return err
	}
	return c.resizeSidebar(sidebar)
}

func (c *Client) resizeSidebar(sidebar string) error {
	_, err := c.R.Run("resize-pane", "-t", sidebar, "-x", strconv.Itoa(SidebarWidth))
	return err
}

func (c *Client) tag(pane, role, session, cwd string) error {
	opts := [][2]string{{"@atree_role", role}}
	if session != "" {
		opts = append(opts, [2]string{"@atree_session", session})
	}
	if cwd != "" {
		opts = append(opts, [2]string{"@atree_cwd", cwd})
	}
	for _, o := range opts {
		if _, err := c.R.Run("set-option", "-p", "-t", pane, o[0], o[1]); err != nil {
			return err
		}
	}
	return nil
}
