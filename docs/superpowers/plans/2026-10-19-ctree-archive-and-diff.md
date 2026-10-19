# ctree Archive, Older-Session Folding and Diff Popup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add project-level archiving (auto at 30 days + manual), session-level archiving into a per-project "更早的 N 个会话" fold (with 7-day folding), and a read-only git diff popup (`d` / `⌥d`) with a full repo tree.

**Architecture:** `tree.Build` stays pure and gains the 7/30-day rules, an `OlderRow`, and archived-project rows. The sidebar (`internal/ui`) handles new keys and clears returned manual project archives. A new `internal/gitdiff` package reads git state; a new `internal/diffui` Bubble Tea program renders it, launched as `ctree diff <dir>` inside `tmux display-popup`.

**Tech Stack:** Go 1.26, Bubble Tea v1.3, lipgloss, go-runewidth, git CLI, tmux 3.8.

**Spec:** `docs/superpowers/specs/2026-10-19-ctree-archive-and-diff-design.md` (extends `docs/superpowers/specs/2026-10-19-ctree-sidebar-design.md`)

## Global Constraints

- Thresholds are constants: `recentWindow = 7 * 24h`, `autoArchiveAfter = 30 * 24h`; manual-archive return slack `archiveSlack = 10s`.
- Never write under `~/.claude`. Diff popup is read-only: only `git status`, `git ls-files`, `git diff`, `git rev-parse`, `git symbolic-ref`, and file reads.
- Copy (exact):
  - older fold row: `… 更早的 %d 个会话`
  - refuse project archive: `这个 project 还有正在运行的会话，请先关闭`
  - auto-archived project `a`: `30 天内没有活动的 project 会自动归档，打开其中一个会话即可恢复`
  - not a repo: `这个目录不是 git 仓库`
  - no changes: `没有未提交的改动`
  - stats: `%d 个文件修改，%d 个新增，%d 个删除` (omit zero parts, joined by `，`)
  - binary: `二进制文件`
- Diff colors: modified `#e0af68` (`M`), added/untracked `#9ece6a` (`A`), deleted `#f7768e` + strikethrough (`D`), diff `+` green, `-` red, `@@`/headers `#7aa2f7`.
- Existing behavior not mentioned in the spec stays unchanged; existing tests keep passing except where this plan rewrites their expectations (noted per task).

## Review Focus

1. A project manually archived, then a session closed right after (Claude writes the transcript late) → must stay archived (10s slack). (Task 2, Task 3)
2. A project with a running session that is >30 days old by transcript mtime (e.g. long-idle session resumed elsewhere) → never auto-archived. (Task 2)
3. Huge repo (50k files) in the diff popup → tree builds and first frame renders in <1s; directories are folded lazily. (Task 4)
4. Paths with spaces/unicode and renamed files in `git status` → parsed with `-z`; rename shows old D + new A. (Task 4)
5. A repo with no commits (`HEAD` missing) → all files shown as added, no error. (Task 4)

---

## File Structure

```
internal/state/state.go       + ArchivedProjects, ExpandedOlder, ArchivedOpen
internal/tree/tree.go         rules rewrite: OlderRow, archived projects, ReturnedArchives
internal/ui/model.go          keys for OlderRow / project a / d; clear returned archives
internal/ui/view.go           render OlderRow, dim archived sessions
internal/gitdiff/gitdiff.go   repo status, file diff
internal/gitdiff/tree.go      file tree from status
internal/diffui/model.go      popup TUI
internal/tmux/tmux.go         Popup(); conf template with ctree path
internal/tmux/tmux.conf       + M-d binding
cmd/ctree/main.go             `diff <dir>` and `diff-slot` subcommands
README.md                     archive + diff docs
```

---

### Task 1: State fields

**Files:** Modify `internal/state/state.go`; Test `internal/state/state_test.go`

**Interfaces:**
- Produces: `State.ArchivedProjects map[string]time.Time` (`json:"archivedProjects"`, cwd → archived at), `State.ExpandedOlder map[string]bool` (`json:"expandedOlder"`), `State.ArchivedOpen map[string]bool` (`json:"archivedOpen"`); all non-nil after `Load`.

- [ ] **Step 1:** Extend `TestLoad_Missing` and `TestSaveLoad_RoundTrip` to cover the three maps.
- [ ] **Step 2:** `go test ./internal/state/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/state/` → PASS.
- [ ] **Step 5:** Commit `feat: state for project archive and older-session folds`.

