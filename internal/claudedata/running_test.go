package claudedata

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeSession(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func allAlive(int, string) bool { return true }

func TestLoadRunning_ParsesFields(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "85329.json", `{"pid":85329,"sessionId":"bb02","cwd":"/Users/didi/project/claude-tree","name":"claude-tree-aa","status":"busy","updatedAt":1792414000000,"statusUpdatedAt":1792414047032}`)
	writeSession(t, root, "79563.json", `{"pid":79563,"sessionId":"5107","cwd":"/Users/didi/project/shop-zone","name":"shop-zone-f8","status":"idle","updatedAt":1792414047032}`)
	writeSession(t, root, "1.json", `{"pid":1,"sessionId":"x","cwd":"/","status":"whatever","updatedAt":1}`)

	got, err := LoadRunning(root, allAlive)
	if err != nil {
		t.Fatal(err)
	}
	by := map[int]Running{}
	for _, r := range got {
		by[r.PID] = r
	}
	if len(by) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(by))
	}
	b := by[85329]
	if b.SessionID != "bb02" || b.Cwd != "/Users/didi/project/claude-tree" || b.Name != "claude-tree-aa" || b.Status != RunBusy {
		t.Errorf("busy session parsed wrong: %+v", b)
	}
	if !b.StatusUpdatedAt.Equal(time.UnixMilli(1792414047032)) {
		t.Errorf("StatusUpdatedAt = %v", b.StatusUpdatedAt)
	}
	i := by[79563]
	if i.Status != RunIdle {
		t.Errorf("idle status = %v", i.Status)
	}
	if !i.StatusUpdatedAt.Equal(time.UnixMilli(1792414047032)) {
		t.Errorf("missing statusUpdatedAt should fall back to updatedAt, got %v", i.StatusUpdatedAt)
	}
	if by[1].Status != RunOther {
		t.Errorf("unknown status = %v", by[1].Status)
	}
}

func TestLoadRunning_SkipsDeadPID(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "62386.json", `{"pid":62386,"sessionId":"dead","cwd":"/","status":"idle","updatedAt":1}`)
	writeSession(t, root, "85329.json", `{"pid":85329,"sessionId":"live","cwd":"/","status":"busy","updatedAt":1}`)
	got, err := LoadRunning(root, func(pid int, _ string) bool { return pid != 62386 })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "live" {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadRunning_IgnoresNonJSONAndKeyFiles(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "85329.abc.key", "secret")
	writeSession(t, root, "2.json", `{not json`)
	writeSession(t, root, "3.json", `{"pid":3,"sessionId":"ok","cwd":"/","status":"idle","updatedAt":1}`)
	got, err := LoadRunning(root, allAlive)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "ok" {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadRunning_MissingDir(t *testing.T) {
	got, err := LoadRunning(t.TempDir(), allAlive)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestLoadRunning_PassesProcStart(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "812.json", `{"pid":812,"sessionId":"stale","cwd":"/","status":"idle","updatedAt":1,"procStart":"Fri Oct 16 08:26:04 2026"}`)
	var gotStart string
	got, _ := LoadRunning(root, func(pid int, procStart string) bool { gotStart = procStart; return false })
	if gotStart != "Fri Oct 16 08:26:04 2026" || len(got) != 0 {
		t.Fatalf("procStart %q, got %+v", gotStart, got)
	}
}

// psStart is the process start time as ps reports it in UTC, independent of
// the sysctl call PIDAlive uses.
func psStart(t *testing.T, pid int) string {
	t.Helper()
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		t.Skip("ps unavailable")
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

func TestPIDAlive_ChecksStartTime(t *testing.T) {
	pid := os.Getpid()
	if !PIDAlive(pid, psStart(t, pid)) {
		t.Error("own process with matching start time should be alive")
	}
	if !PIDAlive(pid, "") {
		t.Error("no recorded start time: fall back to pid check")
	}
	// Same pid, different start time: the pid was reused (e.g. after a reboot).
	if PIDAlive(pid, "Thu Jan  1 00:00:00 2015") {
		t.Error("reused pid must count as dead")
	}
	if PIDAlive(pid, "garbage") != true {
		t.Error("unparseable start time: fall back to pid check")
	}
}
