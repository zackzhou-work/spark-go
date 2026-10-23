package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// cursorHookTimeout is how long Cursor waits for one report. The helper only writes a
	// small file, so this is generous
	cursorHookTimeout = 5
	// CursorHookFlag is the argument that puts the helper in collector mode
	CursorHookFlag = "--cursor-hook"
	cursorProtocol = 1
)

// CursorHooksPath is the user-level hooks file Cursor runs for every project
func CursorHooksPath(home string) string {
	return filepath.Join(home, ".cursor", "hooks.json")
}

// CursorHelperPath is where the executable the hooks call is copied to. It is fixed, so the
// hooks keep working after the app bundle is moved or quit
func CursorHelperPath(home string) string {
	return filepath.Join(home, ".config", "spark", "bin", "spark-cursor-hook")
}

// CursorInstallPath records what was installed, so a stale helper is noticeable
func CursorInstallPath(home string) string {
	return filepath.Join(CursorDir(home), "install.json")
}

type cursorInstallMeta struct {
	Protocol         int    `json:"protocol"`
	CollectorVersion int    `json:"collector_version"`
	InstalledAt      string `json:"installed_at"`
}

// CursorHookState is what is on disk: nothing, what this build installs, a copy from an older
// release, or a file spark must not touch
type CursorHookState int

const (
	// CursorHooksMissing: spark's entries aren't there
	CursorHooksMissing CursorHookState = iota
	// CursorHooksCurrent: entries, helper and metadata all match this build
	CursorHooksCurrent
	// CursorHooksOutdated: entries from an older build, a duplicate, a helper that can't run
	CursorHooksOutdated
	// CursorHooksBroken: the file can't be parsed or has a version spark doesn't know
	CursorHooksBroken
)

func (s CursorHookState) String() string {
	switch s {
	case CursorHooksCurrent:
		return "current"
	case CursorHooksOutdated:
		return "outdated"
	case CursorHooksBroken:
		return "broken"
	default:
		return "missing"
	}
}

// CursorHooksOnDisk reads the hooks file and the install metadata. It says whether the
// configuration is in place, never whether Cursor has loaded it or reported anything
func CursorHooksOnDisk(home string) CursorHookState {
	content, err := os.ReadFile(CursorHooksPath(home))
	if err != nil {
		return CursorHooksMissing
	}
	doc, err := parseCursorHooks(content)
	if err != nil {
		return CursorHooksBroken
	}
	helper := CursorHelperPath(home)
	found := 0
	duplicated := false
	for _, event := range cursorHookEvents {
		entries := doc.entries(event)
		ours := 0
		for _, e := range entries {
			if isCursorHookCommand(entryCommand(e), helper) {
				ours++
			}
		}
		if ours > 1 {
			duplicated = true
		}
		if ours > 0 {
			found++
		}
	}
	if found == 0 {
		return CursorHooksMissing
	}
	if found != len(cursorHookEvents) || duplicated {
		return CursorHooksOutdated
	}
	info, err := os.Stat(helper)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return CursorHooksOutdated
	}
	meta, ok := readCursorInstallMeta(home)
	if !ok || meta.Protocol != cursorProtocol || meta.CollectorVersion != cursorCollectorVersion {
		return CursorHooksOutdated
	}
	return CursorHooksCurrent
}

func readCursorInstallMeta(home string) (cursorInstallMeta, bool) {
	content, err := os.ReadFile(CursorInstallPath(home))
	if err != nil {
		return cursorInstallMeta{}, false
	}
	var meta cursorInstallMeta
	if json.Unmarshal(content, &meta) != nil {
		return cursorInstallMeta{}, false
	}
	return meta, true
}

