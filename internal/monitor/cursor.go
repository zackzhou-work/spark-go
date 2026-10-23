package monitor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

const (
	// cursorSnapshotTTL is how long the last good metadata read may stand in for one that
	// failed. Past it the metadata is stale, and a stale read must not clear an unread flag or
	// confirm that a row can jump
	cursorSnapshotTTL = 30 * time.Second
	// cursorRootsTTL is how long the workspace ID to folder map is reused before it is asked
	// again
	cursorRootsTTL = 5 * time.Minute
	// cursorBuildTTL is how often the installed app is asked for its version
	cursorBuildTTL = 10 * time.Minute
	// cursorPruneEvery keeps event cleanup and the backlog log to once an hour
	cursorPruneEvery = time.Hour
)

// cursorSnapshot is the last metadata read that worked, kept so a locked database doesn't empty
// the list
type cursorSnapshot struct {
	headers []cursorHeader
	readAt  time.Time
	valid   bool
}

// scanCursor reads Cursor's own metadata and hook reports. It reads; it never writes to Cursor's
// files, and it never opens a window
func (s *Scanner) scanCursor(now time.Time) []discovered {
	events := s.cursorEventHistory(now)
	headers, readAt, fresh := s.cursorHeaderSnapshot(now)
	if len(headers) == 0 {
		return nil
	}
	jumpAllowed := s.cursorJumpEnabled(now)
	states := make(map[string]TaskState, len(headers))
	var found []discovered
	for _, header := range headers {
		states[header.ID] = Unknown
		report := events[header.ID]
		roots := header.WorkspaceRoots
		if len(roots) == 0 {
			if root := s.cursorWorkspaceRoot(header.WorkspaceID, now); root != "" {
				roots = []string{root}
			}
		}
		if len(roots) == 0 {
			roots = cursorHookRoots(report)
		}
		activityAt := header.UpdatedAt
		for _, ev := range report {
			// A conversation being opened reports sessionStart too, which is not a turn
			if ev.Event == "sessionStart" {
				continue
			}
			if at := timestampTime(ev.ReceivedAtMs); at.After(activityAt) {
				activityAt = at
			}
		}
		if activityAt.IsZero() {
			activityAt = header.CreatedAt
		}

		// A snapshot older than the window is only a label: with no trustworthy metadata the
		// state falls back to what the events say, and jumping waits for a fresh read
		evidence := cursorSessionEvidence{Events: report, HeaderReadAt: readAt}
		if fresh {
			h := header
			evidence.Header = &h
		}
		verdict := decideCursorVerdict(evidence, now)

		item := SessionItem{
			ID:         header.ID,
			Source:     SourceCursor,
			Title:      header.Title,
			State:      verdict.State,
			WorkingDir: firstCursorRoot(roots),
			ActivityAt: activityAt,
			// The private Agent link is only offered for a local desktop session this build has
			// verified, while the metadata behind it is fresh
			CanJumpCursor: fresh && header.LocalDesktop && jumpAllowed && isUUIDShape(header.ID),
		}
		item.IsToday = verdict.State == Running || verdict.State == Waiting || isSameLocalDay(activityAt, now)
		s.logCursorChange(states, header.ID, verdict)
		found = append(found, discovered{cursorProjectName(roots), item, activityAt.UnixMilli()})
	}
	// Forget conversations Cursor has dropped, so this map can't grow forever
	s.cursorStates = states
	return found
}

// CursorReported reports whether the helper this build installs has written an event. The
// configuration being in place is a different question
func (s *Scanner) CursorReported() bool {
	return s.cursorSawEvent
}

