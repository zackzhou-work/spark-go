package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

// cursorSession is one Cursor agent row
func cursorSession(id, title string, state monitor.TaskState, jump bool) monitor.SessionItem {
	return monitor.SessionItem{
		ID: id, Source: monitor.SourceCursor, Title: title, State: state,
		CanJumpCursor: jump, WorkingDir: "/Users/someone/project",
		ActivityAt: time.Now().Add(-3 * time.Minute), IsToday: true,
	}
}

// tempHome points the window's disk reads at a directory the test owns, so nothing here depends
// on the machine it runs on
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	restore := userHome
	userHome = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHome = restore })
	return home
}

// A row spark can't answer for says so, and the project doesn't wear the caught-up face: nothing
// here is known to be done
func TestUnknownCursorSessionIsNotShownAsFinished(t *testing.T) {
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "project", Sessions: []monitor.SessionItem{
		cursorSession("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "Trace the ingest bug", monitor.Unknown, false),
	}}}}
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Trace the ingest bug, Status unavailable") {
		t.Errorf("the row should say the status is unavailable; it shows %q", tt.Texts())
	}
	if tt.HasText("New") {
		t.Error("an unknown session is not new")
	}
	m, _ := projectLook(a.groups[0])
	if m != moodUnknown {
		t.Errorf("project mood %v, want unknown", m)
	}
	card := projectCards(a.groups)[0]
	if got := card.status(); got != "Status unavailable" {
		t.Errorf("card status %q", got)
	}
	if got := projectSummary(a.groups[0]); got != "Status unavailable" {
		t.Errorf("project summary %q", got)
	}
}

// Once something in the project is running or unread, that is what the card is about
func TestKnownSessionsOutrankAnUnknownOne(t *testing.T) {
	group := monitor.ProjectGroup{ProjectName: "project", Sessions: []monitor.SessionItem{
		cursorSession("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "a", monitor.Unknown, false),
		cursorSession("1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081", "b", monitor.Running, false),
	}}
	if m, _ := projectLook(group); m != moodWorking {
		t.Errorf("mood %v, want working", m)
	}
	group.Sessions[1].State = monitor.Unread
	if m, p := projectLook(group); m != moodToRead || p.count != 1 {
		t.Errorf("mood %v count %d, want to-read 1", m, p.count)
	}
}

// A verified Cursor build puts the private Agent link on the row; an unverified one leaves the row
// static, with no pointer and no arrow
func TestCursorRowJumpsOnlyWhenTheScannerSaysSo(t *testing.T) {
	restore := installCursorHooks
	installCursorHooks = func(string) error { return nil }
	t.Cleanup(func() { installCursorHooks = restore })

	for _, tc := range []struct {
		name    string
		jump    bool
		wantURL string
	}{
		{"verified", true, "cursor://anysphere.cursor-deeplink/agent?id=0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"},
		{"unverified", false, ""},
	} {
		a := &app{groups: []monitor.ProjectGroup{{ProjectName: "project", Sessions: []monitor.SessionItem{
			cursorSession("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "Trace the ingest bug", monitor.Running, tc.jump),
		}}}}
		tt := newViewTester(a, 360, 640)

		rect, ok := tt.Find("Trace the ingest bug, Running")
		if !ok {
			t.Fatalf("%s: no row; it shows %q", tc.name, tt.Texts())
		}
		tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
		tt.Frame()
		wantCursor := ui.CursorDefault
		if tc.jump {
			wantCursor = ui.CursorPointer
		}
		if got := tt.Cursor(); got != wantCursor {
			t.Errorf("%s: cursor %v, want %v", tc.name, got, wantCursor)
		}
		if arrow := tt.HasText("→"); arrow != tc.jump {
			t.Errorf("%s: arrow %v", tc.name, arrow)
		}

		if err := tt.Click("Trace the ingest bug, Running"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		urls := tt.OpenedURLs()
		if tc.wantURL == "" {
			if len(urls) != 0 {
				t.Errorf("%s: a row with no verified link opened %v", tc.name, urls)
			}
			continue
		}
		if len(urls) != 1 || urls[0] != tc.wantURL {
			t.Errorf("%s: opened %v, want %s", tc.name, urls, tc.wantURL)
		}
		if strings.Contains(urls[0], "claude://") {
			t.Error("a Cursor session must not go through Claude's link")
		}
	}
}

// The install entry has to be reachable with sessions on screen: the banner only shows on an
// empty list
func TestCursorInstallNoticeShowsWithSessionsOnScreen(t *testing.T) {
	home := tempHome(t)
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "project", Sessions: []monitor.SessionItem{
		cursorSession("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "Trace the ingest bug", monitor.Unknown, false),
	}}}}
	a.applyCursorSetup(sideState{cursorDetected: true, cursorState: monitor.CursorHooksMissing})

	exe := filepath.Join(home, "spark")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	restore := installCursorHooks
	installCursorHooks = func(gotHome string) error {
		calls++
		if gotHome != home {
			t.Errorf("installed into %q, want the window's home", gotHome)
		}
		return monitor.InstallCursorHooks(gotHome, exe)
	}
	t.Cleanup(func() { installCursorHooks = restore })

	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Spark can show Cursor’s desktop agents once its hooks are installed.") {
		t.Fatalf("no install line; it shows %q", tt.Texts())
	}
	if err := tt.Click("Install for Cursor"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("installed %d times", calls)
	}
	tt.Frame()
	// Configured, but nothing has been heard from Cursor yet, and the line says exactly that
	if !tt.HasText("Cursor is set up. Start a new turn to verify reporting.") {
		t.Errorf("after installing it shows %q", tt.Texts())
	}
	if a.cursorState != monitor.CursorHooksCurrent {
		t.Errorf("state %v", a.cursorState)
	}
}