---

### Task 2: Tree rules

**Files:** Modify `internal/tree/tree.go`; Test `internal/tree/tree_test.go`

**Interfaces:**
- Consumes: Task 1 state fields.
- Produces:
  ```go
  const OlderRow RowKind // after ArchivedHeaderRow
  // Row additions:
  //   Archived bool      // ProjectRow / SessionRow under the 已归档 group
  //   AutoArchived bool  // ProjectRow under 已归档 that is there by the 30-day rule
  //   Running bool       // ProjectRow: some session in it is running
  func ReturnedArchives(in Input) []string // cwds in State.ArchivedProjects whose activity > archivedAt + 10s
  ```

Rules (spec §一): project activity = max LastActive over all its sessions incl. archived ones. Project is in 已归档 iff (no running session AND activity < Now-30d) OR (ArchivedProjects[cwd] set AND activity <= archivedAt+10s). Main list: per project, `visible` = sessions (running OR (not Archived AND LastActive >= Now-7d)); `older` = the rest. Rows: ProjectRow (Count = all sessions), visible SessionRows, then OlderRow (Count = len(older)) if older non-empty, then older SessionRows if `ExpandedOlder[cwd]`. Collapsed project hides all of these. Archived group: ArchivedHeaderRow (Count = number of archived projects, Collapsed = !ArchivedGroupOpen); when open, each archived project as ProjectRow{Archived:true, AutoArchived, Collapsed: !ArchivedOpen[cwd]} followed (if open) by all its SessionRows (Archived row flag true), no 7-day fold. Session `Archived` (manual) no longer removes it from its project.

Existing tests to rewrite: `TestBuild_ArchivedGroup` (session archive now stays in project under OlderRow). Others keep passing with `Now: t0` and recent fixtures (fixtures older than 7 days must be adjusted or expected under OlderRow — keep their intent).

- [ ] **Step 1: Failing tests:**
  - `TestBuild_OlderFold`: sessions at 1d, 3d, 10d, 20d → rows `P zone, s1d, s3d, O(2)`; with `ExpandedOlder[zone]` → `..., O(2), s10d, s20d`.
  - `TestBuild_RunningOldSessionStaysVisible`: running session with LastActive 15d → visible, not under O.
  - `TestBuild_ManualSessionArchiveGoesToOlder`: archived 1h-old session → under O, project stays in main list.
  - `TestBuild_OnlyOlder`: project with only 10d sessions → `P zone, O(n)`.
  - `TestBuild_AutoArchiveProject`: project at 31d → not in main; header Count 1; open group + `ArchivedOpen` → `A, P old (archived, auto), sessions…` all listed.
  - `TestBuild_AutoArchiveSkippedWhenRunning`: 40d project with a running (Elsewhere) session → stays in main.
  - `TestBuild_ManualProjectArchive`: `ArchivedProjects[zone]=t0-1h`, activity t0-2h → archived; activity t0-1h+5s → still archived (slack); activity t0-30m → main list, and `ReturnedArchives` returns `[zone]`.
  - `TestBuild_ProjectsSortedByActivity`: unchanged ordering rule, including archived-session activity.
  - Rewrite `TestBuild_ArchivedGroup` per above.
- [ ] **Step 2:** `go test ./internal/tree/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/tree/` → PASS.
- [ ] **Step 5:** Commit `feat: fold older sessions and archive idle projects`.

---

### Task 3: Sidebar behavior for archive and folds

**Files:** Modify `internal/ui/model.go`, `internal/ui/view.go`; Test `internal/ui/model_test.go`

**Interfaces:**
- Consumes: Task 2 rows, `tree.ReturnedArchives`.
- Produces: no new interfaces (`Controller.Diff` and the `d` key arrive in Task 6).

Behavior:
- refresh: after Build, for each cwd in `ReturnedArchives(in)` delete `ArchivedProjects[cwd]`, save, rebuild.
- OlderRow: `⏎`/click/`→` toggle/expand `ExpandedOlder[cwd]`; `←` collapse. `←` on a SessionRow listed under an expanded OlderRow collapses the fold and lands on the OlderRow.
- ProjectRow in 已归档: `⏎`/`→`/`←` toggle `ArchivedOpen[cwd]`.
- `a` on ProjectRow (main list): if `Running` → toast refuse copy; else `ArchivedProjects[cwd]=now`, save. `a` on archived ProjectRow: AutoArchived → toast auto copy; else delete entry, save.
- Opening any session whose row is `Archived` (inside 已归档) → also delete `ArchivedProjects[cwd]`.
- View: OlderRow renders `  … 更早的 N 个会话` dim, with `▸`/`▾` not needed; archived ProjectRows render like ProjectRows (dim); SessionRows with `Session.Archived` render title dim.

