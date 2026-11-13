package monitor

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// antigravitySnapshotTTL bounds how long a previous summary read stands in if SQLite is
	// temporarily locked by Antigravity.
	antigravitySnapshotTTL = 30 * time.Second
	// antigravityLiveGrace keeps recently active sessions in Running/Waiting briefly even if the
	// process list isn't populated or a worker just restarted.
	antigravityLiveGrace         = 60 * time.Second
	antigravityTranscriptTailMax = 64 * 1024

	agStatusIdle      int32 = 1
	agStatusRunning   int32 = 2
	agStatusCanceling int32 = 3
	agStatusBusy      int32 = 4

	agTypeSideQuestion  int32 = 22
	agTypeCommitMessage int32 = 23
)

// AntigravityDataDir is where Antigravity 2.0 desktop app stores its state and logs.
func AntigravityDataDir(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".gemini", "antigravity")
}

// AntigravityDBPath is the primary SQLite summary store written by Antigravity 2.0.
func AntigravityDBPath(home string) string {
	dir := AntigravityDataDir(home)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "conversation_summaries.db")
}

// AntigravityProtoPath is the fallback protobuf summary snapshot used when the SQLite store is
// absent.
func AntigravityProtoPath(home string) string {
	dir := AntigravityDataDir(home)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "agyhub_summaries_proto.pb")
}

// AntigravityBrainDir is where per-conversation artifacts and transcript logs are stored.
func AntigravityBrainDir(home string) string {
	dir := AntigravityDataDir(home)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "brain")
}

// AntigravityTranscriptPath is the compact JSONL transcript for a single conversation.
func AntigravityTranscriptPath(brainDir, conversationID string) string {
	if brainDir == "" || conversationID == "" {
		return ""
	}
	return filepath.Join(brainDir, conversationID, ".system_generated", "logs", "transcript.jsonl")
}

// antigravityShallowWatchDirs returns the Antigravity data directory to watch non-recursively.
// Antigravity stores large media and brain trees underneath it, so watching only the top directory
// catches summary database updates without adding thousands of kqueue descriptors.
func (s *Scanner) antigravityShallowWatchDirs() []string {
	for _, p := range []string{s.AntigravityDB, s.AntigravityProto} {
		if p == "" {
			continue
		}
		dir := filepath.Dir(p)
		if isDir(dir) {
			return []string{dir}
		}
	}
	return nil
}

type antigravitySnapshot struct {
	summaries []antigravitySummary
	readAt    time.Time
	valid     bool
}

type antigravityTranscriptCacheEntry struct {
	modTime    time.Time
	size       int64
	hasWait    bool
	waitReason WaitReason
	activityAt time.Time
}

type antigravitySummary struct {
	ID                   string
	Title                string
	StepCount            uint32
	LastModified         time.Time
	Created              time.Time
	LastUserInput        time.Time
	LastUserView         time.Time
	Status               int32
	Source               int32
	TrajectoryType       int32
	Killed               bool
	Cloud                bool
	HasActiveChildren    bool
	HasRunningSubagents  bool
	NotFullyIdle         bool
	Archived             bool
	MarkedAsUnread       bool
	HideFromList         bool
	IsBattleModeFork     bool
	ParentConversationID string
	NestingDepth         int32
	AppDataDir           string
	BattleID             string
	WorkspaceURIs        []string
	HasWaitingStep       bool
	WaitReason           WaitReason
}

