#!/usr/bin/env bash
# dev-server.sh — rebuild/restart Cards on source/template/config changes.
#
# Best path: install Air (`go install github.com/air-verse/air@latest`) and this
# wrapper will delegate to .air.toml. Without Air, it falls back to a small
# dependency-free watcher that hashes relevant files every second.
#
# Usage:
#   scripts/dev-server.sh
#   PORT=8788 CARDS_WS=./examples/demo-workspace scripts/dev-server.sh
#
# The production binary embeds templates/CSS, so UI edits require a rebuild.
# This script keeps that dev loop automatic.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

PORT="${PORT:-8787}"
HOST="${HOST:-127.0.0.1}"
WS="${CARDS_WS:-./.cards}"
BIN="${DEV_CARDS_BIN:-.pi/tmp/dev/cards}"
LOG="${DEV_CARDS_LOG:-.pi/run/cards-${PORT}.log}"
PIDFILE="${DEV_CARDS_PID:-.pi/run/cards-${PORT}.pid}"

mkdir -p "$(dirname "$BIN")" "$(dirname "$LOG")"

if command -v air >/dev/null 2>&1 && [ -f .air.toml ] && [ -z "${CARDS_DEV_NO_AIR:-}" ]; then
  echo "dev-server: using Air (.air.toml) on http://${HOST}:${PORT}"
  # Air's args are fixed in .air.toml for the common 8787 demo case. If callers
  # override PORT/WS, use the fallback so the args can vary without generating
  # config files.
  if [ "$PORT" = "8787" ] && [ "$WS" = "./.cards" ]; then
    exec air -c .air.toml
  fi
  echo "dev-server: PORT/CARDS_WS override detected; using fallback watcher"
fi

server_pid=""

cleanup() {
  if [ -n "${server_pid:-}" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

fingerprint() {
  # Hash files that affect the embedded UI/server binary or demo workspace
  # presentation. Exclude tests for fast UI iteration; run `go test ./...`
  # separately before committing.
  find cmd internal "$WS/definitions" \
    -type f \( \
      -name '*.go' -o -name '*.html' -o -name '*.css' -o -name '*.js' -o \
      -name '*.json' -o -name '*.yaml' -o -name '*.yml' \
    \) ! -name '*_test.go' -print0 \
    | sort -z \
    | xargs -0 shasum 2>/dev/null \
    | shasum \
    | awk '{print $1}'
}

stop_port_listener() {
  local pids
  pids="$(lsof -tiTCP:"$PORT" -sTCP:LISTEN 2>/dev/null || true)"
  if [ -n "$pids" ]; then
    echo "dev-server: stopping existing listener(s) on :$PORT: $pids"
    kill $pids 2>/dev/null || true
    sleep 0.3
  fi
}

build_and_start() {
  echo "dev-server: building $BIN"
  go build -o "$BIN" ./cmd/cards

  if [ -n "${server_pid:-}" ] && kill -0 "$server_pid" 2>/dev/null; then
    echo "dev-server: restarting pid $server_pid"
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  else
    stop_port_listener
  fi

  : > "$LOG"
  "$BIN" serve --workspace "$WS" --port "$PORT" --seed >"$LOG" 2>&1 &
  server_pid=$!
  echo "$server_pid" > "$PIDFILE"
  echo "dev-server: serving http://${HOST}:${PORT} pid=$server_pid log=$LOG"
}

last=""
build_and_start
last="$(fingerprint)"

echo "dev-server: watching cmd/, internal/, and $WS/definitions (Ctrl-C to stop)"
while sleep 1; do
  current="$(fingerprint)"
  if [ "$current" != "$last" ]; then
    last="$current"
    if ! build_and_start; then
      echo "dev-server: build failed; keeping previous server if still running" >&2
    fi
  fi
  if [ -n "${server_pid:-}" ] && ! kill -0 "$server_pid" 2>/dev/null; then
    echo "dev-server: server exited; rebuilding/restarting" >&2
    build_and_start || true
    last="$(fingerprint)"
  fi
done
