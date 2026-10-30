#!/bin/sh
# Run with sudo. Server data is deliberately retained.
set -eu
launchctl bootout system/tv.getportico.server.service 2>/dev/null || true
rm -f /Library/LaunchDaemons/tv.getportico.server.service.plist
owner=$(stat -f '%Su' /dev/console)
case "$owner" in root|loginwindow|'') ;; *)
 uid=$(id -u "$owner")
 home=$(dscl . -read "/Users/$owner" NFSHomeDirectory | sed 's/^NFSHomeDirectory: //')
 launchctl bootout "gui/$uid/tv.getportico.server.service" 2>/dev/null || true
 case "$home" in /*) rm -f "$home/Library/LaunchAgents/tv.getportico.server.service.plist";; esac
esac
rm -rf '/Applications/Portico Media Server.app'
pkgutil --forget tv.getportico.server >/dev/null
