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
	// A tool call left pending with no transcript update for this long counts as waiting
	// on the user
	toolPendingIdleMs = 45_000
	// Before a transcript exists, a just-submitted session still counts as running
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

// LiveProcess is a live claude process belonging to one desktop session
type LiveProcess struct {
	PID         int
	HasChildren bool
	CPUUsage    float32
}

// Candidate processes sharing one host session ID: short-lived children of the core
// process inherit this environment variable
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
	CodexRoot    string
	CodexHome    string
	CodexHookDir string
	// PiRoot holds pi's session files, PiReceiptDir what spark's pi extension reports, and
	// HerdrSocket the pane state Herdr keeps for the pi sessions it hosts
	PiRoot        string
	PiReceiptDir  string
	HerdrSocket   string
	cpu           *cpuSampler
	piSeen        *PiSeen
	piCache       map[string]piCached
	herdrErrAt    time.Time
	receiptPruned time.Time
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
		CodexRoot:    CodexSessionsRoot(home),
		CodexHome:    CodexHome(home),
		CodexHookDir: CodexHookDir(home),
		PiRoot:       PiSessionsRoot(home),
		PiReceiptDir: PiReceiptDir(home),
		HerdrSocket:  HerdrSocketPath(home),
		cpu:          newCPUSampler(),
		piSeen:       NewPiSeen(PiSeenPath(home)),
	}
}

// WatchDirs are the directories to watch; the hooks directory is created first, since
// it can't be watched until it exists.
//
// pi's session directory is deliberately left out: watching a directory on macOS means
// watching every file in it, and pi keeps hundreds of session files, so the rescan that
// runs every few seconds is what picks pi up
func (s *Scanner) WatchDirs() []string {
	_ = os.MkdirAll(s.HooksDir, 0o755)
	dirs := []string{s.SessionsDir, s.ProjectsRoot, s.HooksDir}
	return append(dirs, s.codexWatchDirs()...)
}

// liveProcesses attributes each live claude core process to its host session ID through
// the process environment
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

// pickCoreProcesses keeps only the core process per session ID, since one ID can map to
// several processes and the choice would otherwise make PID and busy checks flap between
// scans
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

// pickCore drops candidates whose parent is in the same group, since those are inherited
// children, then takes the earliest-started of what's left
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

// scanClaude scans every unarchived Claude Desktop session
func (s *Scanner) scanClaude() []discovered {
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
			Source:     SourceClaude,
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

	return found
}

// ScanAll merges the Claude, Codex and pi sides into one list
func (s *Scanner) ScanAll() []ProjectGroup {
	now := time.Now()
	found := s.scanClaude()
	found = append(found, s.scanCodex(now)...)
	found = append(found, s.scanPi(now)...)
	// Only a scan can notice these: seen times it just observed, and receipts left behind
	s.piSeen.Save(now)
	s.pruneReceipts(now)
	return groupByProject(found)
}

// pruneReceipts cleans up the extension's directory at most once an hour
func (s *Scanner) pruneReceipts(now time.Time) {
	if now.Sub(s.receiptPruned) < piReceiptPruneEvery {
		return
	}
	s.receiptPruned = now
	prunePiReceipts(s.PiReceiptDir, now)
}

// Timestamps may be milliseconds or seconds; tell them apart by magnitude
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
		// Within a project: Running/Waiting first, the rest by most recent activity
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

	// Projects with running or pending work go first, the rest sort by name
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

// DetermineSessionState decides a session's state:
//   - Waiting: a hook reports a pending approval and the transcript hasn't moved since; a
//     tool is asking the user something; or, with no hook installed, a tool call is pending
//     while the process and transcript have both been quiet for a long time
//   - Running: any other live session; before a transcript exists, fall back to submit time
//   - Unread / Completed: no live process, or the transcript shows the turn ended. Split by
//     whether the desktop app has shown it: lastActivityAt newer than lastFocusedAt means
//     not yet, the same test as the sidebar's yellow dot
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
	// A hook still reporting tool activity means there's nothing to guess from a timeout. A
	// different report (say an idle notification a minute later) has already replaced an
	// earlier approval event, so the timeout fallback still applies
	if hook != nil && hook.Kind == HookBusy {
		return Running
	}
	idleMs := nowMs - activity.LastModifiedMs
	if proc.HasChildren || proc.CPUUsage > 1.0 || idleMs < toolPendingIdleMs {
		return Running
	}
	return Waiting
}
