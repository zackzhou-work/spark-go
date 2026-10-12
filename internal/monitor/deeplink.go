package monitor

import "strings"

// Desktop session IDs are a local_ prefix plus 1..=64 alphanumerics or hyphens
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

// SessionDeeplink is Claude Desktop's private deep link, addressing a session in the Code
// tab by ID. An ID that doesn't match usually means the desktop app changed its format and
// the link is dead
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
