package monitor

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writePiSession lays out a pi session file tree: <root>/--<cwd>--/<name>.jsonl
func writePiSession(t *testing.T, root, project, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, "--"+strings.ReplaceAll(project, "/", "-")+"--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func piHeader(id, cwd string) string {
	return `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-10-15T10:00:00.000Z","cwd":"` + cwd + `"}` + "\n"
}

func TestPiUserTitleIgnoresInjectedContext(t *testing.T) {
	// A skill arrives as user text with the prompt appended inside the same message
	withSkill := "<skill name=\"grill-ask\" location=\"/x/SKILL.md\">\nsteps\n</skill>\n\n帮我适配 pi"
	if got := piUserTitle(withSkill); got != "帮我适配 pi" {
		t.Errorf("skill message = %q", got)
	}
	for _, text := range []string{
		"",
		"<rules>\n- something\n</rules>",
		"# AGENTS.md\nproject rules",
	} {
		if got := piUserTitle(text); got != "" {
			t.Errorf("piUserTitle(%q) = %q, want no title", text, got)
		}
	}
	if got := piUserTitle("[$skill](/x/SKILL.md) real request"); got != "real request" {
		t.Errorf("leading link = %q", got)
	}
	if got := piUserTitle("first line\nsecond line"); got != "first line" {
		t.Errorf("first line = %q", got)
	}
}

func TestReadPiSessionTakesTheFirstUserMessage(t *testing.T) {
	root := t.TempDir()
	path := writePiSession(t, root, "work/spark-go", "2026-10-15T10-00-00-000Z_abc.jsonl",
		piHeader("abc", "/Users/me/work/spark-go")+
			`{"type":"message","id":"m1","timestamp":"2026-10-15T10:00:05.000Z","message":{"role":"user","content":[{"type":"text","text":"<skill name=\"x\">\nbody\n</skill>\n\nAdd pi sessions to the list"}]}}`+"\n"+
			`{"type":"message","id":"m2","timestamp":"2026-10-15T10:00:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}}`+"\n")

	session, ok := ReadPiSession(path)
	if !ok {
		t.Fatal("ReadPiSession failed")
	}
	if session.ID != "abc" || session.Cwd != "/Users/me/work/spark-go" {
		t.Errorf("identity = %+v", session)
	}
	if session.Title != "Add pi sessions to the list" {
		t.Errorf("title = %q", session.Title)
	}
	if session.LastEvent != Ended {
		t.Errorf("last event = %v, want Ended", session.LastEvent)
	}
	want := time.Date(2026, 10, 15, 10, 0, 9, 0, time.UTC).UnixMilli()
	if session.ActivityAt != want {
		t.Errorf("activity = %d, want %d", session.ActivityAt, want)
	}
}

func TestReadPiSessionPrefersARename(t *testing.T) {
	root := t.TempDir()
	path := writePiSession(t, root, "work/spark-go", "2026-10-15T10-00-00-000Z_abc.jsonl",
		piHeader("abc", "/Users/me/work/spark-go")+
			`{"type":"message","id":"m1","timestamp":"2026-10-15T10:00:05.000Z","message":{"role":"user","content":"first words"}}`+"\n"+
			`{"type":"message","id":"m2","timestamp":"2026-10-15T10:00:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"stopReason":"stop"}}`+"\n"+
			`{"type":"session_info","id":"m3","timestamp":"2026-10-15T10:01:00.000Z","name":"Refactor auth"}`+"\n")

	session, ok := ReadPiSession(path)
	if !ok {
		t.Fatal("ReadPiSession failed")
	}
	if session.Title != "Refactor auth" {
		t.Errorf("title = %q, want the session's own name", session.Title)
	}
}

