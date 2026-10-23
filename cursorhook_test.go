package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

var (
	buildOnce   sync.Once
	builtBinary string
	buildErr    error
)

// sparkBinary builds the app once per test run. The hook path only exists in the real
// process, so this is the one place it can be checked
func sparkBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "spark-hook-test")
		if err != nil {
			buildErr = err
			return
		}
		builtBinary = filepath.Join(dir, "spark")
		cmd := exec.Command("go", "build", "-o", builtBinary, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("go build: %s", out)
		}
	})
	if buildErr != nil {
		t.Fatalf("building spark: %v", buildErr)
	}
	return builtBinary
}

// A hook run has to finish on its own: if it reached the app, the process would stay up with a
// window and Cursor would wait for it on every turn
func TestCursorHookRunIsShortLivedAndSilent(t *testing.T) {
	bin := sparkBinary(t)
	home := t.TempDir()
	payload := `{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","generation_id":"gen-1","hook_event_name":"beforeSubmitPrompt","workspace_roots":["/tmp/project"],"cursor_version":"3.24.12","prompt":"secret prompt text"}`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--cursor-hook")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	took := time.Since(start)
	if err != nil {
		t.Fatalf("hook run failed after %s: %v (stderr %s)", took, err, stderr.String())
	}
	if took > 20*time.Second {
		t.Errorf("hook run took %s, which is a window starting", took)
	}
	if stdout.Len() != 0 {
		t.Errorf("hook wrote to stdout, which Cursor reads as a decision: %q", stdout.String())
	}

	days, err := os.ReadDir(filepath.Join(home, ".config", "spark", "cursor", "events"))
	if err != nil || len(days) != 1 {
		t.Fatalf("want one day of events, got %v err %v", days, err)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".config", "spark", "cursor", "events", days[0].Name()))
	if err != nil || len(entries) != 1 {
		t.Fatalf("want one event file, got %v err %v", entries, err)
	}
	body, err := os.ReadFile(filepath.Join(home, ".config", "spark", "cursor", "events", days[0].Name(), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("secret prompt text")) {
		t.Error("the prompt reached disk")
	}
	if !bytes.Contains(body, []byte(`"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"`)) {
		t.Errorf("event file lost the conversation id: %s", body)
	}
}

// Bad input is dropped quietly: a hook failure must never reach the agent loop
func TestCursorHookRunSwallowsBadInput(t *testing.T) {
	bin := sparkBinary(t)
	home := t.TempDir()
	for name, payload := range map[string]string{
		"broken":   `{"conversation_id":`,
		"no id":    `{"hook_event_name":"stop"}`,
		"empty":    ``,
		"too long": `{"conversation_id":"` + strings.Repeat("a", 9<<20) + `","hook_event_name":"stop"}`,
	} {
		cmd := exec.Command(bin, "--cursor-hook")
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Stdin = strings.NewReader(payload)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err != nil {
			t.Errorf("%s: hook should still exit 0, got %v", name, err)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: stdout should stay empty, got %q", name, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "spark", "cursor", "events")); !os.IsNotExist(err) {
		t.Error("nothing should have been written")
	}
}

// The documented way out only takes spark's own entries with it
func TestCursorUninstallRunLeavesOtherHooks(t *testing.T) {
	bin := sparkBinary(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"version":1,"hooks":{"stop":[{"command":"/opt/other --stop"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".cursor", "hooks.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "spark")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := monitor.InstallCursorHooks(home, exe); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "--cursor-uninstall")
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("uninstall failed: %v (stderr %s)", err, stderr.String())
	}
	body, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("/opt/other --stop")) {
		t.Errorf("another tool's hook was removed: %s", body)
	}
	if bytes.Contains(body, []byte("spark-cursor-hook")) {
		t.Errorf("spark's hook survived: %s", body)
	}
	if _, err := os.Stat(monitor.CursorHelperPath(home)); !os.IsNotExist(err) {
		t.Error("the helper should be gone")
	}
}

// The command line install is the documented path for a build without a window
func TestCursorInstallRunWritesACurrentInstall(t *testing.T) {
	bin := sparkBinary(t)
	home := t.TempDir()
	cmd := exec.Command(bin, "--cursor-install")
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("install failed: %v (stderr %s)", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	// The helper is the binary that just installed it, at the fixed path the hooks name
	installed, err := os.ReadFile(filepath.Join(home, ".config", "spark", "bin", "spark-cursor-hook"))
	if err != nil {
		t.Fatal(err)
	}
	running, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, running) {
		t.Error("the helper should be a copy of the installing binary")
	}

	// A second install changes nothing
	before, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	again := exec.Command(bin, "--cursor-install")
	again.Env = append(os.Environ(), "HOME="+home)
	if err := again.Run(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(home, ".cursor", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("installing twice should not change the file")
	}
}

// A home spark can't write to is Cursor's problem to ignore: the hook still exits 0 and says
// nothing
func TestCursorHookRunSurvivesAnUnwritableHome(t *testing.T) {
	bin := sparkBinary(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })

	cmd := exec.Command(bin, "--cursor-hook")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader(`{"conversation_id":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","hook_event_name":"stop","status":"completed"}`)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Errorf("a failed write must not fail the hook: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should stay empty, got %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Error("nothing should have been created")
	}
}