// InstallCursorHooks copies the executable to the fixed helper path and merges spark's events
// into the user-level hooks file. Every other key, event and entry is preserved
func InstallCursorHooks(home, executable string) error {
	release, err := lockCursorInstall(home)
	if err != nil {
		return err
	}
	defer release()

	path := CursorHooksPath(home)
	original, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	var doc cursorHooksDoc
	if existed {
		doc, err = parseCursorHooks(original)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else {
		doc = newCursorHooksDoc()
	}

	// The helper lands first: a config that names it must never point at nothing
	if err := installCursorHelper(home, executable); err != nil {
		return err
	}

	helper := CursorHelperPath(home)
	cmd := shellQuote(helper) + " " + CursorHookFlag
	for _, event := range cursorHookEvents {
		doc.setSparkEntry(event, cmd, helper)
	}
	merged, err := doc.marshal()
	if err != nil {
		return err
	}
	if !existed || !bytes.Equal(merged, original) {
		// Keep the previous bytes so a bad merge can be undone by hand
		if existed {
			if err := os.WriteFile(path+".spark-backup", original, 0o600); err != nil {
				return err
			}
		}
		if err := writeCursorHooksIfUnchanged(path, original, merged); err != nil {
			return err
		}
	}
	return writeCursorInstallMeta(home)
}

// RemoveCursorHooks deletes spark's own entries, the helper and the install metadata. The
// event files stay for the usual expiry to clear, and the user's other hooks are left alone
func RemoveCursorHooks(home string) error {
	release, err := lockCursorInstall(home)
	if err != nil {
		return err
	}
	defer release()

	path := CursorHooksPath(home)
	original, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if readErr == nil {
		doc, parseErr := parseCursorHooks(original)
		if parseErr != nil {
			// Uninstall can't merge into a file it can't read, and must not rewrite it
			return fmt.Errorf("%s: %w", path, parseErr)
		}
		helper := CursorHelperPath(home)
		for _, event := range cursorHookEvents {
			doc.dropSparkEntries(event, helper)
		}
		merged, marshalErr := doc.marshal()
		if marshalErr != nil {
			return marshalErr
		}
		if !bytes.Equal(merged, original) {
			if err := writeCursorHooksIfUnchanged(path, original, merged); err != nil {
				return err
			}
		}
	}
	for _, p := range []string{CursorHelperPath(home), CursorInstallPath(home)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// installCursorHelper writes the copy the hooks call, replacing any older one in place
func installCursorHelper(home, executable string) error {
	info, err := os.Stat(executable)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", executable)
	}
	body, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	dir := filepath.Dir(CursorHelperPath(home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(CursorHelperPath(home), body, 0o755)
}

func writeCursorInstallMeta(home string) error {
	if err := os.MkdirAll(CursorDir(home), 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(cursorInstallMeta{
		Protocol:         cursorProtocol,
		CollectorVersion: cursorCollectorVersion,
		InstalledAt:      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return writeFileAtomic(CursorInstallPath(home), body, 0o600)
}

func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// writeCursorHooksIfUnchanged lands the merge only while the file still holds the bytes it
// was merged from. An editor or another tool can write between the two, and that is outside
// any lock spark can take
func writeCursorHooksIfUnchanged(path string, expected, content []byte) error {
	current, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		current = nil
	case err != nil:
		return err
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("%s changed while spark was merging; nothing was written", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, content, 0o600)
}

// shellQuote wraps a path for the shell the hook command runs in
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isCursorHookCommand recognises spark's own entry by the exact helper path and argument, not
// by a name that happens to contain "spark"
func isCursorHookCommand(cmd, helper string) bool {
	cmd = strings.TrimSpace(cmd)
	return cmd == shellQuote(helper)+" "+CursorHookFlag || cmd == helper+" "+CursorHookFlag
}

// cursorHooksDoc is the hooks file as generic JSON, so keys spark doesn't know survive a
// merge
type cursorHooksDoc struct {
	top map[string]any
}

func newCursorHooksDoc() cursorHooksDoc {
	return cursorHooksDoc{top: map[string]any{
		"version": json.Number("1"),
		"hooks":   map[string]any{},
	}}
}

func parseCursorHooks(content []byte) (cursorHooksDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		return cursorHooksDoc{}, fmt.Errorf("parsing hooks file: %w", err)
	}
	if top == nil {
		return cursorHooksDoc{}, fmt.Errorf("hooks file is not an object")
	}
	version, ok := top["version"].(json.Number)
	if !ok || version.String() != "1" {
		return cursorHooksDoc{}, fmt.Errorf("hooks version %v is not one spark knows", top["version"])
	}
	if hooks, ok := top["hooks"]; ok {
		if _, ok := hooks.(map[string]any); !ok {
			return cursorHooksDoc{}, fmt.Errorf("hooks is not an object")
		}
	}
	return cursorHooksDoc{top: top}, nil
}

func (d cursorHooksDoc) hooksMap() map[string]any {
	hooks, ok := d.top["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		d.top["hooks"] = hooks
	}
	return hooks
}

// entries returns one event's entries without disturbing the rest
func (d cursorHooksDoc) entries(event string) []any {
	raw, ok := d.hooksMap()[event]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	return list
}

func entryCommand(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	cmd, _ := m["command"].(string)
	return cmd
}

func (d cursorHooksDoc) setSparkEntry(event, cmd, helper string) {
	ours := map[string]any{"command": cmd, "timeout": json.Number(fmt.Sprint(cursorHookTimeout))}
	hooks := d.hooksMap()
	existing := d.entries(event)
	kept := make([]any, 0, len(existing)+1)
	replaced := false
	for _, e := range existing {
		if isCursorHookCommand(entryCommand(e), helper) {
			// Duplicates from an earlier merge collapse into the one entry
			if replaced {
				continue
			}
			kept = append(kept, ours)
			replaced = true
			continue
		}
		kept = append(kept, e)
	}
	if !replaced {
		kept = append(kept, ours)
	}
	hooks[event] = kept
}

func (d cursorHooksDoc) dropSparkEntries(event, helper string) {
	hooks := d.hooksMap()
	existing, ok := hooks[event].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(existing))
	for _, e := range existing {
		if isCursorHookCommand(entryCommand(e), helper) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		delete(hooks, event)
		return
	}
	hooks[event] = kept
}

func (d cursorHooksDoc) marshal() ([]byte, error) {
	body, err := json.MarshalIndent(d.top, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}
