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
		// No local_ prefix: neither a cliSessionId nor a bridgeSessionId reaches this path
		"1f079f2a-8c74-449d-b254-1be4a5d9b0d8",
		"session_01NSKvUJpEpTmqnEih8Y3g8R",
		"local_",
		// Underscores aren't in the allowed character set
		"local_has_underscore",
		"local_" + strings.Repeat("a", 65),
		// The placeholder the scanner fills in when a session JSON has no sessionId
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

func TestCodexDeeplinkValidatesThreadID(t *testing.T) {
	got, ok := CodexDeeplink("01a12f69-fdb1-7173-b784-47189d9442d7")
	if !ok || got != "codex://threads/01a12f69-fdb1-7173-b784-47189d9442d7" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	for _, id := range []string{
		"",
		// Not UUID shaped
		"not-a-uuid",
		"01a12f69-fdb1-7173-b784",
		"01a12f69-fdb1-7173-b784-47189d9442d7-extra",
		// Right segment lengths, but not hexadecimal
		"zzzzzzzz-fdb1-7173-b784-47189d9442d7",
	} {
		if _, ok := CodexDeeplink(id); ok {
			t.Errorf("%q should be rejected", id)
		}
	}
}
