# ctree Sidebar Session Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `ctree` binary that runs a dedicated tmux server with a persistent left sidebar listing all Claude CLI sessions (grouped by project, live + history + archived) and the selected Claude session in the right pane.

**Architecture:** `ctree` (launcher) starts/attaches a private tmux server `tmux -L ctree`. Window `main` has two panes: left runs `ctree sidebar` (Bubble Tea TUI), right is the "slot" showing one Claude pane. Every Claude session is its own tmux pane, parked in hidden windows; switching = `swap-pane` into the slot. The sidebar reads Claude's on-disk data (`~/.claude/sessions`, `~/.claude/projects`), ctree's own state file, and the tmux pane list, merges them into a tree, and renders it.

**Tech Stack:** Go 1.26, `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles/textinput`, `github.com/charmbracelet/lipgloss`, tmux 3.8, standard `testing` package.

**Spec:** `docs/superpowers/specs/2026-10-19-ctree-sidebar-design.md`

## Global Constraints

- Module path `github.com/yudongnan/claude-tree`; binary `ctree` built from `./cmd/ctree`.
- All tmux calls use `-L ctree` (overridable by env `CTREE_SOCKET`, used by tests). Never touch the user's default tmux server or `~/.tmux.conf`; the server is started with `-f <ctree's own conf>`.
- Never modify any file under `~/.claude`. ctree's own state lives in `~/.config/ctree/state.json` (dir overridable by env `CTREE_CONFIG_DIR`). Claude data root overridable by env `CTREE_CLAUDE_DIR` (default `~/.claude`).
- Never start a second claude process for a session that is already running (in ctree or elsewhere).
- Status glyphs/colors exactly: `●` green busy, `◆` red waiting-for-you, `○` blue idle-in-ctree, `↗` purple running elsewhere, `·` gray history.
- Keys: `⌥←` (tmux `bind -n M-Left`) focus sidebar; `⌥→` / `esc` focus Claude; `↑↓`/`j k` move; `⏎`/mouse click open; `←→` collapse/expand; `n` new in project; `N` new in directory; `x` close; `a` archive/unarchive. tmux prefix disabled (`set -g prefix None`).
- Refuse to run inside another tmux (`$TMUX` set): print `ctree 不能在 tmux 里运行，请在普通 iTerm2 窗口中启动` and exit 1.
- Message copy for elsewhere-running: `这个会话正在别的终端里运行（pid %d），请到那个窗口操作，或先在那边退出再从这里打开`.
- Out of scope: worktrees, rename, delete, search, auto-restore after reboot, `tmux -CC`.
- Paths with spaces/unicode must work: commands are passed to tmux as separate argv, never as a shell string.

## Review Focus

1. Stale `~/.claude/sessions/<pid>.json` whose pid is dead (crash, reboot) → must be ignored, session shows as history `·`, never `↗`. (Task 1)
2. A `.jsonl` being written while read (truncated last line, huge 13MB file, non-JSON lines) → no crash, line skipped, rescans only changed files. (Task 2)
3. Two projects with the same basename (`~/a/api`, `~/b/api`) → disambiguated as `a/api`, `b/api`. (Task 5)
4. Claude in the right slot exits on its own (`/exit`, crash) → slot is refilled with the placeholder; layout never collapses to sidebar-only. (Task 4, Task 6)
5. Opening a history session twice quickly, or opening one that started running elsewhere since the last refresh → no second claude process; re-check running state right before spawning. (Task 6)

---

## File Structure

```
cmd/ctree/main.go              launcher + subcommands: (none)=launch, sidebar, placeholder
internal/claudedata/running.go running sessions from sessions/*.json
internal/claudedata/history.go history sessions from projects/*/*.jsonl, with cache
internal/state/state.go        archive / lastViewed / collapsed, atomic JSON file
internal/tmux/tmux.go          Runner, Client: layout + pane operations
internal/tmux/tmux.conf        embedded server config
internal/tree/tree.go          pure merge of all inputs → []Row
internal/ui/model.go           Bubble Tea model (sidebar)
internal/ui/newdir.go          `N` modal with path completion
internal/ui/view.go            rendering
README.md                      install + iTerm2 Option-key setup
```

---

### Task 1: Module scaffold + running sessions

**Files:**
- Create: `go.mod`, `internal/claudedata/running.go`, `internal/claudedata/running_test.go`

**Interfaces:**
- Produces:
  ```go
  type RunStatus int // RunBusy, RunIdle, RunOther
  type Running struct {
      PID int; SessionID, Cwd, Name string
      Status RunStatus; StatusUpdatedAt time.Time
  }
  func Root() string                                   // $CTREE_CLAUDE_DIR or ~/.claude
  func LoadRunning(root string, alive func(pid int) bool) ([]Running, error)
  func PIDAlive(pid int) bool                          // syscall.Kill(pid, 0) == nil || err == EPERM
  ```

- [ ] **Step 1:** `go mod init github.com/yudongnan/claude-tree`; add `.gitignore` entry `/ctree`.
- [ ] **Step 2: Write failing tests** in `running_test.go` using `t.TempDir()` with `sessions/` fixtures shaped like the real file (`{"pid":85329,"sessionId":"bb02…","cwd":"/Users/didi/project/claude-tree","name":"claude-tree-aa","status":"busy","updatedAt":…,"statusUpdatedAt":1792414047032}`):
  - `TestLoadRunning_ParsesFields`: status `"busy"`→`RunBusy`, `"idle"`→`RunIdle`, `"whatever"`→`RunOther`; `StatusUpdatedAt` from ms.
  - `TestLoadRunning_SkipsDeadPID`: `alive` returns false for pid 62386 → not in result.
  - `TestLoadRunning_IgnoresNonJSONAndKeyFiles`: `*.key` files and a malformed `.json` are skipped, no error.
  - `TestLoadRunning_MissingDir`: no `sessions/` dir → empty slice, nil error.
- [ ] **Step 3:** Run `go test ./internal/claudedata/ -run Running -v` → FAIL (undefined).
- [ ] **Step 4:** Implement in `running.go`. Fall back to `updatedAt` when `statusUpdatedAt` missing.
- [ ] **Step 5:** Run tests → PASS.
- [ ] **Step 6:** Commit `feat: read running Claude sessions`.

---

### Task 2: History sessions with cache

**Files:**
- Create: `internal/claudedata/history.go`, `internal/claudedata/history_test.go`

**Interfaces:**
- Produces:
  ```go
  type History struct {
      SessionID, Cwd, Title string
      LastActive time.Time // file mtime
  }
  type HistoryScanner struct{ /* cache map[path]{size, mtime, History} */ }
  func NewHistoryScanner(root string) *HistoryScanner
  func (s *HistoryScanner) Scan() ([]History, error)
  ```

Rules (from spec): only top-level `projects/*/<uuid>.jsonl` (ignore subdirs, `*.desktop-released.json`, `memory/`). `SessionID` = file stem. `Cwd` = `cwd` of the first record that has one (never derived from the lossy dir name). `Title` = last `custom-title.customTitle`; else last `last-prompt.lastPrompt`; whitespace collapsed, truncated to 40 runes with `…`; else `(无标题)`. Skip files with no `cwd`. Stream with `bufio.Reader` (lines can exceed 64KB — don't use default `Scanner` buffer); only `json.Unmarshal` lines that start with `{"type":"custom-title"`, `{"type":"last-prompt"`, or (until cwd found) contain `"cwd":"`. Re-read a file only when size or mtime changed.

- [ ] **Step 1: Write failing tests:**
  - `TestScan_TitlePrefersCustomTitle`: file with `last-prompt` "你确定…" then two `custom-title` ("旧", "你好") → Title `"你好"`, Cwd `/Users/didi/project/zone`.
  - `TestScan_TitleFallsBackToLastPrompt` and `TestScan_UntitledFallback` (`"(无标题)"`).
  - `TestScan_TruncatesLongTitle`: 60-rune prompt → 40 runes + `…`.
  - `TestScan_SkipsNonSessionFiles`: `x.desktop-released.json`, `memory/a.md`, subdir `<uuid>/sub.jsonl` ignored.
  - `TestScan_TolerantOfBadLines`: a 200KB single line, a non-JSON line, and a truncated final line → still parsed, no error.
  - `TestScan_UsesCache`: scan, then replace file content without changing size+mtime (`os.Chtimes` back) → old title returned; change mtime → new title.
- [ ] **Step 2:** Run `go test ./internal/claudedata/ -run Scan -v` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run → PASS. Also run once against real data: `go test ./internal/claudedata/ -run Scan` plus a throwaway `go run` that prints `len(Scan())` for `~/.claude` (don't commit it); expect ≥ 13 projects' sessions, under 1s.
- [ ] **Step 5:** Commit `feat: scan Claude session history`.

---

### Task 3: ctree state file

**Files:**
- Create: `internal/state/state.go`, `internal/state/state_test.go`

**Interfaces:**
- Produces:
  ```go
  type State struct {
      Archived   map[string]bool      `json:"archived"`   // sessionID
      LastViewed map[string]time.Time `json:"lastViewed"` // sessionID
      Collapsed  map[string]bool      `json:"collapsed"`  // project cwd
      ArchivedGroupOpen bool          `json:"archivedGroupOpen"`
  }
  func Dir() string                  // $CTREE_CONFIG_DIR or ~/.config/ctree
  func Load(dir string) (*State, error) // missing file → empty State with non-nil maps
  func (s *State) Save(dir string) error // write tmp file + os.Rename
  ```

- [ ] **Step 1: Failing tests:** `TestLoad_Missing` (non-nil maps), `TestSaveLoad_RoundTrip`, `TestLoad_Corrupt` (garbage file → returns empty State and nil error, so a bad file never blocks the UI), `TestSave_Atomic` (no `*.tmp` left behind).
- [ ] **Step 2:** Run `go test ./internal/state/ -v` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat: persist ctree state`.

---

### Task 4: tmux client

**Files:**
- Create: `internal/tmux/tmux.go`, `internal/tmux/tmux.conf`, `internal/tmux/tmux_test.go`

**Interfaces:**
- Produces:
  ```go
  type Runner interface{ Run(args ...string) (string, error) } // args after "tmux -L <socket>"
  func NewExecRunner() Runner        // socket = $CTREE_SOCKET or "ctree"
  //go:embed tmux.conf
  var Conf string
  type Pane struct{ ID, SessionID, Cwd string; PID int; Role string } // Role: "sidebar" | "placeholder" | "claude"
  type Client struct{ R Runner; Self string } // Self = path to ctree binary
  func (c *Client) HasServer() bool
  func (c *Client) Bootstrap(confPath string, width, height int) error
  func (c *Client) Panes() ([]Pane, error)
  func (c *Client) SlotPane() (Pane, error)          // pane in window "main" whose @ctree_role != sidebar
  func (c *Client) NewClaude(sessionID, cwd string, resume bool) (paneID string, err error)
  func (c *Client) Show(paneID string) error         // swap into slot, no-op if already there
  func (c *Client) Kill(paneID string) error
  func (c *Client) EnsureSlot() error                // refill slot with placeholder if main has only the sidebar
  func (c *Client) FocusSlot() error
  ```

Decisions:
- Pane roles are tmux user options on the pane: `@ctree_role`, `@ctree_session`, `@ctree_cwd` (set with `set-option -p -t <pane>`). `Panes()` uses `list-panes -a -F '#{pane_id}\t#{pane_pid}\t#{@ctree_role}\t#{@ctree_session}\t#{@ctree_cwd}\t#{window_name}'`.
- `Bootstrap`: `-f confPath new-session -d -s ctree -n main -x W -y H <Self> sidebar`, tag it `sidebar`, then `split-window -h -t ctree:main <Self> placeholder` tagged `placeholder`, then `resize-pane -t <sidebar> -x 34`.
- `NewClaude`: `new-window -d -t ctree: -n bg -c <cwd> -P -F '#{pane_id}' claude --resume <id>` (or `claude --session-id <id>` when `resume=false`), then tag `claude`/session/cwd. Pass `claude` and its args as separate argv elements.
- `Show`: `swap-pane -d -s <paneID> -t <slot>`.
- `Kill`: if the pane is the slot, first `Show` the placeholder (create one via `new-window -d … <Self> placeholder` if none exists), then `kill-pane`.
- `EnsureSlot`: if `main` has one pane, `split-window -h` a placeholder and resize sidebar to 34.
- `tmux.conf`: `set -g prefix None`, `unbind C-b`, `set -g mouse on`, `set -g focus-events on`, `set -g status off`, `set -g remain-on-exit off`, `set -g pane-active-border-style fg=blue`, `bind -n M-Left select-pane -t ctree:main.0`, `bind -n M-Right select-pane -t ctree:main.1` (sidebar is created first so it is index 0; the slot stays index 1 because `swap-pane` keeps positions), `set -g default-terminal tmux-256color`, `set -g escape-time 10`.

- [ ] **Step 1: Unit tests with a fake Runner** recording argv: `TestNewClaude_ResumeArgs` (exact argv incl. `-c "/Users/didi/my dir"` as one element and `claude --resume <id>`), `TestNewClaude_NewSessionArgs` (`claude --session-id <id>`), `TestPanes_ParsesFormat`, `TestShow_NoopWhenAlreadySlot`, `TestKill_SlotSwapsPlaceholderFirst`, `TestEnsureSlot_SplitsWhenSidebarAlone`.
- [ ] **Step 2: Integration test** `TestIntegration_Layout` (skipped if `tmux` not on PATH): `CTREE_SOCKET=ctree-test-<pid>`, `Self` = `/bin/sh` stand-in via `sleep 1000` commands; Bootstrap → 2 panes in `main`; `NewClaude` with `claude` replaced by `sleep` (make the claude binary name a `Client` field `Claude string`, default `"claude"`) → `Show` → slot pane ID equals new pane; kill slot pane process externally → `EnsureSlot` restores 2 panes. `t.Cleanup` runs `kill-server`.
- [ ] **Step 3:** Run `go test ./internal/tmux/ -v` → FAIL.
- [ ] **Step 4:** Implement.
- [ ] **Step 5:** Run → PASS (integration test runs, not skipped).
- [ ] **Step 6:** Commit `feat: tmux client for ctree layout`.

---

### Task 5: Tree builder

**Files:**
- Create: `internal/tree/tree.go`, `internal/tree/tree_test.go`

**Interfaces:**
- Consumes: `claudedata.Running`, `claudedata.History`, `tmux.Pane`, `state.State`.
- Produces:
  ```go
  type Status int // Busy, Waiting, Idle, Elsewhere, History
  type Session struct {
      ID, Cwd, Title string; Status Status; LastActive time.Time
      PaneID string // non-empty iff running in ctree
      PID int       // for Elsewhere
      Archived bool
  }
  type RowKind int // ProjectRow, SessionRow, ArchivedHeaderRow
  type Row struct {
      Kind RowKind; Project string /*display name*/; Cwd string
      Count int /*sessions when collapsed*/; Collapsed bool
      Session *Session; InSlot bool
  }
  type Input struct {
      Running []claudedata.Running; History []claudedata.History
      Panes []tmux.Pane; SlotPaneID string; State *state.State; Now time.Time
  }
  func Build(in Input) []Row
  ```

Rules:
- Session set = union of History, Running, and `claude` Panes (a just-started pane may have no file yet; its Title is `新会话`, Cwd from `@ctree_cwd`).
- Status: has ctree pane → from Running by SessionID: `RunBusy`→Busy; else Waiting if `StatusUpdatedAt` > `State.LastViewed[id]` and not `InSlot`; else Idle (no Running record yet → Idle). Running but no ctree pane → Elsewhere. Otherwise History.
- Projects keyed by Cwd; display name = basename, or `parent/basename` when two projects share a basename. Projects sorted by max LastActive desc; inside, running (Busy/Waiting/Idle/Elsewhere) first then history, each by LastActive desc.
- Archived sessions excluded from projects; a project with no unarchived sessions is omitted. If any archived exist, append `ArchivedHeaderRow` (Count = n); when `State.ArchivedGroupOpen`, follow with their SessionRows (Project set for display), LastActive desc.
- Collapsed project → only its ProjectRow with Count.

- [ ] **Step 1: Failing tests:** `TestBuild_GroupsAndOrders`, `TestBuild_StatusMapping` (one case per Status incl. Waiting vs Idle via LastViewed and InSlot suppressing Waiting), `TestBuild_NewPaneWithoutFile`, `TestBuild_DuplicateBasenames` (`a/api`, `b/api`), `TestBuild_ArchivedGroup` (closed → header only; open → header + rows; fully archived project omitted), `TestBuild_Collapsed`.
- [ ] **Step 2:** Run `go test ./internal/tree/ -v` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat: build sidebar tree`.

