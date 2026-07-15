#!/usr/bin/env bash
# site-screenshots.sh — regenerate the docs-site screenshots in docs/assets/img/.
#
# Boots the demo workspace (seeded, in a temp dir so the repo stays clean) and
# captures the boards with headless Chrome. JavaScript is disabled for the
# captures: the first paint is fully server-rendered, and skipping JS avoids
# the SSE connection keeping Chrome from ever reaching load-idle.
#
# Usage: scripts/site-screenshots.sh
# Requires: Go toolchain, Google Chrome (or chromium on PATH). macOS `sips` is
# used to downscale when available; captures are kept full-size otherwise.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/docs/assets/img"
PORT="${PORT:-8797}"
BASE="http://127.0.0.1:$PORT"

# --- find chrome -------------------------------------------------------------
CHROME=""
for c in \
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  "$(command -v google-chrome || true)" \
  "$(command -v chromium || true)" \
  "$(command -v chromium-browser || true)"; do
  if [ -n "$c" ] && [ -x "$c" ]; then CHROME="$c"; break; fi
done
[ -n "$CHROME" ] || { echo "error: no Chrome/Chromium found" >&2; exit 1; }

# --- boot a throwaway seeded demo server --------------------------------------
WORK="$(mktemp -d)"
cleanup() {
  [ -n "${SERVER_PID:-}" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ -n "${INIT_PID:-}" ] && kill "$INIT_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

cp -R "$ROOT/examples/demo-workspace" "$WORK/ws"
rm -f "$WORK/ws/work-cards.db"

echo "building cards…"
go -C "$ROOT" build -o "$WORK/cards" ./cmd/cards

"$WORK/cards" serve --workspace "$WORK/ws" --port "$PORT" --seed \
  >"$WORK/serve.log" 2>&1 &
SERVER_PID=$!

# a second server on a fresh `cards init` scaffold — its welcome board ships
# with the starter cards, which the demo workspace's welcome board does not
INIT_PORT=$((PORT + 1))
INIT_BASE="http://127.0.0.1:$INIT_PORT"
mkdir -p "$WORK/fresh" && (cd "$WORK/fresh" && "$WORK/cards" init >/dev/null)
"$WORK/cards" serve --workspace "$WORK/fresh/.cards" --port "$INIT_PORT" \
  >"$WORK/serve-init.log" 2>&1 &
INIT_PID=$!

for _ in $(seq 1 30); do
  curl -sf -o /dev/null "$BASE/ui/boards/engineering" \
    && curl -sf -o /dev/null "$INIT_BASE/ui/boards/welcome" && break
  sleep 0.5
done
curl -sf -o /dev/null "$BASE/ui/boards/engineering" \
  || { echo "error: demo server did not come up; log:"; cat "$WORK/serve.log"; exit 1; }
curl -sf -o /dev/null "$INIT_BASE/ui/boards/welcome" \
  || { echo "error: init server did not come up; log:"; cat "$WORK/serve-init.log"; exit 1; }

# --- capture ------------------------------------------------------------------
mkdir -p "$OUT"

shot() { # shot <outfile> <url> [height]
  local out="$1" url="$2" h="${3:-800}"
  echo "shot: $url -> $out"
  "$CHROME" --headless=new --disable-javascript --hide-scrollbars \
    --force-device-scale-factor=2 --window-size="1440,$h" \
    --screenshot="$OUT/$out" "$url" >/dev/null 2>&1
  # downscale for the web when sips is available (macOS)
  command -v sips >/dev/null && sips -Z 1760 "$OUT/$out" >/dev/null 2>&1 || true
}

shot board.png         "$BASE/ui/boards/engineering"               700
shot welcome.png       "$INIT_BASE/ui/boards/welcome"              700
shot theme-journal.png "$BASE/ui/boards/engineering?theme=journal" 700
shot theme-labels.png  "$BASE/ui/boards/engineering?theme=labels"  700
shot theme-jeeruh.png  "$BASE/ui/boards/engineering?theme=jeeruh"  700

# card detail page — a programming-task card from the seeded board, populated
# so the shot shows real field/work-log/comment content
CARD_ID=$(curl -s "$BASE/v1/cards" | python3 -c '
import sys, json
items = json.load(sys.stdin).get("items", [])
pts = [c for c in items if c.get("type_id") == "programming-task"]
print(pts[0]["id"] if pts else "")')
if [ -n "$CARD_ID" ]; then
  cli() { CARDS_URL="$BASE" CARDS_USER=local-dev "$WORK/cards" "$@" >/dev/null 2>&1 || true; }
  cli claim "$CARD_ID" --version 1
  cli patch "$CARD_ID" --version 2 --field kind=bug
  cli append "$CARD_ID" work_log --version 3 --entry-json \
    '{"commit_hash":"4f2a91c","notes":"Rejected transitions now return the board column ids.","author":"local-dev","timestamp":"2026-07-10T14:12:00Z"}'
  cli comment add "$CARD_ID" --body "Verified against the demo workspace — valid_options lists board columns now."
  shot card-detail-full.png "$BASE/ui/cards/$CARD_ID" 980
fi

# zoomed crops used by the landing page (sips is macOS; skip elsewhere)
if command -v sips >/dev/null; then
  # board lanes only (drop nav + filter chrome)
  cp "$OUT/board.png" "$OUT/board-lanes.png"
  sips -c 470 1240 --cropOffset 280 30 "$OUT/board-lanes.png" >/dev/null 2>&1
  # card detail: content column, head + fields (full shot is 1600px wide after -Z)
  if [ -f "$OUT/card-detail-full.png" ]; then
    cp "$OUT/card-detail-full.png" "$OUT/card-detail.png"
    sips -c 800 800 --cropOffset 110 420 "$OUT/card-detail.png" >/dev/null 2>&1
  fi
fi

echo "done:"
ls -la "$OUT"
