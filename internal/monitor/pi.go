package monitor

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Only sessions touched this recently are read at all; the rest never change and there
	// are hundreds of them
	piScanWindow = 7 * 24 * time.Hour
	// Read this much from each end: the head carries the header and the first user message,
	// the tail says where the turn stands. Tool output in the middle doesn't matter
	piHeadBytes = 128 * 1024
	piTailBytes = 64 * 1024
	// The built-in tool that puts a session in front of the user for an answer
	piAskTool = "ask_user_question"
	// A New badge on a session nothing can report the user's attention for fades after this
	// long, rather than hanging over one they have walked away from
	piUnreadWindow = 30 * time.Minute
	// Session entries are one JSON object per line; a tool output can make one enormous
	piMaxLineBytes = 8 * 1024 * 1024
	// Enough of a record's head for its type, its timestamp and a message's role
	piRecordHeadBytes = 4 * 1024
	piSessionInfo     = `"session_info"`
)

// PiSession is what spark reads out of one pi session file
type PiSession struct {
	ID    string
	Path  string
	Cwd   string
	Title string
	// LastEvent is the turn progress at the end of the file. It only decides what a
	// blocked session is blocked on: whether a turn is live comes from Herdr or the
	// extension, never from a guess
	LastEvent TurnEvent
	// ActivityAt is when anything last happened in the file, metadata included, which is
	// what the row's time and the day it belongs to go by
	ActivityAt int64
	// TurnAt is when the conversation last moved -- a user message, an assistant reply or a
	// tool result. Unread timing uses it, so a rename after a turn can't invent a result
	TurnAt int64
}

// PiSessionsRoot is where pi keeps its sessions, one directory per working directory
func PiSessionsRoot(home string) string {
	return filepath.Join(home, ".pi", "agent", "sessions")
}

type piBlock struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	Name       string `json:"name"`
	ID         string `json:"id"`
	ToolCallID string `json:"toolCallId"`
}

type piMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stopReason"`
}

type piRecord struct {
	Type    string     `json:"type"`
	ID      string     `json:"id"`
	Cwd     string     `json:"cwd"`
	Name    string     `json:"name"`
	Message *piMessage `json:"message"`
}

// ReadPiSession reads both ends of a session file: identity and the first user message from
// the head, the turn's standing and the timestamps from the tail
func ReadPiSession(path string) (PiSession, bool) {
	read, ok := readPiSession(path, 0, "")
	return read.session, ok
}

// piRead is what one read of a session file found. name is the session's own name, empty
// when it has none, and nameAt is the offset the search for it reached
type piRead struct {
	session PiSession
	name    string
	nameAt  int64
}

