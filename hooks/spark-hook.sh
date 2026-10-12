#!/bin/sh
# Claude Code hook: writes each event verbatim to ~/.config/spark/hooks/<session_id>.json for spark to read
dir="$HOME/.config/spark/hooks"
mkdir -p "$dir" || exit 0
payload=$(cat)
sid=$(printf '%s' "$payload" | sed -n 's/.*"session_id":"\([^"]*\)".*/\1/p')
[ -n "$sid" ] || exit 0
printf '%s' "$payload" > "$dir/$sid.json.tmp" && mv -f "$dir/$sid.json.tmp" "$dir/$sid.json"
exit 0
