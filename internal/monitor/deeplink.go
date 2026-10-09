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
