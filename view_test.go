package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

func newViewTester(a *app, w, h int) *ui.Tester {
	tt := ui.NewTester(a.view, w, h)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	return tt
}

func TestTodayShowsTilesAndRecentSessions(t *testing.T) {
	tt := newViewTester(demoApp(), 360, 640)
	for _, s := range []string{"Your agents, today.", "acme-web", "search-api", "Respond", "Answer", "Working on 1", "1 to read", "Recent", "Upgrade the router to v7", "New"} {
		if !tt.HasText(s) {
			t.Errorf("Today should show %q; it shows %q", s, tt.Texts())
		}
	}
	if tt.HasText("Fix flaky checkout integration test") {
		t.Error("a waiting session belongs on its tile, not in Recent")
	}
	if tt.HasText("Migrate to the new auth client") {
		t.Error("Today should leave out sessions from other days")
	}
}

func TestRespondJumpsWithoutOpeningTheProject(t *testing.T) {
	a := demoApp()
	tt := newViewTester(a, 360, 640)
	if err := tt.Click("Respond"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.project != "" {
		t.Fatalf("Respond should jump to the session, not open %q", a.project)
	}
	if got := tt.OpenedURLs(); len(got) != 1 || !strings.Contains(got[0], "local_2") {
		t.Fatalf("Respond should open the waiting session, opened %v", got)
	}
}

func TestTileOpensProjectAndBackReturns(t *testing.T) {
	a := demoApp()
	tt := newViewTester(a, 360, 640)
	if err := tt.Click("search-api"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.project != "search-api" {
		t.Fatalf("clicking the tile should open its project, got %q", a.project)
	}
	for _, s := range []string{"1 question · 1 done", "Pick an embedding model", "Answer in Claude", "Later", "Sessions"} {
		if !tt.HasText(s) {
			t.Errorf("project page should show %q; it shows %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Back to Today"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.project != "" || !tt.HasText("Your agents, today.") {
		t.Fatal("Back should return to Today")
	}
}

func TestLaterHidesTheCardUntilTheSessionMoves(t *testing.T) {
	a := demoApp()
	a.project = "search-api"
	tt := newViewTester(a, 360, 640)
	if err := tt.Click("Later"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Answer in Claude") {
		t.Fatal("Later should put the card away")
	}

	a.groups[3].Sessions[0].ActivityAt = time.Now()
	tt.Frame()
	if !tt.HasText("Answer in Claude") {
		t.Fatal("the card should come back once the session has moved on")
	}
}

func TestAllShowsEveryProjectAndGroupsByDay(t *testing.T) {
	a := demoApp()
	a.filter = filterAll
	tt := newViewTester(a, 360, 640)
	for _, s := range []string{"Every agent.", "6 projects · 16 sessions", "Needs you", "All caught up", "Today", "Yesterday", "Earlier", "Migrate to the new auth client", "Port the release script to Go"} {
		if !tt.HasText(s) {
			t.Errorf("All should show %q; it shows %q", s, tt.Texts())
		}
	}
}

func TestEmptyTodayOffersAllSessionsAndTheHook(t *testing.T) {
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "old", Sessions: []monitor.SessionItem{
		{ID: "local_1", Title: "Last week's work", State: monitor.Completed, ActivityAt: time.Now().Add(-72 * time.Hour)},
	}}}}
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Everyone’s resting.") || !tt.HasText("Hear about prompts right away") {
		t.Fatalf("empty Today should rest and offer the hook; it shows %q", tt.Texts())
	}
	if err := tt.Click("Install for Claude"); err != nil {
		t.Fatal(err)
	}
	if got := tt.OpenedURLs(); len(got) != 1 || got[0] != hookGuideURL {
		t.Fatalf("Install for Claude should open the hook guide, opened %v", got)
	}
	if err := tt.Click("Show all sessions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.filter != filterAll {
		t.Fatal("Show all sessions should switch to All")
	}

	a.hookInstalled = true
	a.codexHookInstalled = true
	a.piHookInstalled = true
	a.filter = filterToday
	tt.Frame()
	if tt.HasText("Hear about prompts right away") {
		t.Fatal("no need to offer a hook that is installed")
	}
}

// The pi extension installs itself, unlike Claude's, which only opens the guide
func TestHookBannerInstallsThePiExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "old", Sessions: []monitor.SessionItem{
		{ID: "local_1", Title: "Last week's work", State: monitor.Completed, ActivityAt: time.Now().Add(-72 * time.Hour)},
	}}}, hookInstalled: true, codexHookInstalled: true}
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Install for pi") {
		t.Fatalf("the pi extension should be offered; it shows %q", tt.Texts())
	}
	if err := tt.Click("Install for pi"); err != nil {
		t.Fatal(err)
	}
	if !a.piHookInstalled {
		t.Error("the button should record the install")
	}
	if !monitor.PiExtensionInstalled(home) {
		t.Error("the extension should be on disk")
	}
	tt.Frame()
	if tt.HasText("Install for pi") {
		t.Error("an installed extension is not offered again")
	}
	// pi reads extensions at startup, so the card stays up until something reports
	if !tt.HasText("One more step for pi") {
		t.Errorf("the card should stay up until pi loads it; it shows %q", tt.Texts())
	}
}

