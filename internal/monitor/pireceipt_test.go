package monitor

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeReceipt(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func receiptJSON(sessionID, path string, pid int, state string, updatedMs, inputMs int64) string {
	return `{"sessionId":"` + sessionID + `","sessionPath":"` + path + `","pid":` + strconv.Itoa(pid) +
		`,"state":"` + state + `","updatedAtMs":` + strconv.FormatInt(updatedMs, 10) +
		`,"lastInputAtMs":` + strconv.FormatInt(inputMs, 10) + `}`
}

func TestReadPiReceiptsTrustsOnlyRunningProcesses(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	livePath := "/sessions/live.jsonl"
	deadPath := "/sessions/dead.jsonl"
	ancientPath := "/sessions/ancient.jsonl"
	writeReceipt(t, dir, "live.json", receiptJSON("live", livePath, os.Getpid(), piWorking, now, now-5000))
	writeReceipt(t, dir, "dead.json", receiptJSON("dead", deadPath, 999999, piWorking, now, now-9000))
	writeReceipt(t, dir, "ancient.json", receiptJSON("ancient", ancientPath, os.Getpid(), piWorking, now-int64(piReceiptLiveWindow.Milliseconds())-1000, now))

	got := ReadPiReceipts(dir)
	if _, ok := got.live[livePath]; !ok {
		t.Error("a live process's state is believed")
	}
	if _, ok := got.live[deadPath]; ok {
		t.Error("a dead process's state is not believed")
	}
	if _, ok := got.live[ancientPath]; ok {
		t.Error("a state older than the live window is not believed")
	}
	// The input receipts outlive the processes: the user was still there
	if got.seen[deadPath] != now-9000 {
		t.Errorf("seen for the dead session = %d, want %d", got.seen[deadPath], now-9000)
	}
	if got.seen["dead"] != now-9000 {
		t.Error("receipts are also keyed by session ID")
	}
	if got.seen[ancientPath] != now {
		t.Errorf("seen for the idle session = %d, want %d", got.seen[ancientPath], now)
	}
}

func TestPrunePiReceiptsDropsFinishedSessionsOnly(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-piReceiptRetention - time.Hour)
	gone := writeReceipt(t, dir, "gone.json", receiptJSON("gone", "/sessions/gone.jsonl", 999999, piExited, old.UnixMilli(), old.UnixMilli()))
	running := writeReceipt(t, dir, "running.json", receiptJSON("running", "/sessions/running.jsonl", os.Getpid(), piWorking, old.UnixMilli(), 0))
	recent := writeReceipt(t, dir, "recent.json", receiptJSON("recent", "/sessions/recent.jsonl", 999999, piExited, time.Now().UnixMilli(), 0))
	// Both old files look untouched; a receipt written now stays
	for _, path := range []string{gone, running} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	prunePiReceipts(dir, time.Now())

	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Error("a finished session's receipt is removed")
	}
	if _, err := os.Stat(running); err != nil {
		t.Error("a receipt whose process still runs is kept")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("a receipt touched recently is kept")
	}
}

