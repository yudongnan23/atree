package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/yudongnan/agent-tree/internal/tmux"
)

func TestLaunch_RefusesInsideTmux(t *testing.T) {
	var stderr strings.Builder
	env := func(k string) string {
		if k == "TMUX" {
			return "/tmp/tmux-501/default,123,0"
		}
		return ""
	}
	if code := run([]string{"atree"}, env, &stderr); code != 1 {
		t.Fatalf("exit code %d", code)
	}
	if got := strings.TrimSpace(stderr.String()); got != "atree 不能在 tmux 里运行，请在普通 iTerm2 窗口中启动" {
		t.Fatalf("stderr %q", got)
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	var stderr strings.Builder
	if code := run([]string{"atree", "bogus"}, func(string) string { return "" }, &stderr); code != 2 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func TestRestartUntilClean(t *testing.T) {
	calls := 0
	runs := []error{errors.New("panic"), errors.New("panic"), nil}
	restartUntilClean(func() error { calls++; return runs[calls-1] }, func() {})
	if calls != 3 {
		t.Fatalf("ran %d times, want 3", calls)
	}
}

type fakeRunner struct {
	out   string
	calls [][]string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if args[0] == "list-panes" {
		return f.out, nil
	}
	return "", nil
}

func TestDiffSlot_SkipsPlaceholder(t *testing.T) {
	r := &fakeRunner{out: "%0\t1\tsidebar\t\t\tmain\t/x\n%1\t2\tplaceholder\t\t\tmain\t/"}
	if err := diffSlot(&tmux.Client{R: r, Self: "/bin/atree"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.calls {
		if c[0] == "display-popup" {
			t.Fatal("no popup for the placeholder")
		}
	}
}

func TestDiffSlot_UsesSlotCurrentPath(t *testing.T) {
	r := &fakeRunner{out: "%0\t1\tsidebar\t\t\tmain\t/x\n%5\t2\tclaude\ts1\t/repo\tmain\t/repo/sub"}
	diffSlot(&tmux.Client{R: r, Self: "/bin/atree"})
	last := r.calls[len(r.calls)-1]
	if last[0] != "display-popup" || last[len(last)-1] != "/repo/sub" {
		t.Fatalf("calls %q", r.calls)
	}
}
