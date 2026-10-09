package monitor

import (
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// 工具调用挂起且 transcript 无更新超过此时长，视为在等用户授权
	toolPendingIdleMs = 45_000
	// transcript 尚未生成时，刚提交的会话仍按运行中处理
	freshSessionMs = 60_000
)

type rawSessionJSON struct {
	SessionID      string `json:"sessionId"`
	CliSessionID   string `json:"cliSessionId"`
	Title          string `json:"title"`
	OriginCwd      string `json:"originCwd"`
	Cwd            string `json:"cwd"`
	WorktreePath   string `json:"worktreePath"`
	IsArchived     bool   `json:"isArchived"`
	LastActivityAt int64  `json:"lastActivityAt"`
	LastFocusedAt  int64  `json:"lastFocusedAt"`
	CreatedAt      int64  `json:"createdAt"`
}

// LiveProcess 是与某个桌面端会话对应的存活 Claude 进程
type LiveProcess struct {
	PID         int
	HasChildren bool
	CPUUsage    float32
}

// 携带同一个宿主会话 ID 的候选进程：核心进程派生的短命子进程会继承这个环境变量
type processCandidate struct {
	pid         int
	parent      int
	startTime   int64
	hasChildren bool
	cpuUsage    float32
}

type Scanner struct {
	SessionsDir  string
	ProjectsRoot string
	HooksDir     string
	cpu          *cpuSampler
}

func NewScanner() *Scanner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return &Scanner{
		SessionsDir:  filepath.Join(home, "Library", "Application Support", "Claude", "claude-code-sessions"),
		ProjectsRoot: filepath.Join(home, ".claude", "projects"),
		HooksDir:     HooksDir(home),
		cpu:          newCPUSampler(),
	}
}

// WatchDirs 是需要监听变化的目录；hooks 目录先建好，否则无法被监听
func (s *Scanner) WatchDirs() []string {
	_ = os.MkdirAll(s.HooksDir, 0o755)
	return []string{s.SessionsDir, s.ProjectsRoot, s.HooksDir}
}

// liveProcesses 通过进程环境变量把每个存活的 Claude 核心进程精确归到宿主会话 ID
func (s *Scanner) liveProcesses() map[string]LiveProcess {
	procs := listProcesses()
	hasChildren := make(map[int]bool, len(procs))
	for _, p := range procs {
		hasChildren[p.ppid] = true
	}

	type tagged struct {
		sessionID string
		proc      procInfo
	}
	var found []tagged
	var pids []int
	for _, p := range procs {
		if !strings.EqualFold(p.name, "claude") {
			continue
		}
		id, ok := hostSessionID(p.pid)
		if !ok {
			continue
		}
		found = append(found, tagged{id, p})
		pids = append(pids, p.pid)
	}

	usage := s.cpu.sample(pids, time.Now())
	candidates := make(map[string][]processCandidate)
	for _, f := range found {
		candidates[f.sessionID] = append(candidates[f.sessionID], processCandidate{
			pid:         f.proc.pid,
			parent:      f.proc.ppid,
			startTime:   f.proc.startTime,
			hasChildren: hasChildren[f.proc.pid],
			cpuUsage:    usage[f.proc.pid],
		})
	}
	return pickCoreProcesses(candidates)
}

// pickCoreProcesses：一个会话 ID 可能对应多个进程，只保留核心那个，否则 PID 和忙碌判断会随扫描顺序抖动
func pickCoreProcesses(grouped map[string][]processCandidate) map[string]LiveProcess {
	live := make(map[string]LiveProcess, len(grouped))
	for id, group := range grouped {
		core, ok := pickCore(group)
		if !ok {
			continue
		}
		live[id] = LiveProcess{PID: core.pid, HasChildren: core.hasChildren, CPUUsage: core.cpuUsage}
	}
	return live
}

// pickCore：父进程同在组里的都是继承来的子进程，先排掉；剩下的取启动最早的那个
func pickCore(group []processCandidate) (processCandidate, bool) {
	if len(group) == 0 {
		return processCandidate{}, false
	}
	inGroup := make(map[int]bool, len(group))
	for _, c := range group {
		inGroup[c.pid] = true
	}
	var roots []processCandidate
	for _, c := range group {
		if c.parent == 0 || !inGroup[c.parent] {
			roots = append(roots, c)
		}
	}
	if len(roots) == 0 {
		roots = group
	}
	return slices.MinFunc(roots, func(a, b processCandidate) int {
		return cmp.Or(cmp.Compare(a.startTime, b.startTime), cmp.Compare(a.pid, b.pid))
	}), true
}

type discovered struct {
	project    string
	item       SessionItem
	activityTs int64
}

