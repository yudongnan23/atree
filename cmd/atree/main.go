// Command atree is a sidebar session manager for Claude Code in the terminal.
//
//	atree              start or re-attach the atree tmux session
//	atree sidebar      (internal) the sidebar program in the left pane
//	atree placeholder  (internal) what the right pane shows when no session is open
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/yudongnan/agent-tree/internal/claudedata"
	"github.com/yudongnan/agent-tree/internal/diffui"
	"github.com/yudongnan/agent-tree/internal/state"
	"github.com/yudongnan/agent-tree/internal/tmux"
	"github.com/yudongnan/agent-tree/internal/ui"
)

const (
	msgInsideTmux   = "atree 不能在 tmux 里运行，请在普通 iTerm2 窗口中启动"
	placeholderText = "← 在左边选择一个会话（⌥← 回到侧栏）"
	usage           = "usage: atree"
)

func main() {
	os.Exit(run(os.Args, os.Getenv, os.Stderr))
}

func run(args []string, env func(string) string, stderr io.Writer) int {
	var err error
	switch {
	case len(args) <= 1:
		if env("TMUX") != "" {
			fmt.Fprintln(stderr, msgInsideTmux)
			return 1
		}
		err = launch()
	case args[1] == "sidebar":
		err = sidebar()
	case args[1] == "placeholder":
		placeholder()
	case args[1] == "diff" && len(args) == 3:
		_, err = tea.NewProgram(diffui.New(args[2]), tea.WithAltScreen()).Run()
	case args[1] == "diff-slot":
		var client *tmux.Client
		if client, err = newClient(); err == nil {
			err = diffSlot(client)
		}
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "atree:", err)
		return 1
	}
	return 0
}

func newClient() (*tmux.Client, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return nil, err
	}
	return &tmux.Client{R: tmux.NewExecRunner(), Self: self, Claude: "claude"}, nil
}

// launch starts the atree tmux server if needed, then attaches to it.
func launch() error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("找不到 tmux，请先安装：brew install tmux")
	}
	client, err := newClient()
	if err != nil {
		return err
	}
	dir := state.Dir()
	if os.Getenv("ATREE_CONFIG_DIR") == "" {
		if err := state.MigrateLegacy(state.LegacyDir(), dir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	conf := filepath.Join(dir, "tmux.conf")
	if err := os.WriteFile(conf, []byte(tmux.ConfFor(client.Self)), 0o644); err != nil {
		return err
	}
	if !client.HasServer() {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			w, h = 200, 50
		}
		if err := client.Bootstrap(conf, w, h); err != nil {
			return err
		}
	} else {
		if err := client.ReloadConf(conf); err != nil {
			return err
		}
		if err := client.EnsureSidebar(); err != nil {
			return err
		}
	}
	return syscall.Exec(tmuxPath, []string{"tmux", "-L", tmux.Socket(), "attach", "-t", tmux.Session}, os.Environ())
}

func sidebar() error {
	client, err := newClient()
	if err != nil {
		return err
	}
	root := claudedata.Root()
	ctl := ui.RealController(client, claudedata.NewHistoryScanner(root), root, state.Dir())
	// The sidebar has no quit key; if it dies (a panic recovered by Bubble Tea)
	// start it again rather than leave the layout without a sidebar.
	return restartUntilClean(func() error {
		p := tea.NewProgram(ui.New(ctl), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
		_, err := p.Run()
		return err
	}, func() { time.Sleep(time.Second) })
}

// restartUntilClean calls run until it returns nil, pausing between attempts.
func restartUntilClean(run func() error, pause func()) error {
	for {
		err := run()
		if err == nil {
			return nil
		}
		fmt.Fprintln(os.Stderr, "atree sidebar:", err)
		pause()
	}
}

// diffSlot opens the diff popup for the session shown in the slot (⌥d).
// Nothing happens while the slot shows the placeholder.
func diffSlot(client *tmux.Client) error {
	slot, err := client.SlotPane()
	if err != nil || slot.Role != tmux.RoleClaude {
		return err
	}
	dir := slot.CurrentPath
	if dir == "" {
		dir = slot.Cwd
	}
	return client.Popup(dir)
}

// placeholder draws a centred hint and waits until tmux kills the pane.
func placeholder() {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	fmt.Print("\x1b[?25l") // hide cursor
	for {
		drawPlaceholder()
		select {
		case <-winch:
		case <-done:
			return
		}
	}
}

func drawPlaceholder() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}
	pad := (w - runewidth.StringWidth(placeholderText)) / 2
	if pad < 0 {
		pad = 0
	}
	text := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(placeholderText)
	fmt.Print("\x1b[2J\x1b[H" + strings.Repeat("\n", h/2) + strings.Repeat(" ", pad) + text)
}
