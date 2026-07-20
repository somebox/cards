#!/usr/bin/env bash
# tui-screenshots.sh — regenerate the terminal-UI screenshots in docs/assets/img/.
#
# The TUI renders headlessly (cmd/tui-shot builds the model, renders one ANSI
# frame, converts it to a dark-terminal HTML page); headless Chrome — the same
# dependency scripts/site-screenshots.sh already uses — turns the page into a
# PNG. No terminal, no server, no recording tools.
#
# Usage: scripts/tui-screenshots.sh
# Requires: Go toolchain, Google Chrome (or chromium on PATH). macOS `sips` is
# used to downscale when available; captures are kept full-size otherwise.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/docs/assets/img"

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

# --- headless render against a throwaway copy of the demo workspace ----------
WORK="$(mktemp -d)"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

cp -R "$ROOT/examples/demo-workspace" "$WORK/ws"

echo "building tui-shot…"
go -C "$ROOT" build -o "$WORK/tui-shot" ./cmd/tui-shot

mkdir -p "$OUT"

shot() { # shot <outfile> <keys> <window WxH>
  local out="$1" keys="$2" size="$3"
  echo "shot: tui keys='$keys' -> $out"
  "$WORK/tui-shot" -workspace "$WORK/ws" -width 140 -height 33 \
    -keys "$keys" -title "cards — demo workspace" > "$WORK/frame.html"
  "$CHROME" --headless=new --hide-scrollbars \
    --force-device-scale-factor=2 --window-size="$size" \
    --screenshot="$OUT/$out" "file://$WORK/frame.html" >/dev/null 2>&1
  command -v sips >/dev/null && sips -Z 1760 "$OUT/$out" >/dev/null 2>&1 || true
}

shot tui-board.png  ""  "1220,690"
shot tui-filter.png "f" "1220,690"

echo "done:"
ls -la "$OUT"/tui-*.png
