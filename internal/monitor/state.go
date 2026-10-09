package monitor

import "time"

// TaskState 和桌面端侧栏那颗点一一对应，多出来的 Waiting 是侧栏不单独标的"等授权"
type TaskState int

const (
	// Running 回合进行中
	Running TaskState = iota
	// Waiting 卡在授权框或模型的提问上，你不动它就不会往下走
	Waiting
	// Unread 回合已结束，但产出还没在桌面端看过
	Unread
	// Completed 回合已结束且已经看过
	Completed
)

func (s TaskState) String() string {
	switch s {
	case Running:
		return "Running"
	case Waiting:
		return "Waiting"
	case Unread:
		return "Unread"
	default:
		return "Completed"
	}
}

// priority 决定排序：进行中和待处理的排在前面
func (s TaskState) priority() int {
	switch s {
	case Running:
		return 3
	case Waiting:
		return 2
	case Unread:
		return 1
	default:
		return 0
	}
}

// WaitReason 说明 Waiting 的会话在等什么，只对 Waiting 有意义
type WaitReason int

const (
	WaitPermission WaitReason = iota
	WaitQuestion
)

func (r WaitReason) String() string {
	if r == WaitQuestion {
		return "Question"
	}
	return "Permission prompt"
}

type SessionItem struct {
	ID         string
	Title      string
	State      TaskState
	WaitReason WaitReason
	PID        int
	WorkingDir string
	ActivityAt time.Time
	IsToday    bool
}

type ProjectGroup struct {
	ProjectName string
	Sessions    []SessionItem
}

// TodayGroups 只保留今天有活动的会话，空组整个去掉
func TodayGroups(groups []ProjectGroup) []ProjectGroup {
	var out []ProjectGroup
	for _, g := range groups {
		var today []SessionItem
		for _, s := range g.Sessions {
			if s.IsToday {
				today = append(today, s)
			}
		}
		if len(today) > 0 {
			out = append(out, ProjectGroup{ProjectName: g.ProjectName, Sessions: today})
		}
	}
	return out
}

type PendingSession struct {
	ProjectName string
	SessionItem
}

// SplitWaiting 把 Waiting 的会话从各自项目里拎出来，按原顺序排在一起；拎空的项目整个去掉
func SplitWaiting(groups []ProjectGroup) ([]PendingSession, []ProjectGroup) {
	var waiting []PendingSession
	var rest []ProjectGroup
	for _, g := range groups {
		var kept []SessionItem
		for _, s := range g.Sessions {
			if s.State == Waiting {
				waiting = append(waiting, PendingSession{g.ProjectName, s})
			} else {
				kept = append(kept, s)
			}
		}
		if len(kept) > 0 {
			rest = append(rest, ProjectGroup{ProjectName: g.ProjectName, Sessions: kept})
		}
	}
	return waiting, rest
}

func CountSessions(groups []ProjectGroup) int {
	n := 0
	for _, g := range groups {
		n += len(g.Sessions)
	}
	return n
}

func HasRunning(groups []ProjectGroup) bool {
	for _, g := range groups {
		for _, s := range g.Sessions {
			if s.State == Running {
				return true
			}
		}
	}
	return false
}
