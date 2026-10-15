package monitor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

const (
	// A focused pane is re-marked on every scan, so the file is written at most this often
	piSeenSaveEvery = time.Minute
	// Seen times older than this are dropped; they can't matter after a month
	piSeenRetention = 30 * 24 * time.Hour
)

// PiSeen remembers when the user last looked at a pi session. A terminal agent has no
// desktop app to record that, so spark takes it from the Herdr pane that is focused, and
// keeps it on disk: without that, every restart would turn sessions the user has already
// read back into new ones
type PiSeen struct {
	path    string
	at      map[string]int64
	dirty   bool
	savedAt time.Time
}

// PiSeenPath is spark's own state file, next to the window's
func PiSeenPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Spark", "pi-seen.json")
}

func NewPiSeen(path string) *PiSeen {
	seen := &PiSeen{path: path, at: map[string]int64{}}
	content, err := os.ReadFile(path)
	if err != nil {
		return seen
	}
	_ = json.Unmarshal(content, &seen.at)
	// Saving is throttled from the file's own age, so a restart doesn't rewrite it at once
	if info, err := os.Stat(path); err == nil {
		seen.savedAt = info.ModTime()
	}
	return seen
}

func (s *PiSeen) Seen(key string) int64 {
	if s == nil || key == "" {
		return 0
	}
	return s.at[key]
}

func (s *PiSeen) Mark(key string, atMs int64) {
	if s == nil || key == "" || atMs <= s.at[key] {
		return
	}
	s.at[key] = atMs
	s.dirty = true
}

// Save writes the file at most once a minute, and only when something moved
func (s *PiSeen) Save(now time.Time) {
	if s == nil || !s.dirty || now.Sub(s.savedAt) < piSeenSaveEvery {
		return
	}
	s.savedAt = now
	s.prune(now)
	content, err := json.Marshal(s.at)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		log.Printf("[Seen] %v", err)
		return
	}
	if err := os.WriteFile(s.path, content, 0o644); err != nil {
		log.Printf("[Seen] %v", err)
		return
	}
	s.dirty = false
}

func (s *PiSeen) prune(now time.Time) {
	cutoff := now.Add(-piSeenRetention).UnixMilli()
	for key, at := range s.at {
		if at < cutoff {
			delete(s.at, key)
		}
	}
}
