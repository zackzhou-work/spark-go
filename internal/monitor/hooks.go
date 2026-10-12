package monitor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// HookKind is the most recent Claude Code hook event on disk, one <cliSessionId>.json
// per session where each write replaces the last
type HookKind int

const (
	// HookPermissionPrompt: waiting on the user to approve or answer
	HookPermissionPrompt HookKind = iota
	// HookBusy: the user submitted a prompt, or a tool started or finished
	HookBusy
	// HookStopped: the turn ended or the session exited
	HookStopped
	HookOther
)

type HookSignal struct {
	Kind HookKind
	AtMs int64
}

// HooksDir is where hooks/spark-hook.sh writes events
func HooksDir(home string) string {
	return filepath.Join(home, ".config", "spark", "hooks")
}

func ReadHookSignal(dir, cliSessionID string) (HookSignal, bool) {
	path := filepath.Join(dir, cliSessionID+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		return HookSignal{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return HookSignal{}, false
	}
	kind, ok := classifyHook(content)
	if !ok {
		return HookSignal{}, false
	}
	return HookSignal{Kind: kind, AtMs: info.ModTime().UnixMilli()}, true
}

func classifyHook(payload []byte) (HookKind, bool) {
	var p struct {
		HookEventName    string `json:"hook_event_name"`
		NotificationType string `json:"notification_type"`
	}
	if json.Unmarshal(payload, &p) != nil || p.HookEventName == "" {
		return 0, false
	}
	switch p.HookEventName {
	case "PermissionRequest":
		return HookPermissionPrompt, true
	case "Notification":
		if p.NotificationType == "permission_prompt" || p.NotificationType == "elicitation_dialog" {
			return HookPermissionPrompt, true
		}
		return HookOther, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return HookBusy, true
	case "Stop", "SessionEnd":
		return HookStopped, true
	}
	return HookOther, true
}

// HookInstalled reports whether ~/.claude/settings.json names spark-hook.sh. It only
// matches the script name and doesn't check that every event is wired up
func HookInstalled(home string) bool {
	content, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	return err == nil && bytes.Contains(content, []byte("spark-hook.sh"))
}
