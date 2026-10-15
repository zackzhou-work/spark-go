package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

// piWaitingSession is a pi session stuck on a question, in Herdr when pane is set
func piWaitingSession(id, title, pane string) monitor.SessionItem {
	return monitor.SessionItem{
		ID: id, Source: monitor.SourcePi, Title: title, State: monitor.Waiting,
		WaitReason: monitor.WaitQuestion, Pane: pane, ActivityAt: time.Now().Add(-2 * time.Minute), IsToday: true,
	}
}

// fakeHerdrSocket serves the Herdr API from a socket under home, recording the focus calls
func fakeHerdrSocket(t *testing.T, home string) *[]string {
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

	var focused []string
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
					focused = append(focused, target)
				}
				_, _ = conn.Write([]byte(`{"id":"spark","result":{"type":"agent_info","agent":{}}}` + "\n"))
			}()
		}
	}()
	return &focused
}

func waitForFocus(t *testing.T, focused *[]string, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, got := range *focused {
			if got == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("focus calls = %v, want %s", *focused, want)
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
	focused := fakeHerdrSocket(t, home)
	raised := ""
	restore := activateApp
	activateApp = func(name string) { raised = name }
	t.Cleanup(func() { activateApp = restore })

	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		{ID: "abc", Source: monitor.SourcePi, Title: "Add pi sessions", State: monitor.Running,
			Pane: "w4:pC", ActivityAt: time.Now(), IsToday: true},
	}}}}
	tt := newViewTester(a, 360, 640)

	if err := tt.Click("Add pi sessions, Running"); err != nil {
		t.Fatal(err)
	}
	waitForFocus(t, focused, "w4:pC")
	if raised != "Herdr" {
		t.Errorf("raised %q, want Herdr", raised)
	}
	if got := tt.OpenedURLs(); len(got) != 0 {
		t.Errorf("a pi session has no deep link, opened %v", got)
	}
}

// The card's button goes the same way as the row
func TestPiWaitingCardFocusesThePane(t *testing.T) {
	home := shortHome(t)
	t.Setenv("HOME", home)
	focused := fakeHerdrSocket(t, home)
	restore := activateApp
	activateApp = func(string) {}
	t.Cleanup(func() { activateApp = restore })

	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		piWaitingSession("abc", "Add pi sessions", "w4:pC"),
	}}}, project: "spark-go"}
	tt := newViewTester(a, 360, 640)

	if err := tt.Click("Answer in pi"); err != nil {
		t.Fatal(err)
	}
	waitForFocus(t, focused, "w4:pC")
	if got := tt.OpenedURLs(); len(got) != 0 {
		t.Errorf("a pi session has no deep link, opened %v", got)
	}
}

func TestPiRowWithoutASignalSaysSoOnHoverOnly(t *testing.T) {
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "spark-go", Sessions: []monitor.SessionItem{
		{ID: "abc", Source: monitor.SourcePi, Title: "Rewrite the guide", State: monitor.Completed,
			NoSignal: true, ActivityAt: time.Now().Add(-time.Hour), IsToday: true},
	}}}}
	tt := newViewTester(a, 360, 640)
	if tt.HasText("no live signal") {
		t.Fatal("the hint belongs on hover, not in a resting view")
	}

	rect, ok := tt.Find("Rewrite the guide, Completed")
	if !ok {
		t.Fatalf("no row found; it shows %q", tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	if !tt.HasText("no live signal") {
		t.Errorf("hovering should explain itself; it shows %q", tt.Texts())
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
