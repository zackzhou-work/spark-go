package monitor

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	// Only scan rollouts this recent; older threads never change and aren't worth showing
	codexScanWindow = 7 * 24 * time.Hour
	// With no rollout activity and no hook for this long, treat the thread as no longer running
	codexLiveWindow = 30 * time.Minute
	// Read this much from each end of a rollout; the tool output in between doesn't matter
	codexChunkBytes = 64 * 1024
	// Read at most this far looking for the title: injected AGENTS.md can be long, but it ends
	codexHeadBytes = 2 * 1024 * 1024
)

// CodexSignal is a Codex hook event on disk, carrying a few more locating fields than HookSignal
type CodexSignal struct {
	HookSignal
	ThreadID string
	Cwd      string
	Tool     string
}

// CodexRollout is the part of a Codex thread's rollout log we care about
type CodexRollout struct {
	// ThreadID is the thread's identity. Resuming a thread writes another rollout for it,
	// so several rollouts can carry the same id
	ThreadID       string
	Cwd            string
	Title          string
	LastEvent      TurnEvent
	LastModifiedMs int64
	// Subagent is true for a sub-agent thread rather than one a user started
	Subagent bool
}

func (r CodexRollout) activity() TranscriptActivity {
	return TranscriptActivity{LastEvent: r.LastEvent, LastModifiedMs: r.LastModifiedMs}
}

func CodexHome(home string) string {
	return filepath.Join(home, ".codex")
}

// CodexSessionsRoot is the rollout root, laid out as YYYY/MM/DD
func CodexSessionsRoot(home string) string {
	return filepath.Join(CodexHome(home), "sessions")
}

// CodexHookDir is where spark-codex-hook.sh writes events, one <threadId>.json each
func CodexHookDir(home string) string {
	return filepath.Join(home, ".config", "spark", "codex-hooks")
}

// CodexHooksPath is the hook config Codex CLI reads
func CodexHooksPath(home string) string {
	return filepath.Join(CodexHome(home), "hooks.json")
}

// CodexGlobalStatePath records which threads the desktop app still considers unread. It
// sits in the Codex home, the way the readers below take it
func CodexGlobalStatePath(codexHome string) string {
	return filepath.Join(codexHome, ".codex-global-state.json")
}

// CodexThreadIndexPath is the desktop app's thread index, which holds the name it shows for
// each thread
func CodexThreadIndexPath(codexHome string) string {
	return filepath.Join(codexHome, "session_index.jsonl")
}

func ReadCodexSignal(dir, threadID string) (CodexSignal, bool) {
	path := filepath.Join(dir, threadID+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		return CodexSignal{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return CodexSignal{}, false
	}
	kind, ok := classifyCodexHook(content)
	if !ok {
		return CodexSignal{}, false
	}
	var p struct {
		ThreadID string `json:"thread_id"`
		Session  string `json:"session_id"`
		Cwd      string `json:"cwd"`
		Tool     string `json:"tool_name"`
	}
	_ = json.Unmarshal(content, &p)
	return CodexSignal{
		HookSignal: HookSignal{Kind: kind, AtMs: info.ModTime().UnixMilli()},
		ThreadID:   cmp.Or(p.ThreadID, p.Session),
		Cwd:        p.Cwd,
		Tool:       p.Tool,
	}, true
}

func classifyCodexHook(payload []byte) (HookKind, bool) {
	var p struct {
		Event string `json:"hook_event_name"`
	}
	if json.Unmarshal(payload, &p) != nil || p.Event == "" {
		return 0, false
	}
	switch p.Event {
	case "PermissionRequest":
		return HookPermissionPrompt, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "SessionStart", "SubagentStart", "SubagentStop":
		return HookBusy, true
	case "Stop", "SessionEnd":
		return HookStopped, true
	}
	return HookOther, true
}

// CodexHooksInstalled reports whether ~/.codex/hooks.json names spark's hook
func CodexHooksInstalled(home string) bool {
	content, err := os.ReadFile(CodexHooksPath(home))
	return err == nil && bytes.Contains(content, []byte("spark-codex-hook.sh"))
}

// LocateCodexRollout finds a rollout log by thread ID. Files are named
// rollout-<ISO time>-<threadId>.jsonl under date directories, so search newest first
func LocateCodexRollout(root, threadID string) string {
	if root == "" || threadID == "" {
		return ""
	}
	var found string
	days := recentDayDirs(root, 14)
	for _, day := range days {
		entries, err := os.ReadDir(day)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "rollout-") || filepath.Ext(name) != ".jsonl" {
				continue
			}
			if strings.Contains(name, threadID) {
				found = filepath.Join(day, name)
				break
			}
		}
		if found != "" {
			break
		}
	}
	return found
}

