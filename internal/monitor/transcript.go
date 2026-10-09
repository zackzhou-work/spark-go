package monitor

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// TurnEvent 是 transcript 末尾记录反映的回合进度
type TurnEvent int

const (
	// Generating 模型正在生成：用户刚提交、工具结果已回填、或消息尚未收尾
	Generating TurnEvent = iota
	// ToolPending 模型发出工具调用后尚无结果：工具执行中，或在等用户授权
	ToolPending
	// AwaitingUser 工具本身就是在向用户提问（AskUserQuestion / ExitPlanMode）
	AwaitingUser
	// Ended 回合已结束：end_turn、stop hook 已执行、或被用户打断
	Ended
)

type TranscriptActivity struct {
	LastEvent      TurnEvent
	LastModifiedMs int64
}

const (
	tailBytes     = 64 * 1024
	interruptMark = "[Request interrupted by user"
)

var userFacingTools = []string{"AskUserQuestion", "ExitPlanMode"}

type record struct {
	Type        string   `json:"type"`
	Subtype     string   `json:"subtype"`
	IsSidechain bool     `json:"isSidechain"`
	Message     *message `json:"message"`
}

type message struct {
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Text string `json:"text"`
}

// LocateTranscript 按候选 cwd 依次找 transcript；Claude Code 把 cwd 里所有非字母数字字符换成 '-' 作为项目目录名
func LocateTranscript(projectsRoot, cliSessionID string, cwdCandidates ...string) string {
	for _, cwd := range cwdCandidates {
		if cwd == "" {
			continue
		}
		p := filepath.Join(projectsRoot, encodeCwd(cwd), cliSessionID+".jsonl")
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func encodeCwd(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func ReadActivity(transcript string) (TranscriptActivity, bool) {
	event, ok := lastTurnEvent(transcript)
	if !ok {
		return TranscriptActivity{}, false
	}
	return TranscriptActivity{
		LastEvent:      event,
		LastModifiedMs: max(mtimeMs(transcript), newestSubagentMtimeMs(transcript)),
	}, true
}

func lastTurnEvent(transcript string) (TurnEvent, bool) {
	f, err := os.Open(transcript)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, false
	}
	start := max(info.Size()-tailBytes, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, false
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return 0, false
	}

	lines := bytes.Split(raw, []byte("\n"))
	// 从中间截断的第一行不完整
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	return lastEventOf(lines)
}

func lastEventOf(lines [][]byte) (TurnEvent, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if event, ok := classifyRecord(lines[i]); ok {
			return event, true
		}
	}
	return 0, false
}

func classifyRecord(line []byte) (TurnEvent, bool) {
	var r record
	if err := json.Unmarshal(line, &r); err != nil || r.IsSidechain {
		return 0, false
	}
	switch r.Type {
	case "system":
		return Ended, r.Subtype == "stop_hook_summary"
	case "user":
		if r.Message != nil && hasTextPrefix(r.Message, interruptMark) {
			return Ended, true
		}
		return Generating, true
	case "assistant":
		if r.Message == nil {
			return 0, false
		}
		switch r.Message.StopReason {
		case "end_turn", "stop_sequence":
			return Ended, true
		case "tool_use":
			if usesTool(r.Message, userFacingTools) {
				return AwaitingUser, true
			}
			return ToolPending, true
		default:
			return Generating, true
		}
	}
	return 0, false
}

// content 可能是字符串也可能是块数组，只有数组里才有工具调用和打断标记
func contentBlocks(m *message) []contentBlock {
	var blocks []contentBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return nil
	}
	return blocks
}

func hasTextPrefix(m *message, prefix string) bool {
	for _, b := range contentBlocks(m) {
		if strings.HasPrefix(b.Text, prefix) {
			return true
		}
	}
	return false
}

func usesTool(m *message, names []string) bool {
	for _, b := range contentBlocks(m) {
		if b.Type != "tool_use" {
			continue
		}
		for _, n := range names {
			if b.Name == n {
				return true
			}
		}
	}
	return false
}

func mtimeMs(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixMilli()
}

// 子 agent 的 transcript 放在 <目录>/<会话ID>/subagents/ 下，主文件在它们运行期间不会更新
func newestSubagentMtimeMs(transcript string) int64 {
	dir := filepath.Join(strings.TrimSuffix(transcript, filepath.Ext(transcript)), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var newest int64
	for _, e := range entries {
		newest = max(newest, mtimeMs(filepath.Join(dir, e.Name())))
	}
	return newest
}