// logCursorChange writes one line when a conversation's state moves, with the reducer's own reason.
// It is the only place the internal reason surfaces: the list itself shows a state, not why
func (s *Scanner) logCursorChange(states map[string]TaskState, id string, verdict cursorVerdict) {
	if s.cursorStates == nil {
		s.cursorStates = map[string]TaskState{}
	}
	states[id] = verdict.State
	previous, seen := s.cursorStates[id]
	if !seen || previous == verdict.State {
		return
	}
	terminal := ""
	if verdict.Terminal != "" {
		terminal = ", terminal " + verdict.Terminal
	}
	log.Printf("[Cursor] %s: %s -> %s (%s%s)", id, previous, verdict.State, verdict.Reason, terminal)
}

// pruneCursorEvents clears expired hook files and forgets their names, at most once an hour
func (s *Scanner) pruneCursorEvents(now time.Time) {
	if !s.cursorPrunedAt.IsZero() && now.Sub(s.cursorPrunedAt) < cursorPruneEvery {
		return
	}
	s.cursorPrunedAt = now
	if PruneCursorEventsRoot(s.CursorEventsRoot, now) > 0 {
		s.cursorLog.Forget(now)
	}
}

// cursorHeaderSnapshot reads the headers, falling back to the last good read while Cursor holds
// the database. fresh says whether a row may be trusted for state and jumping
func (s *Scanner) cursorHeaderSnapshot(now time.Time) (headers []cursorHeader, readAt time.Time, fresh bool) {
	read, err := ReadCursorHeaders(s.CursorDB, now)
	if err == nil {
		s.cursorSnap = cursorSnapshot{headers: read, readAt: now, valid: true}
		return read, now, true
	}
	if s.cursorSnap.valid {
		// Inside the window the last read still counts. Past it the rows are only labels: the
		// state says it can't be told, and nothing claims to be able to jump
		age := now.Sub(s.cursorSnap.readAt)
		if age <= cursorSnapshotTTL {
			return s.cursorSnap.headers, s.cursorSnap.readAt, true
		}
		if _, statErr := os.Stat(s.CursorDB); statErr == nil {
			return s.cursorSnap.headers, s.cursorSnap.readAt, false
		}
	}
	s.cursorSnap = cursorSnapshot{}
	return nil, time.Time{}, false
}

// cursorEventHistory reads new hook reports and keeps the retained history per conversation, so
// the reducer always sees a whole turn instead of only what arrived since the last scan
func (s *Scanner) cursorEventHistory(now time.Time) map[string][]CursorEvent {
	if s.cursorEvents == nil {
		s.cursorEvents = map[string][]CursorEvent{}
	}
	events, backlog := s.cursorLog.Poll(s.CursorEventsRoot, now)
	for _, ev := range events {
		if ev.CollectorVersion == cursorCollectorVersion {
			// Something is running the helper this build installed, which is all "reporting
			// works" can mean without Cursor's own acknowledgement
			s.cursorSawEvent = true
		}
		s.cursorEvents[ev.ConversationID] = append(s.cursorEvents[ev.ConversationID], ev)
	}
	if backlog > 0 {
		log.Printf("[Cursor] %d hook events still queued", backlog)
	}
	// Anything past the retention window is gone from disk as well
	cutoff := now.Add(-cursorEventRetention).UnixMilli()
	for id, list := range s.cursorEvents {
		kept := list[:0]
		for _, ev := range list {
			if ev.ReceivedAtMs >= cutoff {
				kept = append(kept, ev)
			}
		}
		if len(kept) == 0 {
			delete(s.cursorEvents, id)
			continue
		}
		s.cursorEvents[id] = kept
	}
	return s.cursorEvents
}

// cursorWorkspaceRoot resolves a workspace ID to its folder through the per-workspace files.
// The map is reused for a few minutes: it only changes when a workspace is opened
func (s *Scanner) cursorWorkspaceRoot(workspaceID string, now time.Time) string {
	if workspaceID == "" {
		return ""
	}
	if s.cursorRoots == nil || now.Sub(s.cursorRootsAt) >= cursorRootsTTL {
		s.cursorRoots = ReadCursorWorkspaceRoots(s.CursorUserDir)
		s.cursorRootsAt = now
	}
	return s.cursorRoots[workspaceID]
}

