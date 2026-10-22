package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const threadA = "01a12f69-fdb1-7173-b784-47189d9442d7"

func writeRollout(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// One line from each end of a real rollout: the head has the cwd, the tail the turn state
const rolloutHead = `{"timestamp":"2026-10-12T09:20:38.668Z","type":"session_meta","payload":{"session_id":"` + threadA + `","cwd":"/Users/me/work/joyspace-ai","originator":"Codex Desktop"}}
{"timestamp":"2026-10-12T09:20:39.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<app-context>injected</app-context>"}]}}
{"timestamp":"2026-10-12T09:20:40.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"what is left in this worktree\nsecond line does not count"}]}}
`

func TestReadsRolloutHeader(t *testing.T) {
	dir := t.TempDir()
	path := writeRollout(t, dir, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", rolloutHead)
	got, ok := ReadCodexRollout(path)
	if !ok {
		t.Fatal("rollout should be readable")
	}
	if got.Cwd != "/Users/me/work/joyspace-ai" {
		t.Errorf("cwd = %q", got.Cwd)
	}
	// Injected context carries the user role too, so it must not become the title
	if got.Title != "what is left in this worktree" {
		t.Errorf("title = %q", got.Title)
	}
}

func TestReadsRolloutTail(t *testing.T) {
	cases := []struct {
		name string
		body string
		want TurnEvent
	}{
		{"task started means generating", `{"type":"event_msg","payload":{"type":"task_started"}}`, Generating},
		{"tool call means pending", `{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec"}}`, ToolPending},
		{"tool output means generating again", `{"type":"response_item","payload":{"type":"custom_tool_call_output"}}`, Generating},
		{"task complete means ended", `{"type":"event_msg","payload":{"type":"task_complete"}}`, Ended},
		{"aborted means ended", `{"type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`, Ended},
		{"token count is ignored", `{"type":"event_msg","payload":{"type":"token_count"}}`, Generating},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			// End with a record that always gets skipped, to prove the classifier walks
			// backwards
			body := `{"type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
				c.body + "\n" + `{"type":"event_msg","payload":{"type":"token_count"}}` + "\n"
			path := writeRollout(t, dir, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", body)
			got, ok := ReadCodexRollout(path)
			if !ok {
				t.Fatal("rollout should be readable")
			}
			if got.LastEvent != c.want {
				t.Errorf("event = %v, want %v", got.LastEvent, c.want)
			}
		})
	}
}

func TestExtractsThreadIDFromRolloutName(t *testing.T) {
	got := threadIDFromRollout("rollout-2026-10-12T17-20-37-" + threadA + ".jsonl")
	if got != threadA {
		t.Errorf("thread id = %q", got)
	}
	if threadIDFromRollout("rollout-short.jsonl") != "" {
		t.Error("malformed name should give no thread id")
	}
	// Resuming a thread writes another rollout named after the thread and the segment it
	// belongs to; the thread is the part before the underscore
	resumed := "rollout-2026-10-21T15-35-05-" + threadA + "_01a15d62-98b3-75c3-9471-3d457a50c519.jsonl"
	if got := threadIDFromRollout(resumed); got != threadA {
		t.Errorf("resumed rollout thread id = %q, want %q", got, threadA)
	}
}

func TestClassifiesCodexHookEvents(t *testing.T) {
	cases := []struct {
		payload string
		want    HookKind
		ok      bool
	}{
		{`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`, HookPermissionPrompt, true},
		{`{"hook_event_name":"UserPromptSubmit","prompt":"hi"}`, HookBusy, true},
		{`{"hook_event_name":"PostToolUse","tool_name":"exec"}`, HookBusy, true},
		{`{"hook_event_name":"SessionStart"}`, HookBusy, true},
		{`{"hook_event_name":"Stop"}`, HookStopped, true},
		{`{"hook_event_name":"SessionEnd"}`, HookStopped, true},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := classifyCodexHook([]byte(c.payload))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %v ok=%v", c.payload, got, ok)
		}
	}
}

func TestReadsCodexSignal(t *testing.T) {
	dir := t.TempDir()
	body := `{"hook_event_name":"PostToolUse","session_id":"` + threadA + `","cwd":"/tmp/x","tool_name":"exec"}`
	if err := os.WriteFile(filepath.Join(dir, threadA+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sig, ok := ReadCodexSignal(dir, threadA)
	if !ok || sig.Kind != HookBusy || sig.Cwd != "/tmp/x" || sig.Tool != "exec" {
		t.Errorf("got %+v ok=%v", sig, ok)
	}
	if _, ok := ReadCodexSignal(dir, "missing"); ok {
		t.Error("missing file should give no signal")
	}
}

func TestReadsUnreadThreads(t *testing.T) {
	codexHome := t.TempDir()
	if got := UnreadCodexThreads(codexHome); got != nil {
		t.Errorf("missing state should give nothing, got %v", got)
	}
	state := `{"electron-thread-read-state-v1":{"version":1,"unreadByIdentity":{"ident":{"local":["` + threadA + `"]}}}}`
	if err := os.WriteFile(CodexGlobalStatePath(codexHome), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := UnreadCodexThreads(codexHome); !got[threadA] {
		t.Errorf("thread should be unread, got %v", got)
	}
}

func TestLocatesRolloutByThreadID(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "10", "12")
	writeRollout(t, day, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", rolloutHead)
	got := LocateCodexRollout(root, threadA)
	if filepath.Base(got) != "rollout-2026-10-12T17-20-37-"+threadA+".jsonl" {
		t.Errorf("got %q", got)
	}
	if LocateCodexRollout(root, "00000000-0000-0000-0000-000000000000") != "" {
		t.Error("unknown thread should give no path")
	}
}

func codexRolloutAt(modifiedAgoMs int64) CodexRollout {
	return CodexRollout{LastModifiedMs: now - modifiedAgoMs}
}

func codexHook(kind HookKind, atAgoMs int64) *CodexSignal {
	return &CodexSignal{HookSignal: HookSignal{Kind: kind, AtMs: now - atAgoMs}}
}

// Codex state uses the same semantics as Claude, except there's no process to look at,
// so it falls back to whether Codex is running plus how long the rollout has been quiet
func TestCodexStateFollowsRollout(t *testing.T) {
	cases := []struct {
		name    string
		rollout CodexRollout
		signal  *CodexSignal
		unread  bool
		alive   bool
		want    TaskState
	}{
		{"no codex running is completed", codexRolloutAt(0), nil, false, false, Completed},
		{"no codex running keeps unread", codexRolloutAt(0), nil, true, false, Unread},
		{"generating is running", codexRolloutAt(1_000), nil, false, true, Running},
		{"ended is completed", CodexRollout{LastEvent: Ended, LastModifiedMs: now}, nil, false, true, Completed},
		{"ended but unread", CodexRollout{LastEvent: Ended, LastModifiedMs: now}, nil, true, true, Unread},
		{"fresh tool call is running", CodexRollout{LastEvent: ToolPending, LastModifiedMs: now - 5_000}, nil, false, true, Running},
		{"stale tool call is waiting", CodexRollout{LastEvent: ToolPending, LastModifiedMs: now - 120_000}, nil, false, true, Waiting},
		{"stale generating gives up", codexRolloutAt(codexLiveWindow.Milliseconds() + 1), nil, false, true, Completed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codexState(c.rollout, c.signal, c.unread, c.alive, now)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// A hook newer than the rollout wins: events like a running tool or a just-finished turn
// haven't reached the rollout yet
func TestCodexHookOverridesRollout(t *testing.T) {
	staleRollout := CodexRollout{LastEvent: Generating, LastModifiedMs: now - 10_000}
	if got := codexState(staleRollout, codexHook(HookPermissionPrompt, 1_000), false, true, now); got != Waiting {
		t.Errorf("permission prompt should be waiting, got %v", got)
	}
	if got := codexState(staleRollout, codexHook(HookStopped, 1_000), false, true, now); got != Completed {
		t.Errorf("stop should finish, got %v", got)
	}
	if got := codexState(staleRollout, codexHook(HookStopped, 1_000), true, true, now); got != Unread {
		t.Errorf("stop with unread should stay unread, got %v", got)
	}
	if got := codexState(codexRolloutAt(10_000), codexHook(HookBusy, 1_000), false, true, now); got != Running {
		t.Errorf("busy hook should be running, got %v", got)
	}
	// An older hook must not override a newer rollout conclusion
	ended := CodexRollout{LastEvent: Ended, LastModifiedMs: now}
	if got := codexState(ended, codexHook(HookBusy, 60_000), false, true, now); got != Completed {
		t.Errorf("older hook must not override newer rollout, got %v", got)
	}
}

func TestCodexHooksInstalled(t *testing.T) {
	home := t.TempDir()
	if CodexHooksInstalled(home) {
		t.Fatal("no hooks file should mean not installed")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexHooksPath(home), []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if CodexHooksInstalled(home) {
		t.Fatal("hooks without the script should mean not installed")
	}
	body := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/Users/me/.config/spark/spark-codex-hook.sh"}]}]}}`
	if err := os.WriteFile(CodexHooksPath(home), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !CodexHooksInstalled(home) {
		t.Fatal("hooks naming the script should mean installed")
	}
}

func TestScanCodexFindsThreads(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "12")
	writeRollout(t, day, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", rolloutHead)
	// Sessions that are too old must not show up
	old := filepath.Join(home, ".codex", "sessions", "2020", "01", "01")
	oldPath := writeRollout(t, old, "rollout-2020-01-01T00-00-00-00000000-0000-0000-0000-000000000001.jsonl", rolloutHead)
	stale := nowTime().Add(-codexScanWindow - 24*60*60*1000*1000*1000)
	if err := os.Chtimes(oldPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	s := &Scanner{
		CodexRoot:    filepath.Join(home, ".codex", "sessions"),
		CodexHome:    filepath.Join(home, ".codex"),
		CodexHookDir: CodexHookDir(home),
	}
	got := s.scanCodex(nowTime())
	if len(got) != 1 {
		t.Fatalf("want 1 thread, got %d: %+v", len(got), got)
	}
	item := got[0].item
	if item.ID != threadA || item.Source != SourceCodex {
		t.Errorf("item = %+v", item)
	}
	if got[0].project != "joyspace-ai" {
		t.Errorf("project = %q", got[0].project)
	}
}

// Resuming a thread writes a second rollout, so one thread owns two files. Both are the
// same thread: listed separately, the thread shows up twice and the second copy can't be
// opened at all, because its name carries the segment as well
func TestResumedRolloutStaysOneThread(t *testing.T) {
	const segment = "01a15d62-98b3-75c3-9471-3d457a50c519"
	meta := `{"type":"session_meta","payload":{"id":"` + threadA + `","session_id":"` + threadA + `","cwd":"/Users/me/work/docs"}}` + "\n"
	userMsg := func(text string) string {
		return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}` + "\n"
	}

	home := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "21")
	stub := writeRollout(t, day, "rollout-2026-10-21T15-34-42-"+threadA+".jsonl", meta)
	resumed := writeRollout(t, day, "rollout-2026-10-21T15-35-05-"+threadA+"_"+segment+".jsonl",
		meta+userMsg("redesign the last diagram")+`{"type":"event_msg","payload":{"type":"task_started"}}`+"\n")
	setModTime(t, stub, nowTime().Add(-2*time.Hour))
	setModTime(t, resumed, nowTime().Add(-time.Minute))

	s := &Scanner{
		CodexRoot:    filepath.Join(home, ".codex", "sessions"),
		CodexHome:    filepath.Join(home, ".codex"),
		CodexHookDir: CodexHookDir(home),
	}
	got := s.scanCodex(nowTime())
	if len(got) != 1 {
		t.Fatalf("one thread should be one row, got %d: %+v", len(got), got)
	}
	item := got[0].item
	if item.ID != threadA {
		t.Errorf("id = %q, want the thread id", item.ID)
	}
	if _, ok := CodexDeeplink(item.ID); !ok {
		t.Errorf("thread %q can't be opened", item.ID)
	}
	// The resumed segment is where the turn runs, so it decides the state and the time
	if item.State != Running {
		t.Errorf("state = %v, want the resumed segment's", item.State)
	}
	if got[0].activityTs != nowTime().Add(-time.Minute).UnixMilli() {
		t.Errorf("activity = %v, want the resumed segment's", got[0].activityTs)
	}
	if item.Title != "redesign the last diagram" {
		t.Errorf("title = %q", item.Title)
	}
}

// Codex keeps its own name per thread, and it is the name the app shows; the first user
// message is only a guess, and on a resumed segment it opens mid-thread
func TestTitlePrefersCodexThreadName(t *testing.T) {
	const segment = "01a15d62-98b3-75c3-9471-3d457a50c519"
	meta := `{"type":"session_meta","payload":{"id":"` + threadA + `","cwd":"/Users/me/work/docs"}}` + "\n"
	userMsg := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"what the first message said"}]}}` + "\n"

	home := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "21")
	stub := writeRollout(t, day, "rollout-2026-10-21T15-34-42-"+threadA+".jsonl", meta+userMsg)
	resumed := writeRollout(t, day, "rollout-2026-10-21T15-35-05-"+threadA+"_"+segment+".jsonl",
		meta+`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"follow up"}]}}`+"\n")
	setModTime(t, stub, nowTime().Add(-2*time.Hour))
	setModTime(t, resumed, nowTime().Add(-time.Minute))

	s := &Scanner{
		CodexRoot:    filepath.Join(home, ".codex", "sessions"),
		CodexHome:    filepath.Join(home, ".codex"),
		CodexHookDir: CodexHookDir(home),
	}
	if got := s.scanCodex(nowTime()); len(got) != 1 || got[0].item.Title != "what the first message said" {
		t.Fatalf("without a name from Codex, the first message of the oldest segment is the title, got %+v", got)
	}

	index := `{"id":"` + threadA + `","thread_name":"重设计文章最后一张图","updated_at":"2026-10-21T07:35:02.220019Z"}` + "\n"
	if err := os.WriteFile(CodexThreadIndexPath(s.CodexHome), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.scanCodex(nowTime()); len(got) != 1 || got[0].item.Title != "重设计文章最后一张图" {
		t.Fatalf("Codex's own thread name should win, got %+v", got)
	}
}

func setModTime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestProjectNameFallsBackToTitle(t *testing.T) {
	if got := projectName("/Users/me/work/joyspace-ai", "t"); got != "joyspace-ai" {
		t.Errorf("got %q", got)
	}
	if got := projectName("", "only title"); got != "only title" {
		t.Errorf("got %q", got)
	}
	if got := projectName("", ""); got != "Unknown" {
		t.Errorf("got %q", got)
	}
}

func nowTime() time.Time { return time.UnixMilli(now) }

// Injected header context can run to tens of KB with the title after it; reading a fixed
// window would miss the real prompt
func TestFindsTitleAfterLargeInjectedHeader(t *testing.T) {
	filler := strings.Repeat("x", 200*1024)
	body := `{"type":"session_meta","payload":{"session_id":"` + threadA + `","cwd":"/Users/me/work/acme"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions\n` + filler + `"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"the real first line\nsecond line"}]}}` + "\n"
	dir := t.TempDir()
	path := writeRollout(t, dir, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", body)
	got, ok := ReadCodexRollout(path)
	if !ok {
		t.Fatal("rollout should be readable")
	}
	if got.Cwd != "/Users/me/work/acme" {
		t.Errorf("cwd = %q", got.Cwd)
	}
	if got.Title != "the real first line" {
		t.Errorf("title = %q", got.Title)
	}
}

// A skill prompt starts with a markdown link, and the title must drop that path
func TestStripsSkillLinkFromTitle(t *testing.T) {
	body := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"[$grill-me](/Users/me/.agents/skills/grill-me/SKILL.md) look over this repo\nsecond line"}]}}` + "\n"
	dir := t.TempDir()
	path := writeRollout(t, dir, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", body)
	got, _ := ReadCodexRollout(path)
	if got.Title != "look over this repo" {
		t.Errorf("title = %q", got.Title)
	}
}

func TestStripLeadingLinksKeepsPlainText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[$s](/p/SKILL.md) do the thing", "do the thing"},
		{"plain text", "plain text"},
		{"[no parens] after", "[no parens] after"},
		{"[link only](/p/x)", "[link only](/p/x)"},
	}
	for _, c := range cases {
		if got := stripLeadingLinks(c.in); got != c.want {
			t.Errorf("stripLeadingLinks(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A sub-agent thread carries parent_thread_id and has no terminal, so it stays out of
// the list
func TestSkipsSubagentThreads(t *testing.T) {
	body := `{"type":"session_meta","payload":{"session_id":"` + threadA + `","cwd":"/Users/me/work/acme","parent_thread_id":"01a12f69-fdb1-7173-b784-47189d9442d0"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	dir := t.TempDir()
	path := writeRollout(t, dir, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", body)
	got, ok := ReadCodexRollout(path)
	if !ok {
		t.Fatal("rollout should be readable")
	}
	if !got.Subagent {
		t.Error("parent_thread_id should mark a subagent thread")
	}

	s := &Scanner{
		CodexRoot:    filepath.Dir(dir),
		CodexHome:    dir,
		CodexHookDir: CodexHookDir(dir),
	}
	if got := s.scanCodex(nowTime()); len(got) != 0 {
		t.Errorf("subagent threads should be skipped, got %+v", got)
	}
}

// A sub-agent rollout starts with its own meta and then replays the parent's, which has no
// parent_thread_id. The parent's meta must not overwrite what the first one said, or the
// sub-agent shows up as a session titled after the parent's prompt
func TestSubagentMetaReplayKeepsSubagentFlag(t *testing.T) {
	parent := "01a13337-57d0-7453-a319-7ad00258eb21"
	body := `{"type":"session_meta","payload":{"id":"` + threadA + `","parent_thread_id":"` + parent + `","thread_source":"subagent","cwd":"/Users/me/work/acme"}}` + "\n" +
		`{"type":"session_meta","payload":{"id":"` + parent + `","thread_source":"user","cwd":"/Users/me/work/acme"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"split the persistence layer"}]}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	dir := t.TempDir()
	path := writeRollout(t, dir, "rollout-2026-10-12T17-20-37-"+threadA+".jsonl", body)
	got, ok := ReadCodexRollout(path)
	if !ok {
		t.Fatal("rollout should be readable")
	}
	if !got.Subagent {
		t.Error("the first session_meta should still mark this a subagent thread")
	}
	if got.Cwd != "/Users/me/work/acme" {
		t.Errorf("cwd = %q", got.Cwd)
	}

	s := &Scanner{
		CodexRoot:    filepath.Dir(dir),
		CodexHome:    dir,
		CodexHookDir: CodexHookDir(dir),
	}
	if found := s.scanCodex(nowTime()); len(found) != 0 {
		t.Errorf("subagent threads should be skipped, got %+v", found)
	}
}
