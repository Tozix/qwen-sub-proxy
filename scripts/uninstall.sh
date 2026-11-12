#!/usr/bin/env bash
# Removes the qwen-sub-proxy LaunchAgent.
set -euo pipefail

LABEL="${QWEN_SUB_PROXY_LABEL:-com.qwen-sub-proxy}"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
if [[ -f "$PLIST" ]]; then
  rm -f "$PLIST"
  echo "removed $PLIST"
fi
