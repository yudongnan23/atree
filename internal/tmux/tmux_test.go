package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeRunner records argv and answers from a per-command queue.
type fakeRunner struct {
	calls   [][]string
	answers map[string][]string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	if len(args) > 2 && args[0] == "-f" { // server config flag precedes the command
		args = args[2:]
	}
	f.calls = append(f.calls, args)
	q := f.answers[args[0]]
	if len(q) == 0 {
		return "", nil
	}
	f.answers[args[0]] = q[1:]
	return q[0], nil
}

func (f *fakeRunner) find(cmd string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if c[0] == cmd {
			out = append(out, c)
		}
	}
	return out
}

func newFake(answers map[string][]string) (*fakeRunner, *Client) {
	f := &fakeRunner{answers: answers}
	return f, &Client{R: f, Self: "/usr/local/bin/atree", Claude: "claude"}
}

// paneLine builds one line of the list-panes format used by Panes/SlotPane.
func paneLine(id string, pid int, role, session, cwd, window string) string {
	return strings.Join([]string{id, fmt.Sprint(pid), role, session, cwd, window}, "\t")
}

func TestNewClaude_ResumeArgs(t *testing.T) {
	f, c := newFake(map[string][]string{"new-window": {"%7"}})
	id, err := c.NewClaude("e858", "/Users/didi/my dir", true)
	if err != nil || id != "%7" {
		t.Fatalf("got %q, %v", id, err)
	}
	want := []string{"new-window", "-d", "-t", "atree:", "-n", "bg", "-c", "/Users/didi/my dir", "-P", "-F", "#{pane_id}", "claude", "--resume", "e858"}
	if got := f.find("new-window")[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("argv\n got %q\nwant %q", got, want)
	}
	tags := f.find("set-option")
	if len(tags) != 3 {
		t.Fatalf("want 3 tag calls, got %q", tags)
	}
	wantTags := [][]string{
		{"set-option", "-p", "-t", "%7", "@atree_role", "claude"},
		{"set-option", "-p", "-t", "%7", "@atree_session", "e858"},
		{"set-option", "-p", "-t", "%7", "@atree_cwd", "/Users/didi/my dir"},
	}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("tags %q", tags)
	}
}

func TestNewClaude_NewSessionArgs(t *testing.T) {
	f, c := newFake(map[string][]string{"new-window": {"%8"}})
	c.NewClaude("1111-2222", "/tmp", false)
	got := f.find("new-window")[0]
	if tail := got[len(got)-3:]; !reflect.DeepEqual(tail, []string{"claude", "--session-id", "1111-2222"}) {
		t.Errorf("tail %q", tail)
	}
}

func TestPanes_ParsesFormat(t *testing.T) {
	out := paneLine("%0", 100, "sidebar", "", "", "main") + "\n" +
		paneLine("%3", 200, "claude", "e858", "/Users/didi/my dir", "bg")
	_, c := newFake(map[string][]string{"list-panes": {out}})
	ps, err := c.Panes()
	if err != nil {
		t.Fatal(err)
	}
	want := []Pane{
		{ID: "%0", PID: 100, Role: "sidebar", Window: "main"},
		{ID: "%3", PID: 200, Role: "claude", SessionID: "e858", Cwd: "/Users/didi/my dir", Window: "bg"},
	}
	if !reflect.DeepEqual(ps, want) {
		t.Errorf("got %+v", ps)
	}
}

const mainPanes = "%0\t100\tsidebar\t\t\tmain\n%1\t101\tplaceholder\t\t\tmain"

func TestShow_NoopWhenAlreadySlot(t *testing.T) {
	f, c := newFake(map[string][]string{"list-panes": {mainPanes}})
	if err := c.Show("%1"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("swap-pane")) != 0 {
		t.Error("swap-pane should not be called")
	}
}