---

### Task 6: Sidebar UI

**Files:**
- Create: `internal/ui/model.go`, `internal/ui/view.go`, `internal/ui/model_test.go`
- Modify: `go.mod` (add bubbletea, bubbles, lipgloss)

**Interfaces:**
- Consumes: `tree.Build`, `tree.Row`, `state.State`, `tmux.Client` methods via:
  ```go
  type Controller interface {
      Snapshot() (tree.Input, error)              // reloads running/history/panes/state
      Open(s tree.Session) error                  // Show existing pane, or NewClaude(resume=true)+Show; then FocusSlot
      New(cwd string) error                       // NewClaude(uuid, resume=false)+Show+FocusSlot
      Close(paneID string) error
      EnsureSlot() error
      FocusSlot() error
      SaveState(*state.State) error
  }
  func New(c Controller) tea.Model
  ```
- Produces: `ui.New`, and `ui.RealController(client *tmux.Client, hist *claudedata.HistoryScanner, root, cfgDir string) Controller` (in `model.go`).

Behavior:
- Refresh every 1s (`tea.Tick`): `EnsureSlot()`, `Snapshot()`, `tree.Build`; keep cursor on the same session ID / project across rebuilds.
- `⏎`/click on SessionRow: Elsewhere → toast with the exact copy from Global Constraints; otherwise **re-Snapshot first** and re-check the session is not now Elsewhere / already has a pane (Review Focus 5), then `Open`, set `LastViewed[id]=now`, and if Archived, unarchive. On ProjectRow/ArchivedHeaderRow: toggle collapse / `ArchivedGroupOpen`.
- `←`/`→`: collapse/expand the project under cursor (on a SessionRow, `←` collapses its project and moves cursor to it).
- `n`: `New(cwd of project under cursor)`.
- `x`: only for sessions with PaneID; ask `关闭这个会话？(y/n)`; `y` → `Close`.
- `a`: Elsewhere → toast `在别处运行的会话不能归档`; has PaneID → confirm `归档会结束这个会话，继续？(y/n)` then `Close` + archive; else toggle `Archived`. Save state after each change.
- `esc` / `⌥→` handled by tmux; inside the TUI `esc` calls `FocusSlot()`.
- Track focus with `tea.FocusMsg`/`tea.BlurMsg` (enable `tea.WithReportFocus()`); footer key help only when focused; otherwise footer `⌥← 回到侧栏`.
- Mouse: `tea.WithMouseCellMotion()`; left-click on a row = move cursor + `⏎`.
- Toasts disappear after 4s or on next key.
- View: header `Claude Tree`; ProjectRow `▾ name` / `▸ name   N` (yellow); SessionRow `  <glyph> title` with date `01-02` right-aligned for History; `InSlot` row bold white; cursor row reversed when focused; ArchivedHeaderRow `▸ 已归档  N`. Glyph colors per Global Constraints. Truncate to pane width (use `lipgloss.Width` for CJK).