// A failed install must not start claiming the hooks are in place
func TestFailedCursorInstallKeepsTheStateAndSaysSo(t *testing.T) {
	tempHome(t)
	a := &app{}
	a.applyCursorSetup(sideState{cursorDetected: true, cursorState: monitor.CursorHooksMissing})
	restore := installCursorHooks
	installCursorHooks = func(string) error { return errors.New("disk full") }
	t.Cleanup(func() { installCursorHooks = restore })

	tt := newViewTester(a, 360, 640)
	if err := tt.Click("Install for Cursor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.cursorState != monitor.CursorHooksMissing {
		t.Errorf("state after a failure: %v", a.cursorState)
	}
	if !tt.HasText("Spark couldn't install its Cursor hooks.") {
		t.Errorf("the failure should be visible; it shows %q", tt.Texts())
	}
	if !tt.HasText("Install for Cursor") {
		t.Error("the install button should still be there to try again")
	}
}

// A hooks file spark can't parse is reported and left alone: there is no button, because
// rewriting it would take the user's own hooks with it
func TestBrokenCursorHooksAreReportedNotRewritten(t *testing.T) {
	tempHome(t)
	a := &app{}
	a.applyCursorSetup(sideState{cursorDetected: true, cursorState: monitor.CursorHooksBroken})
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Cursor’s hooks file can’t be read, so spark left it alone.") {
		t.Fatalf("it shows %q", tt.Texts())
	}
	if tt.HasText("Install for Cursor") {
		t.Error("a broken file should not offer an install")
	}
}

// Someone who doesn't run Cursor never sees the subject
func TestNoCursorNoticeWithoutCursor(t *testing.T) {
	tempHome(t)
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "project", Sessions: []monitor.SessionItem{
		cursorSession("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "a", monitor.Unknown, false),
	}}}}
	a.applyCursorSetup(sideState{cursorDetected: false, cursorState: monitor.CursorHooksMissing})
	tt := newViewTester(a, 360, 640)
	for _, text := range tt.Texts() {
		if strings.Contains(text, "Cursor") {
			t.Errorf("a machine without Cursor shows %q", text)
		}
	}
}