// readPiSession reads one session file. nameFrom and name are what an earlier read already
// covered for the session's own name, so a file that only grew is searched from where that
// read stopped; pass 0 and "" for a first read
func readPiSession(path string, nameFrom int64, name string) (piRead, bool) {
	f, err := os.Open(path)
	if err != nil {
		return piRead{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return piRead{}, false
	}

	// Activity comes from the newest entry the tail shows, falling back to the file's own
	// time only when nothing in it parses
	out := PiSession{Path: path}

	head, err := readAt(f, 0, min(info.Size(), piHeadBytes))
	if err != nil {
		return piRead{}, false
	}
	headLines := piLines(head, info.Size() > piHeadBytes)
	if len(headLines) == 0 {
		return piRead{}, false
	}
	if header, ok := decodePiRecord(headLines[0]); ok {
		out.ID = header.ID
		out.Cwd = header.Cwd
	}
	// The first user message names the session unless the user gave it one of their own
	for _, line := range headLines[1:] {
		r, ok := decodePiRecord(line)
		if !ok || r.Message == nil || r.Message.Role != "user" {
			continue
		}
		if title := piUserTitle(piMessageText(r.Message.Content)); title != "" {
			out.Title = title
			break
		}
	}

	start := max(info.Size()-piTailBytes, 0)
	tail, err := readAt(f, start, info.Size()-start)
	if err != nil {
		return piRead{}, false
	}
	tailLines := piLines(tail, false)
	if start > 0 && len(tailLines) > 0 {
		// The window can open inside the last record, which is then the only one here and
		// usually the longest: a single assistant reply or tool argument can pass 64KB. Its
		// head says when it happened, and reading that beats pulling megabytes in to decode
		if head, ok := piLastRecordHead(f, start); ok {
			if ts := piRecordTime(head.Timestamp); ts > out.ActivityAt {
				out.ActivityAt = ts
			}
			if head.Type == "message" {
				if ts := piRecordTime(head.Timestamp); ts > out.TurnAt {
					out.TurnAt = ts
				}
			}
		}
		// The first line is incomplete when the tail starts mid-file
		tailLines = tailLines[1:]
	}
	results := piToolResults(tailLines)
	gotEvent := false
	for i := len(tailLines) - 1; i >= 0; i-- {
		r, ok := decodePiRecord(tailLines[i])
		if !ok {
			continue
		}
		if ts := piEntryTime(tailLines[i]); ts > out.ActivityAt {
			out.ActivityAt = ts
		}
		switch r.Type {
		case "message":
			// Unread timing goes by the conversation, not by whatever was written last: a
			// rename, a model switch or an extension's own state say nothing about a result
			if ts := piEntryTime(tailLines[i]); ts > out.TurnAt {
				out.TurnAt = ts
			}
			if !gotEvent && r.Message != nil {
				if event, ok := piTurnEvent(r.Message, results); ok {
					out.LastEvent = event
					gotEvent = true
				}
			}
		}
	}
	found, nameAt := piNameAt(f, nameFrom, info.Size(), name)
	if found != "" {
		out.Title = found
	}
	if out.ActivityAt == 0 {
		out.ActivityAt = info.ModTime().UnixMilli()
	}
	// The name has to come back with the session: the next read resumes from nameAt and can
	// only keep what this one keeps
	return piRead{session: out, name: found, nameAt: nameAt}, true
}

// piNameAt looks for the name the user gave the session, in [from, size) only. /name writes
// a new entry wherever the session happens to be, so the newest one wins; the answer is the
// name found, or fallback when the range holds none. The offset it returns is the end of the
// last complete line, so a line the writer is still producing is looked at again next time
// rather than skipped
func piNameAt(f *os.File, from, size int64, fallback string) (string, int64) {
	if from < 0 || from > size {
		from = 0
	}
	sc := bufio.NewScanner(io.NewSectionReader(f, from, size-from))
	sc.Buffer(make([]byte, 64*1024), piMaxLineBytes)
	name, scanned := fallback, from
	for sc.Scan() {
		line := sc.Bytes()
		end := scanned + int64(len(line)) + 1
		if end > size {
			// The last line has no newline yet: leave it whole for the next read
			break
		}
		scanned = end
		if !bytes.Contains(line, []byte(piSessionInfo)) {
			continue
		}
		// The latest entry decides, and an empty name is how pi records a clear, so this
		// takes the value as written rather than keeping the last name it saw
		if r, ok := decodePiRecord(line); ok && r.Type == "session_info" {
			name = strings.TrimSpace(r.Name)
		}
	}
	return name, scanned
}

// piRecordHead is the start of a record: enough to tell when it happened and whether it was
// part of the conversation
type piRecordHead struct {
	Type      string
	Timestamp string
	Role      string
}

// piLastRecordHead reads the fields at the start of the record that contains offset. Only a
// bounded prefix is read, and the fields that matter are the ones pi writes first
func piLastRecordHead(f *os.File, offset int64) (piRecordHead, bool) {
	start := piLastRecordStart(f, offset)
	head, err := readAt(f, start, piRecordHeadBytes)
	if err != nil || len(head) == 0 {
		return piRecordHead{}, false
	}

	var out piRecordHead
	dec := json.NewDecoder(bytes.NewReader(head))
	depth, expecting := 0, ""
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case string:
			if expecting == "" {
				expecting = t
				continue
			}
			switch {
			case expecting == "type" && depth == 1:
				out.Type = t
			case expecting == "timestamp" && depth == 1:
				out.Timestamp = t
			case expecting == "role" && depth == 2:
				out.Role = t
			}
			expecting = ""
		case json.Delim:
			if t == '{' || t == '[' {
				depth++
			} else {
				depth--
			}
		default:
			expecting = ""
		}
		if out.Type != "" && out.Timestamp != "" && (out.Type != "message" || out.Role != "") {
			return out, true
		}
	}
	return out, out.Timestamp != ""
}

// piLastRecordStart walks back to the newline before the record holding offset, so a record
// larger than the tail window can still be read from its start. A record longer than the
// longest line worth reading gives up rather than walking the whole file
func piLastRecordStart(f *os.File, offset int64) int64 {
	const chunk = 64 * 1024
	limit := max(offset-piMaxLineBytes, 0)
	for end := offset; end > limit; {
		start := max(end-chunk, limit)
		buf, err := readAt(f, start, end-start)
		if err != nil {
			break
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			return start + int64(i) + 1
		}
		end = start
	}
	return limit
}

