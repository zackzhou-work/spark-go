package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	endTurn     = `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`
	toolUse     = `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","input":{}}]}}`
	askUser     = `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"AskUserQuestion","input":{}}]}}`
	toolResult  = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	prompt      = `{"type":"user","message":{"role":"user","content":"hello"}}`
	interrupted = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	stopHook    = `{"type":"system","subtype":"stop_hook_summary","hookCount":1}`
	bridge      = `{"type":"bridge-session","sessionId":"x"}`
	lastPrompt  = `{"type":"last-prompt","lastPrompt":"hi"}`
	sidechain   = `{"type":"assistant","isSidechain":true,"message":{"role":"assistant","stop_reason":"tool_use","content":[]}}`
)

func eventOf(t *testing.T, lines ...string) (TurnEvent, bool) {
	t.Helper()
	raw := make([][]byte, len(lines))
	for i, l := range lines {
		raw[i] = []byte(l)
	}
	return lastEventOf(raw)
}

func expectEvent(t *testing.T, want TurnEvent, lines ...string) {
	t.Helper()
	got, ok := eventOf(t, lines...)
	if !ok || got != want {
		t.Errorf("got %v (ok=%v), want %v", got, ok, want)
	}
}

func TestEndTurnIsEndedEvenWithTrailingMetadata(t *testing.T) {
	expectEvent(t, Ended, toolResult, endTurn, stopHook, bridge, lastPrompt)
	expectEvent(t, Ended, toolResult, endTurn)
}

func TestPendingToolCall(t *testing.T) {
	expectEvent(t, ToolPending, prompt, toolUse)
	expectEvent(t, AwaitingUser, prompt, askUser)
}

func TestGeneratingAfterPromptOrToolResult(t *testing.T) {
	expectEvent(t, Generating, endTurn, prompt)
	expectEvent(t, Generating, toolUse, toolResult)
}

func TestUserInterruptEndsTurn(t *testing.T) {
	expectEvent(t, Ended, toolUse, interrupted)
}

func TestSidechainAndUnknownRecordsAreSkipped(t *testing.T) {
	expectEvent(t, Ended, endTurn, sidechain, bridge)
	if _, ok := eventOf(t, bridge, "not json"); ok {
		t.Error("expected no event")
	}
}

func TestReadActivityFromFileTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc.jsonl")
	body := strings.Repeat(toolResult+"\n", 2000) + endTurn + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, ok := ReadActivity(path)
	if !ok || a.LastEvent != Ended || a.LastModifiedMs <= 0 {
		t.Errorf("got %+v ok=%v", a, ok)
	}
}

func TestSubagentTranscriptsCountAsActivity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc.jsonl")
	_ = os.WriteFile(path, []byte(toolUse+"\n"), 0o644)
	sub := filepath.Join(dir, "abc", "subagents")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "agent.jsonl"), []byte("{}"), 0o644)
	future := mtimeMs(path) + 60_000
	setMtime(t, filepath.Join(sub, "agent.jsonl"), future)

	a, _ := ReadActivity(path)
	if a.LastModifiedMs != future {
		t.Errorf("got %d, want subagent mtime %d", a.LastModifiedMs, future)
	}
}

func TestCwdEncodingMatchesClaudeCode(t *testing.T) {
	got := encodeCwd("/Users/me/work/spark/.claude/worktrees/x-1")
	if got != "-Users-me-work-spark--claude-worktrees-x-1" {
		t.Errorf("got %q", got)
	}
}

func TestLocateTriesEachCandidate(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, encodeCwd("/b"))
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "sid.jsonl"), nil, 0o644)
	if got := LocateTranscript(root, "sid", "", "/a", "/b"); got != filepath.Join(dir, "sid.jsonl") {
		t.Errorf("got %q", got)
	}
	if got := LocateTranscript(root, "sid", "/a"); got != "" {
		t.Errorf("got %q", got)
	}
}
