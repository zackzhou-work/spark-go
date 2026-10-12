package monitor

import "strings"

// 桌面端会话 ID 的格式：local_ 前缀加 1..=64 个字母数字或连字符
func isDesktopSessionID(id string) bool {
	rest, ok := strings.CutPrefix(id, "local_")
	if !ok || len(rest) < 1 || len(rest) > 64 {
		return false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// SessionDeeplink 是 Claude Desktop 的私有深链，用会话 ID 直接定位到 Code 标签页里的那个会话。
// ID 格式对不上多半意味着桌面端换了格式，深链整体已经失效
func SessionDeeplink(sessionID string) (string, bool) {
	if !isDesktopSessionID(sessionID) {
		return "", false
	}
	return "claude://code/continue?session=" + sessionID + "&source=spark", true
}

// CodexDeeplink is Codex's thread deep link: it raises Codex and opens the thread. A
// thread that only ran in a CLI or IDE has no tab in the desktop app, so it opens empty
func CodexDeeplink(threadID string) (string, bool) {
	if !isCodexThreadID(threadID) {
		return "", false
	}
	return "codex://threads/" + threadID, true
}

// isCodexThreadID checks UUID shape: 8-4-4-4-12 hexadecimal
func isCodexThreadID(id string) bool {
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		return false
	}
	want := []int{8, 4, 4, 4, 12}
	for i, p := range parts {
		if len(p) != want[i] {
			return false
		}
		for j := 0; j < len(p); j++ {
			c := p[j]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
