package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The extension reports the same three states Herdr does, plus exited for a pi process
// that is going away, so spark reads a session without caring where the signal came from
const (
	piWorking = "working"
	piBlocked = "blocked"
	piIdle    = "idle"
	piExited  = "exited"
)

const (
	// A receipt older than this says nothing about a running process: a pi that has been
	// idle for a day has no state left to report
	piReceiptLiveWindow = 24 * time.Hour
	// Receipts for sessions whose process is long gone are deleted this long after their
	// last update
	piReceiptRetention = 30 * 24 * time.Hour
	// Receipts are read every scan but only pruned this often
	piReceiptPruneEvery = time.Hour
)

// piLive is a live signal about one session: the extension's report on the pi process, or
// Herdr's view of the pane it runs in
type piLive struct {
	Status    string
	BlockKind string
	Tool      string
	PaneID    string
}

// PiReceipt is what the extension writes, one file per session. Each write replaces the
// last, and the file's own mtime is the update time
type PiReceipt struct {
	SessionID   string `json:"sessionId"`
	SessionPath string `json:"sessionPath"`
	PID         int    `json:"pid"`
	State       string `json:"state"`
	BlockKind   string `json:"blockKind"`
	Tool        string `json:"tool"`
	UpdatedAtMs int64  `json:"updatedAtMs"`
	LastInputMs int64  `json:"lastInputAtMs"`
}

// PiReceiptDir is where the extension writes its per-session files
func PiReceiptDir(home string) string {
	return filepath.Join(home, ".config", "spark", "pi")
}

// piReceipts is the live state and the read receipts found in that directory, both keyed by
// session path and by session ID
type piReceipts struct {
	live map[string]piLive
	seen map[string]int64
}

// ReadPiReceipts reads every receipt. A reported state is only believed while the pi process
// behind it is running: a session whose process is gone is over, whatever the file last
// said, though the user's last input still counts as having seen it
func ReadPiReceipts(dir string) piReceipts {
	out := piReceipts{live: map[string]piLive{}, seen: map[string]int64{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	nowMs := time.Now().UnixMilli()
	alive := map[int]bool{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r PiReceipt
		if json.Unmarshal(content, &r) != nil || r.SessionPath == "" {
			continue
		}
		keys := []string{r.SessionPath}
		if r.SessionID != "" {
			keys = append(keys, r.SessionID)
		}
		if r.LastInputMs > 0 {
			for _, k := range keys {
				out.seen[k] = max(out.seen[k], r.LastInputMs)
			}
		}
		if r.State == "" || nowMs-r.UpdatedAtMs > piReceiptLiveWindow.Milliseconds() {
			continue
		}
		if !processAliveCached(alive, r.PID) {
			continue
		}
		for _, k := range keys {
			out.live[k] = piLive{Status: r.State, BlockKind: r.BlockKind, Tool: r.Tool}
		}
	}
	return out
}

// processAliveCached asks the system about each pid once per scan
func processAliveCached(cache map[int]bool, pid int) bool {
	if known, ok := cache[pid]; ok {
		return known
	}
	living := processAlive(pid)
	cache[pid] = living
	return living
}

// prunePiReceipts deletes receipts whose process is gone and whose last update is ancient,
// so the directory doesn't keep growing with every session ever run
func prunePiReceipts(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < piReceiptRetention {
			continue
		}
		path := filepath.Join(dir, e.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var r PiReceipt
		if json.Unmarshal(content, &r) != nil {
			continue
		}
		if r.PID > 0 && processAlive(r.PID) {
			continue
		}
		_ = os.Remove(path)
	}
}
