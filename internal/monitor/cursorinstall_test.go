package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fakeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spark")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readHooks(t *testing.T, home string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(CursorHooksPath(home))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(content)))
	dec.UseNumber()
	var file map[string]any
	if err := dec.Decode(&file); err != nil {
		t.Fatal(err)
	}
	return file
}

func hookCommands(t *testing.T, file map[string]any, event string) []string {
	t.Helper()
	raw, ok := file["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks is not an object: %T", file["hooks"])
	}
	entries, ok := raw[event].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := entry["command"].(string); ok {
			out = append(out, cmd)
		}
	}
	return out
}

func TestInstallCursorHooksFromScratch(t *testing.T) {
	home := t.TempDir()
	exe := fakeExecutable(t)
	if got := CursorHooksOnDisk(home); got != CursorHooksMissing {
		t.Fatalf("state before install: %v", got)
	}
	if err := InstallCursorHooks(home, exe); err != nil {
		t.Fatal(err)
	}

	file := readHooks(t, home)
	if file["version"] != json.Number("1") {
		t.Errorf("version %#v", file["version"])
	}
	for _, event := range cursorHookEvents {
		cmds := hookCommands(t, file, event)
		if len(cmds) != 1 {
			t.Fatalf("%s: want one command, got %v", event, cmds)
		}
		if !isCursorHookCommand(cmds[0], CursorHelperPath(home)) {
			t.Errorf("%s: %q doesn't name spark's helper", event, cmds[0])
		}
	}

	// The helper has to be a copy at a fixed path: hooks must keep working when the app is
	// moved or quit
	helper, err := os.ReadFile(CursorHelperPath(home))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(helper) != string(want) {
		t.Error("the helper should be a copy of the running executable")
	}
	info, err := os.Stat(CursorHelperPath(home))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("helper mode %v err %v", info.Mode(), err)
	}
	if filepath.Dir(CursorHelperPath(home)) != filepath.Join(home, ".config", "spark", "bin") {
		t.Errorf("helper lives at %s", CursorHelperPath(home))
	}
	if meta, err := os.ReadFile(CursorInstallPath(home)); err != nil {
		t.Fatal(err)
	} else {
		var m cursorInstallMeta
		if err := json.Unmarshal(meta, &m); err != nil {
			t.Fatal(err)
		}
		if m.CollectorVersion != cursorCollectorVersion {
			t.Errorf("install meta %+v", m)
		}
	}
	if got := CursorHooksOnDisk(home); got != CursorHooksCurrent {
		t.Errorf("state after install: %v", got)
	}
}