// The name can be set at any point and later output pushes it out of the windows the head
// and the tail are read in
func TestReadPiSessionFindsARenameOutsideTheWindows(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/spark-go"
	line := `{"type":"message","id":"x","timestamp":"2026-10-15T10:10:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"` +
		strings.Repeat("output ", 140) + `"}],"stopReason":"stop"}}` + "\n"
	path := writePiSession(t, root, cwd, "2026-10-15T10-00-00-000Z_abc.jsonl",
		piHeader("abc", cwd)+
			`{"type":"message","id":"m1","timestamp":"2026-10-15T10:00:05.000Z","message":{"role":"user","content":"first words"}}`+"\n"+
			`{"type":"session_info","id":"m2","timestamp":"2026-10-15T10:01:00.000Z","name":"Rewritten later"}`+"\n"+
			strings.Repeat(line, 200))
	if size := fileSize(t, path); size < piHeadBytes+piTailBytes {
		t.Fatalf("the fixture is %d bytes, not long enough to push the name out of both windows", size)
	}

	session, ok := ReadPiSession(path)
	if !ok {
		t.Fatal("ReadPiSession failed")
	}
	if session.Title != "Rewritten later" {
		t.Errorf("title = %q, want the name the user set", session.Title)
	}
	if session.LastEvent != Ended || session.ActivityAt == 0 {
		t.Errorf("the tail still has to be read: %+v", session)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return info.Size()
}

func TestPiTurnEventFindsAPendingQuestion(t *testing.T) {
	ask := &piMessage{
		Role:       "assistant",
		StopReason: "toolUse",
		Content:    []byte(`[{"type":"toolCall","id":"call_1","name":"ask_user_question","arguments":{}}]`),
	}
	if event, ok := piTurnEvent(ask, map[string]bool{}); !ok || event != AwaitingUser {
		t.Errorf("pending question = %v %v, want AwaitingUser", event, ok)
	}
	if event, _ := piTurnEvent(ask, map[string]bool{"call_1": true}); event != ToolPending {
		t.Errorf("answered question = %v, want ToolPending", event)
	}
	other := &piMessage{
		Role:       "assistant",
		StopReason: "toolUse",
		Content:    []byte(`[{"type":"toolCall","id":"call_2","name":"bash","arguments":{}}]`),
	}
	if event, _ := piTurnEvent(other, map[string]bool{}); event != ToolPending {
		t.Errorf("pending tool = %v, want ToolPending", event)
	}
	if event, _ := piTurnEvent(&piMessage{Role: "user", Content: []byte(`"hi"`)}, nil); event != Generating {
		t.Errorf("user message = %v, want Generating", event)
	}
	if event, _ := piTurnEvent(&piMessage{Role: "assistant", StopReason: "stop"}, nil); event != Ended {
		t.Errorf("finished turn = %v, want Ended", event)
	}
}

func TestPiSessionStateNeedsALiveSignal(t *testing.T) {
	now := time.Now().UnixMilli()
	// A turn that just finished, one that finished two minutes ago, and ones either side of
	// the window a badge on an unreportable session gets
	ended := PiSession{ActivityAt: now, TurnAt: now, LastEvent: Ended}
	endedEarlier := PiSession{ActivityAt: now - 120_000, TurnAt: now - 120_000, LastEvent: Ended}
	endedSoon := PiSession{ActivityAt: now - piUnreadWindow.Milliseconds()/2, TurnAt: now - piUnreadWindow.Milliseconds()/2, LastEvent: Ended}
	endedEarly := PiSession{ActivityAt: now - piUnreadWindow.Milliseconds() - 60_000, TurnAt: now - piUnreadWindow.Milliseconds() - 60_000, LastEvent: Ended}

	cases := []struct {
		name     string
		session  PiSession
		live     piLive
		ok       bool
		lastSeen int64
		now      int64
		want     TaskState
		reason   WaitReason
	}{
		{name: "no signal", session: ended, ok: false, now: now, want: Completed},
		{name: "working", session: ended, live: piLive{Status: piWorking}, ok: true, now: now, want: Running},
		{name: "blocked on a question", session: ended, live: piLive{Status: piBlocked, Tool: piAskTool}, ok: true, now: now, want: Waiting, reason: WaitQuestion},
		{name: "blocked on a confirm dialog", session: ended, live: piLive{Status: piBlocked, BlockKind: "confirm"}, ok: true, now: now, want: Waiting, reason: WaitPermission},
		{name: "blocked on a select dialog", session: ended, live: piLive{Status: piBlocked, BlockKind: "select"}, ok: true, now: now, want: Waiting, reason: WaitQuestion},
		{name: "idle and unseen", session: ended, live: piLive{Status: piIdle}, ok: true, now: now, want: Unread},
		{name: "idle since before the user looked", session: endedEarlier, live: piLive{Status: piIdle}, ok: true, lastSeen: now - 60_000, now: now, want: Completed},
		{name: "idle and watched", session: ended, live: piLive{Status: piIdle}, ok: true, lastSeen: now, now: now, want: Completed},
		{name: "exited and unseen", session: ended, live: piLive{Status: piExited}, ok: true, now: now, want: Unread},
		// Herdr says nobody has looked at this one, however long ago it finished
		{name: "herdr done", session: endedEarly, live: piLive{Status: herdrDone, FromHerdr: true, PaneID: "w4:pC"}, ok: true, now: now, want: Unread},
		// Herdr says the tab has been seen, so its idle is a session the user has read
		{name: "herdr idle", session: endedEarly, live: piLive{Status: piIdle, FromHerdr: true, PaneID: "w4:pC"}, ok: true, now: now, want: Completed},
		// Nothing can report the user looking at a session in an IDE or a plain terminal, so
		// its badge fades instead of hanging on
		{name: "the extension's idle and faded", session: endedEarly, live: piLive{Status: piIdle}, ok: true, now: now, want: Completed},
		{name: "the extension's idle and fresh", session: endedSoon, live: piLive{Status: piIdle}, ok: true, now: now, want: Unread},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, reason := piSessionState(c.session, c.live, c.ok, c.lastSeen, c.now)
			if state != c.want {
				t.Errorf("state = %v, want %v", state, c.want)
			}
			if state == Waiting && reason != c.reason {
				t.Errorf("reason = %v, want %v", reason, c.reason)
			}
		})
	}
}

