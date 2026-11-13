package monitor

import (
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Minimal protobuf encoder helpers for tests ---

func pbTag(field uint32, wire uint8) []byte {
	return pbVarint(uint64(field)<<3 | uint64(wire))
}

func pbVarint(v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return buf[:n]
}

func pbFieldVarint(field uint32, v uint64) []byte {
	out := pbTag(field, wireVarint)
	return append(out, pbVarint(v)...)
}

func pbFieldBool(field uint32, v bool) []byte {
	if v {
		return pbFieldVarint(field, 1)
	}
	return pbFieldVarint(field, 0)
}

func pbFieldBytes(field uint32, b []byte) []byte {
	out := pbTag(field, wireBytes)
	out = append(out, pbVarint(uint64(len(b)))...)
	return append(out, b...)
}

func pbFieldString(field uint32, s string) []byte {
	return pbFieldBytes(field, []byte(s))
}

func pbTimestamp(t time.Time) []byte {
	if t.IsZero() {
		return nil
	}
	var out []byte
	out = append(out, pbFieldVarint(1, uint64(t.Unix()))...)
	if ns := t.Nanosecond(); ns > 0 {
		out = append(out, pbFieldVarint(2, uint64(ns))...)
	}
	return out
}

func pbConcat(chunks ...[]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func createAntigravityFixtureDB(t *testing.T, dbPath string, rows []agFixtureRow) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS conversation_summaries (
		conversation_id TEXT PRIMARY KEY,
		title TEXT,
		status TEXT,
		source TEXT,
		preview TEXT,
		killed INTEGER,
		group_id TEXT,
		battle_id TEXT,
		step_count INTEGER,
		project_id TEXT,
		agent_name TEXT,
		app_data_dir TEXT,
		nesting_depth INTEGER,
		last_modified_time TEXT,
		not_fully_idle INTEGER,
		workspace_uris TEXT,
		last_user_input_time TEXT,
		has_subtrajectory INTEGER,
		parent_conversation_id TEXT,
		winning_conversation_id TEXT,
		last_user_input_step_index INTEGER,
		raw_summary BLOB
	)`)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO conversation_summaries (
			conversation_id, title, status, source, killed, battle_id, step_count,
			app_data_dir, nesting_depth, last_modified_time, not_fully_idle, workspace_uris,
			parent_conversation_id, raw_summary
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.id, r.title, r.status, r.source, r.killed, r.battleID, r.stepCount,
			r.appDataDir, r.nestingDepth, r.lastModified, r.notFullyIdle, r.workspaceURIs,
			r.parentID, r.rawSummary,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

type agFixtureRow struct {
	id            string
	title         string
	status        string
	source        string
	killed        int
	battleID      string
	stepCount     int
	appDataDir    string
	nestingDepth  int
	lastModified  string
	notFullyIdle  int
	workspaceURIs string
	parentID      string
	rawSummary    []byte
}

func TestAntigravityProtoDecodingAndStates(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)

	// 1. Running session with workspace containing %20
	idRun := "11111111-1111-4111-8111-111111111111"
	rawRun := pbConcat(
		pbFieldString(1, "Refactor watcher"),
		pbFieldVarint(2, 5),
		pbFieldBytes(3, pbTimestamp(now.Add(-10*time.Second))),
		pbFieldString(4, idRun),
		pbFieldVarint(5, uint64(agStatusRunning)),
		pbFieldBytes(9, pbFieldString(1, "file:///Users/dev/my%20project")),
		pbFieldVarint(20, 12), // INTERACTIVE_CASCADE
	)

	// 2. Waiting on ask_question (field 154)
	idWaitQ := "22222222-2222-4222-8222-222222222222"
	stepAskQ := pbFieldBytes(154, []byte{0x08, 0x01})
	rawWaitQ := pbConcat(
		pbFieldString(1, "Ask target DB"),
		pbFieldVarint(2, 3),
		pbFieldBytes(3, pbTimestamp(now.Add(-5*time.Second))),
		pbFieldString(4, idWaitQ),
		pbFieldVarint(5, uint64(agStatusRunning)),
		pbFieldBytes(8, pbConcat(pbFieldBytes(1, stepAskQ), pbFieldVarint(2, 2))),
		pbFieldVarint(20, 1),
	)

	// 3. Waiting on run_command permission (field 28)
	idWaitPerm := "33333333-3333-4333-8333-333333333333"
	stepRunCmd := pbFieldBytes(28, []byte{0x08, 0x01})
	rawWaitPerm := pbConcat(
		pbFieldString(1, "Run tests"),
		pbFieldVarint(2, 4),
		pbFieldBytes(3, pbTimestamp(now.Add(-3*time.Second))),
		pbFieldString(4, idWaitPerm),
		pbFieldVarint(5, uint64(agStatusBusy)),
		pbFieldBytes(8, pbFieldBytes(1, stepRunCmd)),
		pbFieldBytes(17, pbFieldString(7, "file:///Users/dev/spark-go")),
		pbFieldVarint(20, 12),
	)

	// 4. Waiting via requested_interaction.elicitation (field 56 -> field 20)
	idWaitElicit := "44444444-4444-4444-8444-444444444444"
	stepElicit := pbFieldBytes(56, pbFieldBytes(20, []byte{0x08, 0x01}))
	rawWaitElicit := pbConcat(
		pbFieldString(1, "Elicitation prompt"),
		pbFieldVarint(2, 2),
		pbFieldBytes(3, pbTimestamp(now.Add(-4*time.Second))),
		pbFieldString(4, idWaitElicit),
		pbFieldVarint(5, uint64(agStatusIdle)),
		pbFieldBytes(8, pbFieldBytes(1, stepElicit)),
		pbFieldVarint(20, 1),
	)

	// 5. Unread (last_modified_time > last_user_view_time)
	idUnread := "55555555-5555-4555-8555-555555555555"
	rawUnread := pbConcat(
		pbFieldString(1, "Summary title"),
		pbFieldVarint(2, 7),
		pbFieldBytes(3, pbTimestamp(now.Add(-2*time.Minute))),
		pbFieldString(4, idUnread),
		pbFieldVarint(5, uint64(agStatusIdle)),
		pbFieldBytes(15, pbConcat(
			pbFieldString(1, "Custom annotated title"),
			pbFieldBytes(7, pbTimestamp(now.Add(-5*time.Minute))),
		)),
		pbFieldBytes(9, pbFieldString(1, "file:///Users/dev/spark-go")),
		pbFieldVarint(20, 12),
	)

	// 6. Completed (last_modified_time <= last_user_view_time && !marked_as_unread)
	idDone := "66666666-6666-4666-8666-666666666666"
	rawDone := pbConcat(
		pbFieldString(1, "Done task"),
		pbFieldVarint(2, 9),
		pbFieldBytes(3, pbTimestamp(now.Add(-10*time.Minute))),
		pbFieldString(4, idDone),
		pbFieldVarint(5, uint64(agStatusIdle)),
		pbFieldBytes(15, pbFieldBytes(7, pbTimestamp(now.Add(-5*time.Minute)))),
		pbFieldVarint(20, 1),
	)

	// 7. Killed running session -> Completed (because last_user_view == now)
	idKilled := "77777777-7777-4777-8777-777777777777"
	rawKilled := pbConcat(
		pbFieldString(1, "Killed session"),
		pbFieldVarint(2, 3),
		pbFieldBytes(3, pbTimestamp(now.Add(-3*time.Minute))),
		pbFieldString(4, idKilled),
		pbFieldVarint(5, uint64(agStatusRunning)),
		pbFieldBytes(15, pbFieldBytes(7, pbTimestamp(now))),
		pbFieldBool(23, true),
		pbFieldVarint(20, 1),
	)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "conversation_summaries.db")
	createAntigravityFixtureDB(t, dbPath, []agFixtureRow{
		{id: idRun, appDataDir: "antigravity", rawSummary: rawRun},
		{id: idWaitQ, appDataDir: "antigravity", rawSummary: rawWaitQ},
		{id: idWaitPerm, appDataDir: "antigravity", rawSummary: rawWaitPerm},
		{id: idWaitElicit, appDataDir: "antigravity", rawSummary: rawWaitElicit},
		{id: idUnread, appDataDir: "antigravity", rawSummary: rawUnread},
		{id: idDone, appDataDir: "antigravity", rawSummary: rawDone},
		{id: idKilled, appDataDir: "antigravity", rawSummary: rawKilled},
	})

	sc := &Scanner{
		AntigravityDB:         dbPath,
		AntigravityBrain:      filepath.Join(dir, "brain"),
		antigravityAssumeLive: true,
	}

	groups := groupByProject(sc.scanAntigravity(now))
	byID := map[string]SessionItem{}
	groupOf := map[string]string{}
	for _, g := range groups {
		for _, item := range g.Sessions {
			byID[item.ID] = item
			groupOf[item.ID] = g.ProjectName
		}
	}

	if got := byID[idRun]; got.State != Running || got.WorkingDir != "/Users/dev/my project" || groupOf[idRun] != "my project" {
		t.Fatalf("run item = %+v, group=%q; want Running /Users/dev/my project", got, groupOf[idRun])
	}
	if got := byID[idWaitQ]; got.State != Waiting || got.WaitReason != WaitQuestion || groupOf[idWaitQ] != "Antigravity" {
		t.Fatalf("waitQ item = %+v, group=%q; want Waiting(Question) under Antigravity", got, groupOf[idWaitQ])
	}
	if got := byID[idWaitPerm]; got.State != Waiting || got.WaitReason != WaitPermission || groupOf[idWaitPerm] != "spark-go" {
		t.Fatalf("waitPerm item = %+v, group=%q; want Waiting(Permission) under spark-go", got, groupOf[idWaitPerm])
	}
	if got := byID[idWaitElicit]; got.State != Waiting || got.WaitReason != WaitQuestion {
		t.Fatalf("waitElicit item = %+v; want Waiting(Question)", got)
	}
	if got := byID[idUnread]; got.State != Unread || got.Title != "Custom annotated title" {
		t.Fatalf("unread item = %+v; want Unread with Custom annotated title", got)
	}
	if got := byID[idDone]; got.State != Completed {
		t.Fatalf("done item = %+v; want Completed", got)
	}
	if got := byID[idKilled]; got.State != Completed {
		t.Fatalf("killed item = %+v; want Completed", got)
	}
}

func TestAntigravityTranscriptWaitClassification(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "conversation_summaries.db")
	brainDir := filepath.Join(dir, "brain")

	idPlan := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	idFeedback := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	idResolved := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

	mkSummary := func(id, title string) []byte {
		return pbConcat(
			pbFieldString(1, title),
			pbFieldVarint(2, 4),
			pbFieldBytes(3, pbTimestamp(now.Add(-5*time.Second))),
			pbFieldString(4, id),
			pbFieldVarint(5, uint64(agStatusRunning)),
			pbFieldVarint(20, 12),
		)
	}
	createAntigravityFixtureDB(t, dbPath, []agFixtureRow{
		{id: idPlan, appDataDir: "antigravity", rawSummary: mkSummary(idPlan, "Plan mode")},
		{id: idFeedback, appDataDir: "antigravity", rawSummary: mkSummary(idFeedback, "Artifact feedback")},
		{id: idResolved, appDataDir: "antigravity", rawSummary: mkSummary(idResolved, "Already answered")},
	})

	writeTranscript := func(id, content string) {
		p := AntigravityTranscriptPath(brainDir, id)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeTranscript(idPlan, `{"type":"USER_INPUT","content":"plan this"}`+"\n"+
		`{"type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-13T11:59:58+08:00","tool_calls":[{"name":"exit_plan_mode","args":{"Summary":"ready"}}]}`+"\n")
	writeTranscript(idFeedback,
		`{"type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"write_file","args":{"ArtifactMetadata":{"RequestFeedback":true}}}]}`+"\n")
	// User already replied after ask_question -> should remain Running, not Waiting.
	writeTranscript(idResolved, `{"type":"PLANNER_RESPONSE","tool_calls":[{"name":"ask_question","args":{}}]}`+"\n"+
		`{"type":"USER_INPUT","content":"yes"}`+"\n")

	sc := &Scanner{
		AntigravityDB:         dbPath,
		AntigravityBrain:      brainDir,
		antigravityAssumeLive: true,
	}

	got := map[string]SessionItem{}
	for _, d := range sc.scanAntigravity(now) {
		got[d.item.ID] = d.item
	}

	if got[idPlan].State != Waiting || got[idPlan].WaitReason != WaitQuestion {
		t.Fatalf("exit_plan_mode item = %+v; want Waiting(Question)", got[idPlan])
	}
	if got[idFeedback].State != Waiting || got[idFeedback].WaitReason != WaitQuestion {
		t.Fatalf("RequestFeedback item = %+v; want Waiting(Question)", got[idFeedback])
	}
	if got[idResolved].State != Running {
		t.Fatalf("resolved item = %+v; want Running", got[idResolved])
	}
}

func TestAntigravityFiltersExclusions(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "conversation_summaries.db")

	validID := "10101010-1010-4010-8010-101010101010"
	rows := []agFixtureRow{
		{
			id: validID, appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Keep me"), pbFieldVarint(2, 2), pbFieldBytes(3, pbTimestamp(now)), pbFieldString(4, validID), pbFieldVarint(20, 1)),
		},
		{
			id: "e1", appDataDir: "antigravity-ide",
			rawSummary: pbConcat(pbFieldString(1, "IDE"), pbFieldVarint(2, 2), pbFieldBytes(3, pbTimestamp(now)), pbFieldString(4, "e1"), pbFieldVarint(20, 1)),
		},
		{
			id: "e2", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Archived"), pbFieldVarint(2, 2), pbFieldString(4, "e2"), pbFieldBytes(15, pbFieldBool(4, true))),
		},
		{
			id: "e3", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Subagent"), pbFieldVarint(2, 2), pbFieldString(4, "e3"), pbFieldBytes(17, pbFieldString(5, validID))),
		},
		{
			id: "e4", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Deep"), pbFieldVarint(2, 2), pbFieldString(4, "e4"), pbFieldBytes(17, pbFieldVarint(17, 1))),
		},
		{
			id: "e5", appDataDir: "antigravity", battleID: "battle-1",
			rawSummary: pbConcat(pbFieldString(1, "Battle"), pbFieldVarint(2, 2), pbFieldString(4, "e5")),
		},
		{
			id: "e6", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Side Q"), pbFieldVarint(2, 2), pbFieldString(4, "e6"), pbFieldVarint(22, uint64(agTypeSideQuestion))),
		},
		{
			id: "e7", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Commit msg"), pbFieldVarint(2, 2), pbFieldString(4, "e7"), pbFieldVarint(22, uint64(agTypeCommitMessage))),
		},
		{
			id: "e8", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Hidden"), pbFieldVarint(2, 2), pbFieldString(4, "e8"), pbFieldBytes(17, pbFieldBytes(12, pbFieldBool(3, true)))),
		},
		{
			id: "e9", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "Cloud"), pbFieldVarint(2, 2), pbFieldString(4, "e9"), pbFieldBool(27, true)),
		},
		{
			id: "e10", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(1, "CLI source"), pbFieldVarint(2, 2), pbFieldString(4, "e10"), pbFieldVarint(20, 2)),
		},
		{
			id: "e11", appDataDir: "antigravity",
			rawSummary: pbConcat(pbFieldString(4, "e11"), pbFieldVarint(20, 1)), // empty draft
		},
	}

	createAntigravityFixtureDB(t, dbPath, rows)
	sc := &Scanner{AntigravityDB: dbPath, antigravityAssumeLive: true}
	got := sc.scanAntigravity(now)
	if len(got) != 1 || got[0].item.ID != validID {
		t.Fatalf("filtered sessions = %+v; want only %s", got, validID)
	}
}

