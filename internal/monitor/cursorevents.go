package monitor

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	cursorEventSchema      = 1
	cursorCollectorVersion = 1
	// cursorHookInputLimit bounds what the collector reads off stdin, before it parses
	// anything. A hook that reads 8 MiB of prompt has already lost
	cursorHookInputLimit = 8 << 20
	// cursorEventRetention is how long the collector's own files stay: long enough to cover a
	// spark that wasn't running, short enough not to become an archive of when you worked
	cursorEventRetention = 7 * 24 * time.Hour
	// cursorEventReadLimit caps one scan's replay so a long backlog is worked through over
	// several scans instead of stalling the window
	cursorEventReadLimit = 2000
	cursorMaxRoots       = 32
	cursorMaxRootLen     = 4096
	cursorMaxIDLen       = 128
	cursorMaxVersionLen  = 64
	// cursorTempMaxAge is how long a half-written event file may sit there before pruning
	cursorTempMaxAge = time.Hour
)

// cursorHookEvents are the events spark asks Cursor for. The permission hooks (preToolUse,
// beforeShellExecution, beforeReadFile, subagentStart) are deliberately absent: an empty
// answer from one of those can block an action, and spark only observes
var cursorHookEvents = []string{
	"sessionStart",
	"beforeSubmitPrompt",
	"postToolUse",
	"postToolUseFailure",
	"afterAgentThought",
	"afterAgentResponse",
	"stop",
	"sessionEnd",
}

var (
	cursorTerminalStatuses = map[string]bool{"completed": true, "aborted": true, "error": true}
	cursorEndReasons       = map[string]bool{"completed": true, "aborted": true, "error": true, "window_close": true, "user_close": true}
)

// CursorEvent is one whitelisted hook report. It is spark's own record, not a copy of the
// hook payload: prompts, agent text, tool content, email and transcript paths never reach it
type CursorEvent struct {
	Schema           int    `json:"schema"`
	EventID          string `json:"event_id"`
	CollectorVersion int    `json:"collector_version"`
	// ReceivedAtMs is when this machine took the event in hand. It is not Cursor's own order
	// and not a sequence number
	ReceivedAtMs      int64    `json:"received_at_ms"`
	ConversationID    string   `json:"conversation_id"`
	GenerationID      string   `json:"generation_id,omitempty"`
	Event             string   `json:"event"`
	WorkspaceRoots    []string `json:"workspace_roots,omitempty"`
	CursorVersion     string   `json:"cursor_version,omitempty"`
	IsBackgroundAgent bool     `json:"is_background_agent"`
	Status            string   `json:"status,omitempty"`
	Reason            string   `json:"reason,omitempty"`
}

// CursorDir is where spark keeps everything it owns for Cursor
func CursorDir(home string) string {
	return filepath.Join(home, ".config", "spark", "cursor")
}

// CursorEventsDir holds one directory per day of collector events
func CursorEventsDir(home string) string {
	return filepath.Join(CursorDir(home), "events")
}

// cursorHookPayload is the whitelisted part of Cursor's hook input. Fields spark doesn't
// name here are dropped by the decoder, which is the point
type cursorHookPayload struct {
	ConversationID    string   `json:"conversation_id"`
	SessionID         string   `json:"session_id"`
	GenerationID      string   `json:"generation_id"`
	HookEventName     string   `json:"hook_event_name"`
	CursorVersion     string   `json:"cursor_version"`
	WorkspaceRoots    []string `json:"workspace_roots"`
	IsBackgroundAgent bool     `json:"is_background_agent"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason"`
}

// ReadAndCollectCursorHook reads a hook payload off r, refusing anything over the limit
func ReadAndCollectCursorHook(r io.Reader, home string, now time.Time) (CursorEvent, error) {
	body, err := io.ReadAll(io.LimitReader(r, cursorHookInputLimit+1))
	if err != nil {
		return CursorEvent{}, err
	}
	if len(body) > cursorHookInputLimit {
		return CursorEvent{}, fmt.Errorf("hook payload over %d bytes", cursorHookInputLimit)
	}
	return CollectCursorHook(body, home, now)
}

// CollectCursorHook keeps the fields spark needs from one hook payload and writes them as a
// single immutable file. A non-nil error means nothing was written: the caller logs it and
// leaves Cursor's own behaviour untouched
func CollectCursorHook(payload []byte, home string, now time.Time) (CursorEvent, error) {
	var p cursorHookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return CursorEvent{}, fmt.Errorf("parsing hook payload: %w", err)
	}
	if !knownCursorEvent(p.HookEventName) {
		return CursorEvent{}, fmt.Errorf("event %q is not one spark subscribes to", p.HookEventName)
	}
	// sessionStart and sessionEnd report the conversation as session_id; the docs make it the
	// same value as conversation_id
	conversation := cmpOrClean(p.ConversationID, p.SessionID)
	if err := validCursorID(conversation); err != nil {
		return CursorEvent{}, fmt.Errorf("conversation id: %w", err)
	}
	generation := p.GenerationID
	if validCursorID(generation) != nil {
		// A generation is an opaque correlation key: something unusable is left out rather
		// than replaced, so it can't end another turn
		generation = ""
	}
	ev := CursorEvent{
		Schema:            cursorEventSchema,
		CollectorVersion:  cursorCollectorVersion,
		ReceivedAtMs:      now.UnixMilli(),
		ConversationID:    conversation,
		GenerationID:      generation,
		Event:             p.HookEventName,
		WorkspaceRoots:    cleanCursorRoots(p.WorkspaceRoots),
		CursorVersion:     cleanCursorVersion(p.CursorVersion),
		IsBackgroundAgent: p.IsBackgroundAgent,
	}
	if p.HookEventName == "stop" && cursorTerminalStatuses[p.Status] {
		ev.Status = p.Status
	}
	if p.HookEventName == "sessionEnd" && cursorEndReasons[p.Reason] {
		ev.Reason = p.Reason
	}
	id, err := newCursorEventID()
	if err != nil {
		return CursorEvent{}, err
	}
	ev.EventID = id
	if err := writeCursorEvent(home, now, ev); err != nil {
		return CursorEvent{}, err
	}
	return ev, nil
}