// recentDayDirs returns the YYYY/MM/DD directories under sessions, newest first
func recentDayDirs(root string, maxDays int) []string {
	years, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, y := range years {
		if !y.IsDir() {
			continue
		}
		months, err := os.ReadDir(filepath.Join(root, y.Name()))
		if err != nil {
			continue
		}
		for _, m := range months {
			if !m.IsDir() {
				continue
			}
			days, err := os.ReadDir(filepath.Join(root, y.Name(), m.Name()))
			if err != nil {
				continue
			}
			for _, d := range days {
				if d.IsDir() {
					dirs = append(dirs, filepath.Join(root, y.Name(), m.Name(), d.Name()))
				}
			}
		}
	}
	slices.Sort(dirs)
	slices.Reverse(dirs)
	if len(dirs) > maxDays {
		dirs = dirs[:maxDays]
	}
	return dirs
}

// ReadCodexRollout reads both ends of a rollout: the head has the cwd and first user
// message, the tail says where the turn stands
func ReadCodexRollout(path string) (CodexRollout, bool) {
	f, err := os.Open(path)
	if err != nil {
		return CodexRollout{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return CodexRollout{}, false
	}

	var out CodexRollout
	out.LastModifiedMs = info.ModTime().UnixMilli()

	out.readHeader(f)

	start := max(info.Size()-codexChunkBytes, 0)
	tail := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(tail, start); err != nil && err != io.EOF {
		return CodexRollout{}, false
	}
	lines := bytes.Split(tail, []byte("\n"))
	// The first line is incomplete when the tail starts mid-file
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	if event, ok := codexLastEvent(lines); ok {
		out.LastEvent = event
	}
	return out, true
}

// codexRecord decodes only the fields needed to place a rollout and judge turn progress
type codexRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Role      string `json:"role"`
		// A non-empty parent_thread_id means a sub-agent thread, not a user-started session
		ParentThreadID string `json:"parent_thread_id"`
		Content        []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

// readHeader takes the thread's id and cwd from session_meta and the title from the first
// real user message. Injected AGENTS.md and environment context can run to tens of KB, so
// keep reading until the title shows up, but never past codexHeadBytes so a huge file can't
// eat memory. A true subagent means a sub-agent thread the caller should skip
func (r *CodexRollout) readHeader(f *os.File) {
	reader := bufio.NewReaderSize(f, 64*1024)
	read := 0
	metaSeen := false
	for read < codexHeadBytes {
		line, err := reader.ReadBytes('\n')
		read += len(line)
		if len(bytes.TrimSpace(line)) > 0 {
			var rec codexRecord
			if json.Unmarshal(line, &rec) == nil {
				switch rec.Type {
				case "session_meta":
					// A sub-agent rollout carries its own meta first and then replays the
					// parent's, which has no parent_thread_id. Only the first meta says what
					// this file is; a later one can only fill in a missing cwd
					if !metaSeen {
						metaSeen = true
						r.Subagent = rec.Payload.ParentThreadID != ""
						r.ThreadID = cmp.Or(rec.Payload.ID, rec.Payload.SessionID)
					}
					r.Cwd = cmp.Or(r.Cwd, rec.Payload.Cwd)
				case "response_item":
					if r.Title == "" && rec.Payload.Role == "user" {
						r.Title = codexUserText(rec.Payload.Content)
					}
				}
			}
		}
		if r.Cwd != "" && r.Title != "" {
			break
		}
		if err != nil {
			break
		}
	}
}

// codexUserText picks out the real user input. Injected context also arrives with the
// user role, but always as an XML tag or a #-prefixed AGENTS.md preamble
func codexUserText(blocks []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	for _, b := range blocks {
		if b.Type != "input_text" {
			continue
		}
		text := strings.TrimSpace(b.Text)
		if text == "" || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "#") {
			continue
		}
		return titleOf(text)
	}
	return ""
}

