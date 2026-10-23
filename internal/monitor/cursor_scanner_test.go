package monitor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cursorTestScanner points every other source at empty directories, so a test only sees the
// Cursor side it built
func cursorTestScanner(t *testing.T, dbPath, userDir, eventsRoot, appPath string) *Scanner {
	t.Helper()
	empty := t.TempDir()
	s := NewScanner()
	s.SessionsDir = filepath.Join(empty, "claude-sessions")
	s.ProjectsRoot = filepath.Join(empty, "projects")
	s.HooksDir = filepath.Join(empty, "hooks")
	s.CodexRoot = filepath.Join(empty, "codex")
	s.CodexHome = filepath.Join(empty, "codex-home")
	s.CodexHookDir = filepath.Join(empty, "codex-hooks")
	s.PiRoot = filepath.Join(empty, "pi")
	s.PiReceiptDir = filepath.Join(empty, "pi-receipts")
	s.HerdrSocket = filepath.Join(empty, "herdr.sock")
	s.CursorDB = dbPath
	s.CursorUserDir = userDir
	s.CursorEventsRoot = eventsRoot
	s.CursorAppPath = appPath
	return s
}

func seedCursorEvent(t *testing.T, eventsRoot string, at time.Time, ev CursorEvent) {
	t.Helper()
	dir := filepath.Join(eventsRoot, at.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ev.EventID+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func sessionsOf(groups []ProjectGroup) map[string]ProjectGroup {
	out := map[string]ProjectGroup{}
	for _, g := range groups {
		out[g.ProjectName] = g
	}
	return out
}

func sessionByID(groups []ProjectGroup, id string) (SessionItem, bool) {
	for _, g := range groups {
		for _, s := range g.Sessions {
			if s.ID == id {
				return s, true
			}
		}
	}
	return SessionItem{}, false
}

// Two conversations in one project, one of them running, is the whole point: both show up, each
// with its own state
func TestScanListsCursorSessionsSideBySide(t *testing.T) {
	eventsRoot := t.TempDir()
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			createdAt: headerTestNow.Add(-time.Hour).UnixMilli(), lastUpdatedAt: headerTestNow.Add(-time.Minute).UnixMilli(),
			value: headerValue(map[string]any{
				"type": "head", "name": "First", "unifiedMode": "agent",
				"hasUnreadMessages":   true,
				"workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/alpha")},
			})},
		fixtureComposer{id: "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081", workspaceID: "w1",
			createdAt: headerTestNow.Add(-2 * time.Hour).UnixMilli(), lastUpdatedAt: headerTestNow.Add(-time.Hour).UnixMilli(),
			value: headerValue(map[string]any{
				"type": "head", "name": "Second", "unifiedMode": "agent",
				"workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/alpha")},
			})},
	)
	seedCursorEvent(t, eventsRoot, headerTestNow.Add(-10*time.Second), CursorEvent{
		Schema: cursorEventSchema, EventID: "e1", ConversationID: "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081",
		GenerationID: "g1", Event: "postToolUse", ReceivedAtMs: headerTestNow.Add(-10 * time.Second).UnixMilli(),
	})
	s := cursorTestScanner(t, dbPath, t.TempDir(), eventsRoot, t.TempDir())
	groups := s.scanAll(headerTestNow)

	alpha, ok := sessionsOf(groups)["alpha"]
	if !ok {
		t.Fatalf("no alpha group in %+v", groups)
	}
	if len(alpha.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %+v", alpha.Sessions)
	}
	first, _ := sessionByID(groups, "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	if first.Source != SourceCursor || first.Title != "First" {
		t.Errorf("session %+v", first)
	}
	if first.State != Unread {
		t.Errorf("a session with unread metadata and no events should be unread, got %v", first.State)
	}
	second, _ := sessionByID(groups, "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081")
	if second.State != Running {
		t.Errorf("recent activity should be running, got %v", second.State)
	}
	// Running sessions stay in today's list even if their last write was yesterday
	if !second.IsToday {
		t.Error("a running session is today's")
	}
	if first.WorkingDir != "/Users/someone/alpha" {
		t.Errorf("working dir %q", first.WorkingDir)
	}
}

// Conversations Cursor only ever reported a hook for are not desktop sessions; the metadata is
// what says a session can be listed
func TestCursorEventsAloneDoNotAddASession(t *testing.T) {
	eventsRoot := t.TempDir()
	dbPath := newFixtureDB(t, fixtureHeaderColumns)
	seedCursorEvent(t, eventsRoot, headerTestNow.Add(-10*time.Second), CursorEvent{
		Schema: cursorEventSchema, EventID: "e1", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		GenerationID: "g1", Event: "stop", Status: "completed", ReceivedAtMs: headerTestNow.Add(-10 * time.Second).UnixMilli(),
	})
	s := cursorTestScanner(t, dbPath, t.TempDir(), eventsRoot, t.TempDir())
	if groups := s.scanAll(headerTestNow); len(groups) != 0 {
		t.Errorf("hook-only conversations should not be listed: %+v", groups)
	}
}

