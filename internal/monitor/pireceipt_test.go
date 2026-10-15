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

// The baseline belongs to one scan: a store that stayed "first look" for the life of the
// process would re-mark every session as seen on every scan, and nothing could ever be new
func TestPiSeenBaselinesOnce(t *testing.T) {
	fresh := NewPiSeen(filepath.Join(t.TempDir(), "pi-seen.json"))
	if !fresh.FirstLook() {
		t.Error("a store with no file behind it is a first look")
	}
	if fresh.FirstLook() {
		t.Error("only the first look is the first look")
	}

	path := filepath.Join(t.TempDir(), "pi-seen.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := NewPiSeen(path); got.FirstLook() {
		t.Error("a store that was written before is not a first look")
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := NewPiSeen(path); !got.FirstLook() {
		t.Error("an unreadable store is treated as a first look rather than as authoritative")
	}
}

func TestPiSeenRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pi-seen.json")
	seen := NewPiSeen(path)
	if got := seen.Seen("/sessions/a.jsonl"); got != 0 {
		t.Errorf("unknown session = %d", got)
	}

	now := time.Now()
	seen.Mark("/sessions/a.jsonl", now.UnixMilli())
	seen.Mark("/sessions/a.jsonl", now.Add(-time.Hour).UnixMilli()) // older marks never win
	seen.Save(now)
	if seen.dirty {
		t.Error("saving clears the dirty flag")
	}

	reloaded := NewPiSeen(path)
	if got := reloaded.Seen("/sessions/a.jsonl"); got != now.UnixMilli() {
		t.Errorf("after reload = %d, want %d", got, now.UnixMilli())
	}

	// A save within the interval is skipped, so a focused pane doesn't write every scan
	reloaded.Mark("/sessions/b.jsonl", now.UnixMilli())
	reloaded.Save(now.Add(piSeenSaveEvery / 2))
	if got := NewPiSeen(path).Seen("/sessions/b.jsonl"); got != 0 {
		t.Errorf("saved too early: %d", got)
	}
	reloaded.Save(now.Add(piSeenSaveEvery * 2))
	if got := NewPiSeen(path).Seen("/sessions/b.jsonl"); got != now.UnixMilli() {
		t.Errorf("after the interval = %d", got)
	}
}

func TestPiSeenPrunesOldEntries(t *testing.T) {
	seen := NewPiSeen(filepath.Join(t.TempDir(), "pi-seen.json"))
	now := time.Now()
	seen.Mark("/sessions/old.jsonl", now.Add(-piSeenRetention-time.Hour).UnixMilli())
	seen.Mark("/sessions/new.jsonl", now.UnixMilli())
	seen.Save(now)

	if got := seen.Seen("/sessions/old.jsonl"); got != 0 {
		t.Errorf("old entry survived prune: %d", got)
	}
	if got := seen.Seen("/sessions/new.jsonl"); got != now.UnixMilli() {
		t.Errorf("recent entry was pruned: %d", got)
	}
}

// A nil store has to answer: a Scanner built by hand in a test has none
func TestPiSeenNilIsSafe(t *testing.T) {
	var seen *PiSeen
	if got := seen.Seen("/x"); got != 0 {
		t.Errorf("Seen = %d", got)
	}
	seen.Mark("/x", 1)
	seen.Save(time.Now())
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
