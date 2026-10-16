package monitor

import "time"

// TaskState maps onto the dot in the desktop sidebar, plus Waiting, which the sidebar
// doesn't call out separately
type TaskState int

const (
	// Running: a turn is in progress
	Running TaskState = iota
	// Waiting: stuck on an approval prompt or a question from the model, so nothing
	// moves until you act
	Waiting
	// Unread: the turn ended but the result hasn't been looked at in the desktop app
	Unread
	// Completed: the turn ended and the result has been seen
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

// priority drives sorting: running and pending sessions come first
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

// WaitReason says what a Waiting session is waiting on; only meaningful for Waiting
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

// SessionSource says which agent a session came from, which picks the deep link on click
type SessionSource int

const (
	// SourceClaude is Claude Desktop's Code tab
	SourceClaude SessionSource = iota
	// SourceCodex is a Codex thread, whether from the desktop app, a CLI, or an IDE
	SourceCodex
	// SourcePi is a pi session, whether it runs in Herdr, an IDE or a plain terminal
	SourcePi
)

func (s SessionSource) String() string {
	switch s {
	case SourceCodex:
		return "Codex"
	case SourcePi:
		return "pi"
	}
	return "Claude"
}

type SessionItem struct {
	ID         string
	Source     SessionSource
	Title      string
	State      TaskState
	WaitReason WaitReason
	PID        int
	WorkingDir string
	ActivityAt time.Time
	IsToday    bool
	// Pane is the Herdr pane hosting the session, empty when there is nothing to focus
	Pane string
	// NoSignal marks a pi session nothing is reporting on: spark only knows its turn is
	// over, so the row says so instead of guessing that it is running
	NoSignal bool
}

type ProjectGroup struct {
	ProjectName string
	Sessions    []SessionItem
}

// TodayGroups keeps only sessions with activity today, dropping groups that end up empty
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

// SplitWaiting pulls Waiting sessions out of their projects in their original order,
// dropping any project left empty
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

func (s SessionItem) Jumpable() bool {
	switch s.Source {
	case SourcePi:
		// A pi session has somewhere to go only while Herdr hosts its pane; anywhere else
		// there is nothing to focus
		return s.Pane != ""
	default:
		return true
	}
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
