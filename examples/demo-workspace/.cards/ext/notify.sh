#!/usr/bin/env bash
# Demo hook: fires when a card reaches the "review" column on the engineering
# board. Reads the event JSON on stdin and logs a notification. SPEC EXTENSIONS.md.
event="$(cat)"
card_id="$(printf '%s' "$event" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("card_id","?"))' 2>/dev/null || echo ?)"
echo "🔔 [notify] card ${card_id} reached review at $(date -u +%FT%TZ)" >> "$CARDS_WORKSPACE/.cards/logs/notify.log"