func TestPiWaitReasonFallsBackToTheSessionFile(t *testing.T) {
	// Herdr only says "blocked", so a pending ask_user_question in the file decides
	question := PiSession{LastEvent: AwaitingUser}
	if got := piWaitReason(question, piLive{Status: piBlocked}); got != WaitQuestion {
		t.Errorf("herdr blocked on a question file = %v", got)
	}
	if got := piWaitReason(PiSession{LastEvent: ToolPending}, piLive{Status: piBlocked}); got != WaitPermission {
		t.Errorf("herdr blocked on a running tool = %v", got)
	}
}

func TestScanPiSkipsSubagentSessions(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/spark-go"
	// Timestamps relative to now: a session touched now is today's whatever the hour
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	writePiSession(t, root, cwd, "2026-10-15T10-00-00-000Z_top.jsonl",
		piHeader("top", cwd)+
			`{"type":"message","id":"m1","timestamp":"`+stamp+`","message":{"role":"user","content":"real session"}}`+"\n")
	// A sub-agent session lives in a directory under its parent's file
	sub := filepath.Join(root, "--Users-me-work-spark-go--", "2026-10-15T10-00-00-000Z_top", "run-0")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "session.jsonl"), []byte(piHeader("child", cwd)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &Scanner{PiRoot: root, PiReceiptDir: filepath.Join(root, "receipts"), HerdrSocket: filepath.Join(root, "missing.sock")}
	found := s.scanPi(time.Now())
	if len(found) != 1 {
		t.Fatalf("found %d sessions, want the top-level one only: %+v", len(found), found)
	}
	if found[0].item.Title != "real session" || found[0].project != "spark-go" {
		t.Errorf("session = %+v in %q", found[0].item, found[0].project)
	}
	if !found[0].item.NoSignal || found[0].item.Source != SourcePi {
		t.Errorf("a pi session with no source behind it = %+v", found[0].item)
	}
	if found[0].item.IsToday != true {
		t.Error("a session touched now is today's")
	}
}

func writePiReceipt(t *testing.T, dir, name string, receipt string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(receipt), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanPiBelievesAReceiptFromALiveProcess(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/spark-go"
	path := writePiSession(t, root, cwd, "2026-10-15T10-00-00-000Z_top.jsonl",
		piHeader("top", cwd)+
			`{"type":"message","id":"m1","timestamp":"2026-10-15T10:00:05.000Z","message":{"role":"user","content":"real session"}}`+"\n"+
			`{"type":"message","id":"m2","timestamp":"2026-10-15T10:00:09.000Z","message":{"role":"assistant","stopReason":"toolUse","content":[{"type":"toolCall","id":"c1","name":"ask_user_question","arguments":{}}]}}`+"\n")
	receiptDir := filepath.Join(root, "receipts")
	writePiReceipt(t, receiptDir, "top.json", `{"sessionId":"top","sessionPath":"`+path+`","pid":`+
		strconv.Itoa(os.Getpid())+`,"state":"blocked","tool":"ask_user_question","updatedAtMs":`+strconv.FormatInt(time.Now().UnixMilli(), 10)+`}`)
	// A second receipt whose process is gone must not report a state
	writePiReceipt(t, receiptDir, "dead.json", `{"sessionId":"dead","sessionPath":"/tmp/gone.jsonl","pid":999999,"state":"working","updatedAtMs":`+
		strconv.FormatInt(time.Now().UnixMilli(), 10)+`,"lastInputAtMs":`+strconv.FormatInt(time.Now().UnixMilli()-1000, 10)+`}`)

	s := &Scanner{PiRoot: root, PiReceiptDir: receiptDir, HerdrSocket: filepath.Join(root, "missing.sock")}
	found := s.scanPi(time.Now())
	if len(found) != 1 {
		t.Fatalf("found %d sessions: %+v", len(found), found)
	}
	item := found[0].item
	if item.State != Waiting || item.WaitReason != WaitQuestion {
		t.Errorf("state = %v/%v, want Waiting on a question", item.State, item.WaitReason)
	}
	if item.NoSignal {
		t.Error("a session reported by the extension has a signal")
	}
}

func TestScanPiDoesNotGoLookingForOldSessions(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/old"
	path := writePiSession(t, root, cwd, "2026-01-01T10-00-00-000Z_old.jsonl", piHeader("old", cwd))
	old := time.Now().Add(-piScanWindow - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	s := &Scanner{PiRoot: root, PiReceiptDir: filepath.Join(root, "receipts"), HerdrSocket: filepath.Join(root, "missing.sock")}
	if found := s.scanPi(time.Now()); len(found) != 0 {
		t.Errorf("found %d old sessions, want none: %+v", len(found), found)
	}
}

// serveHerdr answers one agent.list per connection from a socket at path
func serveHerdr(t *testing.T, path, reply string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
					return
				}
				_, _ = conn.Write([]byte(reply + "\n"))
			}()
		}
	}()
}

