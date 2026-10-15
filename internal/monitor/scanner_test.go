package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

const now int64 = 1_000_000_000

func idleProcess() *LiveProcess { return &LiveProcess{PID: 1} }

func activity(event TurnEvent, modifiedAgoMs int64) *TranscriptActivity {
	return &TranscriptActivity{LastEvent: event, LastModifiedMs: now - modifiedAgoMs}
}

func hook(kind HookKind, atAgoMs int64) *HookSignal {
	return &HookSignal{Kind: kind, AtMs: now - atAgoMs}
}

func expectState(t *testing.T, got, want TaskState) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNoProcessIsCompleted(t *testing.T) {
	expectState(t, DetermineSessionState(nil, activity(Generating, 0), nil, now, now, now), Completed)
}

func TestTurnEndedIsCompletedRegardlessOfProcessActivity(t *testing.T) {
	busy := &LiveProcess{PID: 1, HasChildren: true, CPUUsage: 50}
	expectState(t, DetermineSessionState(busy, activity(Ended, 0), nil, now, now, now), Completed)
}

func TestGeneratingIsRunningEvenWhenProcessLooksIdle(t *testing.T) {
	expectState(t, DetermineSessionState(idleProcess(), activity(Generating, 600_000), nil, now-600_000, now-600_000, now), Running)
}

func TestUserFacingToolIsWaiting(t *testing.T) {
	expectState(t, DetermineSessionState(idleProcess(), activity(AwaitingUser, 0), nil, now, now, now), Waiting)
}

func TestPendingToolIsRunningWhileBusyAndWaitingWhenStale(t *testing.T) {
	expectState(t, DetermineSessionState(idleProcess(), activity(ToolPending, 5_000), nil, now, now, now), Running)
	stale := activity(ToolPending, 120_000)
	expectState(t, DetermineSessionState(idleProcess(), stale, nil, now, now, now), Waiting)
	withChild := &LiveProcess{PID: 1, HasChildren: true}
	expectState(t, DetermineSessionState(withChild, stale, nil, now, now, now), Running)
}

func TestMissingTranscriptFallsBackToSubmitTime(t *testing.T) {
	expectState(t, DetermineSessionState(idleProcess(), nil, nil, now-10_000, now-10_000, now), Running)
	expectState(t, DetermineSessionState(idleProcess(), nil, nil, now-120_000, now-120_000, now), Completed)
}

func TestHookPermissionPromptWinsUntilTranscriptMovesOn(t *testing.T) {
	asked := hook(HookPermissionPrompt, 5_000)
	pending := activity(ToolPending, 10_000)
	expectState(t, DetermineSessionState(idleProcess(), pending, asked, now, now, now), Waiting)

	// After the user approves, the tool result lands in the transcript, newer than the
	// hook event
	expectState(t, DetermineSessionState(idleProcess(), activity(Generating, 1_000), asked, now, now, now), Running)

	// A hook does nothing without a process
	expectState(t, DetermineSessionState(nil, pending, asked, now, now, now), Completed)
}

func TestHookedSessionDoesNotGuessWaitingFromIdleTool(t *testing.T) {
	expectState(t, DetermineSessionState(idleProcess(), activity(ToolPending, 120_000), hook(HookBusy, 100_000), now, now, now), Running)
}

func TestIdleNotificationDoesNotMaskAPermissionWait(t *testing.T) {
	// A hook file keeps only a session's last event, so an idle notification arriving while
	// waiting for approval replaces the PermissionRequest
	expectState(t, DetermineSessionState(idleProcess(), activity(ToolPending, 120_000), hook(HookOther, 5_000), now, now, now), Waiting)
}

func TestFinishedTurnIsUnreadUntilTheDesktopFocusesIt(t *testing.T) {
	ended := activity(Ended, 10_000)
	focusedBefore := now - 30_000
	expectState(t, DetermineSessionState(idleProcess(), ended, nil, now, focusedBefore, now), Unread)
	expectState(t, DetermineSessionState(idleProcess(), ended, nil, now, now, now), Completed)
	// A dead process still separates unread from read; that's how the desktop sidebar marks it
	expectState(t, DetermineSessionState(nil, ended, nil, now, focusedBefore, now), Unread)
	// A session never focused (a missing lastFocusedAt reads as 0) counts as unseen
	expectState(t, DetermineSessionState(nil, ended, nil, now, 0, now), Unread)
}

func TestSessionJSONReadsTheDesktopFocusTimestamp(t *testing.T) {
	var data rawSessionJSON
	if err := json.Unmarshal([]byte(`{"sessionId":"local_x","title":"t","cwd":"/tmp","lastActivityAt":2,"lastFocusedAt":1}`), &data); err != nil {
		t.Fatal(err)
	}
	if data.LastActivityAt != 2 || data.LastFocusedAt != 1 {
		t.Errorf("got %+v", data)
	}
}

func candidate(pid, parent int, start int64) processCandidate {
	return processCandidate{pid: pid, parent: parent, startTime: start}
}

