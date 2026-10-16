# spark

A small macOS window that shows the live status of every session in the Code tab of Claude Desktop, every Codex thread, and every pi session. Built with Go and the native UI of [mygo](https://github.com/egoist/mygo).

![spark: every Claude Desktop session, at a glance](docs/banner.png)

## Install

Download the DMG for your Mac from [Releases](https://github.com/zackzhou-work/spark-go/releases): `apple-silicon` for M-series Macs, `intel` for Intel Macs. Open it and drag Spark into Applications.

The app is not signed with a Developer ID or notarized, so the first time you open it macOS says it can't verify the developer. Open **System Settings → Privacy & Security**, scroll down and click **Open Anyway**. Or clear the quarantine flag once:

```bash
xattr -dr com.apple.quarantine /Applications/Spark.app
```

## Where the status comes from

spark watches three agents: Claude Desktop's Code tab, Codex, and pi.

### Claude

- **Processes**: each core `claude` process carries the desktop session ID in its `CLAUDE_CODE_HOST_SESSION_ID` environment variable. No process means the turn is over.
- **Transcripts**: spark reads the tail of `~/.claude/projects/<cwd>/<cliSessionId>.jsonl`. `end_turn` or a user interrupt ends the turn; a pending tool call means Running; AskUserQuestion or ExitPlanMode means Waiting.
- **Hooks (optional)**: with the hook installed, Claude Code reports permission prompts directly. Without it, spark can only guess them from a tool call that has been pending for 45 seconds with nothing happening.
- **`lastFocusedAt` in the session JSON**: once a turn is over, a `lastActivityAt` newer than `lastFocusedAt` means you haven't looked at the result in the desktop app yet, so it shows as Unread.

### Codex

- **Rollouts**: each thread writes `~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<time>-<threadId>.jsonl`. The first line carries the cwd; the first real user message becomes the title; the tail says where the turn stands (`task_complete` ends it, a tool call with no output means it's running). Threads with a `parent_thread_id` are sub-agents and are left out.
- **Hooks (optional)**: spark can install itself into `~/.codex/hooks.json` from the banner in the app. Codex reports `PermissionRequest`, `Stop` and the rest directly, and a hook newer than the rollout wins, because events land on disk a moment after they happen.
- **`electron-thread-read-state-v1` in `~/.codex/.codex-global-state.json`**: the list of threads the desktop app still marks unread, which plays the same role as Claude's `lastFocusedAt`.
- Codex threads aren't separate processes the way `claude` is, so spark can only tell that Codex is running at all, and falls back to "has this rollout been quiet for 30 minutes" for the rest.

### pi

- **Sessions**: pi writes `~/.pi/agent/sessions/--<cwd>--/<time>_<sessionId>.jsonl`. The header carries the working directory, which names the project; the title is the name from `/name` if there is one, and otherwise the first user message. Sub-agent sessions live in a directory under their parent's file and are left out.
- **Herdr**: when pi runs in [Herdr](https://herdr.dev), spark reads Herdr's own agent list: `agent.list` over its socket at `~/.config/herdr/herdr.sock`, no CLI and no `PATH` lookup. Herdr knows the session file behind every pane, so `working`, `blocked` and `idle` come from there. Clicking a pi row focuses that pane, which is also how Herdr marks it seen.
- **Extension (optional)**: **Install for pi** in the banner writes `~/.pi/agent/extensions/spark-pi.ts`, and pi's own events then report the state from the process itself, including what a blocking dialog is asking for. That is what covers a pi session running in an IDE or a plain terminal. Without it, a pi session outside Herdr is only ever shown as finished: spark doesn't guess at a live state it can't see.
- **Read state**: pi has no desktop app to mark a session read. In Herdr, `idle` and `done` already tell that apart — Herdr marks a tab seen as soon as it is focused, so its `idle` is a session you have looked at and its `done` is work that finished while you weren't — which is what spark shows as Unread. A session only the extension reports has nothing to go on but submitted prompts, and those come before the turn ran, so its New badge fades after 30 minutes rather than hanging on a session you walked away from.
- **Boundaries**: only the local Herdr session is read, so sessions on a `herdr --machine` remote don't appear. Runs started with `--no-session` leave no file to scan, and a `--session-dir` or `PI_CODING_AGENT_SESSION_DIR` override is not followed. pi changes are noticed by the rescan that runs every few seconds rather than by file watching: pi keeps hundreds of session files, and watching a directory on macOS means watching every file in it.

Each project gets a character — eight body shapes by five colours, forty in all — picked by hashing its name, and the character's face shows the most urgent state among the project's sessions: needs you › working › to read › all caught up. Two projects that hash to the same character are pulled apart so a screenful doesn't repeat: the later one keeps its shape and takes a colour nobody is using. Rows keep an ink-only marker that matches the dot in the desktop app's sidebar:

| spark character | spark row | Desktop sidebar | Meaning |
| --- | --- | --- | --- |
| Raised brows, a bobbing `!` or `?` bubble | Solid dot, on the project's tile | (still a solid dark dot) | Stuck on a permission prompt (`!`) or a question from the model (`?`) |
| Eyes glancing sideways | Spinner | Solid dark dot | The turn is in progress |
| Smiling, with a count badge | Solid dot and a **New** pill | Yellow | The turn is over and you haven't seen the result |
| Eyes closed | Hollow ring | Hollow ring | The turn is over and you've seen it |

The sidebar doesn't mark "waiting for permission" separately. spark adds it because it needs you to act. pi sessions use the same four row poses; the sidebar column doesn't apply to them, since there is no desktop app to compare against.

Click a project to see all its sessions. A session waiting on you gets a card there: **Respond/Answer in Claude**, **in Codex** or **in pi** jumps to it, **Later** puts the card away until the session moves again. A pi session spark can't open — one running in an IDE or a terminal without Herdr — still gets a card, because it needs you either way, but the card explains itself and offers only **Later**. Narrower than 340 points, the cards stack into a single column. The window doesn't shrink below 260 points wide.

One thing pi rows can't say: hovering a pi row that nothing is reporting on shows `no live signal` where the time goes, because spark only knows the turn is over. Everywhere else the row shows a `→` on hover when clicking it goes somewhere.

File watching on the session, transcript and hooks directories drives the scans, and a 5-second timer catches changes that touch no file, such as a process exiting or a pi session moving on.

## Jumping to a session

Clicking any row, finished sessions included, brings you back to where the session is.

**Claude** goes through the desktop app's private deep link:

```
claude://code/continue?session=<sessionId>&source=spark
```

`sessionId` is the `local_<uuid>` from the session JSON, so no mapping is needed. Claude Desktop raises and focuses its own window; spark only opens the URL.

**Codex** goes through its own thread deep link:

```
codex://threads/<threadId>
```

Codex raises and focuses its own window; spark only opens the URL. A thread that only ever ran in a terminal or an IDE has no tab in the desktop app, so this opens an empty thread instead — that's the same caveat Vibe Island documents for its app-server jump.

**pi** has no deep link, so spark asks Herdr to focus the pane its session runs in (`agent.focus` on the same socket) and then raises Herdr itself. A pi session running anywhere else — an IDE, a plain terminal, `tmux` — has nothing to focus: the row keeps its time on hover instead of showing the arrow, and a waiting session gets a card with only **Later**.

### Caveats

**The Claude link has no public contract.** It was read from the routing code in the `app.asar` of Claude.app 2.2553.1 and may change at any time:

- The app validates the ID with `^local_[A-Za-z0-9-]{1,64}$`, and `internal/monitor/deeplink_test.go` pins the same rule. An ID that doesn't match is never sent; spark logs a `[Jump]` line instead, which usually means the format changed.
- Deep links as a whole are gated by a remote flag and by `disableDeepLinks` in `~/Library/Application Support/Claude/config.json`. When they're off, clicks do nothing.
- If `code/continue` stops working, `claude://resume?session=<cliSessionId>` (a bare UUID) is another option. It goes through the "import a CLI session" path and may create a duplicate session, so spark doesn't fall back to it automatically.

**Multiple accounts aren't handled**: spark scans the sessions of every `<account>/<org>`, but the deep link only reaches those of the account signed in now.

**Herdr's socket is not a public contract either.** `agent.list` and `agent.focus` are read straight off the socket API of Herdr 20261013.1, pinned by `internal/monitor/herdr_test.go` against a fake server rather than against a published schema. If Herdr changes a field, the scan finds no pi agents and pi sessions fall back to "no live signal" instead of breaking: nothing else in spark depends on it.

## Installing the hook

Claude, Codex and pi each install separately; the banner in the app offers whichever is missing.

### Claude

Copy the script to a fixed location, then merge the settings below into `~/.claude/settings.json`. The script only writes each event to `~/.config/spark/hooks/<session_id>.json` and never affects what Claude Code decides.

```bash
mkdir -p ~/.config/spark && cp hooks/spark-hook.sh ~/.config/spark/spark-hook.sh && chmod +x ~/.config/spark/spark-hook.sh
```

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "PreToolUse": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "PermissionRequest": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "Notification": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }],
    "SessionEnd": [{ "hooks": [{ "type": "command", "command": "/Users/<you>/.config/spark/spark-hook.sh", "timeout": 5 }] }]
  }
}
```

Replace `<you>` with your user name.

### Codex

Press **Install for Codex** in the banner, or do it by hand:

```bash
mkdir -p ~/.config/spark && cp internal/monitor/hookscript/spark-codex-hook.sh ~/.config/spark/spark-codex-hook.sh && chmod +x ~/.config/spark/spark-codex-hook.sh
```

Then merge the same command into the nine events in `~/.codex/hooks.json` (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, `Stop`, `SubagentStart`, `SubagentStop`, `SessionEnd`). Existing hooks are preserved, and a copy of the file is left at `hooks.json.spark-backup`.

`SessionEnd` is set to 3 seconds because that's Codex's cap for it (its default is 1); anything higher gets clamped and logged as a warning at startup. The rest get a few seconds, since they only need to report a state change.

The hook **only observes**. It writes the event to disk and exits with no output — `exit 0` with empty stdout is success, equivalent to returning `{"continue": true}`. It skips that field because it's only meaningful as `false` ("stop"), and `PreToolUse` and `PermissionRequest` reject `continue: false`; `SessionEnd` doesn't parse stdout at all. It never returns `permissionDecision`, so it can't change whether you get an approval prompt. spark shows the pending approval in the notch; the approval itself still happens in Codex's own UI.

Codex asks you to trust new hooks the first time one runs, and re-asks after the file changes. To skip that prompt:

```bash
codex --dangerously-bypass-hook-trust
```

### pi

Press **Install for pi** in the banner, or copy the file by hand:

```bash
mkdir -p ~/.pi/agent/extensions && cp internal/monitor/hookscript/spark-pi.ts ~/.pi/agent/extensions/spark-pi.ts
```

pi loads plain TypeScript from that directory, so this is the whole install; nothing in `settings.json` changes. The extension **only observes**: it writes `~/.config/spark/pi/<sessionId>.json` for spark to read, one file per session, and never returns a decision or blocks a tool. Delete the file to uninstall.

It reports what pi already knows — the turn's state, what a blocking dialog is asking for, and the time of your last input — so a session in an IDE or a terminal shows up as working, blocked or idle even when Herdr isn't involved.

The file carries no version, so an older copy still counts as installed and won't be offered again: delete `~/.pi/agent/extensions/spark-pi.ts` and press the button to refresh it.

## Development

[mise](https://mise.jdx.dev) pins the Go version (`.mise.toml`).

```bash
go run .
```

```bash
go test ./...
```

```bash
SPARK_REAL=1 go test ./internal/monitor -run TestScanRealSessions -v
```

The last one prints the sessions and states spark finds on this machine.

To look at the characters, or render every screen with made-up sessions for comparing against the design:

```bash
go run . -avatars
```

```bash
mkdir -p /tmp/spark-screens && SPARK_SCREENS=/tmp/spark-screens go test -run TestScreens .
```

The screens include the pi waiting cards: `project-pi` is a session Herdr can focus, `project-pi-nowhere` one spark can't open.

The banner at the top of this README is rendered from the app's own view with made-up sessions, then laid out by `docs/banner/index.html` in headless Google Chrome:

```bash
SPARK_BANNER=1 go test -run TestBanner .
```

## Building the app

```bash
go tool mygo build -skip-dmg
```

The app lands in `build/darwin-arm64/Spark.app`, with the bundle ID `dev.spark.app` and the icon from `resources/icon.png`, which is rendered from `docs/icon/index.html`:

```bash
SPARK_ICON=1 go test -run TestIcon .
```
 The window's position and size are kept in `~/Library/Application Support/Spark/window-state.json`.

`LSUIElement` isn't set, so spark is a normal Dock app. To keep it in the menu bar only, add `"macos": {"infoPlist": {"LSUIElement": true}}` to `mygo.json`.

## License

[MIT](LICENSE)