// piToolResults collects the tool calls that already came back, so a call still hanging at
// the end of the tail counts as pending
func piToolResults(lines [][]byte) map[string]bool {
	results := map[string]bool{}
	for _, line := range lines {
		r, ok := decodePiRecord(line)
		if !ok || r.Message == nil || r.Message.Role != "toolResult" {
			continue
		}
		for _, b := range piBlocks(r.Message.Content) {
			if b.ToolCallID != "" {
				results[b.ToolCallID] = true
			}
		}
	}
	return results
}

// piTurnEvent classifies the newest message in a tail
func piTurnEvent(m *piMessage, results map[string]bool) (TurnEvent, bool) {
	switch m.Role {
	case "user", "toolResult":
		return Generating, true
	case "assistant":
		for _, b := range piBlocks(m.Content) {
			if b.Type != "toolCall" || results[b.ID] {
				continue
			}
			if b.Name == piAskTool {
				return AwaitingUser, true
			}
			return ToolPending, true
		}
		if m.StopReason == "toolUse" {
			// The call's result fell outside the tail window; either way the turn goes on
			return ToolPending, true
		}
		return Ended, true
	}
	return 0, false
}

func piBlocks(raw json.RawMessage) []piBlock {
	var blocks []piBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

// piMessageText reads content, which is either a plain string or an array of blocks
func piMessageText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	for _, b := range piBlocks(raw) {
		if (b.Type == "text" || b.Type == "input_text") && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}

// piUserTitle picks out the user's own words. Injected context arrives with the user role,
// and a skill is prepended to the prompt inside the same message, so anything up to the
// last closing tag is dropped
func piUserTitle(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") {
		if end := strings.LastIndex(text, "</"); end >= 0 {
			if close := strings.IndexByte(text[end:], '>'); close >= 0 {
				text = strings.TrimSpace(text[end+close+1:])
			}
		}
	}
	if text == "" || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "#") {
		return ""
	}
	return firstLine(stripLeadingLink(text))
}

func decodePiRecord(line []byte) (piRecord, bool) {
	var r piRecord
	if json.Unmarshal(line, &r) != nil {
		return piRecord{}, false
	}
	return r, true
}

// piEntryTime is the entry's own timestamp, falling back to the message's, which is a Unix
// time in milliseconds rather than an ISO string
func piEntryTime(line []byte) int64 {
	var r struct {
		Timestamp string `json:"timestamp"`
		Message   *struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &r) != nil {
		return 0
	}
	if ts := piRecordTime(r.Timestamp); ts > 0 {
		return ts
	}
	if r.Message != nil {
		return r.Message.Timestamp
	}
	return 0
}