// Herdr is the only source that can say whether a finished turn has been looked at, so its
// word decides: done is work nobody has seen, idle is work somebody has
func TestScanPiTakesHerdrsWordForUnseenWork(t *testing.T) {
	cwd := "/Users/me/work/spark-go"
	cases := []struct {
		status   string
		want     TaskState
		noSignal bool
	}{
		{"working", Running, false},
		{"done", Unread, false},
		{"idle", Completed, false},
		// An agent Herdr cannot classify is not proof that the work finished
		{"unknown", Completed, true},
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			root := t.TempDir()
			path := writePiSession(t, root, cwd, "2026-10-15T10-00-00-000Z_session.jsonl",
				piHeader("session", cwd)+
					`{"type":"message","id":"m1","timestamp":"2026-10-15T10:00:05.000Z","message":{"role":"user","content":"start a turn"}}`+"\n"+
					`{"type":"message","id":"m2","timestamp":"2026-10-15T10:00:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}}`+"\n")
			socket := shortSocketPath(t)
			serveHerdr(t, socket, `{"id":"spark","result":{"type":"agent_list","agents":[{"agent":"pi","pane_id":"w4:pC",`+
				`"agent_status":"`+c.status+`","focused":false,"agent_session":{"kind":"path","value":"`+path+`"}}]}}`)

			s := &Scanner{PiRoot: root, PiReceiptDir: filepath.Join(root, "receipts"), HerdrSocket: socket}
			found := s.scanPi(time.Now())
			if len(found) != 1 {
				t.Fatalf("found %d sessions: %+v", len(found), found)
			}
			item := found[0].item
			if item.State != c.want {
				t.Errorf("state = %v, want %v", item.State, c.want)
			}
			if item.NoSignal != c.noSignal {
				t.Errorf("no signal = %v, want %v", item.NoSignal, c.noSignal)
			}
			if item.Pane != "w4:pC" {
				t.Errorf("pane = %q, want the pane Herdr reports", item.Pane)
			}
		})
	}
}

