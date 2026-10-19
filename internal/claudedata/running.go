// Package claudedata reads Claude Code's on-disk session data.
//
// The formats are undocumented and internal to Claude Code; everything that
// depends on them lives in this package so a format change touches one place.
package claudedata

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// RunStatus is the status Claude reports for a live session.
type RunStatus int

const (
	RunBusy RunStatus = iota
	RunIdle
	RunOther
)

// Running is a live Claude process, from ~/.claude/sessions/<pid>.json.
type Running struct {
	PID             int
	SessionID       string
	Cwd             string
	Name            string
	Status          RunStatus
	StatusUpdatedAt time.Time
}

// Root is the Claude data directory: $ATREE_CLAUDE_DIR or ~/.claude.
func Root() string {
	if d := os.Getenv("ATREE_CLAUDE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// PIDAlive reports whether pid is running and, when procStart is known, that
// it is the same process that wrote the sessions file. After a reboot a stale
// file's pid can belong to an unrelated process; its start time won't match.
// procStart is Claude's asctime-style UTC start time ("Fri Oct 16 08:26:04 2026").
func PIDAlive(pid int, procStart string) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	want, err := time.Parse(procStartLayout, strings.Join(strings.Fields(procStart), " "))
	if err != nil {
		return true // unknown format: the pid check is all we have
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return true
	}
	tv := kp.Proc.P_starttime
	got := time.Unix(tv.Sec, int64(tv.Usec)*1000)
	d := got.Sub(want)
	return d > -2*time.Second && d < 2*time.Second
}

const procStartLayout = "Mon Jan 2 15:04:05 2006"

type runningFile struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	Cwd             string `json:"cwd"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	UpdatedAt       int64  `json:"updatedAt"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
	ProcStart       string `json:"procStart"`
}

// LoadRunning returns the live sessions under root/sessions. Files whose process is
// not alive (left behind by a crash or reboot) and unreadable files are skipped.
func LoadRunning(root string, alive func(pid int, procStart string) bool) ([]Running, error) {
	dir := filepath.Join(root, "sessions")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Running
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var f runningFile
		if json.Unmarshal(data, &f) != nil || f.PID == 0 || f.SessionID == "" {
			continue
		}
		if !alive(f.PID, f.ProcStart) {
			continue
		}
		ts := f.StatusUpdatedAt
		if ts == 0 {
			ts = f.UpdatedAt
		}
		out = append(out, Running{
			PID:             f.PID,
			SessionID:       f.SessionID,
			Cwd:             f.Cwd,
			Name:            f.Name,
			Status:          parseStatus(f.Status),
			StatusUpdatedAt: time.UnixMilli(ts),
		})
	}
	return out, nil
}

func parseStatus(s string) RunStatus {
	switch s {
	case "busy":
		return RunBusy
	case "idle":
		return RunIdle
	}
	return RunOther
}
