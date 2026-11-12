#!/usr/bin/env bash
# End-to-end smoke test against a running qwen-sub-proxy instance.
set -euo pipefail

HOST="127.0.0.1"
PORT="${PORT:-8791}"
BASE="http://$HOST:$PORT"
KEY_FILE="$HOME/.config/qwen-sub-proxy/client-key"

if [[ ! -f "$KEY_FILE" ]]; then
  echo "local key not found at $KEY_FILE, run 'qwen-sub-proxy login' first" >&2
  exit 1
fi
KEY="$(cat "$KEY_FILE")"

echo "== health =="
curl -sS -H "Authorization: Bearer $KEY" "$BASE/health" | tee /dev/stderr | python3 -m json.tool >/dev/null
echo

echo "== models =="
curl -sS -H "Authorization: Bearer $KEY" "$BASE/v1/models" | python3 -m json.tool
echo

echo "== chat completion =="
curl -sS -N -X POST "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.8-flash","stream":false,"messages":[{"role":"user","content":"Reply with the single word: ok"}]}' \
  | python3 -m json.tool
