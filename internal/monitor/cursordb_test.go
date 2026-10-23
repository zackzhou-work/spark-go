package monitor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureComposer is one row of a synthetic composerHeaders table. Every fixture in this file
// is hand written: no real database, title or transcript is copied into a test
type fixtureComposer struct {
	id            string
	workspaceID   string
	createdAt     int64
	lastUpdatedAt int64
	archived      int
	subagent      int
	value         string
}

const fixtureHeaderColumns = `composerId TEXT PRIMARY KEY, workspaceId TEXT, createdAt INTEGER,
	lastUpdatedAt INTEGER, isArchived INTEGER, isSubagent INTEGER, recency INTEGER,
	checkpointAt INTEGER, subagentTypeName TEXT, value TEXT`

// newFixtureDB writes a database with the modern header layout
func newFixtureDB(t *testing.T, columns string, composers ...fixtureComposer) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	writeFixtureDB(t, path, columns, "", composers...)
	return path
}

func writeFixtureDB(t *testing.T, path, columns, dsnSuffix string, composers ...fixtureComposer) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+dsnSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if columns != "" {
		if _, err := db.Exec(`CREATE TABLE composerHeaders (` + columns + `)`); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range composers {
		if _, err := db.Exec(`INSERT INTO composerHeaders
			(composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent, recency, checkpointAt, subagentTypeName, value)
			VALUES (?,?,?,?,?,?,0,0,'',?)`,
			c.id, c.workspaceID, c.createdAt, c.lastUpdatedAt, c.archived, c.subagent, c.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// headerValue builds the JSON a modern row stores, with only the fields called for
func headerValue(fields map[string]any) string {
	if fields == nil {
		return "{}"
	}
	body, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func localURI(path string) map[string]any {
	return map[string]any{"$mid": 1, "external": "file://" + path, "fsPath": path, "path": path, "scheme": "file"}
}

var headerTestNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)

func TestReadCursorHeadersKeepsLocalDesktopAgents(t *testing.T) {
	unreadAt := headerTestNow.Add(-time.Hour).UnixMilli()
	createdAt := headerTestNow.Add(-48 * time.Hour).UnixMilli()
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w1", createdAt: createdAt, lastUpdatedAt: unreadAt,
			value: headerValue(map[string]any{
				"type":                "head",
				"name":                "Refactor the parser",
				"unifiedMode":         "agent",
				"hasUnreadMessages":   true,
				"isDraft":             false,
				"isArchived":          false,
				"workspaceIdentifier": map[string]any{"id": "w1", "uri": localURI("/Users/someone/project")},
				"agentLocation":       map[string]any{"type": "local", "status": "active", "environment": map[string]any{"id": "abc"}},
			})},
		// A local agent with nothing optional recorded
		fixtureComposer{id: "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081", workspaceID: "w2",
			value: headerValue(map[string]any{"type": "head", "unifiedMode": "agent"})},
	)
	headers, err := ReadCursorHeaders(dbPath, headerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 2 {
		t.Fatalf("want 2 headers, got %d: %+v", len(headers), headers)
	}
	a := headers[0]
	if a.ID != "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0" || a.Title != "Refactor the parser" {
		t.Errorf("header %+v", a)
	}
	if a.Unread == nil || !*a.Unread {
		t.Errorf("unread should be true, got %v", a.Unread)
	}
	if len(a.WorkspaceRoots) != 1 || a.WorkspaceRoots[0] != "/Users/someone/project" {
		t.Errorf("roots %v", a.WorkspaceRoots)
	}
	if !a.UpdatedAt.Equal(time.UnixMilli(unreadAt)) || !a.CreatedAt.Equal(time.UnixMilli(createdAt)) {
		t.Errorf("times %v %v", a.CreatedAt, a.UpdatedAt)
	}
	b := headers[1]
	if b.Title != "Untitled Session" {
		t.Errorf("a header without a name should be untitled, got %q", b.Title)
	}
	if b.Unread != nil {
		t.Errorf("a missing unread field must stay unknown, got %v", *b.Unread)
	}
	if !b.CreatedAt.IsZero() {
		t.Errorf("a missing time should be zero, got %v", b.CreatedAt)
	}
}

// Drafts, archives, subagents, internal project and spec records and cloud agents are not the
// desktop conversations the list is about
func TestReadCursorHeadersDropsWhatTheListIsNotAbout(t *testing.T) {
	base := func(extra map[string]any) string {
		fields := map[string]any{"type": "head", "unifiedMode": "agent", "name": "x"}
		for k, v := range extra {
			fields[k] = v
		}
		return headerValue(fields)
	}
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "a", workspaceID: "w", value: base(map[string]any{"hasUnreadMessages": true})},
		fixtureComposer{id: "b", workspaceID: "w", archived: 1, value: base(nil)},
		fixtureComposer{id: "c", workspaceID: "w", subagent: 1, value: base(nil)},
		fixtureComposer{id: "d", workspaceID: "w", value: base(map[string]any{"isArchived": true})},
		fixtureComposer{id: "e", workspaceID: "w", value: base(map[string]any{"isDraft": true})},
		fixtureComposer{id: "f", workspaceID: "w", value: base(map[string]any{"isSpec": true})},
		fixtureComposer{id: "g", workspaceID: "w", value: base(map[string]any{"isProject": true})},
		fixtureComposer{id: "h", workspaceID: "w", value: base(map[string]any{"isBestOfNSubcomposer": true})},
		fixtureComposer{id: "i", workspaceID: "w", value: base(map[string]any{"agentLocation": map[string]any{"type": "cloud", "status": "active"}})},
		fixtureComposer{id: "j", workspaceID: "w", value: base(map[string]any{"agentLocation": map[string]any{"type": "background"}})},
		// A row whose JSON is unusable only loses itself
		fixtureComposer{id: "k", workspaceID: "w", value: `{"type":`},
		fixtureComposer{id: "l", workspaceID: "w", value: base(map[string]any{"hasUnreadMessages": false})},
	)
	headers, err := ReadCursorHeaders(dbPath, headerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range headers {
		ids = append(ids, h.ID)
	}
	want := []string{"a", "l"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("kept %v, want %v", ids, want)
	}
}

func TestReadCursorHeadersSkipsOversizedValues(t *testing.T) {
	huge := `{"type":"head","name":"` + strings.Repeat("a", cursorHeaderValueLimit) + `"}`
	dbPath := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "huge", workspaceID: "w", value: huge},
		fixtureComposer{id: "ok", workspaceID: "w", value: headerValue(map[string]any{"type": "head", "name": "fine"})},
	)
	headers, err := ReadCursorHeaders(dbPath, headerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 1 || headers[0].ID != "ok" {
		t.Errorf("got %+v", headers)
	}
}