// cursorJumpEnabled reports whether this Cursor build is one the private Agent link is verified
// for. The app bundle is read at most every ten minutes
func (s *Scanner) cursorJumpEnabled(now time.Time) bool {
	if s.cursorBuildAt.IsZero() || now.Sub(s.cursorBuildAt) >= cursorBuildTTL {
		build, ok := CursorBuild(s.CursorAppPath)
		s.cursorBuildAt = now
		s.cursorBuildOK = ok
		s.cursorBuild = build
	}
	if !s.cursorBuildOK {
		return false
	}
	return CursorJumpAllowed(s.cursorBuild.Version, s.cursorBuild.Commit)
}

// cursorHookRoots is the newest workspace list a hook reported for a conversation, used only when
// the metadata names no workspace
func cursorHookRoots(events []CursorEvent) []string {
	var roots []string
	newest := int64(-1)
	for _, ev := range events {
		if len(ev.WorkspaceRoots) == 0 || ev.ReceivedAtMs < newest {
			continue
		}
		newest = ev.ReceivedAtMs
		roots = ev.WorkspaceRoots
	}
	return roots
}

// firstCursorRoot is the root the list groups by: the first one, cleaned. Multi-root workspaces
// keep the rest for association
func firstCursorRoot(roots []string) string {
	for _, r := range roots {
		if r != "" {
			return r
		}
	}
	return ""
}

func cursorProjectName(roots []string) string {
	for _, r := range roots {
		base := filepath.Base(r)
		if base != "" && base != "/" && base != "." {
			return base
		}
	}
	return "Unknown"
}

// CursorBuild reports the installed Cursor's version and commit. The compatibility table is keyed
// by both: a newer version is a different build, and the private link is a build-specific fact
func CursorBuild(appPath string) (cursorBuild, bool) {
	if appPath == "" {
		return cursorBuild{}, false
	}
	appDir := filepath.Join(appPath, "Contents", "Resources", "app")
	var pkg struct {
		Version string `json:"version"`
	}
	body, err := os.ReadFile(filepath.Join(appDir, "package.json"))
	if err != nil || json.Unmarshal(body, &pkg) != nil || pkg.Version == "" {
		return cursorBuild{}, false
	}
	var prod struct {
		Commit string `json:"commit"`
	}
	if body, err := os.ReadFile(filepath.Join(appDir, "product.json")); err == nil {
		_ = json.Unmarshal(body, &prod)
	}
	return cursorBuild{Version: pkg.Version, Commit: prod.Commit}, true
}

// CursorPresent reports whether Cursor looks installed: its app bundle or its metadata. Users who
// don't run Cursor shouldn't be told that something is missing
func CursorPresent(home string) bool {
	if isDir(CursorAppPath()) {
		return true
	}
	_, err := os.Stat(CursorGlobalDBPath(home))
	return err == nil
}

// CursorAppPath is where the stable Cursor app installs on macOS. Insiders and other profiles are
// out of scope
func CursorAppPath() string {
	return "/Applications/Cursor.app"
}

// cursorWatchDirs are the two places a Cursor change can show up: its global storage, and spark's
// own event directory. Neither is created here, and a directory that doesn't exist yet is left to
// the fallback poll
func (s *Scanner) cursorWatchDirs() []string {
	var dirs []string
	if isDir(filepath.Dir(s.CursorDB)) {
		dirs = append(dirs, filepath.Dir(s.CursorDB))
	}
	eventsParent := filepath.Dir(s.CursorEventsRoot)
	if isDir(eventsParent) {
		dirs = append(dirs, eventsParent)
	} else if parent := filepath.Dir(eventsParent); isDir(parent) {
		// spark's own config directory: watching it notices the event directory appearing, and it
		// holds nothing else big enough to be expensive
		dirs = append(dirs, parent)
	}
	return dirs
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
