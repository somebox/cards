#!/usr/bin/env bash
#
# smoke-adoption.sh — deterministic end-to-end smoke for the install and
# adoption surfaces. No API key, no agent, no network: this is the regression
# net that belongs in the PR gate.
#
# The agentic half — can a real agent actually set a board up from the installed
# skill? — lives in internal/smoke/adoption_test.go and needs a model:
#
#   CARDS_AGENT_CMD='claude -p' go test -tags smoke ./internal/smoke/
#
# Usage:
#   scripts/smoke-adoption.sh            # builds ./cmd/cards into a temp binary
#   CARDS_BIN=/path/to/cards scripts/smoke-adoption.sh
#
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

SCRATCH="$(mktemp -d)"
CARDS="${CARDS_BIN:-$SCRATCH/cards-under-test}"
if [[ -z "${CARDS_BIN:-}" ]]; then
  echo "building ./cmd/cards"
  go build -o "$CARDS" ./cmd/cards
fi

pass=0 fail=0
cleanup() { rm -rf "$SCRATCH"; }
trap cleanup EXIT

ok()   { pass=$((pass+1)); printf '  \033[32mok\033[0m   %s\n' "$1"; }
bad()  { fail=$((fail+1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
check(){ if eval "$2" >/dev/null 2>&1; then ok "$1"; else bad "$1"; fi; }

echo "scratch: $SCRATCH"

# ── A. Fresh init: workspace, skill, and a short handshake ───────────────────
echo; echo "A. fresh init"
mkdir -p "$SCRATCH/fresh"
init_out="$("$CARDS" init "$SCRATCH/fresh")"

check "workspace scaffolded"            "test -f '$SCRATCH/fresh/.cards/definitions/workspace.json'"
check "skill installed"                 "test -f '$SCRATCH/fresh/.claude/skills/cards/SKILL.md'"
check "cli reference installed"         "test -f '$SCRATCH/fresh/.claude/skills/cards/references/cli-reference.md'"
check "adoption playbook installed"     "test -f '$SCRATCH/fresh/.claude/skills/cards/references/project-practices.md'"
check "skill is beside .cards, not in"  "test ! -e '$SCRATCH/fresh/.cards/.claude'"
check "no staging debris"               "! ls -a '$SCRATCH/fresh/.claude/skills/' | grep -q staging"
check "init reported both artifacts"    "grep -q 'initialized workspace' <<<'$init_out' && grep -q 'installed the cards agent skill' <<<'$init_out'"
check "workspace loads"                 "'$CARDS' --workspace '$SCRATCH/fresh/.cards' workspace show"

# The handshake sits in every MCP session's prompt prefix; the caps are the
# enforcement that it stays cheap. Guarded in Go too, asserted here end to end.
hs_bytes=$("$CARDS" mcp --print-instructions | wc -c | tr -d ' ')
hs_lines=$("$CARDS" mcp --print-instructions | wc -l | tr -d ' ')
check "handshake <= 2048 bytes ($hs_bytes)" "[ '$hs_bytes' -le 2048 ]"
check "handshake <= 40 lines ($hs_lines)"   "[ '$hs_lines' -le 40 ]"
check "handshake needs no workspace"        "cd / && '$CARDS' mcp --print-instructions"

# ── B. No-clobber, existing projects, and --global ───────────────────────────
echo; echo "B. install paths"
echo 'locally edited' > "$SCRATCH/fresh/.claude/skills/cards/SKILL.md"
reinit_out="$("$CARDS" init "$SCRATCH/fresh")"
check "local edits survive re-init"     "grep -q 'locally edited' '$SCRATCH/fresh/.claude/skills/cards/SKILL.md'"
check "re-init reports no-clobber"      "grep -q 'not overwritten' <<<'$reinit_out'"

mkdir -p "$SCRATCH/existing"
"$CARDS" init --quiet --no-skill "$SCRATCH/existing"
check "--no-skill installs nothing"     "test ! -e '$SCRATCH/existing/.claude/skills/cards'"
"$CARDS" init --quiet "$SCRATCH/existing"
check "existing board gains the skill"  "test -f '$SCRATCH/existing/.claude/skills/cards/SKILL.md'"

FAKE_HOME="$SCRATCH/home"; FAKE_CARDS="$SCRATCH/cardshome"; mkdir -p "$FAKE_HOME" "$FAKE_CARDS"
HOME="$FAKE_HOME" CARDS_HOME="$FAKE_CARDS" "$CARDS" init --quiet --global
check "--global follows HOME"           "test -f '$FAKE_HOME/.claude/skills/cards/SKILL.md'"
check "--global ignores CARDS_HOME"     "test ! -e '$FAKE_CARDS/.claude'"

# Debris from an interrupted install must fail loudly and still report the
# workspace it created, rather than masquerading as a protected user skill.
mkdir -p "$SCRATCH/debris/.claude/skills/cards/references"
set +e
debris_out="$("$CARDS" init "$SCRATCH/debris" 2>&1)"; debris_rc=$?
set -e
check "debris exits non-zero"           "[ $debris_rc -ne 0 ]"
check "debris names the remedy"         "grep -q 'has no SKILL.md' <<<'$debris_out'"
check "debris still reports workspace"  "grep -q 'initialized workspace' <<<'$debris_out'"

# ── C. The starter is a tutorial, not the ladder ─────────────────────────────
# What an adopting agent has to overcome. If any of these start passing, the
# starter changed and the adoption playbook needs rereading.
echo; echo "C. adoption starting conditions"
"$CARDS" --workspace "$SCRATCH/existing/.cards" workspace show > "$SCRATCH/starter.json"
cat > "$SCRATCH/check_starter.py" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
which = sys.argv[2]
if which == "no-links":
    sys.exit(0 if not d["workspace"].get("link_types") else 1)
if which == "no-default-board":
    sys.exit(0 if not d["workspace"]["settings"].get("default_board") else 1)
if which == "welcome-board":
    sys.exit(0 if "welcome" in d["boards"] else 1)
sys.exit(2)
PYEOF

check "starter has no link types"       "python3 '$SCRATCH/check_starter.py' '$SCRATCH/starter.json' no-links"
check "starter has no default_board"    "python3 '$SCRATCH/check_starter.py' '$SCRATCH/starter.json' no-default-board"
check "starter ships the welcome board" "python3 '$SCRATCH/check_starter.py' '$SCRATCH/starter.json' welcome-board"

# ── D. MCP handshake against a live workspace ────────────────────────────────
echo; echo "D. mcp handshake"
# Via files, not shell arguments: the handshake body contains quotes, backticks
# and newlines, and round-tripping it through `eval` mangles it.
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  | "$CARDS" mcp --workspace "$SCRATCH/fresh/.cards" | head -1 > "$SCRATCH/handshake.json"
"$CARDS" mcp --print-instructions > "$SCRATCH/printed.txt"

cat > "$SCRATCH/check_handshake.py" <<'PYEOF'
import json, sys
r = json.load(open(sys.argv[1]))["result"]
printed = open(sys.argv[2]).read()
which = sys.argv[3]
if which == "served":
    sys.exit(0 if r.get("instructions", "").strip() else 1)
if which == "matches":
    sys.exit(0 if r.get("instructions") == printed else 1)
if which == "version":
    sys.exit(0 if r.get("serverInfo", {}).get("version") not in ("", "poc", None) else 1)
sys.exit(2)
PYEOF

check "initialize serves instructions"  "python3 '$SCRATCH/check_handshake.py' '$SCRATCH/handshake.json' '$SCRATCH/printed.txt' served"
check "instructions == --print output"  "python3 '$SCRATCH/check_handshake.py' '$SCRATCH/handshake.json' '$SCRATCH/printed.txt' matches"
check "version is not the poc stub"     "python3 '$SCRATCH/check_handshake.py' '$SCRATCH/handshake.json' '$SCRATCH/printed.txt' version"

echo
printf 'smoke: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