// A receipt is only proof that the extension was loaded if it came from the version this
// build installs: a session still running an older copy keeps writing its own, on every state
// change and on every heartbeat
func TestPiReportedNeedsACurrentReceipt(t *testing.T) {
	home := t.TempDir()
	if PiReported(home) {
		t.Error("nothing has reported in a fresh home")
	}
	dir := PiReceiptDir(home)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if PiReported(home) {
		t.Error("an empty directory is not a report")
	}
	if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(`{"payload":0}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if PiReported(home) {
		t.Error("a receipt from an older extension says nothing about this one")
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if PiReported(home) {
		t.Error("an unreadable receipt is not a report")
	}
	if err := os.WriteFile(filepath.Join(dir, "fresh.json"), []byte(`{"payload":1}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !PiReported(home) {
		t.Error("a receipt from the current payload is a report")
	}
}

// A session that ended says so, and its process being gone is the point: the badge window
// still applies to the turn it finished
func TestReadPiReceiptsKeepsAnExitedReceiptWithoutItsProcess(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	writeReceipt(t, dir, "exited.json", receiptJSON("exited", "/sessions/exited.jsonl", 999999, piExited, now, now))

	got := ReadPiReceipts(dir)
	if _, ok := got.live["/sessions/exited.jsonl"]; !ok {
		t.Error("an exited session is still a session spark knows about")
	}
}

func TestPiExtensionInstallsAndIsNoticed(t *testing.T) {
	home := t.TempDir()
	if PiExtensionInstalled(home) {
		t.Error("nothing is installed in a fresh home")
	}
	if err := InstallPiExtension(home); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := PiExtensionPath(home)
	if !PiExtensionInstalled(home) {
		t.Fatal("the extension is not seen after installing")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(content) == 0 {
		t.Error("the embedded extension is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o400 == 0 {
		t.Errorf("mode = %v, want it readable by pi", info.Mode())
	}
}

func receiptJSONStarted(sessionID, path string, pid int, startedMs, updatedMs, inputMs int64, state string) string {
	return `{"sessionId":"` + sessionID + `","sessionPath":"` + path + `","pid":` + strconv.Itoa(pid) +
		`,"state":"` + state + `","startedAtMs":` + strconv.FormatInt(startedMs, 10) +
		`,"updatedAtMs":` + strconv.FormatInt(updatedMs, 10) +
		`,"lastInputAtMs":` + strconv.FormatInt(inputMs, 10) + `}`
}

// A receipt speaks for the process that wrote it. A crashed pi leaves one behind, and by the
// time spark reads it the recorded pid can belong to something else
func TestReadPiReceiptsChecksTheProcessBehindThePid(t *testing.T) {
	starts := processStarts()
	mine, ok := starts[os.Getpid()]
	if !ok {
		t.Skip("this platform has no process table to check against")
	}
	dir := t.TempDir()
	now := time.Now().UnixMilli()

	writeReceipt(t, dir, "mine.json", receiptJSONStarted("mine", "/sessions/mine.jsonl", os.Getpid(), mine*1000, now, now-1000, piWorking))
	// Same pid, an hour older: the process that wrote this is gone and its pid was reused
	writeReceipt(t, dir, "reused.json", receiptJSONStarted("reused", "/sessions/reused.jsonl", os.Getpid(), (mine-3600)*1000, now, now-2000, piBlocked))
	// An extension that doesn't say when it started is taken at its word
	writeReceipt(t, dir, "quiet.json", receiptJSON("quiet", "/sessions/quiet.jsonl", os.Getpid(), piWorking, now, 0))

	got := ReadPiReceipts(dir)
	if _, ok := got.live["/sessions/mine.jsonl"]; !ok {
		t.Error("a receipt from this process is live state")
	}
	if _, ok := got.live["/sessions/reused.jsonl"]; ok {
		t.Error("a receipt whose pid now belongs to another process is not")
	}
	if got.seen["/sessions/reused.jsonl"] != now-2000 {
		t.Errorf("seen = %d, want the look to still count", got.seen["/sessions/reused.jsonl"])
	}
	if _, ok := got.live["/sessions/quiet.jsonl"]; !ok {
		t.Error("a receipt with no start time is believed")
	}
}

func TestPiExtensionOnDiskTellsTheThreeStates(t *testing.T) {
	home := t.TempDir()
	if got := PiExtensionOnDisk(home); got != PiExtensionAbsent {
		t.Errorf("a fresh home = %v, want absent", got)
	}
	path := PiExtensionPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("// what an older release installed\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := PiExtensionOnDisk(home); got != PiExtensionOutdated {
		t.Errorf("an older file = %v, want outdated", got)
	}
	if PiExtensionInstalled(home) {
		t.Error("an outdated copy is not installed as far as spark is concerned")
	}
	if err := InstallPiExtension(home); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := PiExtensionOnDisk(home); got != PiExtensionCurrent {
		t.Errorf("after installing = %v, want current", got)
	}
}