// The line has to fit the narrow window too
func TestCursorInstallNoticeFitsTheNarrowWindow(t *testing.T) {
	tempHome(t)
	a := &app{}
	a.applyCursorSetup(sideState{cursorDetected: true, cursorState: monitor.CursorHooksMissing})
	for _, width := range []int{300, 360, 700} {
		tt := newViewTester(a, width, 640)
		if !tt.HasText("Spark can show Cursor’s desktop agents once its hooks are installed.") {
			t.Errorf("width %d lost the line; it shows %q", width, tt.Texts())
		}
		if !tt.HasText("Install for Cursor") {
			t.Errorf("width %d lost the button", width)
		}
	}
}

// An unknown session with a verified link is still a target: the state and the link are separate
func TestUnknownCursorRowCanStillJump(t *testing.T) {
	tempHome(t)
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "project", Sessions: []monitor.SessionItem{
		cursorSession("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", "Trace the ingest bug", monitor.Unknown, true),
	}}}}
	tt := newViewTester(a, 360, 640)
	if err := tt.Click("Trace the ingest bug, Status unavailable"); err != nil {
		t.Fatal(err)
	}
	urls := tt.OpenedURLs()
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "cursor://") {
		t.Errorf("opened %v", urls)
	}
}

// Two sources can hand out the same ID string; the rows stay separate, including their snoozes
func TestSessionKeysSeparateTheSources(t *testing.T) {
	codex := monitor.SessionItem{ID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Source: monitor.SourceCodex, Title: "codex"}
	cursor := monitor.SessionItem{ID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Source: monitor.SourceCursor, Title: "cursor"}
	if sessionKey(codex) == sessionKey(cursor) {
		t.Fatal("the same ID from two sources should not share a key")
	}
	a := &app{
		groups:  []monitor.ProjectGroup{{ProjectName: "p", Sessions: []monitor.SessionItem{codex, cursor}}},
		snoozed: map[string]time.Time{sessionKey(codex): codex.ActivityAt},
	}
	if !a.isSnoozed(codex) {
		t.Error("the codex session should be snoozed")
	}
	if a.isSnoozed(cursor) {
		t.Error("the cursor session should not be snoozed with it")
	}
}

// SPARK_REAL=1 go test -run TestRealHomeRenders . renders the window against this machine's own
// sessions, Cursor included, which is where a real database and a real hooks file meet the view
func TestRealHomeRenders(t *testing.T) {
	if os.Getenv("SPARK_REAL") == "" {
		t.Skip("set SPARK_REAL=1 to render this machine's sessions")
	}
	scanner := monitor.NewScanner()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	a := &app{scanner: scanner, pinned: true, groups: scanner.ScanAll()}
	a.applySideState(readSideStateFor(scanner, home))
	cursor := 0
	for _, g := range a.groups {
		for _, s := range g.Sessions {
			if s.Source == monitor.SourceCursor {
				cursor++
			}
		}
	}
	if cursor == 0 {
		t.Log("no Cursor sessions on this machine; rendering the rest")
	}
	// Both pages, both layouts, and a project page: the narrow one stacks where the wide one
	// lays out, and elements disappear between frames
	for _, width := range []int{260, 300, 360, 700} {
		for _, filter := range []int{filterToday, filterAll} {
			a.filter = filter
			tt := ui.NewTester(a.view, width, 640)
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			tt.Frame()
			tt.Frame()
			if filter == filterAll && !tt.HasText("Every agent.") {
				t.Errorf("width %d: the All page didn't render", width)
			}
		}
	}
	for _, g := range a.groups {
		a.project = g.ProjectName
		tt := ui.NewTester(a.view, 360, 640)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		tt.Frame()
	}
	t.Logf("rendered %d projects, %d Cursor sessions, %d sessions in all", len(a.groups), cursor, monitor.CountSessions(a.groups))
}
