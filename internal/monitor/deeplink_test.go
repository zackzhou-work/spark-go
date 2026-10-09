package monitor

import (
	"strings"
	"testing"
)

func TestRealSessionIDBuildsURL(t *testing.T) {
	got, ok := SessionDeeplink("local_1f079f2a-8c74-449d-b254-1be4a5d9b0d8")
	if !ok || got != "claude://code/continue?session=local_1f079f2a-8c74-449d-b254-1be4a5d9b0d8&source=spark" {
		t.Errorf("got %q", got)
	}
}

func TestRejectsIDsTheDesktopAppWouldNotMatch(t *testing.T) {
	for _, id := range []string{
		// 没有 local_ 前缀：cliSessionId 和 bridgeSessionId 都走不了这条链
		"1f079f2a-8c74-449d-b254-1be4a5d9b0d8",
		"session_01NSKvUJpEpTmqnEih8Y3g8R",
		"local_",
		// 下划线不在允许的字符集里
		"local_has_underscore",
		"local_" + strings.Repeat("a", 65),
		// 会话 JSON 缺 sessionId 时 scanner 会填这个占位值
		"session",
	} {
		if _, ok := SessionDeeplink(id); ok {
			t.Errorf("%q should be rejected", id)
		}
	}
	if _, ok := SessionDeeplink("local_" + strings.Repeat("a", 64)); !ok {
		t.Error("64 chars should be accepted")
	}
}