// titleOf picks the words of a message that belong in a title. Injected context opens it and
// a pasted image leaves its path behind, and neither names the session
func titleOf(text string) string {
	return stripTrailingPaths(firstLine(stripLeadingPaths(stripLeadingLinks(text))))
}

// stripLeadingLinks drops the markdown links a user message can open with. When a skill is
// used and a file is pasted, the message reads "[$grill-me](/path/SKILL.md) [doc](<doc>)
// body", and those paths have no place in a title
func stripLeadingLinks(text string) string {
	for strings.HasPrefix(text, "[") {
		close := strings.IndexByte(text, ']')
		if close < 0 || close+1 >= len(text) || text[close+1] != '(' {
			break
		}
		end := strings.IndexByte(text[close+2:], ')')
		if end < 0 {
			break
		}
		rest := strings.TrimSpace(text[close+2+end+1:])
		if rest == "" {
			break
		}
		text = rest
	}
	return text
}

// stripLeadingPaths drops the paths a message can open with. A pasted image arrives as its
// file path in plain text rather than as a link, and that path alone can fill a title, so the
// words the user typed never show
func stripLeadingPaths(text string) string {
	for {
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
		end := strings.IndexFunc(text, unicode.IsSpace)
		if end < 0 {
			end = len(text)
		}
		if end == 0 || !isAttachmentPath(text[:end]) {
			return text
		}
		text = text[end:]
	}
}

// isAttachmentPath tells a pasted path from a slash command: "/tdd" is a command the user
// typed, "/var/folders/.../image.png" and "@/Users/me/notes.md" are paths
func isAttachmentPath(token string) bool {
	path := strings.TrimPrefix(token, "@")
	return strings.HasPrefix(path, "/") && strings.Contains(path[1:], "/")
}

// stripTrailingPaths drops an attachment's path from the end of a line, where a paste that
// came after the user's words leaves it
func stripTrailingPaths(text string) string {
	for {
		text = strings.TrimRightFunc(text, unicode.IsSpace)
		start := strings.LastIndexFunc(text, unicode.IsSpace) + 1
		if !isAttachmentPath(text[start:]) {
			return text
		}
		text = text[:start]
	}
}

// codexLastEvent walks backwards for the newest record that says where the turn stands
func codexLastEvent(lines [][]byte) (TurnEvent, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(lines[i])) == 0 {
			continue
		}
		var r codexRecord
		if json.Unmarshal(lines[i], &r) != nil {
			continue
		}
		if event, ok := classifyCodexRecord(r); ok {
			return event, true
		}
	}
	return 0, false
}

func classifyCodexRecord(r codexRecord) (TurnEvent, bool) {
	switch r.Type {
	case "event_msg":
		switch r.Payload.Type {
		case "task_complete", "turn_aborted":
			return Ended, true
		case "task_started", "item_completed", "item_started":
			return Generating, true
		}
	case "response_item":
		switch r.Payload.Type {
		case "custom_tool_call", "function_call", "local_shell_call":
			return ToolPending, true
		case "custom_tool_call_output", "function_call_output":
			return Generating, true
		case "message":
			if r.Payload.Role == "assistant" {
				return Generating, true
			}
		}
	}
	return 0, false
}

