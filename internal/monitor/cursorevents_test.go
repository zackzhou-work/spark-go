package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func dayDir(home string, at time.Time) string {
	return filepath.Join(CursorEventsDir(home), at.Format("2006-01-02"))
}

// oneEventIn returns the only event file under home, parsed
func oneEventIn(t *testing.T, home string) CursorEvent {
	t.Helper()
	entries, err := os.ReadDir(dayDir(home, eventTestNow))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 event file, got %d", len(entries))
	}
	content, err := os.ReadFile(filepath.Join(dayDir(home, eventTestNow), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var ev CursorEvent
	if err := json.Unmarshal(content, &ev); err != nil {
		t.Fatalf("event file is not json: %v", err)
	}
	return ev
}

var eventTestNow = time.Date(2026, 10, 21, 10, 30, 0, 0, time.Local)

// The hook payload carries the prompt, the agent's text, tool input and the user's email.
// None of that belongs on disk: the collector keeps the fields a session list needs
func TestCollectorKeepsOnlyTheWhitelist(t *testing.T) {
	home := t.TempDir()
	payload := `{
		"conversation_id": "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		"generation_id": "gen-7",
		"hook_event_name": "beforeSubmitPrompt",
		"cursor_version": "3.24.12",
		"workspace_roots": ["/Users/someone/project"],
		"model": "claude-opus-4-7",
		"user_email": "someone@example.com",
		"transcript_path": "/Users/someone/.cursor/transcript.txt",
		"prompt": "refactor the billing module",
		"attachments": [{"type": "file", "file_path": "/Users/someone/project/a.go"}]
	}`
	ev, err := CollectCursorHook([]byte(payload), home, eventTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ConversationID != "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0" || ev.GenerationID != "gen-7" ||
		ev.Event != "beforeSubmitPrompt" || ev.CursorVersion != "3.24.12" {
		t.Errorf("unexpected event: %+v", ev)
	}
	if ev.Schema != cursorEventSchema || ev.CollectorVersion != cursorCollectorVersion {
		t.Errorf("schema %d collector %d", ev.Schema, ev.CollectorVersion)
	}
	if ev.ReceivedAtMs != eventTestNow.UnixMilli() {
		t.Errorf("received_at_ms should be the collector's own clock, got %d", ev.ReceivedAtMs)
	}
	if len(ev.WorkspaceRoots) != 1 || ev.WorkspaceRoots[0] != "/Users/someone/project" {
		t.Errorf("roots %v", ev.WorkspaceRoots)
	}

	raw, err := os.ReadFile(filepath.Join(dayDir(home, eventTestNow), ev.EventID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"refactor the billing", "someone@example.com", "transcript", "attachments", "claude-opus"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("event file still carries %q", banned)
		}
	}
}

func TestCollectorDropsIDsItCannotTrust(t *testing.T) {
	for name, payload := range map[string]string{
		"no id":         `{"hook_event_name":"stop"}`,
		"empty id":      `{"conversation_id":"","hook_event_name":"stop"}`,
		"overlong id":   `{"conversation_id":"` + strings.Repeat("a", 129) + `","hook_event_name":"stop"}`,
		"control char":  `{"conversation_id":"abc\u0000def","hook_event_name":"stop"}`,
		"unknown event": `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","hook_event_name":"beforeReadFile"}`,
		"broken json":   `{"conversation_id":`,
	} {
		home := t.TempDir()
		if _, err := CollectCursorHook([]byte(payload), home, eventTestNow); err == nil {
			t.Errorf("%s should be dropped", name)
		}
		if entries, err := os.ReadDir(CursorEventsDir(home)); err == nil && len(entries) > 0 {
			t.Errorf("%s should not be written", name)
		}
	}
}

// A new Cursor field is not a reason to guess: unknown statuses stay unset rather than being
// read as a finished turn
func TestCollectorKeepsOnlyKnownStatusesAndReasons(t *testing.T) {
	home := t.TempDir()
	payload := `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","generation_id":"g1","hook_event_name":"stop","status":"paused"}`
	ev, err := CollectCursorHook([]byte(payload), home, eventTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != "" {
		t.Errorf("unknown status should be dropped, got %q", ev.Status)
	}

	home = t.TempDir()
	payload = `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","hook_event_name":"stop","status":"aborted"}`
	if ev, err = CollectCursorHook([]byte(payload), home, eventTestNow); err != nil || ev.Status != "aborted" {
		t.Errorf("known status should be kept, got %+v err=%v", ev, err)
	}

	home = t.TempDir()
	payload = `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","hook_event_name":"sessionEnd","reason":"window_close","is_background_agent":true}`
	if ev, err = CollectCursorHook([]byte(payload), home, eventTestNow); err != nil {
		t.Fatal(err)
	}
	if ev.Reason != "window_close" || !ev.IsBackgroundAgent {
		t.Errorf("session end reason lost: %+v", ev)
	}
}

// sessionStart and sessionEnd report the conversation as session_id, which the common schema
// documents as the same value
func TestCollectorTakesTheSessionIDAsTheConversation(t *testing.T) {
	home := t.TempDir()
	payload := `{"session_id":"1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081","hook_event_name":"sessionStart","is_background_agent":false}`
	ev, err := CollectCursorHook([]byte(payload), home, eventTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ConversationID != "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081" {
		t.Errorf("got %q", ev.ConversationID)
	}
}

func TestCollectorBoundsWorkspaceRoots(t *testing.T) {
	home := t.TempDir()
	roots := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		roots = append(roots, "/w/"+strings.Repeat("x", i+1))
	}
	roots = append(roots, "", "/w//dup", "/w/dup", strings.Repeat("/", 5000))
	body, err := json.Marshal(map[string]any{
		"conversation_id": "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		"hook_event_name": "sessionStart",
		"workspace_roots": roots,
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := CollectCursorHook(body, home, eventTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.WorkspaceRoots) > cursorMaxRoots {
		t.Errorf("kept %d roots, cap is %d", len(ev.WorkspaceRoots), cursorMaxRoots)
	}
	for _, r := range ev.WorkspaceRoots {
		if r == "" || strings.HasSuffix(r, "/") || len(r) > cursorMaxRootLen {
			t.Errorf("root %q should be cleaned or dropped", r)
		}
	}
	seen := map[string]bool{}
	for _, r := range ev.WorkspaceRoots {
		if seen[r] {
			t.Errorf("duplicate root %q", r)
		}
		seen[r] = true
	}
}

// Cursor runs hooks concurrently, so two events arriving at once must not overwrite each other
func TestConcurrentCollectsDoNotOverwrite(t *testing.T) {
	home := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","hook_event_name":"postToolUse"}`
			if _, err := CollectCursorHook([]byte(payload), home, eventTestNow); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	entries, err := os.ReadDir(dayDir(home, eventTestNow))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		t.Errorf("want 20 event files, got %d", len(entries))
	}
	for _, e := range entries {
		content, err := os.ReadFile(filepath.Join(dayDir(home, eventTestNow), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var ev CursorEvent
		if err := json.Unmarshal(content, &ev); err != nil {
			t.Errorf("%s is a partial write: %v", e.Name(), err)
		}
	}
}

func TestCollectorBoundedRead(t *testing.T) {
	big := strings.Repeat("a", cursorHookInputLimit+1)
	payload := `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","hook_event_name":"stop","prompt":"` + big + `"}`
	home := t.TempDir()
	if _, err := ReadAndCollectCursorHook(strings.NewReader(payload), home, eventTestNow); err == nil {
		t.Error("a payload over the limit should be dropped")
	}
	if entries, err := os.ReadDir(CursorEventsDir(home)); err == nil && len(entries) > 0 {
		t.Error("nothing should be written")
	}
}

func TestEventLogReadsEachEventOnce(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 3; i++ {
		payload := `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","generation_id":"g1","hook_event_name":"postToolUse"}`
		if _, err := CollectCursorHook([]byte(payload), home, eventTestNow); err != nil {
			t.Fatal(err)
		}
	}
	var log CursorEventLog
	events, backlog := log.Poll(CursorEventsDir(home), eventTestNow)
	if len(events) != 3 || backlog != 0 {
		t.Fatalf("first poll: %d events, backlog %d", len(events), backlog)
	}
	if events, _ = log.Poll(CursorEventsDir(home), eventTestNow); len(events) != 0 {
		t.Errorf("second poll should add nothing, got %d", len(events))
	}
	// The window resets to a fresh log, which is what a restarted spark does
	var fresh CursorEventLog
	if events, _ = fresh.Poll(CursorEventsDir(home), eventTestNow); len(events) != 3 {
		t.Errorf("replay should see the retained events, got %d", len(events))
	}
}

func TestEventLogIgnoresExpiredDays(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(CursorEventsDir(home), eventTestNow.Add(-cursorEventRetention-48*time.Hour).Format("2006-01-02"))
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	ev := CursorEvent{Schema: cursorEventSchema, EventID: "old", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Event: "stop"}
	body, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(old, "old.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	var log CursorEventLog
	if events, _ := log.Poll(CursorEventsDir(home), eventTestNow); len(events) != 0 {
		t.Errorf("events outside the retention window should not be replayed, got %d", len(events))
	}
}

func TestEventLogCapsOneScanAndReportsTheBacklog(t *testing.T) {
	home := t.TempDir()
	dir := dayDir(home, eventTestNow)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	total := cursorEventReadLimit + 5
	for i := 0; i < total; i++ {
		ev := CursorEvent{Schema: cursorEventSchema, EventID: fmt.Sprintf("%032d", i), ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Event: "postToolUse"}
		body, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ev.EventID+".json"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var log CursorEventLog
	events, backlog := log.Poll(CursorEventsDir(home), eventTestNow)
	if len(events) != cursorEventReadLimit || backlog != total-cursorEventReadLimit {
		t.Fatalf("first scan: %d events, backlog %d", len(events), backlog)
	}
	// The rest arrives next scan, so a burst is worked off without losing the newest turn
	events, backlog = log.Poll(CursorEventsDir(home), eventTestNow)
	if len(events) != total-cursorEventReadLimit || backlog != 0 {
		t.Errorf("second scan: %d events, backlog %d", len(events), backlog)
	}
}

func TestPruneRemovesOnlyExpiredEventFiles(t *testing.T) {
	home := t.TempDir()
	expired := filepath.Join(CursorEventsDir(home), eventTestNow.Add(-cursorEventRetention-time.Hour).Format("2006-01-02"))
	fresh := dayDir(home, eventTestNow)
	for _, dir := range []string{expired, fresh} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	partial := filepath.Join(fresh, ".partial-123")
	if err := os.WriteFile(partial, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := eventTestNow.Add(-2 * cursorTempMaxAge)
	if err := os.Chtimes(partial, stale, stale); err != nil {
		t.Fatal(err)
	}
	// Not spark's to delete
	outside := filepath.Join(home, "keep.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if removed := PruneCursorEventsRoot(CursorEventsDir(home), eventTestNow); removed != 2 {
		t.Errorf("want the expired day and the partial file gone, removed %d", removed)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Error("expired day should be gone")
	}
	if _, err := os.Stat(filepath.Join(fresh, "a.json")); err != nil {
		t.Error("a retained event should stay")
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Error("the stale partial file should be gone")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("files outside the events directory must stay")
	}
}
