#!/usr/bin/env bash
#
# review-bot_test.sh — automated proof of the review-bot cards-extension seed
# (Sprint 07-19 Phase 3; docs/plans/2026-07-19-sprint-plan.md).
#
# Against a temp workspace copied from examples/demo-workspace, proves:
#   1. BLOCKER — `cards serve --run-extensions` autostarts the bot; a
#      status_changed → review triggers a comment authored by "review-bot",
#      asserted via the CLI. The pre-existing review-notify HOOK fires on the
#      identical event (intentional hook-vs-service illustration), so the
#      assertion is scoped to the bot's author, not "something happened".
#   2. MAJOR (SSE resumption) — kill -9 the server mid-stream, deliberately
#      orphaning the bot (a dead supervisor cannot drain it), restart on the
#      same port WITHOUT --run-extensions so only the resumed bot can
#      comment, drive a second card to review, and assert the comment plus a
#      re-subscribe carrying the last event id (Last-Event-ID replay — no
#      missed status_changed).
#   3. MAJOR (supervisor stability) — the bot logs its stable
#      {"event":"subscribed",...} line within $REVIEW_BOT_SUBSCRIBE_TIMEOUT
#      (default 10s) and restarts ≤1× in a 5s window (counted via the
#      supervisor's `--- <ts> start ---` markers in the service log).
#   4. The seed has zero npm dependencies (grep gate — the card's demo line).
#
# Runnable standalone (bash scripts/review-bot_test.sh) and via
# `go test ./cmd/cards -run TestReviewBotScript` (the wrapper skips when node
# or bash is absent). Requires: go, node (the bot itself), curl. No npm, no jq.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

SUBSCRIBE_TIMEOUT="${REVIEW_BOT_SUBSCRIBE_TIMEOUT:-10}"
COMMENT_TIMEOUT="${REVIEW_BOT_COMMENT_TIMEOUT:-20}"
STABILITY_WINDOW="${REVIEW_BOT_STABILITY_WINDOW:-5}"
BOT_SEED="examples/demo-workspace/.cards/ext/review-bot.mjs"

fail() { echo "FAIL: $*" >&2; exit 1; }
note() { echo "== $*"; }

for tool in go node curl; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool not found on PATH (required; the go test wrapper skips instead)"
done

# --- Gate: zero npm dependencies in the seed (exit criterion) ---------------
if grep -nE 'require\(|import ' "$BOT_SEED" | grep -v 'node:'; then
  fail "$BOT_SEED has non-stdlib dependencies (see grep above)"
fi
note "zero-npm-deps gate: clean"

# --- Temp dir + cleanup ------------------------------------------------------
TMP="$(mktemp -d "${TMPDIR:-/tmp}/review-bot-test.XXXXXX")"
SRV1_PID=""
SRV2_PID=""
SUCCESS=0
cleanup() {
  [ -n "$SRV2_PID" ] && kill "$SRV2_PID" 2>/dev/null || true
  [ -n "$SRV1_PID" ] && kill "$SRV1_PID" 2>/dev/null || true
  # Bot processes orphaned by the kill -9 test log their own pids; TERM them.
  if [ -f "$TMP/ws/.cards/logs/review-bot.log" ]; then
    grep -o '"pid":[0-9]*' "$TMP/ws/.cards/logs/review-bot.log" 2>/dev/null \
      | cut -d: -f2 | sort -u | while read -r pid; do kill "$pid" 2>/dev/null || true; done
  fi
  if [ "$SUCCESS" = "1" ]; then
    rm -rf "$TMP"
  else
    echo "temp dir kept for inspection: $TMP" >&2
  fi
}
trap cleanup EXIT

# --- Build + provision -------------------------------------------------------
note "building cards"
go build -o "$TMP/cards" ./cmd/cards