Existing tests to rewrite: `TestArchive_Toggle`, `TestEnter_ArchivedSessionUnarchives` (archived session now sits under OlderRow, not the archived group). Fixtures in `historyOnly()` are 1h/2h old — stay visible.

- [ ] **Step 1: Failing tests:** `TestOlderRow_EnterExpands`, `TestOlderRow_LeftFromChildCollapses`, `TestProjectArchive_Manual`, `TestProjectArchive_RefusedWhenRunning` (view contains refuse copy), `TestProjectArchive_AutoToast`, `TestProjectArchive_Unarchive`, `TestRefresh_ClearsReturnedArchive` (SaveState called, entry gone), `TestOpenFromArchivedProjectClearsArchive`, `TestView_OlderRowText` (`… 更早的 2 个会话`); rewrite the two tests above.
- [ ] **Step 2:** `go test ./internal/ui/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5:** Commit `feat: sidebar keys for archived projects and older sessions`.

---

### Task 4: gitdiff package

**Files:** Create `internal/gitdiff/gitdiff.go`, `internal/gitdiff/tree.go`, `internal/gitdiff/gitdiff_test.go`

**Interfaces:**
- Produces:
  ```go
  type Status byte // Unchanged=0, Modified='M', Added='A', Deleted='D'
  type File struct{ Path string; Status Status } // repo-relative, '/' separated
  type Repo struct {
      Root, Name, Branch string // Name = basename(Root); Branch or short hash when detached
      Files []File              // tracked + untracked-not-ignored + deleted, sorted by path
      HasHead bool
  }
  var ErrNotRepo = errors.New("not a git repository")
  func Load(dir string) (*Repo, error)
  func (r *Repo) Stats() (modified, added, deleted int)
  func (r *Repo) Diff(f File) (string, error) // M/D: `git diff HEAD -- path`; A untracked: synthesize "+" lines from file; no HEAD: as added; Unchanged: file content; binary → "二进制文件"
  type Node struct{ Name, Path string; Dir bool; Status Status; Changes int; Children []*Node }
  func BuildTree(files []File) *Node // dirs before files, by name; Changes = changed descendants
  ```
- Status from `git status --porcelain=v1 -z --untracked-files=all`; files from `git ls-files -z` (tracked) ∪ untracked from status ∪ deleted from status. Rename `R  old -> new` in `-z` form (two NUL fields) → old D + new A. Staged-new `A` and untracked `??` → Added. Any of `M`,`T`,`U` in X or Y → Modified; `D` → Deleted.

- [ ] **Step 1: Failing tests** with a temp repo built via `git init` / `git -c user.name=t -c user.email=t@t commit` in `t.TempDir()`:
  - `TestLoad_NotRepo` → `ErrNotRepo`.
  - `TestLoad_Statuses`: modify `a.go`, delete `b.go`, add untracked `new dir/新.go`, `git mv c.go d.go` → `a.go M`, `b.go D`, `new dir/新.go A`, `c.go D`, `d.go A`, untouched `e.go` Unchanged; ignored file (in `.gitignore`) absent; Stats `(1, 2, 2)`.
  - `TestLoad_NoCommits`: fresh repo with files → all Added, `HasHead=false`, Diff returns `+` lines.
  - `TestLoad_DetachedBranch`: checkout commit hash → Branch is 7-char short hash.
  - `TestDiff_Modified` contains `-old` and `+new`; `TestDiff_Untracked` all lines prefixed `+`; `TestDiff_Binary` returns `二进制文件`.
  - `TestBuildTree_OrderAndCounts`: dirs first, `Changes` aggregated.
  - `TestLoad_LargeRepoFast`: 50,000 tracked files (create via `git update-index --index-info` with one blob, no worktree checkout cost) → `Load` + `BuildTree` < 1s.
- [ ] **Step 2:** `go test ./internal/gitdiff/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/gitdiff/` → PASS.
- [ ] **Step 5:** Commit `feat: read git changes for the diff popup`.

---

### Task 5: Diff popup TUI

**Files:** Create `internal/diffui/model.go`, `internal/diffui/model_test.go`

**Interfaces:**
- Consumes: Task 4.
- Produces: `func New(dir string) tea.Model` (loads repo; on `ErrNotRepo` shows the not-a-repo copy and quits on any key).

Behavior (spec §二): header `name · branch · stats` (or `没有未提交的改动`); left tree (~40% width, min 28) with status letters/colors, dirs containing changes expanded with `  N` count, others collapsed; flattened visible rows recomputed on toggle (lazy: children of collapsed dirs never flattened); default selection first changed file; right pane shows `Diff(selected)` colored, cached per path; `tab` switches focus; tree keys `↑↓ jk ←→ ⏎ c`; diff keys `↑↓ jk pgup pgdown g G`; `q`/`esc` → `tea.Quit`; footer per spec.

- [ ] **Step 1: Failing tests** (temp repo helper from Task 4 test copied into a `testutil_test.go` or re-created): `TestPopup_HeaderStats`, `TestPopup_SelectsFirstChange`, `TestPopup_ChangedDirsExpanded` (unchanged dir shows `▸`), `TestPopup_ToggleChangedOnly` (`c` hides unchanged files), `TestPopup_TabScrollsDiff` (offset changes after tab + `j`), `TestPopup_NotRepo` (view has copy; any key → Quit cmd), `TestPopup_QuitKeys`.
- [ ] **Step 2:** `go test ./internal/diffui/` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/diffui/` → PASS.
- [ ] **Step 5:** Commit `feat: diff popup`.

