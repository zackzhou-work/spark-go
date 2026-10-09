package monitor

import (
	"encoding/json"
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

	// 用户授权后工具结果写入 transcript，比 hook 事件更新
	expectState(t, DetermineSessionState(idleProcess(), activity(Generating, 1_000), asked, now, now, now), Running)

	// 没有进程时 hook 不起作用
	expectState(t, DetermineSessionState(nil, pending, asked, now, now, now), Completed)
}

func TestHookedSessionDoesNotGuessWaitingFromIdleTool(t *testing.T) {
	expectState(t, DetermineSessionState(idleProcess(), activity(ToolPending, 120_000), hook(HookBusy, 100_000), now, now, now), Running)
}

func TestIdleNotificationDoesNotMaskAPermissionWait(t *testing.T) {
	// hook 文件一个会话只留最后一条事件，等授权时来的 idle 通知会把 PermissionRequest 盖掉
	expectState(t, DetermineSessionState(idleProcess(), activity(ToolPending, 120_000), hook(HookOther, 5_000), now, now, now), Waiting)
}

func TestFinishedTurnIsUnreadUntilTheDesktopFocusesIt(t *testing.T) {
	ended := activity(Ended, 10_000)
	focusedBefore := now - 30_000
	expectState(t, DetermineSessionState(idleProcess(), ended, nil, now, focusedBefore, now), Unread)
	expectState(t, DetermineSessionState(idleProcess(), ended, nil, now, now, now), Completed)
	// 进程退了也照样分未读/已读，桌面端侧栏就是这么标的
	expectState(t, DetermineSessionState(nil, ended, nil, now, focusedBefore, now), Unread)
	// 从没聚焦过的会话（lastFocusedAt 缺失按 0 读）算没看过
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
	// 91963 是长期存活的核心进程，其余都是它派生的短命子进程
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
	// 实测：短命进程挂在中间进程下，父进程不在组里，只能靠启动时间区分
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

// SPARK_REAL=1 go test -run TestScanRealSessions -v 打印本机真实会话，用来核对扫描结果
func TestScanRealSessions(t *testing.T) {
	if os.Getenv("SPARK_REAL") == "" {
		t.Skip("set SPARK_REAL=1 to scan this machine")
	}
	groups := NewScanner().Scan()
	for _, g := range groups {
		t.Logf("Project: %s (%d sessions)", g.ProjectName, len(g.Sessions))
		for _, s := range g.Sessions {
			t.Logf("  - [%v] %s (PID: %d, today=%v)", s.State, s.Title, s.PID, s.IsToday)
		}
	}
	if len(groups) == 0 {
		t.Error("no sessions found")
	}
}
