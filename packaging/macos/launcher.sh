#!/bin/sh
set -eu
# Registration also covers a package installed before any user had signed in.
uid=$(id -u)
if ! launchctl print "gui/$uid/tv.getportico.server.service" >/dev/null 2>&1; then
 contents="$(cd -P -- "$(dirname -- "$0")/.." && pwd)"
 agents="$HOME/Library/LaunchAgents"
 mkdir -p "$agents"
 plist="$agents/tv.getportico.server.service.plist"
 install -m 644 "$contents/Resources/launchd/tv.getportico.server.service.plist" "$plist"
 launchctl enable "gui/$uid/tv.getportico.server.service"
 launchctl bootstrap "gui/$uid" "$plist"
fi
open "${PORTICO_CONSOLE_URL:-http://127.0.0.1:32500/}"
