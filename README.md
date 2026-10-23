# spark

A small macOS window that shows the live status of every session in the Code tab of Claude Desktop, every Codex thread, every pi session, and every local Cursor agent. Built with Go and the native UI of [mygo](https://github.com/egoist/mygo).

![spark: every Claude Desktop session, at a glance](docs/banner.png)

## Install

Download the DMG for your Mac from [Releases](https://github.com/zackzhou-work/spark-go/releases): `apple-silicon` for M-series Macs, `intel` for Intel Macs. Open it and drag Spark into Applications.

The app is not signed with a Developer ID or notarized, so the first time you open it macOS says it can't verify the developer. Open **System Settings → Privacy & Security**, scroll down and click **Open Anyway**. Or clear the quarantine flag once:

```bash
xattr -dr com.apple.quarantine /Applications/Spark.app
```

## Where the status comes from

spark watches four agents: Claude Desktop's Code tab, Codex, pi, and Cursor's desktop agents.

### Claude

- **Processes**: each core `claude` process carries the desktop session ID in its `CLAUDE_CODE_HOST_SESSION_ID` environment variable. No process means the turn is over.
- **Transcripts**: spark reads the tail of `~/.claude/projects/<cwd>/<cliSessionId>.jsonl`. `end_turn` or a user interrupt ends the turn; a pending tool call means Running; AskUserQuestion or ExitPlanMode means Waiting.
- **Hooks (optional)**: with the hook installed, Claude Code reports permission prompts directly. Without it, spark can only guess them from a tool call that has been pending for 45 seconds with nothing happening.
- **`lastFocusedAt` in the session JSON**: once a turn is over, a `lastActivityAt` newer than `lastFocusedAt` means you haven't looked at the result in the desktop app yet, so it shows as Unread.

### Codex

- **Rollouts**: each thread writes `~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<time>-<threadId>.jsonl`. The first line carries the thread ID and the cwd, and the tail says where the turn stands (`task_complete` ends it, a tool call with no output means it's running). Threads with a `parent_thread_id` are sub-agents and are left out. Resuming a thread writes another rollout, named `<threadId>_<segmentId>`, so a thread can own several files: spark keeps the newest for the state and lists the thread once.
- **`thread_name` in `~/.codex/session_index.jsonl`**: the name the desktop app shows for each thread, which is what spark titles the row with. Without one, the first real user message of the thread's oldest rollout stands in.
- **Hooks (optional)**: spark can install itself into `~/.codex/hooks.json` from the banner in the app. Codex reports `PermissionRequest`, `Stop` and the rest directly, and a hook newer than the rollout wins, because events land on disk a moment after they happen.
- **`electron-thread-read-state-v1` in `~/.codex/.codex-global-state.json`**: the list of threads the desktop app still marks unread, which plays the same role as Claude's `lastFocusedAt`.
- Codex threads aren't separate processes the way `claude` is, so spark can only tell that Codex is running at all, and falls back to "has this rollout been quiet for 30 minutes" for the rest.

### pi

- **Sessions**: pi writes `~/.pi/agent/sessions/--<cwd>--/<time>_<sessionId>.jsonl`. The header carries the working directory, which names the project; the title is the name from `/name` if there is one, and otherwise the first user message. Sub-agent sessions live in a directory under their parent's file and are left out. A session gets a row only while something is behind it — a Herdr pane, or a live pi process the extension reports — because a session whose pi has gone can be neither opened nor asked anything.
- **Herdr**: when pi runs in [Herdr](https://herdr.dev), spark reads Herdr's own agent list: `agent.list` over its socket at `~/.config/herdr/herdr.sock`, no CLI and no `PATH` lookup. Herdr knows the session file behind every pane, so `working`, `blocked` and `idle` come from there. Clicking a pi row focuses that pane, which is also how Herdr marks it seen.
- **Extension (optional)**: **Install for pi** in the banner writes `~/.pi/agent/extensions/spark-pi.ts`, and pi's own events then report the state from the process itself, including what a blocking dialog is asking for. That is what covers a pi session running in an IDE or a plain terminal. Without it, a pi session outside Herdr has nothing reporting on it, so spark leaves it out rather than guess at a state it can't see.
- **Read state**: pi has no desktop app to mark a session read. In Herdr, `idle` and `done` already tell that apart — Herdr marks a tab seen as soon as it is focused, so its `idle` is a session you have looked at and its `done` is work that finished while you weren't — which is what spark shows as Unread. A session only the extension reports has nothing to go on but submitted prompts, and those come before the turn ran, so its New badge fades after 30 minutes rather than hanging on a session you walked away from.
- **Boundaries**: only the local Herdr session is read, so sessions on a `herdr --machine` remote don't appear. Runs started with `--no-session` leave no file to scan, and a `--session-dir` or `PI_CODING_AGENT_SESSION_DIR` override is not followed. pi changes are noticed by the rescan that runs every few seconds rather than by file watching: pi keeps hundreds of session files, and watching a directory on macOS means watching every file in it.

### Cursor

- **Composer headers**: Cursor keeps one row per agent conversation in `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb` (`composerHeaders`). spark opens that database **read-only** (`mode=ro`, one connection, 250 ms deadline) and reads only the columns and JSON fields it knows: `composerId`, `workspaceId`, the two timestamps, `isArchived`, `isSubagent`, and from `value` the title, `hasUnreadMessages`, the draft/spec/project flags, `workspaceIdentifier` (the project path) and `agentLocation` (local versus cloud). Drafts, archives, subagents, spec and project records, and cloud or background agents are left out. A schema it doesn't recognise is reported as unavailable rather than read as "no sessions".
- **Hooks (optional)**: **Install for Cursor** copies the running executable to `~/.config/spark/bin/spark-cursor-hook` and merges eight events into `~/.cursor/hooks.json`, leaving every other key, event and entry where it was. Exposed as `spark --cursor-hook`, that helper reads one payload off stdin, keeps only the whitelisted fields, and writes one immutable file per event under `~/.config/spark/cursor/events/`. Prompts, agent text, tool input and output, email and transcript paths never reach the disk. The permission hooks (`preToolUse`, `beforeShellExecution`, …) are deliberately not subscribed to: an empty answer from one of those can block an action.
- **Turns**: `beforeSubmitPrompt` opens a generation, tool and thought events keep it alive, `stop` ends it, `sessionEnd` closes the session. Cursor reports a new generation for each user message, so an old generation's `stop` can't close a new turn, and two turns that could both be current are reported as unknown rather than guessed.
- **Read state**: Cursor's own header timestamps are written when a turn *starts* (`lastUpdatedAt`) and when its last content lands (`checkpointAt`), never at the stop, so they can't be compared with an ending. What spark compares is when it read the metadata: a turn's ending is only turned into Unread or Completed from a read that happened at least two seconds after that ending. Older reads leave it unknown.
- **Unknown**: with no hook event for a conversation there is nothing to decide from — an old session spark never watched, or one whose last turn predates the install, shows `Status unavailable` instead of a made-up state. Clicking a row never changes that, since Cursor gives no receipt for "this conversation got selected".
- **Boundaries**: only the stable macOS app and its default profile are read; Cursor CLI, Cloud Agent, remote machines, Insiders and other profiles are out of scope. Conversations that only exist in the classic IDE layout (`workspaceStorage/*/state.vscdb` → `composer.composerData`) are not listed: none of them matched a `composerHeaders` row on the verified build, and none was proven to be reachable by the Agent link.
- `sessionStart` also fires when a conversation is simply reopened, so it is never treated as a turn or as activity. spark reads the database again on every scan (a few milliseconds for a few dozen rows) rather than caching it, and keeps the last good read for 30 seconds if Cursor is holding the database.

Each project gets a character — eight body shapes by eleven colours, eighty-eight in all — picked by hashing its name, and the character's face shows the most urgent state among the project's sessions: needs you › working › to read › all caught up. Two projects that hash to the same character are pulled apart so a screenful doesn't repeat: the later one keeps its shape and takes a colour nobody is using. A colour dark enough to swallow the face's lines, such as black, takes white ones: the mouth, the brows and the closed eyes swap, while the eye whites and the pupils inside them keep their own colours. Rows keep an ink-only marker that matches the dot in the desktop app's sidebar:

| spark character | spark row | Desktop sidebar | Meaning |
| --- | --- | --- | --- |
| Raised brows, a bobbing `!` or `?` bubble | Solid dot, on the project's tile | (still a solid dark dot) | Stuck on a permission prompt (`!`) or a question from the model (`?`) |
| Eyes glancing sideways | Spinner | Solid dark dot | The turn is in progress |
| Smiling, with a count badge | Solid dot and a **New** pill | Yellow | The turn is over and you haven't seen the result |
| Eyes closed | Hollow ring | Hollow ring | The turn is over and you've seen it |
| Level stare, flat mouth | Faint ring, `Status unavailable` | — | Nothing known: Cursor reported no turn for this session |

The sidebar doesn't mark "waiting for permission" separately. spark adds it because it needs you to act. pi sessions use the same four row poses; the sidebar column doesn't apply to them, since there is no desktop app to compare against. The last row is Cursor only: a project whose sessions are all unknown doesn't say **All caught up**, because nothing says those sessions are done.

Click a project to see all its sessions, and its character at the top. Hovering the character shows a pencil: clicking it opens **Custom character**, where eight shapes and eleven colours can be tried on a live preview before **Save**. The choice is per project name in `~/.config/spark/characters.json` and replaces the automatic one everywhere the character shows, until it is changed again; **Cancel**, the close button or Escape leaves it as it was. Saving a character the automatic assignment picked keeps it, so it won't move when projects come and go. A spoilt choices file is reported and left alone rather than written over.

A session waiting on you gets a card on the project's page: **Respond/Answer in Claude**, **in Codex** or **in pi** jumps to it, **Later** puts the card away until the session moves again. A pi session spark can't open — one running in an IDE or a terminal without Herdr — still gets a card, because it needs you either way, but the card explains itself and offers only **Later**. Narrower than 340 points, the cards stack into a single column. The window doesn't shrink below 260 points wide.

A row shows `→` on hover only when clicking it goes somewhere. A pi session in a terminal or an IDE has no window for spark to bring forward, so its row keeps the time and doesn't take the pointer.

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

**Cursor** goes through the Agent link its own deep-link handler takes:

```
cursor://anysphere.cursor-deeplink/agent?id=<composerId>
```

`composerId` is the `composerHeaders` primary key, so no mapping is needed. Cursor raises its Agents window and selects that conversation; the classic IDE tab is not where it lands. Two limits shape how spark uses it:

- **A build is verified or it isn't.** A URL that raises the window and selects nothing looks exactly like success from here, and the system opener's exit code means only "dispatched". The table in `internal/monitor/cursorcompat.go` names the builds that were clicked through by hand — currently 3.24.12 with commit `37b865941ccb31e2fc90c685157534efca87f960`, see [the validation record](docs/research/cursor-integration-validation.md). Any other build lists its sessions read-only, keeps the rows static and offers no link. `SPARK_CURSOR_JUMP=1` overrides that for one run, which is how another build gets checked; it is not a supported configuration.
- **There is no receipt.** Cursor doesn't record which conversation its Agents window selected, so a click sends one URL and nothing else: it doesn't mark the session read, doesn't write to Cursor's database, and doesn't fall back to opening the project, a new chat or a CLI resume when the link can't do its job.

**pi** has no deep link, so spark asks Herdr to focus the pane its session runs in (`agent.focus` on the same socket) and then raises Herdr itself. A pi session running anywhere else — an IDE, a plain terminal, `tmux` — has nothing to focus: the row keeps its time on hover instead of showing the arrow, and a waiting session gets a card with only **Later**.

### Caveats

**The Claude link has no public contract.** It was read from the routing code in the `app.asar` of Claude.app 2.2553.1 and may change at any time:

- The app validates the ID with `^local_[A-Za-z0-9-]{1,64}$`, and `internal/monitor/deeplink_test.go` pins the same rule. An ID that doesn't match is never sent; spark logs a `[Jump]` line instead, which usually means the format changed.
- Deep links as a whole are gated by a remote flag and by `disableDeepLinks` in `~/Library/Application Support/Claude/config.json`. When they're off, clicks do nothing.
- If `code/continue` stops working, `claude://resume?session=<cliSessionId>` (a bare UUID) is another option. It goes through the "import a CLI session" path and may create a duplicate session, so spark doesn't fall back to it automatically.

**Multiple accounts aren't handled**: spark scans the sessions of every `<account>/<org>`, but the deep link only reaches those of the account signed in now.

**Herdr's socket is not a public contract either.** `agent.list` and `agent.focus` are read straight off the socket API of Herdr 20261013.1, pinned by `internal/monitor/herdr_test.go` against a fake server rather than against a published schema. If Herdr changes a field, the scan finds no pi agents and only the extension's reports are left: nothing else in spark depends on it.

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

### Cursor

Press **Install for Cursor** in the banner, the setup line on the Today page, or run it from the command line:

```bash
/Applications/Spark.app/Contents/MacOS/Spark --cursor-install
```

Either way spark copies the executable to `~/.config/spark/bin/spark-cursor-hook` and merges one entry per event into `~/.cursor/hooks.json`, under these eight: `sessionStart`, `beforeSubmitPrompt`, `postToolUse`, `postToolUseFailure`, `afterAgentThought`, `afterAgentResponse`, `stop`, `sessionEnd`. Every other key, event and entry is left alone, the file before the merge is kept at `hooks.json.spark-backup`, and the write is aborted if the file changed while it was being merged. A file that can't be parsed, or carries a version spark doesn't know, is reported and left untouched.

The command is the absolute path, single-quoted for the shell spark hands it to, followed by `--cursor-hook`:

```json
{ "version": 1, "hooks": { "stop": [{ "command": "'/Users/<you>/.config/spark/bin/spark-cursor-hook' --cursor-hook", "timeout": 5 }] } }
```

The helper is the whole of spark's hook process: it reads one payload off stdin (8 MiB cap), keeps the whitelisted fields and exits 0 with **empty stdout**, so Cursor sees an observer and not a decision. It writes no output on success or failure — a hook that reports a problem can still change what the agent does. Cursor reloads `hooks.json` when it changes; if the hooks tab in **Customize** stays empty, restart Cursor.

Uninstall takes only spark's own entries, the helper and the install metadata, and leaves the rest of your hooks where they are:

```bash
/Applications/Spark.app/Contents/MacOS/Spark --cursor-uninstall
```

The event files under `~/.config/spark/cursor/events/` expire after seven days on their own.

### pi

Press **Install for pi** in the banner, or copy the file by hand:

```bash
mkdir -p ~/.pi/agent/extensions && cp internal/monitor/hookscript/spark-pi.ts ~/.pi/agent/extensions/spark-pi.ts
```

pi loads plain TypeScript from that directory, so this is the whole install; nothing in `settings.json` changes. pi reads extensions when a process starts, so a session that is already running keeps going without it: run `/reload` in it, or restart pi, to have it report from then on. The banner says so until the first receipt arrives. The extension **only observes**: it writes `~/.config/spark/pi/<sessionId>.json` for spark to read, one file per session, and never returns a decision or blocks a tool. Delete the file to uninstall.

It reports what pi already knows — the turn's state, what a blocking dialog is asking for, and the time of your last input — so a session in an IDE or a terminal shows up as working, blocked or idle even when Herdr isn't involved.

spark compares the installed file with the one it ships: a copy from an older release is offered again, and the banner says what is left until a session reports from the current one.

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

The last one prints the sessions and states spark finds on this machine, Cursor included.

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