// An older header names its workspace only by ID, which the per-workspace files still resolve
func TestCursorWorkspaceFallsBackToTheWorkspaceFile(t *testing.T) {
	userDir := t.TempDir()
	ws := filepath.Join(userDir, "workspaceStorage", "w9")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"folder":"file:///Users/someone/beta"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w9",
			value: headerValue(map[string]any{"type": "head", "name": "x", "workspaceIdentifier": map[string]any{"id": "w9"}})},
	)
	s := cursorTestScanner(t, dbPath, userDir, t.TempDir(), t.TempDir())
	groups := s.scanAll(headerTestNow)
	if _, ok := sessionsOf(groups)["beta"]; !ok {
		t.Errorf("want a beta group, got %+v", groups)
	}
}

// Hook roots only fill a gap: metadata that names the workspace wins, and every root of a
// multi-root workspace is kept
func TestCursorRootsPreferMetadataThenHooks(t *testing.T) {
	eventsRoot := t.TempDir()
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			value: headerValue(map[string]any{
				"type": "head", "name": "known", "workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/known")},
			})},
		fixtureComposer{id: "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081", workspaceID: "w2",
			value: headerValue(map[string]any{"type": "head", "name": "unknown"})},
	)
	seedCursorEvent(t, eventsRoot, headerTestNow.Add(-30*time.Second), CursorEvent{
		Schema: cursorEventSchema, EventID: "e1", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		Event: "sessionStart", ReceivedAtMs: headerTestNow.Add(-30 * time.Second).UnixMilli(),
		WorkspaceRoots: []string{"/Users/someone/elsewhere"},
	})
	seedCursorEvent(t, eventsRoot, headerTestNow.Add(-20*time.Second), CursorEvent{
		Schema: cursorEventSchema, EventID: "e2", ConversationID: "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081",
		Event: "sessionStart", ReceivedAtMs: headerTestNow.Add(-20 * time.Second).UnixMilli(),
		WorkspaceRoots: []string{"/Users/someone/fromhooks", "/Users/someone/secondroot"},
	})
	s := cursorTestScanner(t, dbPath, t.TempDir(), eventsRoot, t.TempDir())
	groups := s.scanAll(headerTestNow)
	if _, ok := sessionsOf(groups)["known"]; !ok {
		t.Errorf("metadata roots should win, got %+v", groups)
	}
	if _, ok := sessionsOf(groups)["elsewhere"]; ok {
		t.Error("hook roots should not override metadata")
	}
	if _, ok := sessionsOf(groups)["fromhooks"]; !ok {
		t.Errorf("hook roots should fill a gap, got %+v", groups)
	}
}

// While Cursor holds the database, the last good read keeps standing for 30 seconds; after that
// the metadata is stale and the sessions say so instead of pretending
func TestCursorLockedDatabaseFallsBackThenGivesUp(t *testing.T) {
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			lastUpdatedAt: headerTestNow.Add(-time.Minute).UnixMilli(),
			value: headerValue(map[string]any{
				"type": "head", "name": "held", "hasUnreadMessages": true,
				"workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/held")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), t.TempDir())
	if groups := s.scanAll(headerTestNow); len(groups) != 1 {
		t.Fatalf("first scan: %+v", groups)
	}

	holder, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}

	soon := headerTestNow.Add(10 * time.Second)
	held := s.scanAll(soon)
	if len(held) != 1 || held[0].Sessions[0].Title != "held" {
		t.Fatalf("the last read should stand for a moment: %+v", held)
	}
	if held[0].Sessions[0].State != Unread {
		t.Errorf("metadata inside the snapshot window still counts, got %v", held[0].Sessions[0].State)
	}

	later := s.scanAll(headerTestNow.Add(cursorSnapshotTTL + 5*time.Second))
	if len(later) != 1 {
		t.Fatalf("the session should still be listed: %+v", later)
	}
	if got := later[0].Sessions[0].State; got != Unknown {
		t.Errorf("stale metadata should read as unknown, got %v", got)
	}
	if later[0].Sessions[0].CanJumpCursor {
		t.Error("jump eligibility needs fresh metadata")
	}
}

