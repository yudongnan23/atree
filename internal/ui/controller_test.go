package ui

import (
	"strings"
	"testing"

	"github.com/yudongnan23/atree/internal/claudedata"
	"github.com/yudongnan23/atree/internal/tmux"
	"github.com/yudongnan23/atree/internal/tree"
)

type recRunner struct{ calls []string }

func (r *recRunner) Run(args ...string) (string, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	return "%1", nil
}

func TestRealController_MissingCwd(t *testing.T) {
	r := &recRunner{}
	ctl := RealController(&tmux.Client{R: r, Self: "atree", Claude: "claude"}, claudedata.NewHistoryScanner(t.TempDir()), t.TempDir(), t.TempDir())
	gone := "/nonexistent/atree-test/project"
	if err := ctl.Open(tree.Session{ID: "s1", Cwd: gone}); err == nil || !strings.Contains(err.Error(), "目录不存在") {
		t.Fatalf("Open err = %v", err)
	}
	if err := ctl.New(gone); err == nil || !strings.Contains(err.Error(), "目录不存在") {
		t.Fatalf("New err = %v", err)
	}
	for _, c := range r.calls {
		if strings.HasPrefix(c, "new-window") {
			t.Fatalf("must not start claude in a missing directory: %q", c)
		}
	}
}