- [ ] **Step 1: Failing tests** with a fake Controller and fixed `tree.Input`, driving `Update` with `tea.KeyMsg`s:
  - `TestEnter_OpensHistory` (Open called with that session; LastViewed set; SaveState called).
  - `TestEnter_ElsewhereShowsToast` (Open not called; view contains `pid 62386`).
  - `TestEnter_RechecksBeforeSpawn`: first Snapshot says History, the re-Snapshot says Elsewhere → Open not called, toast shown.
  - `TestClose_RequiresConfirm` (`x` then `n` → no Close; `x` then `y` → Close).
  - `TestArchive_Toggle`, `TestArchive_RunningConfirmsAndCloses`, `TestArchive_ElsewhereRefused`.
  - `TestEnter_ArchivedSessionUnarchives`.
  - `TestCursorStableAcrossRefresh` (new session inserted above → cursor stays on same ID).
  - `TestView_Glyphs` (each status renders its glyph; footer differs focused/blurred).
  - `TestRefresh_CallsEnsureSlot` (Review Focus 4).
- [ ] **Step 2:** Run `go test ./internal/ui/ -v` → FAIL.
- [ ] **Step 3:** Implement `model.go`, `view.go`, `RealController` (UUIDs via `crypto/rand` v4 formatting; no extra dependency).
- [ ] **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat: sidebar TUI`.

---

### Task 7: `N` new-directory modal

**Files:**
- Create: `internal/ui/newdir.go`, `internal/ui/newdir_test.go`
- Modify: `internal/ui/model.go` (open modal on `N`, route keys while open)

**Interfaces:**
- Produces: `func Complete(input string) (completed string, candidates []string)` — expands leading `~`, lists directories only (hidden dirs only if the typed segment starts with `.`), returns the longest common prefix with trailing `/` when unique; candidates displayed with `~` re-abbreviated, max 5.

Modal: title `在新目录开会话`, `textinput` prompt `目录: `, `tab` → Complete, `⏎` → if the expanded path is an existing directory, `New(path)` and close; else inline error `目录不存在`. `esc` cancels. Footer `⏎ 确定  tab 补全  esc 取消`.

- [ ] **Step 1: Failing tests** with a temp HOME tree: `TestComplete_Unique` (`~/proj/new-thi` → `~/proj/new-thing/`), `TestComplete_Ambiguous` (`~/proj/new-` → candidates `new-api/`,`new-thing/`, completed stays at common prefix), `TestComplete_FilesExcluded`, `TestModal_EnterNonexistentShowsError`, `TestModal_EnterExistingCallsNew`.
- [ ] **Step 2:** Run `go test ./internal/ui/ -run 'Complete|Modal' -v` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `go test ./internal/ui/ -v` → all PASS.
- [ ] **Step 5:** Commit `feat: open session in a new directory`.

---

### Task 8: Launcher, subcommands, README, end-to-end check

**Files:**
- Create: `cmd/ctree/main.go`, `cmd/ctree/main_test.go`, `README.md`

**Interfaces:**
- Consumes: everything above.
- Produces: `ctree` binary. `ctree` = launch/attach; `ctree sidebar` = run `ui.New(RealController(...))` with alt screen, focus reporting, mouse; `ctree placeholder` = print centered dim text `← 在左边选择一个会话（⌥← 回到侧栏）` and block until signaled.

Launch: `$TMUX` set → exact error copy from Global Constraints, exit 1. Write `tmux.Conf` to `<state.Dir()>/tmux.conf` (overwrite each launch). If `!HasServer()` → `Bootstrap(conf, termWidth, termHeight)` (size from `golang.org/x/term` or `tput`, fallback 200×50). Then `syscall.Exec` tmux `-L <socket> attach -t ctree`.

- [ ] **Step 1: Failing test** `TestLaunch_RefusesInsideTmux` (factor launch into `run(args []string, env func(string) string, stderr io.Writer) int`; with `TMUX=/tmp/x` → returns 1 and stderr has the exact copy).
- [ ] **Step 2:** Run `go test ./cmd/ctree/ -v` → FAIL.
- [ ] **Step 3:** Implement `main.go`.
- [ ] **Step 4:** Run `go test ./... ` → all PASS; `go vet ./...` clean; `go build -o ctree ./cmd/ctree`.
- [ ] **Step 5: README.md** — install (`go install github.com/yudongnan/claude-tree/cmd/ctree@latest` or `go build`), **iTerm2 setup: Settings → Profiles → Keys → Left Option key → `Esc+`** (required for `⌥←/⌥→`), key table from the spec, "关掉 iTerm2 后再次运行 `ctree` 回到原状态", "重启后会话以历史形式出现", "不支持在 tmux 里运行 / 不支持 -CC 模式", where state lives.
- [ ] **Step 6: End-to-end check in a real terminal** (with `CTREE_SOCKET=ctree-e2e`): run `./ctree` in iTerm2 and verify, noting results in the commit message:
  1. Sidebar shows real projects from `~/.claude/projects`, history `·` with dates.
  2. `⏎` on a history session → Claude resumes in the right pane, focus moves right; `⌥←` returns to sidebar.
  3. Open a second session, switch back and forth; the first keeps running (glyph `●`/`○`).
  4. `n` starts a new session in that project; `N` with tab completion opens one in another dir.
  5. A session running in a separate iTerm2 tab shows `↗`; `⏎` shows the toast.
  6. `/exit` in the right Claude → placeholder appears in slot.
  7. `a` archives; it moves under `已归档`; `⏎` there reopens and unarchives.
  8. Close the iTerm2 window, run `./ctree` again → same layout and sessions.
  9. Run `./ctree` inside a tmux → refusal message.
  Then `tmux -L ctree-e2e kill-server`.
- [ ] **Step 7:** Commit `feat: ctree launcher and docs`.
