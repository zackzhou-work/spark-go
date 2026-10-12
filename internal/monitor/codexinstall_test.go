package monitor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func readHooksFile(t *testing.T, home string) codexHooksFile {
	t.Helper()
	content, err := os.ReadFile(CodexHooksPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var file codexHooksFile
	if err := json.Unmarshal(content, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestInstallCodexHooksFromScratch(t *testing.T) {
	home := t.TempDir()
	script := CodexHookScriptPath(home)
	if err := InstallCodexHooks(home, script); err != nil {
		t.Fatal(err)
	}
	file := readHooksFile(t, home)
	if len(file.Hooks) != len(CodexHookEvents) {
		t.Errorf("want %d events, got %d", len(CodexHookEvents), len(file.Hooks))
	}
	// PermissionRequest needs no long timeout: spark only shows it in the notch, and the
	// approval itself happens in Codex's own UI, so the hook needn't hang around waiting
	var permission *codexHookGroup
	for _, g := range file.Hooks["PermissionRequest"] {
		if groupHasSparkHook(g, script) {
			permission = &g
		}
	}
	if permission == nil {
		t.Fatal("PermissionRequest should carry the spark hook")
	}
	if permission.Hooks[0].Timeout > 600 {
		t.Errorf("PermissionRequest timeout should not exceed the 600s default, got %d", permission.Hooks[0].Timeout)
	}
	if !CodexHooksInstalled(home) {
		t.Error("hooks should report as installed")
	}
}

// Other tools use hooks.json too, and a merge must not squeeze them out
func TestInstallCodexHooksPreservesOthers(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
	  "hooks": {
	    "Stop": [{"hooks":[{"type":"command","command":"/usr/local/bin/other-tool","timeout":10}]}],
	    "SessionEnd": [{"hooks":[{"type":"command","command":"/usr/local/bin/another","timeout":3}]}]
	  }
	}`
	if err := os.WriteFile(CodexHooksPath(home), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	script := CodexHookScriptPath(home)
	if err := InstallCodexHooks(home, script); err != nil {
		t.Fatal(err)
	}
	file := readHooksFile(t, home)

	kept := map[string]bool{}
	for event, groups := range file.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Command != "'"+script+"'" {
					kept[event+" "+h.Command] = true
				}
			}
		}
	}
	if !kept["Stop /usr/local/bin/other-tool"] {
		t.Error("existing Stop hook was dropped")
	}
	if !kept["SessionEnd /usr/local/bin/another"] {
		t.Error("existing SessionEnd hook was dropped")
	}

	// Leave a backup behind so a rollback is easy
	if _, err := os.Stat(CodexHooksPath(home) + ".spark-backup"); err != nil {
		t.Errorf("backup missing: %v", err)
	}
}

// Installing twice must not stack up duplicate hooks
func TestInstallCodexHooksIsIdempotent(t *testing.T) {
	home := t.TempDir()
	script := CodexHookScriptPath(home)
	for range 3 {
		if err := InstallCodexHooks(home, script); err != nil {
			t.Fatal(err)
		}
	}
	file := readHooksFile(t, home)
	for _, e := range CodexHookEvents {
		n := 0
		for _, g := range file.Hooks[e.Name] {
			if groupHasSparkHook(g, script) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s has %d spark hooks, want 1", e.Name, n)
		}
	}
}

func TestInstallCodexHooksRejectsBrokenFile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CodexHooksPath(home), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Don't silently overwrite a config the user broke
	if err := InstallCodexHooks(home, CodexHookScriptPath(home)); err == nil {
		t.Error("broken hooks file should be reported, not overwritten")
	}
	content, err := os.ReadFile(CodexHooksPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "{not json" {
		t.Error("broken file was modified")
	}
}

// Event names must match the ones Codex knows, or the hook never fires
func TestCodexHookEventNames(t *testing.T) {
	valid := map[string]bool{
		"SessionStart": true, "SessionEnd": true, "UserPromptSubmit": true,
		"PreToolUse": true, "PostToolUse": true, "PermissionRequest": true,
		"Stop": true, "SubagentStart": true, "SubagentStop": true,
		"PreCompact": true, "PostCompact": true, "Notification": true,
	}
	for _, e := range CodexHookEvents {
		if !valid[e.Name] {
			t.Errorf("unknown Codex hook event %q", e.Name)
		}
		if e.Timeout <= 0 {
			t.Errorf("%s needs a positive timeout", e.Name)
		}
	}
}

// The installed script must be executable and its contents must run as sh
func TestInstallCodexHookScriptIsExecutable(t *testing.T) {
	home := t.TempDir()
	if err := InstallCodexHookScript(home); err != nil {
		t.Fatal(err)
	}
	path := CodexHookScriptPath(home)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("hook script is not executable: %v", info.Mode())
	}
	if info.Size() == 0 {
		t.Error("hook script is empty")
	}
	// The script must mention the path hooks.json points at, or that path leads nowhere
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "codex-hooks") {
		t.Error("hook script does not write events to the codex-hooks directory")
	}
}

// End to end: install the script and hooks, run the hook process for real, and confirm
// the event lands on disk and the return value is right
func TestCodexHookRunsEndToEnd(t *testing.T) {
	home := t.TempDir()
	if err := InstallCodexHookScript(home); err != nil {
		t.Fatal(err)
	}
	if err := InstallCodexHooks(home, CodexHookScriptPath(home)); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"` + threadA + `","hook_event_name":"UserPromptSubmit","cwd":"/tmp/acme","prompt":"run the end to end test"}`
	cmd := exec.Command("/bin/sh", CodexHookScriptPath(home))
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook run failed: %v (out=%s)", err, out)
	}
	// Empty stdout is success as far as Codex is concerned; stray fields are recorded as
	// a failure on PermissionRequest
	if len(out) != 0 {
		t.Errorf("hook stdout = %q, want empty", out)
	}

	sig, ok := ReadCodexSignal(CodexHookDir(home), threadA)
	if !ok {
		t.Fatalf("no signal written to %s", CodexHookDir(home))
	}
	if sig.Kind != HookBusy || sig.Cwd != "/tmp/acme" {
		t.Errorf("unexpected signal %+v", sig)
	}
}

// Codex clamps SessionEnd's timeout to 3 seconds and warns, so install at the cap
func TestCodexHookTimeoutsRespectLimits(t *testing.T) {
	for _, e := range CodexHookEvents {
		switch e.Name {
		case "SessionEnd":
			if e.Timeout > maxSessionEndTimeout {
				t.Errorf("SessionEnd timeout %d exceeds the %d second cap", e.Timeout, maxSessionEndTimeout)
			}
			if e.Timeout <= 0 {
				t.Errorf("SessionEnd timeout must be positive, got %d", e.Timeout)
			}
		default:
			// Other events default to 600 seconds; a few seconds is plenty and anything
			// longer just holds Codex up
			if e.Timeout <= 0 || e.Timeout > 600 {
				t.Errorf("%s timeout %d is out of range", e.Name, e.Timeout)
			}
		}
	}
}

// In the installed hooks.json, SessionEnd must not exceed 3 seconds or Codex warns at
// startup
func TestInstalledHooksRespectSessionEndCap(t *testing.T) {
	home := t.TempDir()
	if err := InstallCodexHooks(home, CodexHookScriptPath(home)); err != nil {
		t.Fatal(err)
	}
	for _, g := range readHooksFile(t, home).Hooks["SessionEnd"] {
		for _, h := range g.Hooks {
			if h.Timeout > maxSessionEndTimeout {
				t.Errorf("installed SessionEnd timeout = %d, cap is %d", h.Timeout, maxSessionEndTimeout)
			}
		}
	}
}

// The hook must not emit continue: PermissionRequest and SessionEnd don't support that
// field and record it as a hook failure. PermissionRequest also takes allow/deny, so a
// stray field could affect the approval decision
func TestHookScriptStaysSilent(t *testing.T) {
	home := t.TempDir()
	if err := InstallCodexHookScript(home); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(CodexHookScriptPath(home))
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	// Only inspect what actually gets written; comments explaining why continue is
	// omitted are expected
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "continue") {
			t.Errorf("hook script must not emit a continue field, found: %s", trimmed)
		}
	}
	for _, forbidden := range []string{"permissionDecision", "hookSpecificOutput", `"decision"`} {
		if strings.Contains(script, forbidden) {
			t.Errorf("hook script must not emit %s; it only observes", forbidden)
		}
	}
}

// Empty stdout is success as far as Codex is concerned, and every path should exit so
func TestHookScriptProducesNoStdout(t *testing.T) {
	home := t.TempDir()
	if err := InstallCodexHookScript(home); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, payload string }{
		{"permission request", `{"session_id":"` + threadA + `","hook_event_name":"PermissionRequest","cwd":"/tmp","tool_name":"Bash"}`},
		{"session end", `{"session_id":"` + threadA + `","hook_event_name":"SessionEnd","cwd":"/tmp","reason":"other"}`},
		{"stop", `{"session_id":"` + threadA + `","hook_event_name":"Stop","cwd":"/tmp"}`},
		{"tool use", `{"session_id":"` + threadA + `","hook_event_name":"PostToolUse","cwd":"/tmp"}`},
		{"garbage", `not json`},
		{"empty", ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", CodexHookScriptPath(home))
			cmd.Stdin = strings.NewReader(c.payload)
			cmd.Env = append(os.Environ(), "HOME="+home)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("hook failed: %v", err)
			}
			if len(out) != 0 {
				t.Errorf("hook stdout = %q, want empty", out)
			}
		})
	}
}