// appendPiRaw adds text with no newline, the way a writer is caught mid-line
func appendPiRaw(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func appendPiLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// A session the extension reports but no pane hosts can't have its badge cleared by the user
// looking at it, so it fades; one inside the window still counts as something to read
func TestScanPiFadesTheBadgeOnASessionNothingReportsLooking(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/spark-go"
	now := time.Now()
	oldTime := now.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	recentTime := now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	lines := func(id, stamp string) string {
		return piHeader(id, cwd) +
			`{"type":"message","id":"m1","timestamp":"` + stamp + `","message":{"role":"user","content":"go"}}` + "\n" +
			`{"type":"message","id":"m2","timestamp":"` + stamp + `","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}}` + "\n"
	}
	pathOld := writePiSession(t, root, cwd, "2026-10-15T09-00-00-000Z_old.jsonl", lines("old", oldTime))
	pathRecent := writePiSession(t, root, cwd, "2026-10-15T13-00-00-000Z_recent.jsonl", lines("recent", recentTime))

	receiptDir := filepath.Join(root, "receipts")
	receipt := func(id, path string) string {
		return `{"sessionId":"` + id + `","sessionPath":"` + path + `","pid":` + strconv.Itoa(os.Getpid()) +
			`,"state":"idle","updatedAtMs":` + strconv.FormatInt(now.UnixMilli(), 10) + `}`
	}
	writePiReceipt(t, receiptDir, "old.json", receipt("old", pathOld))
	writePiReceipt(t, receiptDir, "recent.json", receipt("recent", pathRecent))

	s := &Scanner{PiRoot: root, PiReceiptDir: receiptDir, HerdrSocket: filepath.Join(root, "missing.sock")}
	found := s.scanPi(now)
	if len(found) != 2 {
		t.Fatalf("found %d sessions: %+v", len(found), found)
	}
	states := map[string]TaskState{}
	for _, f := range found {
		states[f.item.ID] = f.item.State
	}
	if states["old"] != Completed {
		t.Errorf("an unseen session from two hours ago = %v, want Completed", states["old"])
	}
	if states["recent"] != Unread {
		t.Errorf("an unseen session from a minute ago = %v, want Unread", states["recent"])
	}
}

// A rename, a model switch or an extension's own state are not results: unread timing has to
// go by the conversation, or every /name would raise a New badge
func TestPiTurnTimeIgnoresMetadata(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/spark-go"
	now := time.Now()
	turn := now.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	later := now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	path := writePiSession(t, root, cwd, "2026-10-15T08-00-00-000Z_abc.jsonl",
		piHeader("abc", cwd)+
			`{"type":"message","id":"m1","timestamp":"`+turn+`","message":{"role":"user","content":"first words"}}`+"\n"+
			`{"type":"message","id":"m2","timestamp":"`+turn+`","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}}`+"\n"+
			`{"type":"session_info","id":"m3","timestamp":"`+later+`","name":"Named after the fact"}`+"\n"+
			`{"type":"custom","id":"m4","customType":"some-extension","timestamp":"`+later+`","data":{}}`+"\n")

	session, ok := ReadPiSession(path)
	if !ok {
		t.Fatal("ReadPiSession failed")
	}
	wantTurn := now.Add(-2 * time.Hour).UnixMilli()
	if session.TurnAt != wantTurn {
		t.Errorf("turn time = %s, want %s", time.UnixMilli(session.TurnAt), time.UnixMilli(wantTurn))
	}
	if session.ActivityAt <= session.TurnAt {
		t.Errorf("activity = %s, want the metadata's later time", time.UnixMilli(session.ActivityAt))
	}
	// The rename is newer than the turn and old enough to have faded: neither is a result
	if state, _ := piSessionState(session, piLive{Status: piIdle}, true, 0, now.UnixMilli()); state != Completed {
		t.Errorf("state = %v, want Completed: only the conversation counts", state)
	}
}

// The name search resumes where the last read stopped, so an appended rename is found
// without rereading the session, and a line the writer hasn't finished is not skipped
func TestScanPiFindsARenameInWhatWasAppended(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/spark-go"
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	path := writePiSession(t, root, cwd, "2026-10-15T10-00-00-000Z_abc.jsonl",
		piHeader("abc", cwd)+
			`{"type":"message","id":"m1","timestamp":"`+stamp+`","message":{"role":"user","content":"first words"}}`+"\n")
	s := &Scanner{PiRoot: root, PiReceiptDir: filepath.Join(root, "receipts"), HerdrSocket: filepath.Join(root, "missing.sock")}

	title := func() string {
		found := s.scanPi(time.Now())
		if len(found) != 1 {
			t.Fatalf("found %d sessions: %+v", len(found), found)
		}
		return found[0].item.Title
	}
	if got := title(); got != "first words" {
		t.Fatalf("title = %q, want the first user message", got)
	}

	// Half a rename, as a writer caught mid-line: no newline yet
	appendPiRaw(t, path, `{"type":"session_info","id":"m2","timestamp":"`+stamp+`","name":"Renamed`)
	if got := title(); got != "first words" {
		t.Fatalf("title = %q, want it unchanged until the line is complete", got)
	}
	// The rest of it, and the search has to have kept the beginning
	appendPiRaw(t, path, ` later"}`+"\n")
	if got := title(); got != "Renamed later" {
		t.Errorf("title = %q, want the completed rename to be read", got)
	}

	// A second rename still wins
	appendPiLine(t, path, `{"type":"session_info","id":"m3","timestamp":"`+stamp+`","name":"Renamed again"}`)
	if got := title(); got != "Renamed again" {
		t.Errorf("title = %q, want the newest rename", got)
	}
}