// pi loads extensions when a process starts, so installing while sessions are running
// changes nothing for them: the card stays up until something reports afterwards
func TestHookBannerWaitsForPiToLoadTheExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := monitor.InstallPiExtension(home); err != nil {
		t.Fatalf("install: %v", err)
	}
	installedAt := monitor.PiInstalledAt(home)
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "old", Sessions: []monitor.SessionItem{
		{ID: "local_1", Title: "Last week's work", State: monitor.Completed, ActivityAt: time.Now().Add(-72 * time.Hour)},
	}}}, hookInstalled: true, codexHookInstalled: true}
	a.applyPiSetup(readSideState(home))

	tt := newViewTester(a, 360, 640)
	if !tt.HasText("One more step for pi") {
		t.Fatalf("the card should say what is still missing; it shows %q", tt.Texts())
	}
	if tt.HasText("Install for pi") {
		t.Error("nothing left to install")
	}

	// A receipt from a session still running an older extension says nothing about the new
	// one, however recently it was rewritten
	writeReceiptForTest(t, home, "old.json", "another-install")
	a.applyPiSetup(readSideState(home))
	tt.Frame()
	if !tt.HasText("One more step for pi") {
		t.Errorf("an older payload should not count; it shows %q", tt.Texts())
	}

	// One from the payload this build installs means a pi process loaded it
	writeReceiptForTest(t, home, "fresh.json", monitor.PiInstalledID(home))
	a.applyPiSetup(readSideState(home))
	tt.Frame()
	if tt.HasText("One more step for pi") {
		t.Errorf("the card should go once a session reports; it shows %q", tt.Texts())
	}
	_ = installedAt
}

