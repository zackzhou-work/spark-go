package monitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// harness drives the installed extension the way pi would and checks the state file it
// leaves behind: one line per event, so the Go test can read a mismatch off the output
const harness = `
import ext from "EXTENSION";
import { existsSync, readFileSync, statSync } from "node:fs";

const handlers = new Map();
const pi = { on: (name, fn) => handlers.set(name, [...(handlers.get(name) ?? []), fn]) };
// fire waits for the write the event should cause rather than a fixed sleep, since the
// extension folds bursts of state changes together
const snapshot = () => {
  try {
    const info = statSync(file);
    return info.mtimeMs + ":" + info.size;
  } catch {
    return "none";
  }
};
const fire = async (name, event = {}, settle = true) => {
  const before = snapshot();
  for (const fn of handlers.get(name) ?? []) await fn(event, ctx);
  if (!settle) {
    await new Promise((r) => setTimeout(r, 100));
    return;
  }
  const deadline = Date.now() + 2000;
  while (Date.now() < deadline && snapshot() === before) {
    await new Promise((r) => setTimeout(r, 20));
  }
};
const ctx = {
  mode: "tui",
  sessionManager: { getSessionFile: () => "HOME/sessions/abc.jsonl", getSessionId: () => "abc" },
  isIdle: () => true,
};

ext(pi);
const file = "HOME/.config/spark/pi/abc.json";

const line = (label) => {
  const body = existsSync(file) ? JSON.parse(readFileSync(file, "utf8")) : {};
  return label + ": " + (body.state || "none") + " kind=" + (body.blockKind || "-") + " tool=" + (body.tool || "-") + " input=" + ((body.lastInputAtMs ?? 0) > 0);
};

await fire("session_start");
console.log(line("start"));
await fire("agent_start");
console.log(line("working"));
await fire("tool_call", { toolName: "ask_user_question", toolCallId: "c1" });
console.log(line("question"));
await fire("tool_result", { toolCallId: "c1" });
console.log(line("answered"));
await fire("ui_prompt_start", { kind: "confirm" });
console.log(line("confirm"));
await fire("ui_prompt_end", { kind: "confirm" });
console.log(line("closed"));
await fire("input", { source: "interactive", text: "hi" });
console.log(line("typed"));
await fire("agent_settled", { aborted: false });
console.log(line("settled"));

// A heartbeat has to refresh the file without changing what it says, or a session sitting on
// one state looks like a process that died
const body = JSON.parse(readFileSync(file, "utf8"));
console.log("install: " + body.install);
console.log("started: " + ((JSON.parse(readFileSync(file, "utf8")).startedAtMs ?? 0) > 0 && Date.now() - JSON.parse(readFileSync(file, "utf8")).startedAtMs < 600000));
const before = readFileSync(file, "utf8");
await new Promise((r) => setTimeout(r, 900));
const after = readFileSync(file, "utf8");
console.log("heartbeat: " + (JSON.parse(after).state === JSON.parse(before).state && JSON.parse(after).updatedAtMs > JSON.parse(before).updatedAtMs));
await fire("session_shutdown", { reason: "reload" }, false);
console.log(line("reloaded"));
await fire("session_shutdown", { reason: "quit" });
console.log(line("stopped"));
`

// The extension is TypeScript that pi loads itself, so Go can't call it. Where bun is
// installed, this runs it against a stub pi and reads back what it reported; everywhere
// else the test skips rather than pretend it checked anything
func TestPiExtensionReportsItsState(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not installed, so the extension can't be run here")
	}
	// A short home, or the unix socket paths inside it would be too long
	home, err := os.MkdirTemp("", "sparkpi")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(home)
	if err := InstallPiExtension(home); err != nil {
		t.Fatalf("install: %v", err)
	}

	harnessPath := filepath.Join(home, "harness.ts")
	script := strings.ReplaceAll(harness, "EXTENSION", PiExtensionPath(home))
	script = strings.ReplaceAll(script, "HOME", home)
	if err := os.WriteFile(harnessPath, []byte(script), 0o644); err != nil {
		t.Fatalf("harness: %v", err)
	}

	cmd := exec.Command(bun, "run", harnessPath)
	cmd.Env = append(os.Environ(), "HOME="+home, "SPARK_PI_HEARTBEAT_MS=300")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the extension failed: %v\n%s", err, out)
	}

	want := []string{
		"start: idle kind=- tool=- input=false",
		"working: working kind=- tool=- input=false",
		"question: blocked kind=- tool=ask_user_question input=false",
		"answered: working kind=- tool=- input=false",
		"confirm: blocked kind=confirm tool=- input=false",
		"closed: working kind=- tool=- input=false",
		"typed: working kind=- tool=- input=true",
		"settled: idle kind=- tool=- input=true",
		"install: " + InstalledIDForTest(t, home),
		"started: true",
		"heartbeat: true",
		// A reload hands the session to a fresh instance, so the state stays where it was
		"reloaded: idle kind=- tool=- input=true",
		"stopped: exited kind=- tool=- input=true",
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(got) != len(want) {
		t.Fatalf("the extension reported %d lines, want %d:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			t.Errorf("line %d = %q, want %q", i+1, strings.TrimSpace(got[i]), want[i])
		}
	}
}

// InstalledIDForTest is the identity the installer wrote into this home's copy
func InstalledIDForTest(t *testing.T, home string) string {
	t.Helper()
	id := PiInstalledID(home)
	if id == "" {
		t.Fatal("the installed extension carries no identity")
	}
	return id
}
