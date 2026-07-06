#!/usr/bin/env bash
#
# board.sh — sync a Cards board between machines via git.
#
# The board's live state lives in a gitignored SQLite DB (work-cards.db). The
# committed, portable form is a state-only JSONL snapshot (backlog.jsonl):
# definitions + current card state (the mutation log stays SQLite-owned; only
# card_deleted tombstones ride along). This script wraps the export/import so
# the flags and the fresh-DB dance are one command.
#
# Usage:
#   scripts/board.sh export              # snapshot the live board -> backlog.jsonl (then commit it)
#   scripts/board.sh import              # restore backlog.jsonl into a fresh workspace DB
#   scripts/board.sh import --force      # WIPE the local DB first, then restore (re-sync a machine)
#   scripts/board.sh install-hook        # add a git pre-commit hook that auto-exports before each commit
#
# Workspace defaults to examples/demo-workspace; override with CARDS_WS=<dir>.
# The cards binary is auto-detected (./cards, then $PATH, then `go run`);
# override with CARDS_BIN="/path/to/cards".

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

WS="${CARDS_WS:-examples/demo-workspace}"
SNAP="$WS/backlog.jsonl"

# Resolve a cards command: prefer a built ./cards, then one on PATH, else go run.
if [ -n "${CARDS_BIN:-}" ]; then
  CARDS=("$CARDS_BIN")
elif [ -x "./cards" ]; then
  CARDS=("./cards")
elif command -v cards >/dev/null 2>&1; then
  CARDS=("cards")
else
  CARDS=(go run ./cmd/cards)
fi

die() { echo "board.sh: $*" >&2; exit 1; }

cmd_export() {
  [ -d "$WS/definitions" ] || die "no workspace at $WS (missing definitions/)"
  "${CARDS[@]}" export --workspace "$WS" --state-only --out "$SNAP"
  echo "board.sh: wrote $SNAP — commit it to share the board."
}

cmd_import() {
  local force="${1:-}"
  [ -f "$SNAP" ] || die "no snapshot at $SNAP (pull the repo first, or run: board.sh export)"
  if [ "$force" = "--force" ]; then
    echo "board.sh: --force — removing the local DB in $WS before restore."
    rm -f "$WS"/work-cards.db "$WS"/work-cards.db-* 2>/dev/null || true
  fi
  # import refuses a non-empty workspace (never a silent overwrite); on a fresh
  # clone the DB is absent/empty, so this just works. Use --force to re-sync a
  # machine that already has board state.
  "${CARDS[@]}" import --workspace "$WS" --in "$SNAP"
  echo "board.sh: restored $SNAP into $WS."
}

cmd_install_hook() {
  local hook=".git/hooks/pre-commit"
  [ -d ".git" ] || die "not a git repo root"
  cat > "$hook" <<HOOK
#!/usr/bin/env bash
# Auto-export the Cards board so the committed snapshot never goes stale.
# Installed by scripts/board.sh install-hook. Remove this file to disable.
set -euo pipefail
if [ -f "${WS}/work-cards.db" ]; then
  scripts/board.sh export >/dev/null
  git add "${SNAP}"
fi
HOOK
  chmod +x "$hook"
  echo "board.sh: installed $hook — commits now re-export the board first."
}

case "${1:-}" in
  export)        cmd_export ;;
  import)        cmd_import "${2:-}" ;;
  install-hook)  cmd_install_hook ;;
  *) sed -n '3,25p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
