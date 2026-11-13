package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

func TestMissingNoticesFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.json")
	settings, err := readNoticeSettings(path)
	if err != nil {
		t.Fatalf("missing file should start empty, got %v", err)
	}
	if settings.Version != noticesVersion || settings.Cursor != nil || settings.Pi != nil {
		t.Fatalf("unexpected empty settings: %+v", settings)
	}
}

func TestReadNoticesRejectsBrokenAndUnsupportedFilesAndLoadHeals(t *testing.T) {
	for _, body := range []string{
		`{"version": 1, "cursor":`,
		`{"version": 2}`,
		`{"cursor": {"dismissed": true}}`,
		`[]`,
	} {
		path := filepath.Join(t.TempDir(), "notices.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readNoticeSettings(path); err == nil {
			t.Errorf("%s should fail readNoticeSettings", body)
		}
		healed := loadNoticeSettings(path)
		if healed.Version != noticesVersion || healed.Cursor != nil || healed.Pi != nil {
			t.Errorf("%s: loadNoticeSettings returned %+v", body, healed)
		}
		meta := monitor.CursorInstallMeta{Protocol: 1, CollectorVersion: 1, InstalledAt: "2026-11-13T07:00:00Z"}
		next, changed := healed.withCursorFlag(meta, true, false)
		if !changed {
			t.Fatal("expected change")
		}
		if err := saveNoticeSettings(path, next); err != nil {
			t.Fatalf("healing save failed: %v", err)
		}
		back, err := readNoticeSettings(path)
		if err != nil || !back.cursorQuiet(meta) {
			t.Fatalf("expected healed file to read back quiet, err=%v back=%+v", err, back)
		}
	}
}

func TestSavingNoticesRoundTripsAndReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notices.json")
	meta := monitor.CursorInstallMeta{Protocol: 1, CollectorVersion: 1, InstalledAt: "2026-11-13T07:00:00.123456Z"}
	piAt := time.Date(2026, 11, 13, 7, 1, 2, 345000000, time.UTC)

	settings, _ := (noticeSettings{}).withCursorFlag(meta, true, false)
	settings, _ = settings.withCursorFlag(meta, false, true)
	settings, _ = settings.withPiFlag(piAt, "pi-install-1", true, true)

	if err := saveNoticeSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"notices.json"}) {
		t.Fatalf("expected only notices.json, got %v", names)
	}

	back, err := readNoticeSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !back.cursorQuiet(meta) || back.Cursor == nil || !back.Cursor.Dismissed || !back.Cursor.Verified {
		t.Fatalf("cursor record did not round-trip: %+v", back.Cursor)
	}
	if !back.piQuiet(piAt.Local(), "pi-install-1") || back.Pi == nil || !back.Pi.Dismissed || !back.Pi.Verified {
		t.Fatalf("pi record did not round-trip: %+v", back.Pi)
	}

	if err := saveNoticeSettings("", settings); err == nil {
		t.Fatal("saving with empty path should fail")
	}
}

func TestNoticeMatchingAndUpdates(t *testing.T) {
	meta1 := monitor.CursorInstallMeta{Protocol: 1, CollectorVersion: 1, InstalledAt: "2026-11-13T07:00:00Z"}
	meta2 := monitor.CursorInstallMeta{Protocol: 1, CollectorVersion: 1, InstalledAt: "2026-11-13T07:00:01Z"}
	metaUpgrade := monitor.CursorInstallMeta{Protocol: 1, CollectorVersion: 2, InstalledAt: "2026-11-13T07:00:00Z"}

	s, changed := (noticeSettings{}).withCursorFlag(meta1, true, false)
	if !changed || !s.cursorQuiet(meta1) {
		t.Fatalf("expected meta1 quiet, changed=%v", changed)
	}
	if _, again := s.withCursorFlag(meta1, true, false); again {
		t.Error("repeating same dismissal should report unchanged")
	}
	if s.cursorQuiet(meta2) {
		t.Error("different install time should not match")
	}
	if s.cursorQuiet(metaUpgrade) {
		t.Error("different collector version should not match")
	}

	s, changed = s.withCursorFlag(meta1, false, true)
	if !changed || !s.Cursor.Dismissed || !s.Cursor.Verified {
		t.Fatalf("verifying should keep dismissed flag: %+v", s.Cursor)
	}

	piAt := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	s, changed = s.withPiFlag(piAt, "id-1", true, false)
	if !changed || !s.piQuiet(piAt, "id-1") {
		t.Fatalf("expected pi id-1 quiet, changed=%v", changed)
	}
	if s.piQuiet(piAt, "id-2") || s.piQuiet(piAt.Add(time.Second), "id-1") {
		t.Error("changed pi install ID or timestamp should reset quiet state")
	}
	if _, again := s.withPiFlag(piAt, "id-1", true, false); again {
		t.Error("repeating same pi dismissal should report unchanged")
	}
}

func TestNoticeWriterSerializesAsyncSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notices.json")
	var w noticeWriter
	meta := monitor.CursorInstallMeta{Protocol: 1, CollectorVersion: 1, InstalledAt: "2026-11-13T07:00:00Z"}
	piAt := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)

	s1, _ := (noticeSettings{}).withCursorFlag(meta, true, false)
	s2, _ := s1.withPiFlag(piAt, "id-1", true, true)
	w.save(path, s1, true)
	w.save(path, s2, true)
	w.wait()

	got, err := readNoticeSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.cursorQuiet(meta) || !got.piQuiet(piAt, "id-1") {
		t.Fatalf("latest snapshot should win, got %+v", got)
	}
}