// writeReceiptForTest puts a receipt in the pi directory, from a chosen install
func writeReceiptForTest(t *testing.T, home, name, install string) string {
	t.Helper()
	dir := monitor.PiReceiptDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	body := fmt.Sprintf(`{"install":%q,"sessionId":"x","pid":%d}`, install, os.Getpid())
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// The install button only lives in the banner, which an empty list renders; a user with
// sessions in front of them gets a line they can dismiss instead
func TestTodayNoticePointsAtThePiReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := monitor.InstallPiExtension(home); err != nil {
		t.Fatalf("install: %v", err)
	}
	installedAt := monitor.PiInstalledAt(home)
	a := demoApp()
	a.hookInstalled, a.codexHookInstalled = true, true
	a.applyPiSetup(readSideState(home))

	tt := newViewTester(a, 360, 640)
	if !tt.HasText("need /reload or a restart") {
		t.Fatalf("the page should say what pi still needs; it shows %q", tt.Texts())
	}
	if tt.HasText("Install for pi") {
		t.Error("the extension is installed, so nothing should be offered")
	}
	if err := tt.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("need /reload or a restart") {
		t.Errorf("dismissed, so it stays gone; it shows %q", tt.Texts())
	}

	// Dismissing is per install: a new install says what it needs again
	if err := monitor.InstallPiExtension(home); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if err := os.Chtimes(monitor.PiExtensionPath(home), installedAt.Add(time.Hour), installedAt.Add(time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	a.applyPiSetup(readSideState(home))
	tt.Frame()
	if !tt.HasText("need /reload or a restart") {
		t.Errorf("a later install brings the line back; it shows %q", tt.Texts())
	}

	// And a session reporting afterwards takes it away for good
	writeReceiptForTest(t, home, "fresh.json", monitor.PiInstalledID(home))
	a.applyPiSetup(readSideState(home))
	tt.Frame()
	if tt.HasText("need /reload or a restart") {
		t.Errorf("a session loaded it, so there is nothing left to say; it shows %q", tt.Texts())
	}
}

// Each hook installs separately: with Claude's installed and Codex's missing, Codex's is
// still offered
func TestHookBannerCoversCodexSeparately(t *testing.T) {
	a := &app{groups: []monitor.ProjectGroup{{ProjectName: "old", Sessions: []monitor.SessionItem{
		{ID: "local_1", Title: "Last week's work", State: monitor.Completed, ActivityAt: time.Now().Add(-72 * time.Hour)},
	}}}, hookInstalled: true}
	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Install for Codex") {
		t.Fatalf("Codex hook should be offered on its own; it shows %q", tt.Texts())
	}
	// HasText matches substrings and "Install" would hit "Install for Codex", so compare
	// labels exactly
	for _, text := range tt.Texts() {
		if text == "Install" {
			t.Fatal("the Claude hook is already installed, it should not be offered again")
		}
	}
}

// Its own fixture rather than the demo set, so the layout assertions stay about layout
func TestNarrowWindowStacksTheCards(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	a := &app{groups: []monitor.ProjectGroup{
		{ProjectName: "acme-web", Sessions: []monitor.SessionItem{
			{ID: "local_1", Title: "Add dark mode to the settings page", State: monitor.Running, ActivityAt: now, IsToday: true},
			{ID: "local_2", Title: "Upgrade the router to v7", State: monitor.Unread, ActivityAt: ago(14 * time.Minute), IsToday: true},
		}},
		{ProjectName: "data-pipeline", Sessions: []monitor.SessionItem{
			{ID: "local_4", Title: "Profile the nightly aggregation job", State: monitor.Unread, ActivityAt: ago(38 * time.Minute), IsToday: true},
		}},
		{ProjectName: "search-api", Sessions: []monitor.SessionItem{
			{ID: "local_5", Title: "Pick an embedding model", State: monitor.Waiting, WaitReason: monitor.WaitQuestion, ActivityAt: ago(8 * time.Minute), IsToday: true},
		}},
		{ProjectName: "infra", Sessions: []monitor.SessionItem{
			{ID: "local_6", Title: "Rotate the staging certificates", State: monitor.Waiting, WaitReason: monitor.WaitPermission, ActivityAt: ago(3 * time.Minute), IsToday: true},
		}},
	}}
	tt := newViewTester(a, 280, 600)
	for _, s := range []string{"Respond", "Answer", "data-pipeline", "Working", "1 to read", "Recent", "New"} {
		if !tt.HasText(s) {
			t.Errorf("narrow Today should show %q; it shows %q", s, tt.Texts())
		}
	}
	if !tt.HasText("Your agents, today.") {
		t.Error("narrow Today keeps the heading, a size smaller")
	}
	if tt.HasText("Working on 1") {
		t.Error("narrow Today drops the long status")
	}
	if tt.HasText("14m") {
		t.Error("narrow rows show New instead of the time")
	}
	if err := tt.Click("data-pipeline, 1 to read"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.project != "data-pipeline" {
		t.Fatalf("a project row should open its project, got %q", a.project)
	}
}

func TestLayoutFollowsWindowWidth(t *testing.T) {
	cases := []struct {
		w    float32
		want layout
	}{
		{360, layoutRegular}, {340, layoutRegular}, {339, layoutNarrow},
		{minWidth, layoutNarrow},
	}
	for _, tc := range cases {
		if got := layoutFor(tc.w); got != tc.want {
			t.Errorf("layoutFor(%v) = %v, want %v", tc.w, got, tc.want)
		}
	}
}

// A wide but short window keeps the tab buttons and cards, scrolling when content doesn't fit
func TestShortWindowKeepsTheTabs(t *testing.T) {
	tt := newViewTester(demoApp(), 282, 300)
	for _, s := range []string{"Today", "All", "Respond"} {
		if !tt.HasText(s) {
			t.Errorf("a short window should still show %q; it shows %q", s, tt.Texts())
		}
	}
}

func TestFitColumnsMatchesAutoFill(t *testing.T) {
	for _, tc := range []struct {
		width float32
		want  int
	}{{336, 2}, {300, 1}, {500, 3}, {100, 1}} {
		if got := fitColumns(tc.width, 150, 8); got != tc.want {
			t.Errorf("fitColumns(%v) = %d, want %d", tc.width, got, tc.want)
		}
	}
}

func TestByDaySplitsAtMidnight(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.Local)
	at := func(d, h int) monitor.SessionItem {
		return monitor.SessionItem{ActivityAt: time.Date(2026, 10, d, h, 0, 0, 0, time.Local)}
	}
	days := byDay([]monitor.SessionItem{at(10, 0), at(9, 23), at(9, 0), at(8, 23)}, now)
	for i, want := range []int{1, 2, 1} {
		if got := len(days[i].sessions); got != want {
			t.Errorf("%s has %d sessions, want %d", days[i].label, got, want)
		}
	}
}

// SPARK_SCREENS=dir go test -run TestScreens . renders each screen to an image without a
// window, for comparing against the design
// piWaiting adds a pi project whose session is waiting on a question, in Herdr when a pane
// is named: what its waiting card can and can't do hangs off that
func piWaiting(a *app, pane string) {
	a.groups = append(a.groups, monitor.ProjectGroup{ProjectName: "rag", Sessions: []monitor.SessionItem{
		{
			ID: "01a13ed4-da48-74ac-b607-dc2344b528b0", Source: monitor.SourcePi,
			Title: "Migrate the router to the new gateway", State: monitor.Waiting,
			WaitReason: monitor.WaitQuestion, Pane: pane,
			ActivityAt: time.Now().Add(-2 * time.Minute), IsToday: true,
		},
		{ID: "01a13f06-5941-7138-a5d7-056c36fd2894", Source: monitor.SourcePi, Title: "Backfill events for September",
			State: monitor.Unread, ActivityAt: time.Now().Add(-38 * time.Minute), IsToday: true},
		{ID: "01a13919-4037-72b8-8b7e-c47d3459020f", Source: monitor.SourcePi, Title: "Trace the slow ranking query",
			State: monitor.Completed, ActivityAt: time.Now().Add(-11 * time.Hour), IsToday: true},
	}})
}

func TestScreens(t *testing.T) {
	dir := os.Getenv("SPARK_SCREENS")
	if dir == "" {
		t.Skip("set SPARK_SCREENS to a directory to render the screens")
	}
	shots := []struct {
		name string
		w, h int
		dark bool
		set  func(a *app)
	}{
		{"today", 360, 640, false, func(a *app) {}},
		{"today-dark", 360, 640, true, func(a *app) {}},
		{"all", 360, 640, false, func(a *app) { a.filter = filterAll }},
		{"project", 360, 640, false, func(a *app) { a.project = "search-api" }},
		{"project-pi", 360, 640, false, func(a *app) { piWaiting(a, "w4:pC"); a.project = "rag" }},
		{"project-pi-nowhere", 360, 640, false, func(a *app) { piWaiting(a, ""); a.project = "rag" }},
		{"project-pi-narrow", 300, 640, false, func(a *app) { piWaiting(a, "w4:pC"); a.project = "rag" }},
		{"empty", 360, 640, false, func(a *app) { a.groups = nil }},
		{"narrow", 280, 600, false, func(a *app) {}},
		{"narrow-min", minWidth, 600, false, func(a *app) {}},
		{"narrow-all", minWidth, 600, false, func(a *app) { a.filter = filterAll }},
	}
	for _, s := range shots {
		a := demoApp()
		s.set(a)
		tt := ui.NewTester(a.view, s.w, s.h)
		tt.SetScale(2)
		tt.SetDark(s.dark)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		tt.Frame()
		writePNG(t, filepath.Join(dir, s.name+".png"), tt.Image())
	}
}
