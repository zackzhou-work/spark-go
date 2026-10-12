#!/bin/sh
# Codex hook: writes each event to ~/.config/spark/codex-hooks/<thread_id>.json for spark
# to read state from.
#
# This script only observes. It returns no decision and blocks nothing.
dir="$HOME/.config/spark/codex-hooks"
mkdir -p "$dir" || exit 0

payload=$(cat)
[ -n "$payload" ] || exit 0

# Pull the event name and thread ID out of the JSON; sed avoids depending on jq
event=$(printf '%s' "$payload" | sed -n 's/.*"hook_event_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
tid=$(printf '%s' "$payload" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
[ -n "$tid" ] || tid=$(printf '%s' "$payload" | sed -n 's/.*"thread_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
[ -n "$tid" ] || exit 0

# Write the event, named by thread ID; each write replaces the last and spark only cares
# about the most recent one
printf '%s' "$payload" > "$dir/$tid.json.tmp" && mv -f "$dir/$tid.json.tmp" "$dir/$tid.json"

# Emit nothing: exit 0 with empty stdout is success as far as Codex is concerned, the same
# as returning {"continue":true}.
#
# That field is skipped because it only means anything as false ("stop") and true is pure
# redundancy. PreToolUse and PermissionRequest reject continue:false, so writing that value
# into this script would only leave a trap behind. SessionEnd goes further: it doesn't parse
# stdout at all.
exit 0