func (s *Scanner) scanAntigravity(now time.Time) []discovered {
	summaries, ok := s.antigravitySummarySnapshot(now)
	if !ok || len(summaries) == 0 {
		return nil
	}

	var filtered []antigravitySummary
	needLiveCheck := false
	for _, sum := range summaries {
		if !shouldIncludeAntigravitySummary(sum) {
			continue
		}
		if isAntigravityActiveCandidate(sum) {
			needLiveCheck = true
		}
		filtered = append(filtered, sum)
	}
	if len(filtered) == 0 {
		return nil
	}

	appLive := s.antigravityAssumeLive
	if needLiveCheck && !appLive {
		appLive = isAntigravityRunning()
	}

	seen := make(map[string]struct{}, len(filtered))
	found := make([]discovered, 0, len(filtered))
	for _, sum := range filtered {
		seen[sum.ID] = struct{}{}

		activityAt := sum.LastModified
		if activityAt.IsZero() {
			activityAt = sum.LastUserInput
		}
		if activityAt.IsZero() {
			activityAt = sum.Created
		}

		activeCandidate := isAntigravityActiveCandidate(sum)
		hasTranscriptWait := false
		var transcriptReason WaitReason
		if activeCandidate && s.AntigravityBrain != "" {
			tx := s.readAntigravityTranscriptCached(AntigravityTranscriptPath(s.AntigravityBrain, sum.ID))
			if tx.activityAt.After(activityAt) {
				activityAt = tx.activityAt
			}
			hasTranscriptWait = tx.hasWait
			transcriptReason = tx.waitReason
		}

		live := appLive || (!activityAt.IsZero() && now.Sub(activityAt) <= antigravityLiveGrace)
		state, reason := determineAntigravityState(sum, hasTranscriptWait, transcriptReason, live)

		paths := decodeAntigravityWorkspacePaths(sum.WorkspaceURIs)
		workingDir := ""
		if len(paths) > 0 {
			workingDir = paths[0]
		}
		projectName := antigravityProjectName(workingDir)

		item := SessionItem{
			ID:         sum.ID,
			Source:     SourceAntigravity,
			Title:      sum.Title,
			State:      state,
			WaitReason: reason,
			WorkingDir: workingDir,
			ActivityAt: activityAt,
			IsToday:    state == Running || state == Waiting || isSameLocalDay(activityAt, now),
		}
		found = append(found, discovered{projectName, item, activityAt.UnixMilli()})
	}

	for id := range s.antigravityCache {
		if _, ok := seen[id]; !ok {
			delete(s.antigravityCache, id)
		}
	}
	return found
}

func (s *Scanner) antigravitySummarySnapshot(now time.Time) ([]antigravitySummary, bool) {
	if s.AntigravityDB == "" && s.AntigravityProto == "" {
		return nil, false
	}

	if s.AntigravityDB != "" {
		_, statErr := os.Stat(s.AntigravityDB)
		if statErr == nil {
			read, err := readAntigravityDB(s.AntigravityDB)
			if err == nil {
				s.antigravitySnap = antigravitySnapshot{summaries: read, readAt: now, valid: true}
				return read, true
			}
			if s.antigravitySnap.valid && now.Sub(s.antigravitySnap.readAt) <= antigravitySnapshotTTL {
				return s.antigravitySnap.summaries, true
			}
			return nil, false
		}
		if !os.IsNotExist(statErr) {
			if s.antigravitySnap.valid && now.Sub(s.antigravitySnap.readAt) <= antigravitySnapshotTTL {
				return s.antigravitySnap.summaries, true
			}
			return nil, false
		}
	}

	if s.AntigravityProto != "" {
		read, err := readAntigravityProto(s.AntigravityProto)
		if err == nil {
			s.antigravitySnap = antigravitySnapshot{summaries: read, readAt: now, valid: true}
			return read, true
		}
	}

	if s.antigravitySnap.valid && now.Sub(s.antigravitySnap.readAt) <= antigravitySnapshotTTL {
		return s.antigravitySnap.summaries, true
	}
	return nil, false
}