// A build nobody has clicked through gets no link, even with a plausible version number
func TestCursorJumpNeedsAVerifiedBuild(t *testing.T) {
	appPath := t.TempDir()
	appDir := filepath.Join(appPath, "Contents", "Resources", "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte(`{"version":"9.9.9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "product.json"), []byte(`{"commit":"unknown"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			value: headerValue(map[string]any{
				"type": "head", "name": "x", "workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/gamma")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), appPath)
	groups := s.scanAll(headerTestNow)
	if len(groups) != 1 {
		t.Fatalf("%+v", groups)
	}
	if got := groups[0].Sessions[0]; got.CanJumpCursor || got.Jumpable() {
		t.Errorf("an unverified build should not offer the private link: %+v", got)
	}

	// The verified build offers it without an opt-in
	verified := t.TempDir()
	verifiedApp := filepath.Join(verified, "Contents", "Resources", "app")
	if err := os.MkdirAll(verifiedApp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(verifiedApp, "package.json"), []byte(`{"version":"3.24.12"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(verifiedApp, "product.json"), []byte(`{"commit":"37b865941ccb31e2fc90c685157534efca87f960"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	known := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), verified)
	if groups := known.scanAll(headerTestNow); !groups[0].Sessions[0].CanJumpCursor {
		t.Error("the verified build should offer the link")
	}

	// The documented opt-in is how the owner runs that check on another build by hand
	t.Setenv("SPARK_CURSOR_JUMP", "1")
	opted := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), appPath)
	groups = opted.scanAll(headerTestNow)
	if !groups[0].Sessions[0].CanJumpCursor {
		t.Error("the opt-in should offer the link on a local desktop session")
	}
}

// A Cursor conversation and a Codex thread can share a UUID; they are different sessions
func TestCursorRowsAreNotMergedWithOtherSourcesByID(t *testing.T) {
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			value: headerValue(map[string]any{
				"type": "head", "name": "same id", "workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/delta")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), t.TempDir())
	s.ProjectsRoot = t.TempDir()
	groups := s.scanAll(headerTestNow)
	found, ok := sessionByID(groups, "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	if !ok || found.Source != SourceCursor {
		t.Fatalf("got %+v ok=%v", found, ok)
	}
}

func TestCursorSessionWithoutATimeSortsIntoEarlier(t *testing.T) {
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			value: headerValue(map[string]any{
				"type": "head", "workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/epsilon")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), t.TempDir())
	groups := s.scanAll(headerTestNow)
	if len(groups) != 1 {
		t.Fatalf("%+v", groups)
	}
	session := groups[0].Sessions[0]
	if session.Title != "Untitled Session" {
		t.Errorf("title %q", session.Title)
	}
	if !session.ActivityAt.IsZero() || session.IsToday {
		t.Errorf("a session with no time is not today's: %+v", session)
	}
	if session.State != Unknown {
		t.Errorf("no evidence should be unknown, got %v", session.State)
	}
}

func TestCursorBuildReadsTheAppBundle(t *testing.T) {
	appPath := t.TempDir()
	appDir := filepath.Join(appPath, "Contents", "Resources", "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte(`{"version":"3.24.12","distro":"d5c0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "product.json"), []byte(`{"commit":"37b8659"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	build, ok := CursorBuild(appPath)
	if !ok || build.Version != "3.24.12" || build.Commit != "37b8659" {
		t.Errorf("got %+v ok=%v", build, ok)
	}
	if _, ok := CursorBuild(t.TempDir()); ok {
		t.Error("an empty directory is not an install")
	}
}

func TestCursorWatchDirsStayNarrow(t *testing.T) {
	home := t.TempDir()
	s := NewScanner()
	s.CursorEventsRoot = CursorEventsDir(home)
	s.CursorDB = CursorGlobalDBPath(home)
	// Neither exists yet: watching all of Application Support would be thousands of directories
	for _, dir := range s.WatchDirs() {
		if dir == filepath.Dir(filepath.Dir(s.CursorDB)) {
			t.Errorf("should not watch %s", dir)
		}
	}
	if err := os.MkdirAll(filepath.Dir(s.CursorDB), 0o755); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, dir := range s.WatchDirs() {
		if dir == filepath.Dir(s.CursorDB) {
			found = true
		}
	}
	if !found {
		t.Error("the global storage directory should be watched once it exists")
	}
}

// What ties one ended turn to its metadata is when the metadata was read, not the header's own
// timestamps: Cursor stamps those when a turn starts and when its last content lands
func TestCursorEndedTurnNeedsAMetadataReadFromAfterIt(t *testing.T) {
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			lastUpdatedAt: headerTestNow.Add(-2 * time.Minute).UnixMilli(),
			value: headerValue(map[string]any{
				"type": "head", "name": "ended", "hasUnreadMessages": false,
				"workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/zeta")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), t.TempDir())
	// The turn is running when the first read happens
	submit := CursorEvent{Schema: cursorEventSchema, EventID: "e1", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		GenerationID: "g1", Event: "postToolUse", ReceivedAtMs: headerTestNow.Add(-time.Minute).UnixMilli()}
	seedCursorEvent(t, s.CursorEventsRoot, headerTestNow.Add(-time.Minute), submit)
	if got := s.scanAll(headerTestNow)[0].Sessions[0].State; got != Running {
		t.Fatalf("want running, got %v", got)
	}

	// The turn ends while Cursor holds the database, so the only metadata spark has is the read
	// from before it ended
	holder, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	// The stop arrives after that read, so the snapshot cannot describe it
	stopAt := headerTestNow.Add(10 * time.Second)
	seedCursorEvent(t, s.CursorEventsRoot, stopAt, CursorEvent{
		Schema: cursorEventSchema, EventID: "e2", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		GenerationID: "g1", Event: "stop", Status: "completed", ReceivedAtMs: stopAt.UnixMilli(),
	})
	if got := s.scanAll(stopAt.Add(2 * time.Second))[0].Sessions[0].State; got != Unknown {
		t.Errorf("a read from before the stop must not decide the turn, got %v", got)
	}
}

// The header's own timestamp moving past the stop is exactly what this build doesn't do, so it
// must not be what the decision waits for
func TestCursorUnreadComesFromTheHeaderAsItIsNow(t *testing.T) {
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			// Stamped well before the stop, which is what 3.24.12 does
			lastUpdatedAt: headerTestNow.Add(-2 * time.Minute).UnixMilli(),
			value: headerValue(map[string]any{
				"type": "head", "name": "ended", "hasUnreadMessages": true,
				"workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/eta")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), t.TempDir())
	seedCursorEvent(t, s.CursorEventsRoot, headerTestNow.Add(-time.Minute), CursorEvent{
		Schema: cursorEventSchema, EventID: "e1", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		GenerationID: "g1", Event: "beforeSubmitPrompt", ReceivedAtMs: headerTestNow.Add(-time.Minute).UnixMilli(),
	})
	seedCursorEvent(t, s.CursorEventsRoot, headerTestNow.Add(-30*time.Second), CursorEvent{
		Schema: cursorEventSchema, EventID: "e2", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		GenerationID: "g1", Event: "stop", Status: "completed", ReceivedAtMs: headerTestNow.Add(-30 * time.Second).UnixMilli(),
	})
	groups := s.scanAll(headerTestNow)
	if got := groups[0].Sessions[0].State; got != Unread {
		t.Errorf("want unread from the metadata read now, got %v", got)
	}
}

// Opening a conversation reports sessionStart, which is not a turn and must not pull an old
// session into today
func TestCursorSessionStartIsNotActivity(t *testing.T) {
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			lastUpdatedAt: headerTestNow.Add(-72 * time.Hour).UnixMilli(),
			value: headerValue(map[string]any{
				"type": "head", "name": "old", "workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/theta")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), t.TempDir())
	seedCursorEvent(t, s.CursorEventsRoot, headerTestNow.Add(-time.Minute), CursorEvent{
		Schema: cursorEventSchema, EventID: "e1", ConversationID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		Event: "sessionStart", ReceivedAtMs: headerTestNow.Add(-time.Minute).UnixMilli(),
	})
	session := s.scanAll(headerTestNow)[0].Sessions[0]
	if session.IsToday {
		t.Error("a session that was merely opened is not today's work")
	}
	if want := time.UnixMilli(headerTestNow.Add(-72 * time.Hour).UnixMilli()); !session.ActivityAt.Equal(want) {
		t.Errorf("activity %v, want the header's own time %v", session.ActivityAt, want)
	}
}

// Cursor replaces its state database wholesale; the next scan has to read the new one rather than
// keep answering from the old
func TestCursorReplacedDatabaseIsPickedUp(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.vscdb")
	writeFixtureDB(t, dbPath, fixtureHeaderColumns, "",
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1",
			value: headerValue(map[string]any{
				"type": "head", "name": "before", "workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/iota")},
			})},
	)
	s := cursorTestScanner(t, dbPath, t.TempDir(), t.TempDir(), t.TempDir())
	if got := s.scanAll(headerTestNow); len(got) != 1 || got[0].Sessions[0].Title != "before" {
		t.Fatalf("first scan: %+v", got)
	}

	// Cursor writes a fresh file beside the old one and renames it into place
	replacement := filepath.Join(dir, "state.vscdb.new")
	writeFixtureDB(t, replacement, fixtureHeaderColumns, "",
		fixtureComposer{id: "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081", workspaceID: "w1",
			value: headerValue(map[string]any{
				"type": "head", "name": "after", "workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/kappa")},
			})},
	)
	if err := os.Rename(replacement, dbPath); err != nil {
		t.Fatal(err)
	}
	groups := s.scanAll(headerTestNow.Add(5 * time.Second))
	if len(groups) != 1 {
		t.Fatalf("after replacement: %+v", groups)
	}
	if got := groups[0].Sessions[0].Title; got != "after" {
		t.Errorf("kept the old read: %q", got)
	}
}