// firstLine flattens multi-line input into one line for use as a title
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	const maxRunes = 120
	runes := []rune(text)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return text
}

// CodexThreadNames reads the desktop app's thread index. It holds the name the app shows
// for each thread, which is the title the user recognizes; the index appends a line per
// rename, so the last one for an id wins
func CodexThreadNames(codexHome string) map[string]string {
	content, err := os.ReadFile(CodexThreadIndexPath(codexHome))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(content), "\n") {
		var entry struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil || entry.ID == "" || entry.Name == "" {
			continue
		}
		out[entry.ID] = entry.Name
	}
	return out
}

// UnreadCodexThreads reads the desktop app's unread set; it plays the same role as
// lastFocusedAt does for Claude
func UnreadCodexThreads(codexHome string) map[string]bool {
	content, err := os.ReadFile(CodexGlobalStatePath(codexHome))
	if err != nil {
		return nil
	}
	var state struct {
		ReadState struct {
			UnreadByIdentity map[string]map[string][]string `json:"unreadByIdentity"`
		} `json:"electron-thread-read-state-v1"`
	}
	if json.Unmarshal(content, &state) != nil {
		return nil
	}
	out := map[string]bool{}
	for _, byHost := range state.ReadState.UnreadByIdentity {
		for _, ids := range byHost {
			for _, id := range ids {
				out[id] = true
			}
		}
	}
	return out
}

// anyCodexProcess reports whether any Codex CLI or app-server is alive. Threads aren't
// separate processes the way claude is, so there's no per-session attribution and the
// best we can do is ask whether Codex is running at all
func anyCodexProcess() bool {
	for _, p := range listProcesses() {
		if strings.EqualFold(p.name, "codex") {
			return true
		}
	}
	return false
}

// codexThread gathers every rollout of one thread. Resuming a thread writes another
// rollout instead of rewriting the old one, so a thread can own several files
type codexThread struct {
	// latest is the rollout of the segment the thread is running in now
	latest CodexRollout
	// title comes from the oldest segment that has one, since a resumed segment opens
	// mid-thread
	title   string
	titleMs int64
}

func (t *codexThread) add(r CodexRollout) {
	if r.LastModifiedMs >= t.latest.LastModifiedMs {
		latest := r
		latest.Cwd = cmp.Or(r.Cwd, t.latest.Cwd)
		t.latest = latest
	}
	if r.Title != "" && (t.title == "" || r.LastModifiedMs < t.titleMs) {
		t.title, t.titleMs = r.Title, r.LastModifiedMs
	}
}