---

### Task 6: Wiring `d`, `⌥d`, subcommands, README, e2e

**Files:** Modify `internal/tmux/tmux.go`, `internal/tmux/tmux.conf`, `internal/tmux/tmux_test.go`, `internal/ui/model.go`, `internal/ui/model_test.go`, `cmd/ctree/main.go`, `README.md`

**Interfaces:**
- Produces:
  - `func (c *Client) Popup(dir string) error` → `display-popup -E -w 90% -h 90% -d <dir> <Self> diff <dir>`.
  - `func ConfFor(self string) string` → `Conf` with `@CTREE@` replaced by `self`; tmux.conf adds `bind -n M-d run-shell -b '"@CTREE@" diff-slot'`. Launch writes `ConfFor(self)`.
  - `ctree diff <dir>` → run `diffui.New(dir)` with alt screen.
  - `ctree diff-slot` → `SlotPane()`; if Role is placeholder, exit 0 silently; else `Popup(<slot pane_current_path>)` (add `#{pane_current_path}` as a 7th field `CurrentPath` on `Pane`).
  - `Controller.Diff(dir string) error`; sidebar `d` on SessionRow/ProjectRow/OlderRow → `Diff(row.Cwd)`; error → toast.
- Footer key help gains `d diff` (fit within 34 columns; adjust lines).

- [ ] **Step 1: Failing tests:** `TestPopup_Args` (fake runner argv), `TestConfFor_SubstitutesPath`, `TestPanes_ParsesCurrentPath` (update existing format tests to 7 fields), `TestDiffKey_CallsControllerWithCwd`, `TestDiffSlot_SkipsPlaceholder` (factor diff-slot into a function taking a `*tmux.Client`).
- [ ] **Step 2:** `go test ./...` → FAIL.
- [ ] **Step 3:** Implement; README sections for archive rules, `d`/`⌥d`, and the Alt-d note.
- [ ] **Step 4:** `go test ./...` and `go vet ./...` → PASS; `go install ./cmd/ctree`.
- [ ] **Step 5: e2e** (headless, `CTREE_SOCKET=ctree-e2e`, `CTREE_CONFIG_DIR=/tmp/ctree-e2e-cfg`, outer tmux as terminal, real data, no new claude sessions): projects older than 30 days appear only under 已归档; `… 更早的 N 个会话` rows present and expand with Enter; `a` on a project row archives it, `a` in 已归档 restores; `d` on the claude-tree project opens the popup (capture via `display-popup` client — verify with `list-clients`/`capture-pane` of the popup or by running `ctree diff <repo>` in a pane and capturing) showing the colored tree and diff; `q` closes. Clean up servers and config dir.
- [ ] **Step 6:** Commit `feat: open the diff popup from the sidebar and Claude`.