func TestShow_SwapsIntoSlot(t *testing.T) {
	f, c := newFake(map[string][]string{"list-panes": {mainPanes}})
	c.Show("%5")
	want := [][]string{{"swap-pane", "-d", "-s", "%5", "-t", "%1"}}
	if got := f.find("swap-pane"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestKill_SlotSwapsPlaceholderFirst(t *testing.T) {
	slotIsClaude := "%0\t100\tsidebar\t\t\tmain\n%5\t105\tclaude\te858\t/tmp\tmain"
	all := slotIsClaude + "\n%1\t101\tplaceholder\t\t\tbg"
	f, c := newFake(map[string][]string{"list-panes": {slotIsClaude, all, slotIsClaude}})
	if err := c.Kill("%5"); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, call := range f.calls {
		if call[0] == "swap-pane" || call[0] == "kill-pane" {
			seq = append(seq, strings.Join(call, " "))
		}
	}
	want := []string{"swap-pane -d -s %1 -t %5", "kill-pane -t %5"}
	if !reflect.DeepEqual(seq, want) {
		t.Errorf("got %q", seq)
	}
}

func TestKill_NotSlotJustKills(t *testing.T) {
	f, c := newFake(map[string][]string{"list-panes": {mainPanes}})
	c.Kill("%9")
	if len(f.find("swap-pane")) != 0 || len(f.find("kill-pane")) != 1 {
		t.Errorf("calls %q", f.calls)
	}
}

func TestEnsureSlot_SplitsWhenSidebarAlone(t *testing.T) {
	alone := "%0\t100\tsidebar\t\t\tmain"
	f, c := newFake(map[string][]string{"list-panes": {alone, alone}, "split-window": {"%9"}})
	if err := c.EnsureSlot(); err != nil {
		t.Fatal(err)
	}
	sp := f.find("split-window")
	if len(sp) != 1 || !reflect.DeepEqual(sp[0][len(sp[0])-2:], []string{"/usr/local/bin/atree", "placeholder"}) {
		t.Fatalf("split %q", sp)
	}
	if rs := f.find("resize-pane"); len(rs) != 1 || !reflect.DeepEqual(rs[0], []string{"resize-pane", "-t", "%0", "-x", "34"}) {
		t.Errorf("resize %q", rs)
	}
}

func TestEnsureSlot_ReusesParkedPlaceholder(t *testing.T) {
	alone := "%0\t100\tsidebar\t\t\tmain"
	all := alone + "\n%1\t101\tplaceholder\t\t\tbg"
	f, c := newFake(map[string][]string{"list-panes": {alone, all}})
	c.EnsureSlot()
	if len(f.find("split-window")) != 0 {
		t.Error("should not create a new placeholder")
	}
	want := [][]string{{"join-pane", "-h", "-d", "-s", "%1", "-t", "%0"}}
	if got := f.find("join-pane"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestEnsureSlot_NoopWithTwoPanes(t *testing.T) {
	f, c := newFake(map[string][]string{"list-panes": {mainPanes}})
	c.EnsureSlot()
	if len(f.calls) != 1 {
		t.Errorf("calls %q", f.calls)
	}
}

func TestIntegration_Layout(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "stub")
	os.WriteFile(stub, []byte("#!/bin/sh\nexec sleep 1000\n"), 0o755)
	conf := filepath.Join(dir, "tmux.conf")
	os.WriteFile(conf, []byte(Conf), 0o644)
	t.Setenv("ATREE_SOCKET", fmt.Sprintf("atree-test-%d", os.Getpid()))
	r := NewExecRunner()
	t.Cleanup(func() { r.Run("kill-server") })
	c := &Client{R: r, Self: stub, Claude: stub}

	if c.HasServer() {
		t.Fatal("server should not exist yet")
	}
	if err := c.Bootstrap(conf, 160, 40); err != nil {
		t.Fatal(err)
	}
	if !c.HasServer() {
		t.Fatal("server should exist")
	}
	main := func() []Pane {
		out, _ := r.Run("list-panes", "-t", "atree:main", "-F", "#{pane_id}")
		var ps []Pane
		for _, l := range strings.Fields(out) {
			ps = append(ps, Pane{ID: l})
		}
		return ps
	}
	if n := len(main()); n != 2 {
		t.Fatalf("main has %d panes", n)
	}
	id, err := c.NewClaude("s1", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Show(id); err != nil {
		t.Fatal(err)
	}
	slot, err := c.SlotPane()
	if err != nil || slot.ID != id || slot.SessionID != "s1" || slot.Cwd != dir {
		t.Fatalf("slot %+v, %v (want %s)", slot, err, id)
	}
	// Claude exits on its own: the slot pane disappears.
	r.Run("kill-pane", "-t", id)
	time.Sleep(100 * time.Millisecond)
	if n := len(main()); n != 1 {
		t.Fatalf("after exit main has %d panes", n)
	}
	if err := c.EnsureSlot(); err != nil {
		t.Fatal(err)
	}
	if n := len(main()); n != 2 {
		t.Fatalf("EnsureSlot left %d panes", n)
	}
	slot, _ = c.SlotPane()
	if slot.Role != "placeholder" {
		t.Errorf("slot role %q", slot.Role)
	}
}

// The sidebar refreshes (and calls EnsureSlot) as soon as it starts, so the
// slot must exist before the sidebar process is launched.
func TestBootstrap_SlotExistsBeforeSidebar(t *testing.T) {
	f, c := newFake(map[string][]string{"new-session": {"%0"}, "split-window": {"%1"}})
	if err := c.Bootstrap("/tmp/conf", 160, 40); err != nil {
		t.Fatal(err)
	}
	ns := f.find("new-session")[0]
	if got := ns[len(ns)-2:]; !reflect.DeepEqual(got, []string{"/usr/local/bin/atree", "placeholder"}) {
		t.Fatalf("session must start with the placeholder, got %q", got)
	}
	sp := f.find("split-window")[0]
	if !reflect.DeepEqual(sp[:4], []string{"split-window", "-h", "-b", "-l"}) ||
		!reflect.DeepEqual(sp[len(sp)-2:], []string{"/usr/local/bin/atree", "sidebar"}) {
		t.Fatalf("sidebar must be split to the left: %q", sp)
	}
}

func TestEnsureSidebar_RecreatesMissingSidebar(t *testing.T) {
	slotOnly := "%5\t105\tclaude\te858\t/tmp\tmain"
	f, c := newFake(map[string][]string{"list-panes": {slotOnly}, "split-window": {"%9"}})
	if err := c.EnsureSidebar(); err != nil {
		t.Fatal(err)
	}
	sp := f.find("split-window")
	if len(sp) != 1 || !reflect.DeepEqual(sp[0], []string{"split-window", "-h", "-b", "-l", "34", "-t", "%5", "-P", "-F", "#{pane_id}", "/usr/local/bin/atree", "sidebar"}) {
		t.Fatalf("split %q", sp)
	}
	if tags := f.find("set-option"); len(tags) != 1 || tags[0][5] != "sidebar" {
		t.Fatalf("tags %q", tags)
	}
}

func TestEnsureSidebar_NoopWhenPresent(t *testing.T) {
	f, c := newFake(map[string][]string{"list-panes": {mainPanes}})
	c.EnsureSidebar()
	if len(f.find("split-window")) != 0 {
		t.Fatal("sidebar exists; nothing to do")
	}
}

func TestPopup_Args(t *testing.T) {
	f, c := newFake(nil)
	if err := c.Popup("/Users/didi/my dir"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"display-popup", "-E", "-w", "90%", "-h", "90%", "-d", "/Users/didi/my dir", "/usr/local/bin/atree", "diff", "/Users/didi/my dir"}}
	if got := f.find("display-popup"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestConfFor_SubstitutesPath(t *testing.T) {
	conf := ConfFor("/Users/didi/go/bin/atree")
	if strings.Contains(conf, "@ATREE@") || !strings.Contains(conf, `bind -n M-d run-shell -b '"/Users/didi/go/bin/atree" diff-slot'`) {
		t.Fatalf("conf:\n%s", conf)
	}
}

func TestPanes_ParsesCurrentPath(t *testing.T) {
	out := "%3\t200\tclaude\te858\t/a\tmain\t/a/sub"
	_, c := newFake(map[string][]string{"list-panes": {out}})
	ps, _ := c.Panes()
	if len(ps) != 1 || ps[0].CurrentPath != "/a/sub" {
		t.Fatalf("got %+v", ps)
	}
}

func TestReloadConf(t *testing.T) {
	f, c := newFake(nil)
	c.ReloadConf("/cfg/tmux.conf")
	if got := f.find("source-file"); len(got) != 1 || got[0][1] != "/cfg/tmux.conf" {
		t.Fatalf("got %q", got)
	}
}

func TestConf_CtrlQDetaches(t *testing.T) {
	if !strings.Contains(Conf, "bind -n C-q detach-client\n") {
		t.Fatalf("conf:\n%s", Conf)
	}
}

// An existing sidebar may be running an older atree binary; re-launching
// atree restarts it so upgrades take effect without touching sessions.
func TestEnsureSidebar_RespawnsExistingSidebar(t *testing.T) {
	f, c := newFake(map[string][]string{"list-panes": {mainPanes}})
	if err := c.EnsureSidebar(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"respawn-pane", "-k", "-t", "%0", "/usr/local/bin/atree", "sidebar"}}
	if got := f.find("respawn-pane"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if len(f.find("split-window")) != 0 {
		t.Fatal("must not add a second sidebar")
	}
}
