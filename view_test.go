package main

import (
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
	for _, s := range []string{"Every agent.", "5 projects · 11 sessions", "Needs you", "All caught up", "Today", "Yesterday", "Earlier", "Migrate to the new auth client"} {
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
	if err := tt.Click("Install"); err != nil {
		t.Fatal(err)
	}
	if got := tt.OpenedURLs(); len(got) != 1 || got[0] != hookGuideURL {
		t.Fatalf("Install should open the hook guide, opened %v", got)
	}
	if err := tt.Click("Show all sessions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.filter != filterAll {
		t.Fatal("Show all sessions should switch to All")
	}

	a.hookInstalled = true
	a.filter = filterToday
	tt.Frame()
	if tt.HasText("Hear about prompts right away") {
		t.Fatal("no need to offer a hook that is installed")
	}
}

func TestNarrowWindowStacksTheCards(t *testing.T) {
	a := demoApp()
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
	if err := tt.Click("docs-site, 1 to read"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.project != "docs-site" {
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

// 宽但矮的窗口保留切换按钮和卡片，内容放不下就滚动
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

// SPARK_SCREENS=dir go test -run TestScreens . 把各个界面无窗口渲染成图片，方便对照设计稿
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