// Scan 扫描 Claude Desktop 的所有未归档会话
func (s *Scanner) Scan() []ProjectGroup {
	live := s.liveProcesses()
	if _, err := os.Stat(s.SessionsDir); err != nil {
		return nil
	}

	now := time.Now()
	nowMs := now.UnixMilli()
	var found []discovered

	_ = filepath.WalkDir(s.SessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var data rawSessionJSON
		if json.Unmarshal(content, &data) != nil || data.IsArchived {
			return nil
		}

		originCwd := cmp.Or(data.OriginCwd, data.Cwd)
		project := "Unknown"
		if originCwd != "" {
			if base := filepath.Base(originCwd); base != "/" && base != "." {
				project = base
			}
		}
		title := cmp.Or(data.Title, "Untitled Session")
		if project == "Unknown" && title == "Untitled Session" {
			return nil
		}
		sessionID := cmp.Or(data.SessionID, "session")

		var (
			proc     *LiveProcess
			hook     *HookSignal
			activity *TranscriptActivity
		)
		if p, ok := live[sessionID]; ok {
			proc = &p
			if cli := data.CliSessionID; cli != "" {
				if h, ok := ReadHookSignal(s.HooksDir, cli); ok {
					hook = &h
				}
				if path := LocateTranscript(s.ProjectsRoot, cli, data.Cwd, data.WorktreePath, originCwd); path != "" {
					if a, ok := ReadActivity(path); ok {
						activity = &a
					}
				}
			}
		}

		state := DetermineSessionState(proc, activity, hook, data.LastActivityAt, data.LastFocusedAt, nowMs)
		activityTs := max(data.LastActivityAt, data.CreatedAt)
		activityAt := timestampTime(activityTs)
		item := SessionItem{
			ID:         sessionID,
			Title:      title,
			State:      state,
			WorkingDir: originCwd,
			ActivityAt: activityAt,
			IsToday:    state == Running || state == Waiting || isSameLocalDay(activityAt, now),
		}
		if state == Waiting && activity != nil && activity.LastEvent == AwaitingUser {
			item.WaitReason = WaitQuestion
		}
		if proc != nil {
			item.PID = proc.PID
		}
		found = append(found, discovered{project, item, activityTs})
		return nil
	})

	return groupByProject(found)
}

// 时间戳可能是毫秒也可能是秒，按量级区分
func timestampTime(ts int64) time.Time {
	switch {
	case ts <= 0:
		return time.Time{}
	case ts > 100_000_000_000:
		return time.UnixMilli(ts)
	default:
		return time.Unix(ts, 0)
	}
}

func isSameLocalDay(t, now time.Time) bool {
	if t.IsZero() {
		return false
	}
	y1, m1, d1 := t.Local().Date()
	y2, m2, d2 := now.Local().Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func groupByProject(found []discovered) []ProjectGroup {
	if len(found) == 0 {
		return nil
	}
	byProject := map[string][]discovered{}
	for _, d := range found {
		byProject[d.project] = append(byProject[d.project], d)
	}

	groups := make([]ProjectGroup, 0, len(byProject))
	for project, items := range byProject {
		// 项目内会话：Running/Waiting 优先，其余按最后活动时间倒序排列
		slices.SortStableFunc(items, func(a, b discovered) int {
			return cmp.Or(
				cmp.Compare(b.item.State.priority(), a.item.State.priority()),
				cmp.Compare(b.activityTs, a.activityTs),
			)
		})
		sessions := make([]SessionItem, len(items))
		for i, d := range items {
			sessions[i] = d.item
		}
		groups = append(groups, ProjectGroup{ProjectName: project, Sessions: sessions})
	}

	// 有进行中/待处理任务的项目置顶，其余按名字排序
	groupPriority := func(g ProjectGroup) int {
		p := 0
		for _, s := range g.Sessions {
			p = max(p, s.State.priority())
		}
		return p
	}
	slices.SortFunc(groups, func(a, b ProjectGroup) int {
		return cmp.Or(
			cmp.Compare(groupPriority(b), groupPriority(a)),
			cmp.Compare(strings.ToLower(a.ProjectName), strings.ToLower(b.ProjectName)),
			cmp.Compare(a.ProjectName, b.ProjectName),
		)
	})
	return groups
}

// DetermineSessionState 状态判决：
//   - Waiting: hook 报告正在等授权且 transcript 之后没有新动静；工具在向用户提问；
//     没装 hook 时，工具调用挂起且进程、transcript 都长时间无动静也视为等授权
//   - Running: 其余存活会话；transcript 尚未生成时按提交时间兜底
//   - Unread / Completed: 没有存活进程，或 transcript 显示回合已结束。再按桌面端有没有
//     看过分成两档——lastActivityAt 比 lastFocusedAt 新就是还没看过，和侧栏那颗黄点同一套判断
func DetermineSessionState(proc *LiveProcess, activity *TranscriptActivity, hook *HookSignal, lastActivityAt, lastFocusedAt, nowMs int64) TaskState {
	finished := Completed
	if lastActivityAt > lastFocusedAt {
		finished = Unread
	}
	if proc == nil {
		return finished
	}
	var transcriptModifiedMs int64
	if activity != nil {
		transcriptModifiedMs = activity.LastModifiedMs
	}
	if hook != nil && hook.Kind == HookPermissionPrompt && hook.AtMs >= transcriptModifiedMs {
		return Waiting
	}
	if activity == nil {
		if nowMs-lastActivityAt < freshSessionMs {
			return Running
		}
		return finished
	}

	switch activity.LastEvent {
	case Ended:
		return finished
	case Generating:
		return Running
	case AwaitingUser:
		return Waiting
	}
	// hook 还在报工具执行就不用超时去猜；报别的（比如等了一分钟的 idle 通知）
	// 说明它已经盖掉了更早的授权事件，这时候仍然要靠超时兜底
	if hook != nil && hook.Kind == HookBusy {
		return Running
	}
	idleMs := nowMs - activity.LastModifiedMs
	if proc.HasChildren || proc.CPUUsage > 1.0 || idleMs < toolPendingIdleMs {
		return Running
	}
	return Waiting
}