func readAntigravityDB(path string) ([]antigravitySummary, error) {
	db, err := openCursorDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), cursorQueryTimeout)
	defer cancel()

	const query = `SELECT conversation_id, title, status, source, killed, battle_id, step_count,
		app_data_dir, nesting_depth, last_modified_time, not_fully_idle, workspace_uris,
		parent_conversation_id, raw_summary
		FROM conversation_summaries`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []antigravitySummary
	for rows.Next() {
		var (
			convID       sql.NullString
			title        sql.NullString
			status       sql.NullString
			source       sql.NullString
			killed       sql.NullBool
			battleID     sql.NullString
			stepCount    sql.NullInt64
			appDataDir   sql.NullString
			nestingDepth sql.NullInt64
			lastModified sql.NullString
			notFullyIdle sql.NullBool
			workspaceURI sql.NullString
			parentID     sql.NullString
			rawSummary   []byte
		)
		if err := rows.Scan(
			&convID,
			&title,
			&status,
			&source,
			&killed,
			&battleID,
			&stepCount,
			&appDataDir,
			&nestingDepth,
			&lastModified,
			&notFullyIdle,
			&workspaceURI,
			&parentID,
			&rawSummary,
		); err != nil {
			return nil, err
		}

		var sum antigravitySummary
		if len(rawSummary) > 0 {
			decoded, err := decodeCascadeTrajectorySummary(rawSummary)
			if err == nil {
				sum = decoded
			}
		}

		if sum.ID == "" && convID.Valid {
			sum.ID = convID.String
		}
		if sum.Title == "" && title.Valid {
			sum.Title = strings.TrimSpace(title.String)
		}
		if sum.Status == 0 && status.Valid {
			sum.Status = parseAntigravityStatus(status.String)
		}
		if sum.Source == 0 && source.Valid {
			sum.Source = parseAntigravitySource(source.String)
		}
		if killed.Valid && killed.Bool {
			sum.Killed = true
		}
		if battleID.Valid && battleID.String != "" {
			sum.BattleID = battleID.String
		}
		if sum.StepCount == 0 && stepCount.Valid && stepCount.Int64 > 0 {
			sum.StepCount = uint32(stepCount.Int64)
		}
		if appDataDir.Valid {
			sum.AppDataDir = appDataDir.String
		}
		if sum.NestingDepth == 0 && nestingDepth.Valid && nestingDepth.Int64 > 0 {
			sum.NestingDepth = int32(nestingDepth.Int64)
		}
		if sum.LastModified.IsZero() && lastModified.Valid {
			sum.LastModified = parseAntigravityTimeString(lastModified.String)
		}
		if notFullyIdle.Valid && notFullyIdle.Bool {
			sum.NotFullyIdle = true
		}
		if len(sum.WorkspaceURIs) == 0 && workspaceURI.Valid && workspaceURI.String != "" {
			sum.WorkspaceURIs = parseAntigravityWorkspaceColumn(workspaceURI.String)
		}
		if sum.ParentConversationID == "" && parentID.Valid {
			sum.ParentConversationID = parentID.String
		}

		if sum.ID != "" {
			out = append(out, sum)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func readAntigravityProto(path string) ([]antigravitySummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeCascadeTrajectorySummaries(data)
}

func shouldIncludeAntigravitySummary(sum antigravitySummary) bool {
	if sum.ID == "" {
		return false
	}
	if sum.AppDataDir != "" && sum.AppDataDir != "antigravity" {
		return false
	}
	if sum.Archived || sum.HideFromList || sum.Cloud {
		return false
	}
	if sum.ParentConversationID != "" || sum.NestingDepth > 0 {
		return false
	}
	if sum.IsBattleModeFork || sum.BattleID != "" {
		return false
	}
	if sum.TrajectoryType == agTypeSideQuestion || sum.TrajectoryType == agTypeCommitMessage {
		return false
	}
	if !isAntigravityDesktopSource(sum.Source) {
		return false
	}
	if sum.StepCount == 0 && strings.TrimSpace(sum.Title) == "" && len(decodeAntigravityWorkspacePaths(sum.WorkspaceURIs)) == 0 {
		return false
	}
	return true
}

func isAntigravityDesktopSource(source int32) bool {
	switch source {
	case 0, 1, 12, 18, 20:
		return true
	default:
		return false
	}
}

func isAntigravityActiveCandidate(sum antigravitySummary) bool {
	if sum.Killed {
		return false
	}
	if sum.HasWaitingStep {
		return true
	}
	switch sum.Status {
	case agStatusRunning, agStatusCanceling, agStatusBusy:
		return true
	}
	return sum.HasRunningSubagents || sum.HasActiveChildren || sum.NotFullyIdle
}

func determineAntigravityState(sum antigravitySummary, hasTranscriptWait bool, transcriptReason WaitReason, live bool) (TaskState, WaitReason) {
	if !sum.Killed && live {
		if sum.HasWaitingStep {
			reason := sum.WaitReason
			if hasTranscriptWait && transcriptReason == WaitQuestion {
				reason = WaitQuestion
			}
			return Waiting, reason
		}
		if hasTranscriptWait && isAntigravityActiveCandidate(sum) {
			return Waiting, transcriptReason
		}
		if isAntigravityActiveCandidate(sum) {
			return Running, 0
		}
	}

	if sum.MarkedAsUnread {
		return Unread, 0
	}
	var modSec int64
	if !sum.LastModified.IsZero() {
		modSec = sum.LastModified.Unix()
	}
	var viewSec int64
	if !sum.LastUserView.IsZero() {
		viewSec = sum.LastUserView.Unix()
	}
	if modSec > viewSec {
		return Unread, 0
	}
	return Completed, 0
}

func isAntigravityRunning() bool {
	for _, p := range listProcesses() {
		if p.name == "Antigravity" || strings.HasPrefix(p.name, "language_server") {
			return true
		}
	}
	return false
}

func (s *Scanner) readAntigravityTranscriptCached(path string) antigravityTranscriptCacheEntry {
	if path == "" {
		return antigravityTranscriptCacheEntry{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return antigravityTranscriptCacheEntry{}
	}
	if s.antigravityCache == nil {
		s.antigravityCache = make(map[string]antigravityTranscriptCacheEntry)
	}
	if cached, ok := s.antigravityCache[path]; ok && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached
	}

	entry := readAntigravityTranscript(path, info)
	s.antigravityCache[path] = entry
	return entry
}

type antigravityTranscriptLine struct {
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	ToolCalls []struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"tool_calls"`
}

func readAntigravityTranscript(path string, info os.FileInfo) antigravityTranscriptCacheEntry {
	entry := antigravityTranscriptCacheEntry{
		modTime: info.ModTime(),
		size:    info.Size(),
	}
	f, err := os.Open(path)
	if err != nil {
		return entry
	}
	defer f.Close()

	size := info.Size()
	start := int64(0)
	if size > antigravityTranscriptTailMax {
		start = size - antigravityTranscriptTailMax
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return entry
		}
	}

	r := bufio.NewReader(f)
	if start > 0 {
		// Drop a potentially sliced first line after seeking into the middle of the file.
		if _, err := r.ReadBytes('\n'); err != nil {
			return entry
		}
	}

	var lastValid *antigravityTranscriptLine
	for {
		line, err := r.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var parsed antigravityTranscriptLine
			if json.Unmarshal(trimmed, &parsed) == nil && parsed.Type != "" {
				lastValid = &parsed
				if parsed.CreatedAt != "" {
					if ts, tsErr := time.Parse(time.RFC3339Nano, parsed.CreatedAt); tsErr == nil && ts.After(entry.activityAt) {
						entry.activityAt = ts
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	if lastValid == nil || lastValid.Type != "PLANNER_RESPONSE" || len(lastValid.ToolCalls) == 0 {
		return entry
	}
	for _, tc := range lastValid.ToolCalls {
		switch tc.Name {
		case "ask_question", "exit_plan_mode":
			entry.hasWait = true
			entry.waitReason = WaitQuestion
			return entry
		case "write_file", "edit_file":
			if antigravityArgsRequestFeedback(tc.Args) {
				entry.hasWait = true
				entry.waitReason = WaitQuestion
				return entry
			}
		}
	}
	return entry
}

func antigravityArgsRequestFeedback(args map[string]any) bool {
	if len(args) == 0 {
		return false
	}
	raw, ok := args["ArtifactMetadata"]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case map[string]any:
		b, _ := v["RequestFeedback"].(bool)
		return b
	case string:
		var meta struct {
			RequestFeedback bool `json:"RequestFeedback"`
		}
		if json.Unmarshal([]byte(v), &meta) == nil {
			return meta.RequestFeedback
		}
	}
	return false
}

func decodeAntigravityWorkspacePaths(uris []string) []string {
	if len(uris) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(uris))
	out := make([]string, 0, len(uris))
	for _, raw := range uris {
		p := decodeAntigravityURI(raw)
		if p == "" {
			continue
		}
		if _, exists := seen[p]; exists {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func decodeAntigravityURI(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil || u.Path == "" {
			return ""
		}
		return filepath.Clean(u.Path)
	}
	if strings.HasPrefix(raw, "/") {
		return filepath.Clean(raw)
	}
	return ""
}

func antigravityProjectName(workingDir string) string {
	if workingDir != "" {
		base := filepath.Base(workingDir)
		if base != "" && base != "/" && base != "." {
			return base
		}
	}
	return "Antigravity"
}

func parseAntigravityWorkspaceColumn(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []string
		if json.Unmarshal([]byte(trimmed), &list) == nil {
			return list
		}
	}
	if strings.Contains(trimmed, "\n") {
		return strings.Split(trimmed, "\n")
	}
	return []string{trimmed}
}

func parseAntigravityStatus(raw string) int32 {
	if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32); err == nil {
		return int32(n)
	}
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "CASCADE_RUN_STATUS_IDLE", "IDLE":
		return agStatusIdle
	case "CASCADE_RUN_STATUS_RUNNING", "RUNNING":
		return agStatusRunning
	case "CASCADE_RUN_STATUS_CANCELING", "CANCELING":
		return agStatusCanceling
	case "CASCADE_RUN_STATUS_BUSY", "BUSY":
		return agStatusBusy
	default:
		return 0
	}
}

func parseAntigravitySource(raw string) int32 {
	if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32); err == nil {
		return int32(n)
	}
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "", "CORTEX_TRAJECTORY_SOURCE_UNSPECIFIED", "UNSPECIFIED":
		return 0
	case "CORTEX_TRAJECTORY_SOURCE_CASCADE_CLIENT", "CASCADE_CLIENT":
		return 1
	case "CORTEX_TRAJECTORY_SOURCE_INTERACTIVE_CASCADE", "INTERACTIVE_CASCADE":
		return 12
	case "CORTEX_TRAJECTORY_SOURCE_JETBOX", "JETBOX":
		return 18
	case "CORTEX_TRAJECTORY_SOURCE_CIDER", "CIDER":
		return 20
	default:
		return -1
	}
}

func parseAntigravityTimeString(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		switch {
		case n > 1e15:
			return time.Unix(0, n)
		case n > 1e12:
			return time.UnixMilli(n)
		case n > 0:
			return time.Unix(n, 0)
		default:
			return time.Time{}
		}
	}
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts
	}
	return time.Time{}
}

// --- Minimal bounds-checked protobuf wire decoder ---

const (
	wireVarint = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

type protoField struct {
	num     uint32
	wtype   uint8
	varint  uint64
	payload []byte
}

func iterProtoFields(b []byte, fn func(protoField) error) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return io.ErrUnexpectedEOF
		}
		b = b[n:]
		f := protoField{
			num:   uint32(tag >> 3),
			wtype: uint8(tag & 0x7),
		}
		switch f.wtype {
		case wireVarint:
			v, vn := binary.Uvarint(b)
			if vn <= 0 {
				return io.ErrUnexpectedEOF
			}
			f.varint = v
			b = b[vn:]
		case wireFixed64:
			if len(b) < 8 {
				return io.ErrUnexpectedEOF
			}
			f.varint = binary.LittleEndian.Uint64(b[:8])
			b = b[8:]
		case wireBytes:
			ln, lnn := binary.Uvarint(b)
			if lnn <= 0 || ln > uint64(len(b[lnn:])) {
				return io.ErrUnexpectedEOF
			}
			b = b[lnn:]
			f.payload = b[:ln]
			b = b[ln:]
		case wireFixed32:
			if len(b) < 4 {
				return io.ErrUnexpectedEOF
			}
			f.varint = uint64(binary.LittleEndian.Uint32(b[:4]))
			b = b[4:]
		default:
			return io.ErrUnexpectedEOF
		}
		if err := fn(f); err != nil {
			return err
		}
	}
	return nil
}

func decodeCascadeTrajectorySummaries(b []byte) ([]antigravitySummary, error) {
	var out []antigravitySummary
	err := iterProtoFields(b, func(f protoField) error {
		if f.num != 1 || f.wtype != wireBytes {
			return nil
		}
		var mapKey string
		var valBytes []byte
		if err := iterProtoFields(f.payload, func(ef protoField) error {
			switch {
			case ef.num == 1 && ef.wtype == wireBytes:
				mapKey = string(ef.payload)
			case ef.num == 2 && ef.wtype == wireBytes:
				valBytes = ef.payload
			}
			return nil
		}); err != nil {
			return err
		}
		if len(valBytes) == 0 {
			return nil
		}
		sum, err := decodeCascadeTrajectorySummary(valBytes)
		if err != nil {
			return nil
		}
		if sum.ID == "" {
			sum.ID = mapKey
		}
		if sum.ID != "" {
			out = append(out, sum)
		}
		return nil
	})
	return out, err
}

func decodeCascadeTrajectorySummary(b []byte) (antigravitySummary, error) {
	var (
		sum          antigravitySummary
		summaryTitle string
		annTitle     string
		topWS        []string
		metaWS       []string
	)
	err := iterProtoFields(b, func(f protoField) error {
		switch {
		case f.num == 1 && f.wtype == wireBytes:
			summaryTitle = strings.TrimSpace(string(f.payload))
		case f.num == 2 && f.wtype == wireVarint:
			sum.StepCount = uint32(f.varint)
		case f.num == 3 && f.wtype == wireBytes:
			sum.LastModified = decodeProtoTimestamp(f.payload)
		case f.num == 4 && f.wtype == wireBytes:
			sum.ID = string(f.payload)
		case f.num == 5 && f.wtype == wireVarint:
			sum.Status = int32(f.varint)
		case f.num == 7 && f.wtype == wireBytes:
			sum.Created = decodeProtoTimestamp(f.payload)
		case f.num == 8 && f.wtype == wireBytes:
			hasWait, reason := decodeWaitingStepWithIndex(f.payload)
			if hasWait {
				if !sum.HasWaitingStep || reason == WaitQuestion {
					sum.WaitReason = reason
				}
				sum.HasWaitingStep = true
			}
		case f.num == 9 && f.wtype == wireBytes:
			if uri := decodeWorkspaceMetadataURI(f.payload); uri != "" {
				topWS = append(topWS, uri)
			}
		case f.num == 10 && f.wtype == wireBytes:
			sum.LastUserInput = decodeProtoTimestamp(f.payload)
		case f.num == 15 && f.wtype == wireBytes:
			decodeConversationAnnotations(f.payload, &annTitle, &sum)
		case f.num == 17 && f.wtype == wireBytes:
			decodeTrajectoryMetadata(f.payload, &metaWS, &sum)
		case f.num == 18 && f.wtype == wireVarint:
			sum.HasActiveChildren = f.varint != 0
		case f.num == 20 && f.wtype == wireVarint:
			sum.Source = int32(f.varint)
		case f.num == 21 && f.wtype == wireVarint:
			sum.NotFullyIdle = f.varint != 0
		case f.num == 22 && f.wtype == wireVarint:
			sum.TrajectoryType = int32(f.varint)
		case f.num == 23 && f.wtype == wireVarint:
			sum.Killed = f.varint != 0
		case f.num == 27 && f.wtype == wireVarint:
			sum.Cloud = f.varint != 0
		case f.num == 30 && f.wtype == wireVarint:
			sum.HasRunningSubagents = f.varint != 0
		}
		return nil
	})
	if err != nil {
		return antigravitySummary{}, err
	}
	if annTitle != "" {
		sum.Title = annTitle
	} else {
		sum.Title = summaryTitle
	}
	sum.WorkspaceURIs = append(topWS, metaWS...)
	return sum, nil
}

func decodeProtoTimestamp(b []byte) time.Time {
	var sec int64
	var nanos int32
	if iterProtoFields(b, func(f protoField) error {
		switch {
		case f.num == 1 && f.wtype == wireVarint:
			sec = int64(f.varint)
		case f.num == 2 && f.wtype == wireVarint:
			nanos = int32(f.varint)
		}
		return nil
	}) != nil || sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, int64(nanos))
}

func decodeWorkspaceMetadataURI(b []byte) string {
	var uri string
	_ = iterProtoFields(b, func(f protoField) error {
		if f.num == 1 && f.wtype == wireBytes {
			uri = string(f.payload)
		}
		return nil
	})
	return uri
}

func decodeConversationAnnotations(b []byte, title *string, sum *antigravitySummary) {
	_ = iterProtoFields(b, func(f protoField) error {
		switch {
		case f.num == 1 && f.wtype == wireBytes:
			*title = strings.TrimSpace(string(f.payload))
		case f.num == 4 && f.wtype == wireVarint:
			sum.Archived = f.varint != 0
		case f.num == 7 && f.wtype == wireBytes:
			sum.LastUserView = decodeProtoTimestamp(f.payload)
		case f.num == 8 && f.wtype == wireVarint:
			sum.MarkedAsUnread = f.varint != 0
		}
		return nil
	})
}

func decodeTrajectoryMetadata(b []byte, metaWS *[]string, sum *antigravitySummary) {
	_ = iterProtoFields(b, func(f protoField) error {
		switch {
		case f.num == 1 && f.wtype == wireBytes:
			if uri := decodeWorkspaceMetadataURI(f.payload); uri != "" {
				*metaWS = append(*metaWS, uri)
			}
		case f.num == 2 && f.wtype == wireBytes && sum.Created.IsZero():
			sum.Created = decodeProtoTimestamp(f.payload)
		case f.num == 5 && f.wtype == wireBytes:
			sum.ParentConversationID = string(f.payload)
		case f.num == 7 && f.wtype == wireBytes:
			*metaWS = append(*metaWS, string(f.payload))
		case f.num == 12 && f.wtype == wireBytes:
			_ = iterProtoFields(f.payload, func(sf protoField) error {
				if sf.num == 3 && sf.wtype == wireVarint {
					sum.HideFromList = sf.varint != 0
				}
				return nil
			})
		case f.num == 16 && f.wtype == wireVarint:
			sum.IsBattleModeFork = f.varint != 0
		case f.num == 17 && f.wtype == wireVarint:
			sum.NestingDepth = int32(f.varint)
		}
		return nil
	})
}

func decodeWaitingStepWithIndex(b []byte) (bool, WaitReason) {
	var stepBytes []byte
	_ = iterProtoFields(b, func(f protoField) error {
		if f.num == 1 && f.wtype == wireBytes {
			stepBytes = f.payload
		}
		return nil
	})
	if len(stepBytes) == 0 {
		// A waiting step entry with empty payload still represents an active wait.
		return true, WaitPermission
	}
	reason := WaitPermission
	_ = iterProtoFields(stepBytes, func(f protoField) error {
		switch {
		case f.num == 154 && f.wtype == wireBytes:
			reason = WaitQuestion
		case f.num == 56 && f.wtype == wireBytes:
			if decodeRequestedInteractionReason(f.payload) == WaitQuestion {
				reason = WaitQuestion
			}
		}
		return nil
	})
	return true, reason
}

func decodeRequestedInteractionReason(b []byte) WaitReason {
	reason := WaitPermission
	_ = iterProtoFields(b, func(f protoField) error {
		if (f.num == 20 || f.num == 22) && f.wtype == wireBytes {
			reason = WaitQuestion
		}
		return nil
	})
	return reason
}
