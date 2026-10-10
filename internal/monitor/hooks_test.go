package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setMtime(t *testing.T, path string, ms int64) {
	t.Helper()
	at := time.UnixMilli(ms)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestClassifiesHookEvents(t *testing.T) {
	cases := []struct {
		payload string
		want    HookKind
		ok      bool
	}{
		{`{"session_id":"s","hook_event_name":"PermissionRequest","tool_name":"Bash"}`, HookPermissionPrompt, true},
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt"}`, HookPermissionPrompt, true},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, HookOther, true},
		{`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, HookBusy, true},
		{`{"hook_event_name":"Stop"}`, HookStopped, true},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := classifyHook([]byte(c.payload))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %v ok=%v", c.payload, got, ok)
		}
	}
}

func TestReadsSignalFromSessionFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "abc.json"), []byte(`{"hook_event_name":"UserPromptSubmit","session_id":"abc"}`), 0o644)
	sig, ok := ReadHookSignal(dir, "abc")
	if !ok || sig.Kind != HookBusy || sig.AtMs <= 0 {
		t.Errorf("got %+v ok=%v", sig, ok)
	}
	if _, ok := ReadHookSignal(dir, "missing"); ok {
		t.Error("missing file should give no signal")
	}
}

func TestHookInstalledReadsClaudeSettings(t *testing.T) {
	home := t.TempDir()
	if HookInstalled(home) {
		t.Fatal("no settings file should mean no hook")
	}
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if HookInstalled(home) {
		t.Fatal("settings without the script should mean no hook")
	}
	hooked := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/Users/me/.config/spark/spark-hook.sh"}]}]}}`
	if err := os.WriteFile(settings, []byte(hooked), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HookInstalled(home) {
		t.Fatal("settings naming the script should mean the hook is installed")
	}
}