// piRecordTime parses an entry's ISO timestamp
func piRecordTime(stamp string) int64 {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func readAt(f *os.File, offset, length int64) ([]byte, error) {
	if length <= 0 {
		return nil, nil
	}
	buf := make([]byte, length)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// piLines splits a window into lines; truncated says its last line was cut off
func piLines(window []byte, truncated bool) [][]byte {
	lines := bytes.Split(window, []byte("\n"))
	if truncated && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type piCached struct {
	modifiedMs int64
	size       int64
	// nameAt is how far the search for the session's own name has read, and name what it
	// found; both carry over to the next read of a file that only grew
	nameAt  int64
	name    string
	session PiSession
}

// readPiSession caches what a file said the last time it changed: a scan runs every few
// seconds and most of the files it walks are untouched
func (s *Scanner) readPiSession(path string) (PiSession, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return PiSession{}, false
	}
	if cached, ok := s.piCache[path]; ok && cached.modifiedMs == info.ModTime().UnixMilli() && cached.size == info.Size() {
		return cached.session, true
	}
	// A file that only grew is resumed: a session being written to must not be searched
	// from the top for a name every few seconds
	prev := s.piCache[path]
	from, name := int64(0), ""
	if prev.nameAt > 0 && prev.nameAt <= info.Size() && info.Size() >= prev.size {
		from, name = prev.nameAt, prev.name
	}
	read, ok := readPiSession(path, from, name)
	if !ok {
		return PiSession{}, false
	}
	if s.piCache == nil {
		s.piCache = map[string]piCached{}
	}
	s.piCache[path] = piCached{
		modifiedMs: info.ModTime().UnixMilli(),
		size:       info.Size(),
		nameAt:     read.nameAt,
		name:       read.name,
		session:    read.session,
	}
	return read.session, true
}

// scanPi turns recent pi sessions into sessions. Sub-agent sessions live in a directory
// under their parent's file, so only the files directly in a project directory count
func (s *Scanner) scanPi(now time.Time) []discovered {
	root := s.PiRoot
	if root == "" {
		return nil
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	signals := ReadPiReceipts(s.PiReceiptDir)
	herdr := s.herdrAgents(now)
	cutoff := now.Add(-piScanWindow)

	var found []discovered
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, dir.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			path := filepath.Join(root, dir.Name(), e.Name())
			receipt, hasReceipt := signals.live[path]
			host, hosted := herdr[path]
			info, err := e.Info()
			if err != nil {
				continue
			}
			// An old file is history, unless something still reports on the session: a
			// prompt left open for a week writes nothing and is still waiting for an answer
			if info.ModTime().Before(cutoff) && !hasReceipt && !hosted {
				continue
			}
			session, ok := s.readPiSession(path)
			if !ok || session.Cwd == "" {
				continue
			}

			// Herdr gets its status from the same events the extension reports, and only it
			// knows whether the pane's tab has been looked at, so its word wins; the
			// extension still supplies what a blocking dialog is asking for
			live, ok := receipt, hasReceipt
			if hosted && host.Status != herdrUnknown {
				host.BlockKind, host.Tool = receipt.BlockKind, receipt.Tool
				live, ok = host, true
			}
			if live.PaneID == "" {
				live.PaneID = host.PaneID
			}
			state, reason := piSessionState(session, live, ok, signals.seen[path], now.UnixMilli())

			activityAt := timestampTime(session.ActivityAt)
			item := SessionItem{
				ID:         cmp.Or(session.ID, strings.TrimSuffix(e.Name(), ".jsonl")),
				Source:     SourcePi,
				Title:      cmp.Or(session.Title, "Untitled Session"),
				State:      state,
				WaitReason: reason,
				WorkingDir: session.Cwd,
				ActivityAt: activityAt,
				IsToday:    state == Running || state == Waiting || isSameLocalDay(activityAt, now),
				Pane:       herdr[path].PaneID,
				// Without a live signal the row says only that the turn is over; the view
				// uses this to say as much when the pointer is on it
				NoSignal: !ok,
			}
			found = append(found, discovered{projectName(session.Cwd, session.Title), item, session.ActivityAt})
		}
	}
	return found
}

// piSessionState resolves a session from its live signal. Without one -- a session pi ran
// in a terminal spark can't see, with no extension to report it -- the state stays
// Completed rather than guessing: an unknown session must not look like it needs you
func piSessionState(session PiSession, live piLive, ok bool, lastSeenMs, nowMs int64) (TaskState, WaitReason) {
	if !ok {
		return Completed, WaitPermission
	}
	switch live.Status {
	case piWorking:
		return Running, WaitPermission
	case piBlocked:
		return Waiting, piWaitReason(session, live)
	case herdrDone:
		// Herdr only says this for a turn that finished while its tab was unseen
		return Unread, WaitPermission
	case piIdle:
		if live.FromHerdr {
			// Same word, different evidence: Herdr marks a tab seen as soon as it is
			// focused, so a session it reports as idle has been looked at
			return Completed, WaitPermission
		}
	}
	// Only the extension is left, and it sees prompts rather than the user reading: those
	// come before the turn ran, so the badge fades instead of waiting on someone who has
	// walked away
	if session.TurnAt > lastSeenMs && nowMs-session.TurnAt < piUnreadWindow.Milliseconds() {
		return Unread, WaitPermission
	}
	return Completed, WaitPermission
}

// piWaitReason tells an answer from an approval. The extension reports which dialog is up;
// Herdr only says "blocked", so there the session file decides
func piWaitReason(session PiSession, live piLive) WaitReason {
	if live.Tool == piAskTool {
		return WaitQuestion
	}
	switch live.BlockKind {
	case "confirm":
		return WaitPermission
	case "":
		if session.LastEvent == AwaitingUser {
			return WaitQuestion
		}
		return WaitPermission
	}
	return WaitQuestion
}

// herdrAgents maps the pi sessions Herdr hosts to their pane and state, and marks the one
// in the focused pane as seen: Herdr is the only place a terminal session's "the user is
// looking at it" moment can be observed
func (s *Scanner) herdrAgents(now time.Time) map[string]piLive {
	if !HerdrAvailable(s.HerdrSocket) {
		return nil
	}
	agents, err := HerdrClient{SocketPath: s.HerdrSocket}.Agents()
	if err != nil {
		if now.Sub(s.herdrErrAt) > time.Minute {
			s.herdrErrAt = now
			log.Printf("[Herdr] %v", err)
		}
		return nil
	}
	out := make(map[string]piLive, len(agents))
	for _, a := range agents {
		if a.SessionPath == "" {
			continue
		}
		out[a.SessionPath] = piLive{Status: a.Status, PaneID: a.PaneID, FromHerdr: true}
	}
	return out
}
