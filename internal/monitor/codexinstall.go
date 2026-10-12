package monitor

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CodexHookEvents are the Codex events spark cares about.
//
// Timeouts follow Codex's rules: omitting one gives most events a 600 second default,
// but SessionEnd defaults to 1 second and caps at 3, so anything higher is clamped and
// logged as a warning. The rest only need to report a state change, so a few seconds is
// plenty. PermissionRequest doesn't need to wait -- spark only shows it in the notch and
// the approval itself still happens in Codex's own UI
var CodexHookEvents = []struct {
	Name    string
	Matcher string
	Timeout int
}{
	{"SessionStart", "startup|resume|clear|compact", 5},
	{"UserPromptSubmit", "", 5},
	{"PreToolUse", "", 5},
	{"PostToolUse", "", 5},
	{"PermissionRequest", "", 5},
	{"Stop", "", 5},
	{"SubagentStart", "", 5},
	{"SubagentStop", "", 5},
	// SessionEnd caps at 3 seconds; anything higher is clamped and warned about
	{"SessionEnd", "", 3},
}

type codexHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type codexHookGroup struct {
	Matcher string           `json:"matcher,omitempty"`
	Hooks   []codexHookEntry `json:"hooks"`
}

type codexHooksFile struct {
	Hooks map[string][]codexHookGroup `json:"hooks"`
}

// Codex's hard caps on hook timeouts. Exceeding one clamps the value and logs a warning
// at startup
const (
	// maxSessionEndTimeout caps SessionEnd, whose default is only 1 second
	maxSessionEndTimeout = 3
	// maxInterruptTimeout caps Interrupt, whose default is 1 second
	maxInterruptTimeout = 3
)

// InstallCodexHooks merges spark's hooks into ~/.codex/hooks.json, leaving existing
// hooks alone. Codex re-asks for trust whenever the file changes; that's its own
// mechanism, so the user just confirms once the first time a new event runs
func InstallCodexHooks(home, scriptPath string) error {
	path := CodexHooksPath(home)
	var file codexHooksFile
	if content, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(content, &file); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		// Back up before touching it, so a bad merge can be undone by hand
		if err := os.WriteFile(path+".spark-backup", content, 0o600); err != nil {
			return err
		}
	}
	if file.Hooks == nil {
		file.Hooks = map[string][]codexHookGroup{}
	}

	for _, e := range CodexHookEvents {
		group := codexHookGroup{
			Matcher: e.Matcher,
			Hooks: []codexHookEntry{{
				Type:    "command",
				Command: fmt.Sprintf("'%s'", scriptPath),
				Timeout: e.Timeout,
			}},
		}
		existing := file.Hooks[e.Name]
		replaced := false
		for i, g := range existing {
			if groupHasSparkHook(g, scriptPath) {
				existing[i] = group
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, group)
		}
		file.Hooks[e.Name] = existing
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}

func groupHasSparkHook(g codexHookGroup, scriptPath string) bool {
	for _, h := range g.Hooks {
		if h.Command == fmt.Sprintf("'%s'", scriptPath) {
			return true
		}
	}
	return false
}

// CodexHookScriptPath is where the hook script gets installed
func CodexHookScriptPath(home string) string {
	return filepath.Join(home, ".config", "spark", "spark-codex-hook.sh")
}

//go:embed hookscript/spark-codex-hook.sh
var codexHookScript []byte

// InstallCodexHookScript writes the embedded hook script into place. It has to land
// first, or hooks.json points at a file that doesn't exist and every Codex event fails
func InstallCodexHookScript(home string) error {
	path := CodexHookScriptPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, codexHookScript, 0o755)
}
