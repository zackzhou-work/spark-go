package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

const noticesVersion = 1

// cursorNoticeRecord remembers whether the user dismissed or verified reporting for one
// installed copy of spark's Cursor hooks.
type cursorNoticeRecord struct {
	Protocol         int    `json:"protocol,omitempty"`
	CollectorVersion int    `json:"collector_version,omitempty"`
	InstalledAt      string `json:"installed_at,omitempty"`
	Dismissed        bool   `json:"dismissed,omitempty"`
	Verified         bool   `json:"verified,omitempty"`
}

func (r cursorNoticeRecord) matches(meta monitor.CursorInstallMeta) bool {
	if meta.Protocol <= 0 || meta.CollectorVersion <= 0 {
		return false
	}
	return r.Protocol == meta.Protocol &&
		r.CollectorVersion == meta.CollectorVersion &&
		r.InstalledAt == meta.InstalledAt
}

// piNoticeRecord remembers whether the user dismissed or verified reporting for one installed
// copy of spark's pi extension.
type piNoticeRecord struct {
	InstalledAt time.Time `json:"installed_at,omitempty"`
	InstallID   string    `json:"install_id,omitempty"`
	Dismissed   bool      `json:"dismissed,omitempty"`
	Verified    bool      `json:"verified,omitempty"`
}

func (r piNoticeRecord) matches(installedAt time.Time, installID string) bool {
	if installedAt.IsZero() && installID == "" {
		return false
	}
	return r.InstalledAt.Equal(installedAt) && r.InstallID == installID
}

// noticeSettings stores persistent dismissal and verification state for setup notices.
type noticeSettings struct {
	Version int                 `json:"version"`
	Cursor  *cursorNoticeRecord `json:"cursor,omitempty"`
	Pi      *piNoticeRecord     `json:"pi,omitempty"`
}

func noticesPath(home string) string {
	return filepath.Join(home, ".config", "spark", "notices.json")
}

// readNoticeSettings loads saved notice state. A missing file starts empty; malformed JSON or an
// unknown version returns an error so callers can log and recover cleanly.
func readNoticeSettings(path string) (noticeSettings, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return noticeSettings{Version: noticesVersion}, nil
	case err != nil:
		return noticeSettings{}, err
	}
	var file noticeSettings
	if err := json.Unmarshal(data, &file); err != nil {
		return noticeSettings{}, fmt.Errorf("not a notices file: %w", err)
	}
	if file.Version != noticesVersion {
		return noticeSettings{}, fmt.Errorf("notices file version %d, want %d", file.Version, noticesVersion)
	}
	return file, nil
}

// loadNoticeSettings reads saved notice state and falls back to an empty state on corruption so
// subsequent dismissals or verifications heal the file.
func loadNoticeSettings(path string) noticeSettings {
	settings, err := readNoticeSettings(path)
	if err != nil {
		log.Printf("[Notices] reading %s failed, starting fresh: %v", path, err)
		return noticeSettings{Version: noticesVersion}
	}
	return settings
}

func saveNoticeSettings(path string, settings noticeSettings) error {
	if path == "" {
		return errors.New("no notices file to write")
	}
	settings.Version = noticesVersion
	return saveJSONAtomic(path, ".notices-*.json", settings)
}

func (s noticeSettings) clone() noticeSettings {
	out := noticeSettings{Version: noticesVersion}
	if s.Cursor != nil {
		c := *s.Cursor
		out.Cursor = &c
	}
	if s.Pi != nil {
		p := *s.Pi
		out.Pi = &p
	}
	return out
}

func (s noticeSettings) cursorQuiet(meta monitor.CursorInstallMeta) bool {
	return s.Cursor != nil && s.Cursor.matches(meta) && (s.Cursor.Dismissed || s.Cursor.Verified)
}

func (s noticeSettings) piQuiet(installedAt time.Time, installID string) bool {
	return s.Pi != nil && s.Pi.matches(installedAt, installID) && (s.Pi.Dismissed || s.Pi.Verified)
}

func (s noticeSettings) withCursorFlag(meta monitor.CursorInstallMeta, dismissed, verified bool) (noticeSettings, bool) {
	if meta.Protocol <= 0 || meta.CollectorVersion <= 0 {
		return s, false
	}
	cur := cursorNoticeRecord{
		Protocol:         meta.Protocol,
		CollectorVersion: meta.CollectorVersion,
		InstalledAt:      meta.InstalledAt,
	}
	if s.Cursor != nil && s.Cursor.matches(meta) {
		cur = *s.Cursor
	}
	nextDismissed := cur.Dismissed || dismissed
	nextVerified := cur.Verified || verified
	if s.Cursor != nil && s.Cursor.matches(meta) && cur.Dismissed == nextDismissed && cur.Verified == nextVerified && s.Version == noticesVersion {
		return s, false
	}
	cur.Dismissed = nextDismissed
	cur.Verified = nextVerified
	out := s.clone()
	out.Cursor = &cur
	return out, true
}

func (s noticeSettings) withPiFlag(installedAt time.Time, installID string, dismissed, verified bool) (noticeSettings, bool) {
	if installedAt.IsZero() && installID == "" {
		return s, false
	}
	cur := piNoticeRecord{
		InstalledAt: installedAt,
		InstallID:   installID,
	}
	if s.Pi != nil && s.Pi.matches(installedAt, installID) {
		cur = *s.Pi
	}
	nextDismissed := cur.Dismissed || dismissed
	nextVerified := cur.Verified || verified
	if s.Pi != nil && s.Pi.matches(installedAt, installID) && cur.Dismissed == nextDismissed && cur.Verified == nextVerified && s.Version == noticesVersion {
		return s, false
	}
	cur.Dismissed = nextDismissed
	cur.Verified = nextVerified
	out := s.clone()
	out.Pi = &cur
	return out, true
}

// noticeWriter serializes background writes of noticeSettings so rapid updates never land out
// of order or block the UI thread.
type noticeWriter struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	running bool
	hasNext bool
	path    string
	next    noticeSettings
}

func (w *noticeWriter) save(path string, settings noticeSettings, async bool) {
	if path == "" {
		return
	}
	if !async {
		w.wg.Wait()
		if err := saveNoticeSettings(path, settings); err != nil {
			log.Printf("[Notices] saving %s failed: %v", path, err)
		}
		return
	}
	w.mu.Lock()
	w.path = path
	w.next = settings.clone()
	w.hasNext = true
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.wg.Add(1)
	w.mu.Unlock()

	go func() {
		defer w.wg.Done()
		for {
			w.mu.Lock()
			if !w.hasNext {
				w.running = false
				w.mu.Unlock()
				return
			}
			p, s := w.path, w.next
			w.hasNext = false
			w.mu.Unlock()

			if err := saveNoticeSettings(p, s); err != nil {
				log.Printf("[Notices] saving %s failed: %v", p, err)
			}
		}
	}()
}

func (w *noticeWriter) wait() {
	w.wg.Wait()
}