// scanCodex turns each recent thread into a session
func (s *Scanner) scanCodex(now time.Time) []discovered {
	root := s.CodexRoot
	if root == "" {
		return nil
	}
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	alive := anyCodexProcess()
	unread := UnreadCodexThreads(s.CodexHome)
	names := CodexThreadNames(s.CodexHome)
	nowMs := now.UnixMilli()
	cutoff := now.Add(-codexScanWindow)

	threads := map[string]*codexThread{}
	for _, day := range recentDayDirs(root, 14) {
		entries, err := os.ReadDir(day)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "rollout-") || filepath.Ext(name) != ".jsonl" {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			rollout, ok := ReadCodexRollout(filepath.Join(day, name))
			if !ok {
				continue
			}
			// A sub-agent thread has no terminal and no user watching it, so it isn't a session
			if rollout.Subagent {
				continue
			}
			// The file name is only a fallback: a resumed thread names its rollout after both
			// the thread and the segment, and the meta keeps the thread id alone
			threadID := cmp.Or(rollout.ThreadID, threadIDFromRollout(name))
			if threadID == "" {
				continue
			}
			if threads[threadID] == nil {
				threads[threadID] = &codexThread{}
			}
			threads[threadID].add(rollout)
		}
	}

	ids := make([]string, 0, len(threads))
	for id := range threads {
		ids = append(ids, id)
	}
	// Map order is random, and rows that tie on activity must not swap places between scans
	slices.Sort(ids)

	found := make([]discovered, 0, len(ids))
	for _, threadID := range ids {
		thread := threads[threadID]
		rollout := thread.latest
		var signal *CodexSignal
		if sig, ok := ReadCodexSignal(s.CodexHookDir, threadID); ok {
			signal = &sig
		}
		cwd := cmp.Or(rollout.Cwd, signalCwd(signal))
		title := cmp.Or(names[threadID], thread.title, "Untitled Session")

		state := codexState(rollout, signal, unread[threadID], alive, nowMs)
		activityAt := timestampTime(rollout.LastModifiedMs)
		item := SessionItem{
			ID:         threadID,
			Source:     SourceCodex,
			Title:      title,
			State:      state,
			WorkingDir: cwd,
			ActivityAt: activityAt,
			IsToday:    state == Running || state == Waiting || isSameLocalDay(activityAt, now),
		}
		if state == Waiting {
			item.WaitReason = WaitPermission
		}
		found = append(found, discovered{projectName(cwd, title), item, rollout.LastModifiedMs})
	}
	return found
}

func signalCwd(s *CodexSignal) string {
	if s == nil {
		return ""
	}
	return s.Cwd
}

// threadIDFromRollout pulls the thread ID out of rollout-<ISO time>-<threadId>.jsonl,
// dropping the _<segmentId> a resumed thread appends. The timestamp contains hyphens too,
// so slice from the end by UUID segment count
func threadIDFromRollout(name string) string {
	base := strings.TrimSuffix(strings.TrimPrefix(name, "rollout-"), ".jsonl")
	base, _, _ = strings.Cut(base, "_")
	parts := strings.Split(base, "-")
	// The timestamp is 2026-10-12T17-20-37, five segments; a UUID is five segments too
	if len(parts) < 10 {
		return ""
	}
	return strings.Join(parts[len(parts)-5:], "-")
}

// codexState decides a Codex thread's state. A hook newer than the rollout wins, because
// events like a running tool or a just-finished turn haven't reached the rollout yet
func codexState(r CodexRollout, sig *CodexSignal, unread, alive bool, nowMs int64) TaskState {
	finished := Completed
	if unread {
		finished = Unread
	}
	if !alive {
		return finished
	}
	rolloutMs := r.LastModifiedMs
	if sig != nil && sig.AtMs >= rolloutMs {
		switch sig.Kind {
		case HookPermissionPrompt:
			return Waiting
		case HookStopped:
			return finished
		case HookBusy:
			return Running
		}
	}
	switch r.LastEvent {
	case Ended:
		return finished
	case AwaitingUser:
		return Waiting
	case ToolPending:
		if nowMs-rolloutMs < toolPendingIdleMs {
			return Running
		}
		return Waiting
	default:
		// Still generating but silent for a long time, with no hook either: the thread is
		// most likely gone
		if nowMs-rolloutMs > codexLiveWindow.Milliseconds() &&
			(sig == nil || nowMs-sig.AtMs > codexLiveWindow.Milliseconds()) {
			return finished
		}
		return Running
	}
}

// codexWatchDirs are the Codex-side directories to watch
func (s *Scanner) codexWatchDirs() []string {
	_ = os.MkdirAll(s.CodexHookDir, 0o755)
	var dirs []string
	if _, err := os.Stat(s.CodexRoot); err == nil {
		dirs = append(dirs, s.CodexRoot)
	}
	return append(dirs, s.CodexHookDir)
}

// projectName uses the cwd's directory name, falling back to the title
func projectName(cwd, title string) string {
	if cwd != "" {
		if base := filepath.Base(cwd); base != "/" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	if title != "" {
		return title
	}
	return "Unknown"
}