// Users already have hooks from other tools; a merge must leave every one of them alone
func TestInstallCursorHooksKeepsEverythingElse(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
	  "version": 1,
	  "$schema": "https://example.com/hooks.schema.json",
	  "hooks": {
	    "stop": [
	      {"command": "/opt/other/bin/report --stop"},
	      {"command": "/opt/other/bin/second", "timeout": 10}
	    ],
	    "beforeReadFile": [{"command": "/opt/other/bin/read"}],
	    "afterFileEdit": [{"command": "/opt/other/bin/fmt", "matcher": "Write"}]
	  }
	}`
	if err := os.WriteFile(CursorHooksPath(home), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(CursorHooksPath(home))
	if err != nil {
		t.Fatal(err)
	}

	if err := InstallCursorHooks(home, fakeExecutable(t)); err != nil {
		t.Fatal(err)
	}
	file := readHooks(t, home)
	if file["$schema"] != "https://example.com/hooks.schema.json" {
		t.Errorf("unknown top-level key dropped: %#v", file)
	}
	if got := hookCommands(t, file, "beforeReadFile"); len(got) != 1 || got[0] != "/opt/other/bin/read" {
		t.Errorf("an event spark doesn't use changed: %v", got)
	}
	if got := hookCommands(t, file, "afterFileEdit"); len(got) != 1 || got[0] != "/opt/other/bin/fmt" {
		t.Errorf("other tool's edit hook changed: %v", got)
	}
	stop := hookCommands(t, file, "stop")
	if len(stop) != 3 {
		t.Fatalf("stop should hold both other tools and spark, got %v", stop)
	}
	// Order of the entry spark didn't own is preserved
	if stop[0] != "/opt/other/bin/report --stop" || stop[1] != "/opt/other/bin/second" {
		t.Errorf("existing stop entries reordered: %v", stop)
	}
	if backup, err := os.ReadFile(CursorHooksPath(home) + ".spark-backup"); err != nil || string(backup) != string(before) {
		t.Errorf("the original file should be kept as a backup, err %v", err)
	}

	// Installing again changes nothing: same content, still one entry per event
	once, err := os.ReadFile(CursorHooksPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallCursorHooks(home, fakeExecutable(t)); err != nil {
		t.Fatal(err)
	}
	twice, err := os.ReadFile(CursorHooksPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("a second install should be a no-op:\n%s\n%s", once, twice)
	}
	if got := hookCommands(t, file, "stop"); len(got) != 3 {
		t.Errorf("second install duplicated entries: %v", got)
	}
}

// Paths with spaces are the norm on macOS, and a user's home can hold a quote
func TestCursorHookCommandQuotesAwkwardPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home with space", "it's here")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallCursorHooks(home, fakeExecutable(t)); err != nil {
		t.Fatal(err)
	}
	file := readHooks(t, home)
	cmd := hookCommands(t, file, "stop")
	if len(cmd) != 1 {
		t.Fatalf("got %v", cmd)
	}
	if !strings.Contains(cmd[0], `'\''`) {
		t.Errorf("the quote in the path should be escaped for the shell: %q", cmd[0])
	}
	if !isCursorHookCommand(cmd[0], CursorHelperPath(home)) {
		t.Errorf("spark should recognise its own command: %q", cmd[0])
	}
	// A command that only mentions spark is not spark's
	if isCursorHookCommand("/opt/spark-cursor-hook --cursor-hook", CursorHelperPath(home)) {
		t.Error("an unrelated helper with a similar name must not be adopted")
	}
	if isCursorHookCommand(shellQuote(CursorHelperPath(home))+" --other", CursorHelperPath(home)) {
		t.Error("the argument has to be --cursor-hook")
	}
}

func TestInstallCursorHooksRefusesToRewriteBrokenOrUnknownFiles(t *testing.T) {
	for name, content := range map[string]string{
		"broken json":         `{"hooks":`,
		"unknown version":     `{"version": 2, "hooks": {}}`,
		"hooks not an object": `{"version": 1, "hooks": []}`,
	} {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(CursorHooksPath(home), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := InstallCursorHooks(home, fakeExecutable(t)); err == nil {
			t.Errorf("%s should fail", name)
		}
		after, err := os.ReadFile(CursorHooksPath(home))
		if err != nil || string(after) != content {
			t.Errorf("%s should be left exactly as it was", name)
		}
		if got := CursorHooksOnDisk(home); got != CursorHooksBroken {
			t.Errorf("%s: state %v", name, got)
		}
		if _, err := os.Stat(CursorHelperPath(home)); err == nil {
			t.Errorf("%s: nothing should be installed when the merge fails", name)
		}
	}
}

// An editor open on the file is outside any lock spark can take, so the write checks the
// bytes it parsed are still the bytes on disk
func TestCursorHooksWriteDetectsAnOutsideChange(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(CursorHooksPath(home), []byte(`{"version":1,"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := []byte(`{"version":1,"hooks":{}}`)
	edited := `{"version":1,"hooks":{"stop":[{"command":"/opt/other"}]}}`
	if err := os.WriteFile(CursorHooksPath(home), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeCursorHooksIfUnchanged(CursorHooksPath(home), stale, []byte(`{"version":1,"hooks":{"stop":[]}}`))
	if err == nil {
		t.Fatal("a file edited underneath the merge should be a conflict")
	}
	after, err := os.ReadFile(CursorHooksPath(home))
	if err != nil || string(after) != edited {
		t.Errorf("the user's edit must survive, got %s", after)
	}
}

func TestOlderHelperIsOutdatedAndReplaced(t *testing.T) {
	home := t.TempDir()
	if err := InstallCursorHooks(home, fakeExecutable(t)); err != nil {
		t.Fatal(err)
	}
	// An older release reports its own collector version, which is what marks the install as
	// stale rather than hashing the helper on every scan
	if err := os.WriteFile(CursorInstallPath(home), []byte(`{"protocol":1,"collector_version":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CursorHooksOnDisk(home); got != CursorHooksOutdated {
		t.Errorf("an older install should read as outdated, got %v", got)
	}
	exe := fakeExecutable(t)
	if err := InstallCursorHooks(home, exe); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(CursorHelperPath(home)); err != nil || string(got) != string(want) {
		t.Errorf("the helper should be updated, err %v", err)
	}
	if got := CursorHooksOnDisk(home); got != CursorHooksCurrent {
		t.Errorf("state after update: %v", got)
	}

	// Removing the executable bit is the other way an install stops working
	if err := os.Chmod(CursorHelperPath(home), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CursorHooksOnDisk(home); got != CursorHooksOutdated {
		t.Errorf("a helper that can't run should read as outdated, got %v", got)
	}
}

func TestRemoveCursorHooksTakesOnlySparksEntries(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"version":1,"hooks":{"stop":[{"command":"/opt/other --stop"}],"beforeSubmitPrompt":[{"command":"/opt/other --start"}]}}`
	if err := os.WriteFile(CursorHooksPath(home), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallCursorHooks(home, fakeExecutable(t)); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCursorHooks(home); err != nil {
		t.Fatal(err)
	}
	file := readHooks(t, home)
	for _, event := range cursorHookEvents {
		for _, cmd := range hookCommands(t, file, event) {
			if isCursorHookCommand(cmd, CursorHelperPath(home)) {
				t.Errorf("%s still runs spark: %q", event, cmd)
			}
		}
	}
	if got := hookCommands(t, file, "stop"); len(got) != 1 || got[0] != "/opt/other --stop" {
		t.Errorf("other tools' stop hook should stay: %v", got)
	}
	if got := hookCommands(t, file, "beforeSubmitPrompt"); len(got) != 1 {
		t.Errorf("other tools' submit hook should stay: %v", got)
	}
	if _, err := os.Stat(CursorHelperPath(home)); !os.IsNotExist(err) {
		t.Error("removing should take the helper with it")
	}
	if _, err := os.Stat(CursorInstallPath(home)); !os.IsNotExist(err) {
		t.Error("removing should take the install metadata with it")
	}
	if got := CursorHooksOnDisk(home); got != CursorHooksMissing {
		t.Errorf("state after removal: %v", got)
	}
	// Uninstalling twice is not an error
	if err := RemoveCursorHooks(home); err != nil {
		t.Errorf("second removal: %v", err)
	}
}

// Two Spark windows installing at once must not lose each other's merge or leave a half-written
// file: a lock serialises them, and the unchanged-bytes check covers everyone else
func TestConcurrentInstallsSerialise(t *testing.T) {
	home := t.TempDir()
	exe := fakeExecutable(t)
	var (
		wg   sync.WaitGroup
		errs = make([]error, 4)
	)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = InstallCursorHooks(home, exe)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("install %d: %v", i, err)
		}
	}
	file := readHooks(t, home)
	for _, event := range cursorHookEvents {
		cmds := hookCommands(t, file, event)
		ours := 0
		for _, cmd := range cmds {
			if isCursorHookCommand(cmd, CursorHelperPath(home)) {
				ours++
			}
		}
		if ours != 1 {
			t.Errorf("%s: %d spark entries, want 1", event, ours)
		}
	}
	if got := CursorHooksOnDisk(home); got != CursorHooksCurrent {
		t.Errorf("state %v", got)
	}
}
