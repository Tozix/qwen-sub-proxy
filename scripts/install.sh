#!/usr/bin/env bash
# Registers qwen-sub-proxy as a macOS LaunchAgent.
set -euo pipefail

LABEL="${QWEN_SUB_PROXY_LABEL:-com.qwen-sub-proxy}"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="$PROJECT_DIR/qwen-sub-proxy"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG_FILE="$HOME/Library/Logs/qwen-sub-proxy.log"

if [[ ! -x "$BINARY" ]]; then
  echo "binary not found, run 'make build' first" >&2
  exit 1
fi

mkdir -p "$HOME/Library/LaunchAgents" "$(dirname "$LOG_FILE")"

cat >"$PLIST" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
	</dict>
	<key>KeepAlive</key>
	<true/>
	<key>Label</key>
	<string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>$BINARY</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardErrorPath</key>
	<string>$LOG_FILE</string>
	<key>StandardOutPath</key>
	<string>$LOG_FILE</string>
	<key>WorkingDirectory</key>
	<string>$PROJECT_DIR</string>
</dict>
</plist>
PLIST_EOF

launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"

echo "installed $LABEL"
echo "logs: $LOG_FILE"
launchctl print "gui/$(id -u)/$LABEL" | grep -E "state|pid" | head -3 || true