func TestReadCursorHeadersReportsSchemaDrift(t *testing.T) {
	// No composerHeaders table at all
	empty := filepath.Join(t.TempDir(), "state.vscdb")
	writeFixtureDB(t, empty, "", "")
	if _, err := ReadCursorHeaders(empty, headerTestNow); err == nil {
		t.Error("a database without the table should be reported as absent, not as no sessions")
	}
	// The table lost the column the ID comes from
	drifted := newFixtureDB(t, `composerId TEXT, workspaceId TEXT, value TEXT`)
	if _, err := ReadCursorHeaders(drifted, headerTestNow); err == nil {
		t.Error("a table missing rows of columns should be reported")
	}
}

func TestReadCursorHeadersNeverCreatesTheDatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "state.vscdb")
	if _, err := ReadCursorHeaders(missing, headerTestNow); err == nil {
		t.Error("a missing database should be an error")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("reading must not create the file")
	}
}

// Cursor keeps the database in WAL mode, so the newest rows can live in the -wal file while
// the main file is unchanged
func TestReadCursorHeadersReadsThroughTheWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.vscdb")
	writeFixtureDB(t, path, fixtureHeaderColumns, "?_pragma=journal_mode(wal)",
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w", value: headerValue(map[string]any{"type": "head", "name": "wal"})},
	)
	// A writer that stays open keeps the WAL from being folded back into the main file
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO composerHeaders (composerId, workspaceId, createdAt, lastUpdatedAt, isArchived, isSubagent, recency, checkpointAt, subagentTypeName, value)
		VALUES ('1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081','w',0,0,0,0,0,0,'','{"type":"head","name":"live"}')`); err != nil {
		t.Fatal(err)
	}
	headers, err := ReadCursorHeaders(path, headerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 2 {
		t.Fatalf("want both rows through the WAL, got %d", len(headers))
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Skipf("no wal file to have read through: %v", err)
	}
}

// A locked database is a moment, not a hang: the read gives up and the session list keeps what
// it had
func TestReadCursorHeadersGivesUpOnALock(t *testing.T) {
	path := newFixtureDB(t, fixtureHeaderColumns,
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w", value: headerValue(map[string]any{"type": "head"})},
	)
	holder, err := sql.Open("sqlite", "file:"+path)
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
	start := time.Now()
	if _, err := ReadCursorHeaders(path, headerTestNow); err == nil {
		t.Error("a locked database should not answer")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the read waited %s for a lock", took)
	}
}

func TestReadCursorWorkspaceRoots(t *testing.T) {
	userDir := t.TempDir()
	write := func(id, folder string) {
		dir := filepath.Join(userDir, "workspaceStorage", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workspace.json"), []byte(`{"folder":"`+folder+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("abc", "file:///Users/someone/project")
	write("def", "file:///Users/someone/project%20with%20space")
	// A directory that can't be parsed is skipped, not a reason to fail the whole map
	broken := filepath.Join(userDir, "workspaceStorage", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "workspace.json"), []byte(`{"folder":`), 0o600); err != nil {
		t.Fatal(err)
	}

	roots := ReadCursorWorkspaceRoots(userDir)
	if roots["abc"] != "/Users/someone/project" {
		t.Errorf("abc: %q", roots["abc"])
	}
	if roots["def"] != "/Users/someone/project with space" {
		t.Errorf("def: %q", roots["def"])
	}
	if _, ok := roots["broken"]; ok {
		t.Error("an unreadable workspace should be left out")
	}
}

// Cursor's own path has a space in it, so the read-only URI has to survive one
func TestReadCursorHeadersOpensAPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "Cursor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.vscdb")
	writeFixtureDB(t, path, fixtureHeaderColumns, "",
		fixtureComposer{id: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", workspaceID: "w", value: headerValue(map[string]any{"type": "head", "name": "spaced"})},
	)
	headers, err := ReadCursorHeaders(path, headerTestNow)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(headers) != 1 || headers[0].Title != "spaced" {
		t.Errorf("got %+v", headers)
	}
}
