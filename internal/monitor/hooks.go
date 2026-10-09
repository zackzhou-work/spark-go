package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// HookKind 是 Claude Code hook 落盘的最近一次事件，文件名为 <cliSessionId>.json，后写覆盖先写
type HookKind int

const (
	// HookPermissionPrompt 正在等用户授权或回答
	HookPermissionPrompt HookKind = iota
	// HookBusy 用户提交了 prompt，或工具开始/结束执行
	HookBusy
	// HookStopped 回合结束或会话退出
	HookStopped
	HookOther
)

type HookSignal struct {
	Kind HookKind
	AtMs int64
}

// HooksDir 是 hooks/spark-hook.sh 写事件的目录
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
