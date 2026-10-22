package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zackzhou-work/spark-go/internal/monitor"
)

// piWaitingSession is a pi session stuck on a question, in Herdr when pane is set
func piWaitingSession(id, title, pane string) monitor.SessionItem {
	return monitor.SessionItem{
		ID: id, Source: monitor.SourcePi, Title: title, State: monitor.Waiting,
		WaitReason: monitor.WaitQuestion, Pane: pane, ActivityAt: time.Now().Add(-2 * time.Minute), IsToday: true,
	}
}

// jumpRecorder collects what the jump did, from the goroutine that did it
type jumpRecorder struct {
	mu      sync.Mutex
	focused []string
	raised  []string
}

func (r *jumpRecorder) focus(target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.focused = append(r.focused, target)
}

func (r *jumpRecorder) raise(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.raised = append(r.raised, name)
}

// wait blocks until both the focus call and the window raise have happened
func (r *jumpRecorder) wait(t *testing.T, pane, app string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got = strings.Join(r.focused, ",") + " / " + strings.Join(r.raised, ",")
		r.mu.Unlock()
		if strings.Contains(got, pane) && strings.Contains(got, app) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the jump did %q, want focus %s and raise %s", got, pane, app)
}

// fakeHerdrSocket serves the Herdr API from a socket under home, recording the focus calls
func fakeHerdrSocket(t *testing.T, home string, rec *jumpRecorder) {
	t.Helper()
	dir := filepath.Join(home, ".config", "herdr")
	_ = dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "herdr.sock")
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
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if json.Unmarshal(line, &req) != nil {
					return
				}
				if req.Method == "agent.focus" {
					target, _ := req.Params["target"].(string)
					rec.focus(target)
				}
				_, _ = conn.Write([]byte(`{"id":"spark","result":{"type":"agent_info","agent":{}}}` + "\n"))
			}()
		}
	}()
}

// shortHome is a HOME whose socket path still fits unix's ~104 character limit
func shortHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "spark")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.MkdirAll(filepath.Join(dir, ".config", "herdr"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

func TestPiRowInHerdrFocusesThePane(t *testing.T) {
	home := shortHome(t)
	t.Setenv("HOME", home)
	rec := &jumpRecorder{}
	fakeHerdrSocket(t, home, rec)
	restore := activateApp
	activateApp = rec.raise
	t.Cleanup(func() { activateApp = restore })

	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		{ID: "abc", Source: monitor.SourcePi, Title: "Add pi sessions", State: monitor.Running,
			Pane: "w4:pC", ActivityAt: time.Now(), IsToday: true},
	}}}}
	tt := newViewTester(a, 360, 640)

	if err := tt.Click("Add pi sessions, Running"); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "w4:pC", "Herdr")
	if got := tt.OpenedURLs(); len(got) != 0 {
		t.Errorf("a pi session has no deep link, opened %v", got)
	}
}

// The card's button goes the same way as the row
func TestPiWaitingCardFocusesThePane(t *testing.T) {
	home := shortHome(t)
	t.Setenv("HOME", home)
	rec := &jumpRecorder{}
	fakeHerdrSocket(t, home, rec)
	restore := activateApp
	activateApp = rec.raise
	t.Cleanup(func() { activateApp = restore })

	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		piWaitingSession("abc", "Add pi sessions", "w4:pC"),
	}}}, project: "spark-go"}
	tt := newViewTester(a, 360, 640)

	if err := tt.Click("Answer in pi"); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, "w4:pC", "Herdr")
	if got := tt.OpenedURLs(); len(got) != 0 {
		t.Errorf("a pi session has no deep link, opened %v", got)
	}
}