func TestInheritedChildrenDoNotReplaceTheCoreProcess(t *testing.T) {
	// 91963 is the long-lived core process; the rest are short-lived children it spawned
	core := candidate(91963, 600, 100)
	core.hasChildren, core.cpuUsage = true, 12
	live := pickCoreProcesses(map[string][]processCandidate{
		"local_a": {candidate(15742, 91963, 900), core, candidate(16262, 15742, 950)},
	})
	got, ok := live["local_a"]
	if !ok || got.PID != 91963 || !got.HasChildren || got.CPUUsage != 12 {
		t.Errorf("got %+v", got)
	}
}

func TestSessionsStayIndependent(t *testing.T) {
	live := pickCoreProcesses(map[string][]processCandidate{
		"local_a": {candidate(100, 0, 10)},
		"local_b": {candidate(200, 0, 20), candidate(201, 200, 30)},
	})
	if len(live) != 2 || live["local_a"].PID != 100 || live["local_b"].PID != 200 {
		t.Errorf("got %+v", live)
	}
}

func TestOldestWinsWhenTheChildHangsOffAnIntermediateProcess(t *testing.T) {
	// Observed: a short-lived process hangs off an intermediate one, so its parent isn't in
	// the group and only start time tells them apart
	live := pickCoreProcesses(map[string][]processCandidate{
		"local_a": {candidate(29160, 29159, 1_789_892_447), candidate(19140, 19139, 1_789_892_121)},
	})
	if live["local_a"].PID != 19140 {
		t.Errorf("got %+v", live)
	}
}

func TestPickIsStableRegardlessOfScanOrder(t *testing.T) {
	group := []processCandidate{candidate(15792, 91963, 900), candidate(91963, 600, 100), candidate(16048, 91963, 910)}
	first := pickCoreProcesses(map[string][]processCandidate{"a": group})["a"].PID
	reversed := []processCandidate{group[2], group[1], group[0]}
	second := pickCoreProcesses(map[string][]processCandidate{"a": reversed})["a"].PID
	if first != 91963 || second != 91963 {
		t.Errorf("first %d second %d", first, second)
	}
}

func TestGroupsPutActiveProjectsFirstAndSortSessions(t *testing.T) {
	item := func(id string, state TaskState) SessionItem { return SessionItem{ID: id, State: state} }
	groups := groupByProject([]discovered{
		{"beta", item("b1", Completed), 50},
		{"Alpha", item("a1", Completed), 10},
		{"Alpha", item("a2", Unread), 5},
		{"gamma", item("g1", Completed), 1},
		{"gamma", item("g2", Running), 0},
	})
	var names []string
	for _, g := range groups {
		names = append(names, g.ProjectName)
	}
	want := []string{"gamma", "Alpha", "beta"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("project order %v, want %v", names, want)
		}
	}
	if groups[0].Sessions[0].ID != "g2" || groups[1].Sessions[0].ID != "a2" {
		t.Errorf("session order %+v", groups)
	}
}

func TestSplitWaitingLiftsWaitingSessionsOut(t *testing.T) {
	item := func(id string, state TaskState) SessionItem { return SessionItem{ID: id, State: state} }
	waiting, rest := SplitWaiting([]ProjectGroup{
		{ProjectName: "acme", Sessions: []SessionItem{item("a1", Running), item("a2", Waiting)}},
		{ProjectName: "docs", Sessions: []SessionItem{item("d1", Waiting)}},
	})
	if len(waiting) != 2 || waiting[0].ID != "a2" || waiting[0].ProjectName != "acme" || waiting[1].ID != "d1" {
		t.Errorf("waiting %+v", waiting)
	}
	if len(rest) != 1 || rest[0].ProjectName != "acme" || len(rest[0].Sessions) != 1 || rest[0].Sessions[0].ID != "a1" {
		t.Errorf("rest %+v", rest)
	}
}

// SPARK_REAL=1 go test -run TestScanRealSessions -v prints this machine's real sessions, to
// check what the scan finds
func TestScanRealSessions(t *testing.T) {
	if os.Getenv("SPARK_REAL") == "" {
		t.Skip("set SPARK_REAL=1 to scan this machine")
	}
	groups := NewScanner().ScanAll()
	for _, g := range groups {
		t.Logf("Project: %s (%d sessions)", g.ProjectName, len(g.Sessions))
		for _, s := range g.Sessions {
			// pi is the one source whose state depends on where it runs, so say what it
			// found: a pane means clicking the row can go there
			extra := ""
			if s.Source == SourcePi {
				extra = fmt.Sprintf(" pane=%q live=%v", s.Pane, !s.NoSignal)
			}
			t.Logf("  - [%v] %s (%v, PID: %d, today=%v%s)", s.State, s.Title, s.Source, s.PID, s.IsToday, extra)
		}
	}
	if len(groups) == 0 {
		t.Error("no sessions found")
	}
}
