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
	// A turn that just finished, and one that finished two minutes ago
	ended := PiSession{ActivityAt: now, LastEvent: Ended}
	endedEarlier := PiSession{ActivityAt: now - 120_000, LastEvent: Ended}

	cases := []struct {
		name     string
		session  PiSession
		live     piLive
		ok       bool
		lastSeen int64
		want     TaskState
		reason   WaitReason
	}{
		{name: "no signal", session: ended, ok: false, want: Completed},
		{name: "working", session: ended, live: piLive{Status: piWorking}, ok: true, want: Running},
		{name: "blocked on a question", session: ended, live: piLive{Status: piBlocked, Tool: piAskTool}, ok: true, want: Waiting, reason: WaitQuestion},
		{name: "blocked on a confirm dialog", session: ended, live: piLive{Status: piBlocked, BlockKind: "confirm"}, ok: true, want: Waiting, reason: WaitPermission},
		{name: "blocked on a select dialog", session: ended, live: piLive{Status: piBlocked, BlockKind: "select"}, ok: true, want: Waiting, reason: WaitQuestion},
		{name: "idle and unseen", session: ended, live: piLive{Status: piIdle}, ok: true, want: Unread},
		{name: "idle since before the user looked", session: endedEarlier, live: piLive{Status: piIdle}, ok: true, lastSeen: now - 60_000, want: Completed},
		{name: "idle and watched", session: ended, live: piLive{Status: piIdle}, ok: true, lastSeen: now, want: Completed},
		{name: "exited and unseen", session: ended, live: piLive{Status: piExited}, ok: true, want: Unread},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, reason := piSessionState(c.session, c.live, c.ok, c.lastSeen)
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
	writePiSession(t, root, cwd, "2026-10-15T10-00-00-000Z_top.jsonl",
		piHeader("top", cwd)+
			`{"type":"message","id":"m1","timestamp":"2026-10-15T10:00:05.000Z","message":{"role":"user","content":"real session"}}`+"\n")
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

// A turn that finished in a Herdr pane is new only if the user hasn't been back since. The
// first run has nothing to compare against, so it adopts what it finds instead of calling
// every session ever finished new
func TestScanPiReadsHerdrAndOnlyReportsWhatHappensAfterTheFirstRun(t *testing.T) {
	root := t.TempDir()
	cwd := "/Users/me/work/spark-go"
	path := writePiSession(t, root, cwd, "2026-10-15T10-00-00-000Z_session.jsonl",
		piHeader("session", cwd)+
			`{"type":"message","id":"m1","timestamp":"2026-10-15T10:00:05.000Z","message":{"role":"user","content":"start a turn"}}`+"\n"+
			`{"type":"message","id":"m2","timestamp":"2026-10-15T10:00:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stopReason":"stop"}}`+"\n")

	socket := shortSocketPath(t)
	serveHerdr(t, socket, `{"id":"spark","result":{"type":"agent_list","agents":[{"agent":"pi","pane_id":"w4:pC",`+
		`"agent_status":"idle","focused":false,"agent_session":{"kind":"path","value":"`+path+`"}}]}}`)

	// A store that already exists: nothing has recorded the user looking at this session
	seenPath := filepath.Join(root, "pi-seen.json")
	if err := os.WriteFile(seenPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Scanner{PiRoot: root, PiReceiptDir: filepath.Join(root, "receipts"), HerdrSocket: socket, piSeen: NewPiSeen(seenPath)}
	found := s.scanPi(time.Now())
	if len(found) != 1 {
		t.Fatalf("found %d sessions: %+v", len(found), found)
	}
	if found[0].item.State != Unread {
		t.Errorf("state = %v, want Unread for a turn the user hasn't looked at", found[0].item.State)
	}
	if found[0].item.Pane != "w4:pC" || found[0].item.NoSignal {
		t.Errorf("Herdr's pane should carry the row: %+v", found[0].item)
	}

	// The first run has no history: it takes what it finds as read
	fresh := &Scanner{PiRoot: root, PiReceiptDir: filepath.Join(root, "receipts"), HerdrSocket: socket,
		piSeen: NewPiSeen(filepath.Join(root, "fresh.json"))}
	found = fresh.scanPi(time.Now())
	if len(found) != 1 {
		t.Fatalf("found %d sessions: %+v", len(found), found)
	}
	if found[0].item.State != Completed {
		t.Errorf("state = %v, want Completed on the first run", found[0].item.State)
	}
}
