# ctree：终端里的 Claude CLI 会话侧边栏 — 设计

日期：2026-10-19

## 意图

在 iTerm2 里获得和 Claude Desktop 一样的会话体验：左边常驻一个侧栏，按 project 分组列出所有 Claude 会话；右边是当前选中的 Claude CLI 会话。可以随意跨 project 切换，也可以在同一个 project 的多个会话之间切换。

**为什么要做：** 用户用过 claude-squad，不顺手的两点正是本项目要解决的：

1. claude-squad 没有常驻侧栏——要进入会话才能和 Claude 对话，退出才能看到列表。
2. claude-squad 只能管理同一个 project 的会话，不能跨 project。

## 目标

- 侧栏常驻：和右边的 Claude 对话时，侧栏一直可见。
- 跨 project：所有 project 的会话在同一个侧栏里，按 project 分组。
- 切走的会话不停：在后台继续运行，切回来是原样。
- 关掉 iTerm2 也不影响：会话继续跑，再次运行 `ctree` 回到原来的状态（用户明确要求，因此底层用 tmux）。
- 看得到历史会话：从 `~/.claude/projects/` 读取，选中即 `claude --resume` 拉起。
- 每个会话显示状态（见下文"状态标记"）。
- 会话可以归档（用户要求），见下文"归档"。

## 不做（非目标）

- 不为会话创建 git worktree / 分支（claude-squad 的做法）。会话就在 project 原目录里开。（我的推断，用户未反对。）
- 不做重命名（Claude 自带 `/rename`）、删除历史记录、搜索。
- 不做重启后自动恢复：电脑重启或 ctree 的 tmux server 退出后，原来开着的会话不自动 resume，只需在侧栏里作为历史会话可见、可手动打开（用户决定）。
- 不支持 iTerm2 `tmux -CC` 集成模式（该模式下 tmux 按键与焦点边框由 iTerm2 接管，回到侧栏无法实现）。v1 只支持普通 tmux 模式。（我的决定）
- 不支持在另一个 tmux 里运行：检测到 `$TMUX` 时提示并退出。（我的决定）
- 不做独立 GUI 窗口；它是一个终端程序。
- 不做终端模拟器：不自己渲染 Claude 的输出，Claude 跑在 tmux 窗格里。

## 反目标（做出来但等于失败）

- 侧栏不常驻、需要"进入/退出"会话才能操作——这就是 claude-squad 的问题。
- 只能看一个 project。
- 同一个会话被拉起两个 claude 进程，同时写同一份 `.jsonl` 记录。
- 改动或干扰用户自己的 tmux 配置 / tmux server。

## 界面（用户已看过并认可的样子）

方案 A：project 树，最像 Desktop。

```
Claude Tree                   │ ╭ Claude Code ──────────────╮
▾ zone                        │ │ ~/project/zone            │
  ◆ Superpowers 设计文档和计划 │ ╰───────────────────────────╯
  ● 围栏判定排查               │
  ○ H3 编码对比                │ > 看一下当前superpowers下的…
  · 蜂巢重刷          10-13    │
  · 坐标系问题        10-11    │ ⏺ 我找到了 3 份设计文档……
▾ claude-tree                 │
  ● 侧边栏会话管理器           │ > █
▾ InferGate                   │
  ↗ 接口重构                   │
  · 部署脚本          10-09    │
▸ zone-admin             3    │
▸ lark                   5    │
⌥← 回到侧栏                   │
```

- 所有 project 平铺，可折叠；折叠时显示会话数。
- 每个 project 下：运行中的会话在前，历史会话在后（按最后活跃时间倒序）。历史会话显示日期。
- 右边当前显示的会话在侧栏里高亮（粗体/白色）；侧栏有焦点时光标所在行另有选中底色。
- 焦点在哪一边要能看出来（边框颜色等）。
- 侧栏有焦点时，底部显示按键提示。

### 状态标记

| 标记 | 含义 |
|---|---|
| `●` 绿 | 正在干活 |
| `◆` 红 | 等你回复 |
| `○` 蓝 | 空闲，在 ctree 后台开着 |
| `↗` 紫 | 在别处运行（不在 ctree 里，例如用户在另一个 iTerm2 标签里直接敲 `claude`） |
| `·` 灰 | 历史，未运行 |

### 操作

| 按键 | 作用 |
|---|---|
| `⌥←` | 从右边的 Claude 跳回侧栏（tmux `bind -n M-Left`，无前缀；可配置） |
| `⌥→` | 从侧栏回到右边的 Claude（侧栏有焦点时 `esc` 同效） |
| `↑↓` / `j` `k` | 移动 |
| `⏎` / 鼠标点击 | 打开：运行中的会话直接切到右边；历史会话用 `claude --resume <id>` 在 project 的 cwd 新开并切过去，然后焦点到右边 |
| `←→` | 折叠 / 展开 project |
| `n` | 在光标所在 project 的目录开一个新的 Claude 会话 |
| `N` | 在新目录开会话：弹出输入框，支持 tab 补全路径；确定后该目录出现在侧栏 |
| `x` | 关闭一个在 ctree 里运行的会话（结束进程，记录保留，变回历史） |
| `a` | 归档 / 取消归档光标所在会话 |
| `esc` | 焦点回到右边 |