func knownCursorEvent(name string) bool {
	for _, e := range cursorHookEvents {
		if e == name {
			return true
		}
	}
	return false
}

func cmpOrClean(a, b string) string {
	if a = strings.TrimSpace(a); a != "" {
		return a
	}
	return strings.TrimSpace(b)
}

// validCursorID accepts the opaque IDs Cursor hands out: trimmed, bounded, printable UTF-8
func validCursorID(id string) error {
	switch {
	case id == "":
		return errors.New("missing")
	case len(id) > cursorMaxIDLen:
		return fmt.Errorf("%d bytes is over %d", len(id), cursorMaxIDLen)
	case !utf8.ValidString(id):
		return errors.New("not utf-8")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return errors.New("contains a control character")
		}
	}
	return nil
}

// cleanCursorRoots keeps the workspace roots a session belongs to, cleaned, deduplicated and
// bounded. Multi-root workspaces keep every root: the first is only what the list groups by
func cleanCursorRoots(roots []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" || len(r) > cursorMaxRootLen || !utf8.ValidString(r) {
			continue
		}
		r = filepath.Clean(r)
		if r == "." || r == "/" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
		if len(out) == cursorMaxRoots {
			break
		}
	}
	return out
}

func cleanCursorVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > cursorMaxVersionLen || !utf8.ValidString(v) {
		return ""
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return v
}

func newCursorEventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// writeCursorEvent lands the event in the day directory. The temporary file is renamed in the
// same directory, so a reader sees either nothing or the whole event
func writeCursorEvent(home string, now time.Time, ev CursorEvent) error {
	dir := filepath.Join(CursorEventsDir(home), now.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, ev.EventID+".json"))
}

// CursorEventLog reads the collector's files once: the retention window on the first scan,
// then only names it hasn't seen
type CursorEventLog struct {
	seen  map[string]bool
	named bool
}

// Poll returns events it hasn't returned before and how many files are still behind. Files
// that arrive under a new day directory are picked up on the next poll
func (l *CursorEventLog) Poll(root string, now time.Time) (events []CursorEvent, backlog int) {
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	days, err := os.ReadDir(root)
	if err != nil {
		return nil, 0
	}
	names := make([]string, 0, len(days))
	for _, d := range days {
		if !d.IsDir() {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", d.Name(), time.Local)
		if err != nil || now.Sub(day) > cursorEventRetention+24*time.Hour {
			continue
		}
		names = append(names, d.Name())
	}
	sort.Strings(names)

	var files []string
	for _, day := range names {
		entries, err := os.ReadDir(filepath.Join(root, day))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			name := day + "/" + e.Name()
			if l.seen[name] {
				continue
			}
			files = append(files, name)
		}
	}
	sort.Strings(files)
	if len(files) > cursorEventReadLimit {
		backlog = len(files) - cursorEventReadLimit
		files = files[:cursorEventReadLimit]
	}
	for _, name := range files {
		l.seen[name] = true
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		var ev CursorEvent
		if json.Unmarshal(content, &ev) != nil || ev.Schema != cursorEventSchema || ev.ConversationID == "" {
			continue
		}
		events = append(events, ev)
	}
	return events, backlog
}

// Forget drops names that pruning has removed, so a long-running scan doesn't grow one map
// entry per event forever
func (l *CursorEventLog) Forget(now time.Time) {
	if len(l.seen) == 0 {
		return
	}
	cutoff := now.Add(-cursorEventRetention - 24*time.Hour).Format("2006-01-02")
	for name := range l.seen {
		if day, _, ok := strings.Cut(name, "/"); ok && day < cutoff {
			delete(l.seen, name)
		}
	}
}

// PruneCursorEventsRoot deletes the collector's own expired files, and nothing outside its
// directory. It is limited to one run per hour by the caller
func PruneCursorEventsRoot(root string, now time.Time) int {
	days, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, d := range days {
		path := filepath.Join(root, d.Name())
		if !d.IsDir() {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", d.Name(), time.Local)
		if err != nil {
			continue
		}
		if now.Sub(day) > cursorEventRetention {
			if err := os.RemoveAll(path); err == nil {
				removed++
			}
			continue
		}
		// A crash between write and rename leaves a partial file behind
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), ".partial-") {
				continue
			}
			info, err := e.Info()
			if err == nil && now.Sub(info.ModTime()) > cursorTempMaxAge {
				if os.Remove(filepath.Join(path, e.Name())) == nil {
					removed++
				}
			}
		}
	}
	return removed
}