// A pi session spark can't open is not a target: the pointer stays an arrow, hovering shows
// no jump, and the waiting card is where the reason lives
func TestPiRowSparkCantOpenIsntATarget(t *testing.T) {
	running := piWaitingSession("def", "Run the migration", "")
	running.State, running.WaitReason = monitor.Running, monitor.WaitPermission
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		piWaitingSession("abc", "Rewrite the guide", ""),
		running,
	}}}, project: "spark-go"}
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Waiting in a terminal spark can’t reach") {
		t.Fatalf("the card should say why it can't be opened; it shows %q", tt.Texts())
	}

	rect, ok := tt.Find("Run the migration, Running")
	if !ok {
		t.Fatalf("no row found; it shows %q", tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	if got := tt.Cursor(); got != ui.CursorDefault {
		t.Error("a row with nowhere to go shouldn't offer the pointer")
	}
	if tt.HasText("→") {
		t.Error("no arrow for a row with no destination")
	}
}

// A row spark can open still shows the arrow, so the two hover states stay apart
func TestJumpableRowKeepsTheArrowOnHover(t *testing.T) {
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		{ID: "abc", Source: monitor.SourcePi, Title: "Add pi sessions", State: monitor.Running,
			Pane: "w4:pC", ActivityAt: time.Now(), IsToday: true},
	}}}}
	tt := newViewTester(a, 360, 640)
	rect, ok := tt.Find("Add pi sessions, Running")
	if !ok {
		t.Fatalf("no row found; it shows %q", tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	if !tt.HasText("→") {
		t.Errorf("a jumpable row shows the arrow; it shows %q", tt.Texts())
	}
}

func TestWaitingCardNamesTheAgent(t *testing.T) {
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		piWaitingSession("abc", "Add pi sessions", "w4:pC"),
	}}}, project: "spark-go"}
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Answer in pi") {
		t.Fatalf("the card should name pi; it shows %q", tt.Texts())
	}
}

// A question in a terminal spark can't open still needs the user, but the card must not
// pretend it can take them there
func TestWaitingCardWithoutAJumpTargetOnlyOffersLater(t *testing.T) {
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		piWaitingSession("abc", "Add pi sessions", ""),
	}}}, project: "spark-go"}
	tt := newViewTester(a, 360, 640)
	// There is nowhere to go, so the card offers no way there
	if tt.HasText("Answer in pi") {
		t.Errorf("no jump target, so no button; it shows %q", tt.Texts())
	}
	if !tt.HasText("Question · 2m") {
		t.Errorf("the card still says what is waiting; it shows %q", tt.Texts())
	}
	if !tt.HasText("Waiting in a terminal spark can’t reach") {
		t.Errorf("the card should explain itself; it shows %q", tt.Texts())
	}
	if err := tt.Click("Later"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.snoozed["abc"]; !ok {
		t.Error("Later should put the card away")
	}
}

func TestWaitingCardUsesTheSourceName(t *testing.T) {
	claude := monitor.SessionItem{ID: "local_1", Title: "t", State: monitor.Waiting, WaitReason: monitor.WaitPermission}
	codex := monitor.SessionItem{ID: "01a1", Source: monitor.SourceCodex, Title: "t", State: monitor.Waiting}
	pi := monitor.SessionItem{ID: "abc", Source: monitor.SourcePi, Title: "t", State: monitor.Waiting, WaitReason: monitor.WaitQuestion}

	if got := answerLabel(claude); got != "Respond in Claude" {
		t.Errorf("Claude = %q", got)
	}
	if got := answerLabel(codex); got != "Respond in Codex" {
		t.Errorf("Codex = %q", got)
	}
	if got := answerLabel(pi); got != "Answer in pi" {
		t.Errorf("pi = %q", got)
	}
}

// Jumpability drives both the row's arrow and the card's button
func TestJumpable(t *testing.T) {
	cases := []struct {
		name string
		item monitor.SessionItem
		want bool
	}{
		{"claude", monitor.SessionItem{Source: monitor.SourceClaude}, true},
		{"codex", monitor.SessionItem{Source: monitor.SourceCodex}, true},
		{"pi in herdr", monitor.SessionItem{Source: monitor.SourcePi, Pane: "w1:p1"}, true},
		{"pi elsewhere", monitor.SessionItem{Source: monitor.SourcePi}, false},
	}
	for _, c := range cases {
		if got := c.item.Jumpable(); got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

// An outdated extension is a dead end without this: reloading pi loads the same old file, so
// the line under the heading has to offer the update itself
func TestTodayNoticeOffersToUpdateAnOutdatedExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := monitor.PiExtensionPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("// what an older release installed\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	a := demoApp()
	a.hookInstalled, a.codexHookInstalled = true, true
	a.applyPiSetup(readSideState(home))
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("out of date") {
		t.Fatalf("the page should say the extension needs updating; it shows %q", tt.Texts())
	}
	if tt.HasText("Install for pi") {
		t.Error("the line carries the action, so the banner has nothing to offer")
	}
	if err := tt.Click("Update"); err != nil {
		t.Fatal(err)
	}
	if !monitor.PiExtensionInstalled(home) {
		t.Fatal("Update should write the file this build ships")
	}
	// With the file current, what is left is the reload
	tt.Frame()
	if !tt.HasText("need /reload or a restart") {
		t.Errorf("after updating it should ask for the reload; it shows %q", tt.Texts())
	}
}