**快捷键说明：** 不用 `⌃b` 前缀，因为 Claude CLI 自己用 `Ctrl+B`（把运行中的命令转后台），用前缀会让它需要按两次。用户同意改用 `⌥←` / `⌥→`。这要求 iTerm2 的 Option 键设为 "Esc+"，写进安装说明。ctree server 的 tmux 前缀键设为一个不冲突的键或关闭。

**在别处运行的会话：** 选中时不拉起新进程，显示提示："这个会话正在别的终端里运行（pid N），请到那个窗口操作，或先在那边退出再从这里打开"。

### 归档

用户要求会话能归档。以下细节是我的决定：

- 归档只是从 project 树里隐藏，不删除、不修改 Claude 的任何文件；归档状态记录在 ctree 自己的状态文件里（例如 `~/.config/ctree/state.json`，按 sessionId）。
- 侧栏底部有一个默认折叠的「已归档」分组，展开后按 project 列出已归档会话；在里面按 `a` 取消归档，按 `⏎` 可直接打开（打开即自动取消归档）。
- 归档一个在 ctree 里运行的会话：先确认，确认后结束进程再归档。在别处运行的（`↗`）不允许归档，给出提示。
- 某个 project 的会话全部归档后，该 project 不再显示在树里。

（mockup 存档：`.superpowers/brainstorm/*/content/sidebar-layout.html`、`sidebar-a-detail.html`）

## 怎么做（已定）

- **语言：Go**（用户选定）。**TUI 库：Bubble Tea**（我的选择；claude-squad 同栈，证明可行）。产物是单个可执行文件 `ctree`。
- **底层：tmux**（用户的需求决定：关掉终端会话也要继续跑）。本机 tmux 3.8。
- **独立 tmux server：** `tmux -L ctree`，自带配置，不读也不改用户的 `~/.tmux.conf`、不碰用户默认 server。`⌃b s` 等绑定只在这个 server 里生效。（我的选择）
- **布局：** 一个可见窗口，左窗格跑侧栏程序，右窗格是当前 Claude 会话。每个 Claude 会话是一个 tmux 窗格；不显示的放在后台窗口里继续跑；切换就是把选中的窗格换到右边（`swap-pane` / `join-pane` 一类）。（我的选择）
- **启动：** `ctree`。ctree server 已存在则 attach；不存在则新建布局。iTerm2 关掉后再敲 `ctree` 回到原状态。
- **iTerm2：** 默认普通 tmux 模式；提供选项用 `tmux -CC`（iTerm2 原生集成），布局设计需在两种模式下都能工作。（我的选择）
- **会话数据来源（未公开格式，单独成模块，便于格式变化时替换）：**
  - 运行中：`~/.claude/sessions/<pid>.json`，字段含 `pid`、`sessionId`、`cwd`、`name`、`status`、`updatedAt`。已观察到 `status` 取值 `busy`、`idle`。需判断 pid 是否还活着（文件可能残留）。
  - 历史：`~/.claude/projects/<编码后的路径>/<sessionId>.jsonl`。目录名是把路径中的 `/` 换成 `-`（有损，例如 `claude-tree` 本身含 `-`），**真实 cwd 应从 jsonl 记录里的 `cwd` 字段取**。标题取最后一条 `{"type":"custom-title","customTitle":...}`；没有则用 `last-prompt` 的 `lastPrompt` 或首条用户消息截断。时间用记录里的 `timestamp`（或文件 mtime）。文件可能很大（实测 13MB），读取要避免整文件解析。
  - 同目录下还有 `*.desktop-released.json`、子目录、`memory/` 等非会话文件，需忽略。
- **"在 ctree 里" vs "在别处"：** 运行中会话的 pid 若属于 ctree 管理的窗格（窗格进程或其子进程），则是 `●/◆/○`；否则是 `↗`。

## 留给实现者决定

- 侧栏宽度、颜色具体值、刷新频率（文件监听或轮询均可）。
- "等你回复"（`◆`）如何判定：`sessions/*.json` 的 `status` 是否有对应取值需实测；若没有，可退化为 `idle` 且最近一条是 assistant 回复。
- `⌃b s` 之外的快捷键细节、`N` 输入框的补全实现。
- 侧栏程序自身崩溃时的恢复方式（例如窗格里用循环重启）。
- 新会话的 sessionId 如何与窗格关联（例如启动后从 `sessions/*.json` 按 pid 匹配）。
- 测试策略。

## 环境事实

- macOS，iTerm2，tmux 3.8（`/opt/homebrew/bin/tmux`），Claude Code 2.1.292。
- 本机约 13 个 project 目录。
