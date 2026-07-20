#!/usr/bin/env bash
# review-bot.sh — launcher for the review-bot service extension
# (definitions/extensions.json). Gates the Node runtime so a Node-less
# machine gets one clear line in the supervisor-managed service log
# (.cards/logs/review-bot.log) instead of a restart loop: exit 0 means
# restart_policy=on-failure leaves the service down.
set -euo pipefail

if ! command -v node >/dev/null 2>&1; then
  echo "node: not found — service review-bot skipped" >&2
  exit 0
fi

ext_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec node "$ext_dir/review-bot.mjs"