# The temp workspace is a COPY of the demo workspace minus machine-local
# state (live DB, logs): it carries the engineering board (with a `review`
# column and enforced transitions) and the bot's extensions.json declaration.
# No --seed: the only review-lane cards are the ones this test drives, so
# take-next's oldest-unowned claim stays deterministic.
mkdir -p "$TMP/ws"
cp -R examples/demo-workspace/. "$TMP/ws/"
rm -f "$TMP/ws"/work-cards.db "$TMP/ws"/work-cards.db-shm "$TMP/ws"/work-cards.db-wal
rm -f "$TMP/ws"/.cards/logs/*.log 2>/dev/null || true
BOT_LOG="$TMP/ws/.cards/logs/review-bot.log"

PORT="$(node -e 'const s=require("node:net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close();})')"
BASE="http://127.0.0.1:$PORT"
note "port: $PORT"

wait_http() { # url timeout_s
  local deadline=$(( $(date +%s) + $2 ))
  while ! curl -sf "$1" >/dev/null 2>&1; do
    [ "$(date +%s)" -ge "$deadline" ] && return 1
    sleep 0.2
  done
}

cli() { CARDS_URL="$BASE" CARDS_USER=test-driver "$TMP/cards" "$@"; }

drive_to_review() { # title → card id on stdout (engineering enforces the chain)
  local id
  id="$(cli --quiet create --type programming-task --title "$1" --status backlog \
        --field "description=driven by review-bot_test.sh" --field "branch=test/review-bot")"
  cli --quiet patch "$id" --version 1 --status todo >/dev/null
  cli --quiet patch "$id" --version 2 --status in_progress >/dev/null
  cli --quiet patch "$id" --version 3 --status review >/dev/null
  printf '%s\n' "$id"
}

wait_bot_comment() { # card_id timeout_s — comment authored by review-bot
  local card="$1" deadline=$(( $(date +%s) + $2 ))
  while true; do
    if cli --json get "$card" 2>/dev/null | node -e '
      let s = ""; process.stdin.on("data", (d) => (s += d)).on("end", () => {
        try {
          const c = JSON.parse(s);
          if ((c.comments || []).some((m) => m.author === "review-bot")) process.exit(0);
        } catch {}
        process.exit(1);
      });'; then
      return 0
    fi
    [ "$(date +%s)" -ge "$deadline" ] && return 1
    sleep 0.5
  done
}

start_markers() { grep -c -- '--- .* start ---' "$BOT_LOG" 2>/dev/null || true; }

# --- Server #1 with the extension supervisor ---------------------------------
note "starting server #1 (--run-extensions)"
"$TMP/cards" serve --workspace "$TMP/ws" --port "$PORT" --run-extensions \
  > "$TMP/server1.log" 2>&1 &
SRV1_PID=$!
wait_http "$BASE/v1/workspace" 15 || { cat "$TMP/server1.log"; fail "server #1 did not come up"; }

# STABILITY (MAJOR, part a): bot reaches `subscribed` within the timeout.
deadline=$(( $(date +%s) + SUBSCRIBE_TIMEOUT ))
while ! grep -q '"event":"subscribed"' "$BOT_LOG" 2>/dev/null; do
  [ "$(date +%s)" -ge "$deadline" ] && {
    cat "$TMP/server1.log"
    fail "bot did not reach subscribed within ${SUBSCRIBE_TIMEOUT}s (tune via REVIEW_BOT_SUBSCRIBE_TIMEOUT)"
  }
  sleep 0.2
done
note "bot subscribed: $(grep '"event":"subscribed"' "$BOT_LOG" | head -1)"

# STABILITY (MAJOR, part b): ≤1 restart in a 5s window.
starts_before="$(start_markers)"
sleep "$STABILITY_WINDOW"
starts_after="$(start_markers)"
restarts=$(( starts_after - starts_before ))
[ "$restarts" -le 1 ] || fail "bot restarted ${restarts}× in ${STABILITY_WINDOW}s (service log: $BOT_LOG)"
note "supervisor stability: ${restarts} restarts in ${STABILITY_WINDOW}s (starts total: ${starts_after})"

# BLOCKER: drive card A to review; the bot must comment as review-bot.
CARD_A="$(drive_to_review "review-bot test A")"
note "card A: $CARD_A driven to review"
wait_bot_comment "$CARD_A" "$COMMENT_TIMEOUT" \
  || { cat "$BOT_LOG"; fail "no review-bot comment on $CARD_A within ${COMMENT_TIMEOUT}s"; }
note "card A carries a comment authored by review-bot"

A_EVENT="$(grep '"event":"commented"' "$BOT_LOG" | tail -1 | grep -o '"trigger_event":[0-9]*' | cut -d: -f2)"
[ -n "$A_EVENT" ] || fail "could not read the bot's trigger event id from $BOT_LOG"

# RESUMPTION (MAJOR): kill -9 mid-stream; the dead supervisor cannot drain, so
# the bot survives orphaned with its lastEventId. Restart on the SAME port
# WITHOUT --run-extensions — the only live bot is the resumed one, so a
# comment on card B can only come from resumption.
note "kill -9 server #1 mid-stream; restarting without --run-extensions"
kill -9 "$SRV1_PID"
wait "$SRV1_PID" 2>/dev/null || true
SRV1_PID=""
"$TMP/cards" serve --workspace "$TMP/ws" --port "$PORT" > "$TMP/server2.log" 2>&1 &
SRV2_PID=$!
wait_http "$BASE/v1/workspace" 15 || { cat "$TMP/server2.log"; fail "server #2 did not come up"; }

CARD_B="$(drive_to_review "review-bot test B")"
note "card B: $CARD_B driven to review (post-restart)"
wait_bot_comment "$CARD_B" "$COMMENT_TIMEOUT" \
  || { cat "$BOT_LOG"; fail "resumed bot left no comment on $CARD_B within ${COMMENT_TIMEOUT}s"; }

last_sub="$(grep '"event":"subscribed"' "$BOT_LOG" | tail -1)"
echo "$last_sub" | grep -q '"lastEventId":[0-9]' \
  || fail "no re-subscribe with Last-Event-ID after the restart (last: $last_sub)"
resumed_from="$(echo "$last_sub" | grep -o '"lastEventId":[0-9]*' | cut -d: -f2)"
[ "$resumed_from" -ge "$A_EVENT" ] \
  || fail "resumed from event $resumed_from, expected ≥ $A_EVENT — events lost across restart"
note "SSE resumption: re-subscribed with Last-Event-ID=$resumed_from (≥ $A_EVENT); no missed status_changed"

echo
note "PASS: SSE → take-next → comment loop, mid-stream kill/restart resumption, and supervisor stability verified"
SUCCESS=1