func TestAntigravityStaleActiveFallsBackWhenNotLive(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
	sum := antigravitySummary{
		ID:             "88888888-8888-4888-8888-888888888888",
		Status:         agStatusRunning,
		HasWaitingStep: true,
		WaitReason:     WaitPermission,
		LastModified:   now.Add(-2 * time.Minute),
		LastUserView:   now.Add(-3 * time.Minute),
	}
	if state, _ := determineAntigravityState(sum, false, 0, false); state != Unread {
		t.Fatalf("stale not-live waiting session state = %s; want Unread", state)
	}

	sum.LastUserView = now.Add(-1 * time.Minute)
	if state, _ := determineAntigravityState(sum, false, 0, false); state != Completed {
		t.Fatalf("stale viewed session state = %s; want Completed", state)
	}
}

func TestAntigravityProtoFallbackAndSnapshotCache(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "conversation_summaries.db")
	pbPath := filepath.Join(dir, "agyhub_summaries_proto.pb")

	idPB := "99999999-9999-4999-8999-999999999999"
	sumPB := pbConcat(
		pbFieldString(1, "From proto fallback"),
		pbFieldVarint(2, 3),
		pbFieldBytes(3, pbTimestamp(now)),
		pbFieldString(4, idPB),
		pbFieldVarint(5, uint64(agStatusIdle)),
		pbFieldVarint(20, 1),
	)
	entry := pbConcat(pbFieldString(1, idPB), pbFieldBytes(2, sumPB))
	if err := os.WriteFile(pbPath, pbFieldBytes(1, entry), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := &Scanner{
		AntigravityDB:         dbPath,
		AntigravityProto:      pbPath,
		antigravityAssumeLive: true,
	}
	got := sc.scanAntigravity(now)
	if len(got) != 1 || got[0].item.ID != idPB || got[0].item.Title != "From proto fallback" {
		t.Fatalf("proto fallback = %+v; want %s", got, idPB)
	}

	// Now create a real DB, scan it, then replace DB with corrupt bytes to verify TTL cache holds.
	idDB := "12121212-1212-4121-8121-121212121212"
	createAntigravityFixtureDB(t, dbPath, []agFixtureRow{{
		id: idDB, appDataDir: "antigravity",
		rawSummary: pbConcat(pbFieldString(1, "Cached DB"), pbFieldVarint(2, 1), pbFieldBytes(3, pbTimestamp(now)), pbFieldString(4, idDB)),
	}})
	got = sc.scanAntigravity(now)
	if len(got) != 1 || got[0].item.ID != idDB {
		t.Fatalf("DB scan = %+v; want %s", got, idDB)
	}

	if err := os.WriteFile(dbPath, []byte("not a sqlite db"), 0o644); err != nil {
		t.Fatal(err)
	}
	gotCached := sc.scanAntigravity(now.Add(10 * time.Second))
	if len(gotCached) != 1 || gotCached[0].item.ID != idDB {
		t.Fatalf("cached fallback = %+v; want %s", gotCached, idDB)
	}
	gotExpired := sc.scanAntigravity(now.Add(antigravitySnapshotTTL + time.Second))
	if len(gotExpired) != 0 {
		t.Fatalf("expired cache still returned %+v; want empty", gotExpired)
	}
}

func TestAntigravityShallowWatchIgnoresSubdirsAndSHM(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "conversation_summaries.db")
	subDir := filepath.Join(dir, "browser_recordings")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("init"), 0o644); err != nil {
		t.Fatal(err)
	}

	sc := &Scanner{AntigravityDB: dbPath}
	events, closeWatcher := watchDirectories(nil, sc.antigravityShallowWatchDirs(), "")
	if events == nil {
		t.Fatal("expected shallow watch channel")
	}
	defer closeWatcher()

	// Creating/writing inside a subdir should NOT be descended into or watched once subDir already exists.
	if err := os.WriteFile(filepath.Join(subDir, "frame.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Writing a -shm file in top dir should also be ignored.
	if err := os.WriteFile(dbPath+"-shm", []byte("shm"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
		t.Fatal("subdir write or -shm write triggered watcher unexpectedly")
	case <-time.After(350 * time.Millisecond):
	}

	// Updating conversation_summaries.db in top dir must notify.
	if err := os.WriteFile(dbPath, []byte("updated"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("updating conversation_summaries.db did not trigger watcher")
	}
}
