# pi-agent extension for Cards — design spec

**Status:** built externally — shipped as the
[pi-cards](https://github.com/somebox/pi-cards) package in its own repo per
§4; this document remains the design spec — 2026-07-19
**References:** [`docs/extensions/index.md`](../extensions/index.md) (cards
extension model), [`docs/spec/api-surface.md`](../spec/api-surface.md) §11/§13,
[`tui-bus-disposition.md`](tui-bus-disposition.md), roadmap §10–§11,
[pi extensions](https://pi.dev/docs/latest/extensions),
[pi packages](https://pi.dev/docs/latest/packages)

> **Summary.** A [pi](https://pi.dev) extension (TypeScript, loaded by the pi
> coding agent) that makes a Cards workspace a first-class surface inside a
> pi session: a `/cards` command with an interactive board view, LLM-callable
> `cards_*` tools, cards-as-context in discussion, subagent execution of
> cards, git persistence of board state, and automatic worklog/comment
> activity. Ships as a **standalone pi package in its own repo**
> (`~/src/pi-cards`), not inside this repo. From the cards core's perspective
> it is just another API client — a sixth surface, external by design.

---

## 1. Terminology: two kinds of "extension"

This repo and pi both use the word "extension" for different things:

| Term | Meaning |
|---|---|
| **cards extension** | A declared hook/service/run subprocess in `definitions/extensions.yaml`, supervised by `cards run-extensions` / `serve --run-extensions` (`internal/hooks/`, `cmd/cards/extensions.go`). |
| **pi extension** | A TypeScript module loaded into the pi agent process (`~/.pi/agent/extensions/` or an installed pi package), using `ExtensionAPI` (`pi.registerTool`, `pi.registerCommand`, `ctx.ui`, events). |

The pi extension is **not** a cards extension: it is never declared in
`extensions.yaml` and the cards core never loads or supervises it. It lives
in pi's process and talks to cards over the same versioned contracts as
every other client (HTTP `/v1` + SSE, or the CLI). Note the existing name
collision: `cards do <extension_id>` (CLI) invokes a *run-kind cards
extension* — it has nothing to do with executing a card. This spec therefore
names card execution **`/cards work <id>`** (never "do").

## 2. Prior art reviewed

### 2.1 Board cards and plans

- **Card `97cec658` "Example: pi-agent extension developing code from a
  spec"** (deleted from the board, tombstone in `backlog.jsonl`; still
  referenced by roadmap §11 "Worked examples") — this spec effectively
  resurrects it as a real feature. File a fresh dogfood card when
  implementation starts.
- **Card `fa6d5c2f` "Extension example: PR sync — move linked card on
  merge"** (backlog) — establishes the pattern this follows: shippable
  external integrations consuming the HTTP API + events.
- **Card `79bfbebc` "Extensions: review design + use cases + integration
  points"** (deleted; roadmap §10, needs-design) — the pi extension is a
  data point for that review: a client that wants a narrow, stable,
  self-correcting API contract and nothing in-process.
- **Card `ea7ea2a3` "CLI: cards run-extensions + cards do + cards extensions
  list"** (done) — the source of the `cards do` name collision above.
- **`docs/plans/2026-07-18-sprint-plan.md`** — unrelated (contract SoT /
  concurrency hygiene); no pi-agent content.

### 2.2 Lessons from the Go TUI (`internal/tui/`, DEBT-61 disposition)

The TUI is the closest existing analog (terminal board UI, serverless). Its
disposition doc fixes four constraints the pi extension inherits:

1. **Serverless-first is a hard requirement.** Bare `cards` works with no
   server; the pi extension must also be useful when no `cards serve` is
   running → it needs a serverless backend (exec the `cards` CLI, which
   auto-selects its direct in-process backend when `CARDS_URL` is unset).
2. **Cross-process live refresh is SSE or nothing.** The in-process bus does
   not cross processes; "the honest path is `CARDS_URL` client mode." So:
   HTTP mode gets live updates from `GET /v1/events/stream`; serverless CLI
   mode refreshes on demand and must not pretend to be live.
3. **Actor resolution parity.** `--as` → `CARDS_USER` → `USER` → workspace
   `default_user`. The pi extension follows the same chain (§5.4).
4. **Script-safety guard.** The TUI's `interactive()` guard keeps bare
   `cards` script-safe; the pi analog is `ctx.mode` / `ctx.hasUI` — every
   interactive feature degrades to plain text in `print`/`json` modes.

The TUI keymap (`internal/tui/tui.go`) is also the proven feature checklist
for a terminal board UI: lane nav (`h/l/j/k`), open detail (`enter`), find
(`/`), status move (`s`), owner (`o`), edit (`e`), comment (`c`), claim/mine
(`m`), new (`n`), help (`?`), quit (`q`).

### 2.3 The agent surface that already exists

The MCP server (`internal/mcp/`) is the reference for "typed tools for an
agent harness": `workspace`, `get_card`, `list_cards`, `search_cards`,
`claim`, `release`, `take_next`, `append_entry`, `update_entry`,
`remove_entry`, `add_link`, `remove_link`, `add_comment`, `edit_comment`,
`upgrade_schema`, `attach_artifact`, `get_artifact`, `history`, `breaches`,
`events`, plus generated `create_<type>` / `update_<type>` per card type.
The pi extension deliberately starts narrower (§6.3) and borrows the
generated-tools idea as a later refinement.

The **agent coordination loop** (api-surface §13) is the organizing contract
for card execution: introspect → take-next → work → append evidence →
transition → comment → resume from history.

## 3. Goals / non-goals

**Goals**

- G1 — `/cards`: view the workspace board inside a pi session (interactive
  TUI component; text fallback in non-TUI modes).
- G2 — Cards as discussion context: inject a card into the conversation,
  reference cards inline (`#<short-id>`), search and tag from pi.
- G3 — Agent-operable tools: the LLM can list/get/search/create/update/
  comment/claim cards with the same honesty guarantees as the HTTP API
  (structured errors, `version` conflicts surfaced, idempotency).
- G4 — Card execution: `/cards work <id>` runs one card in a subagent;
  `/cards work --todo` drains the todo lane; activity lands back on the card
  (worklog entries, comments, status transitions).
- G5 — Board-state persistence to git: export `backlog.jsonl` and commit,
  reusing `scripts/board.sh` semantics.
- G6 — Task-definition assistance: help the user draft/refine cards
  (descriptions, acceptance criteria, decomposition) through the same tools.

**Non-goals**

- No changes to the cards core, its API, or its repo layout (beyond docs
  pointers). The pi extension is a client; missing API capabilities are
  filed as cards, not worked around.
- Not a cards extension (`extensions.yaml`); not supervised by the hook
  supervisor.
- No reimplementation of the web UI; the pi board view is a terminal
  component, scoped like the Go TUI (browse + act), not a pixel port.
- No multi-workspace federation; one workspace per pi session (mirrors
  one-workspace-per-process).
- Not an MCP re-implementation — pi loads this extension natively; the
  cards MCP server remains for MCP-aware harnesses.

## 4. Packaging: where the code lives

**Decision: standalone repo, `~/src/pi-cards`, published as a pi package.
Not a subdirectory of `somebox/cards`.**

Rationale:

1. **Philosophy §6 (extensions over plugins).** Cards' own norm is that
   extensions are independent processes/repos talking to the versioned HTTP
   + event contracts; the core grows reluctantly. A pi extension is exactly
   that — an external client. Putting it in the core repo contradicts the
   boundary the project argues for everywhere else.
2. **pi's install model wants a package root.** `pi install
   git:github.com/somebox/pi-cards` (or a local path during dev) expects a
   `package.json` with a `pi` manifest (`pi.extensions`) at the repo root;
   pi does not install packages from subdirectories of other repos.
3. **Toolchain mismatch.** The cards repo is a single Go module with a
   deliberate "no JS build step" rule for its own UI. A pi extension is
   TypeScript with npm dependencies (`@earendil-works/pi-coding-agent`,
   `@earendil-works/pi-tui`, `typebox` as `peerDependencies: "*"`, per pi
   package rules). Mixing `node_modules` into this repo violates "boring
   tech."
4. **Precedent.** `pi-llmlayer` and `pi-pipeline` already live as
   standalone repos in `~/src` and install as pi packages; `pi-cards`
   follows the same shape.
5. **Independent cadence.** pi's extension API evolves faster than cards'
   versioned contracts; the extension pins to the stable `/v1` contract and
   releases on its own schedule.

What stays **in this repo**: this spec, a pointer in
`docs/extensions/index.md` (example integrations), a roadmap §11 line, and
a dogfood card tracking the build (resurrecting the `97cec658` intent).

Package layout (pi conventions):

```
pi-cards/
├── package.json          # keywords:["pi-package"], pi.extensions:["./src/index.ts"]
├── README.md             # install: pi install git:github.com/somebox/pi-cards
└── src/
    ├── index.ts          # extension factory (registers everything)
    ├── client.ts         # CardsClient: HTTP backend + CLI backend
    ├── tools.ts          # cards_* LLM tools
    ├── board-ui.ts       # /cards interactive board component
    ├── work.ts           # card execution (subagent loop)
    └── sync.ts           # git export/commit
```

## 5. Architecture

### 5.1 Process model

The extension runs inside the pi process. It never embeds cards code and
never opens `work-cards.db` directly. It reaches the workspace through one
of two backends behind a small `CardsClient` interface:

| | **HTTP backend** (preferred) | **CLI backend** (serverless fallback) |
|---|---|---|
| When | `CARDS_URL` set, or a server answers at the default URL for the resolved workspace | No server reachable |
| Transport | `fetch()` against `/v1` | `pi.exec("cards", [...], { timeout })` with `--json` |
| Live updates | SSE `GET /v1/events/stream` with `Last-Event-ID` resume | None — refresh on demand (same documented boundary as the TUI disposition) |
| Actor | `X-Work-Cards-Actor` header | `--as` / `CARDS_USER` env |
| Notes | Full contract incl. `take-next`, breaches, events feed | One process spawn per op; safe against a concurrent server (same posture as today's serverless CLI) |

Selection happens once per session (`session_start`), is reported via
`ctx.ui.setStatus("cards", …)`, and can be flipped by `/cards connect`.
The extension does **not** auto-spawn `cards serve` in v1 — process
ownership is the user's (`scripts/dev-server.sh` covers the dev loop); an
opt-in `autoServe` setting is an open question (§9).

### 5.2 Workspace resolution

Mirror `cmd/cards/workspace.go` precedence:

1. Extension setting `workspace` (`.pi/cards.json` project config), else
2. `CARDS_WORKSPACE` env, else
3. nearest `.cards/` walking up from `ctx.cwd` (the dir holding
   `definitions/workspace.json`), else
4. global personal workspace `$CARDS_HOME` or `~/.cards`.

When `CARDS_URL` points at a server, verify the server's workspace matches
the resolved dir (`GET /v1/workspace` → `workspace_id`); a mismatch is a
loud error naming both paths, never a silent cross-board write.

### 5.3 pi mode behavior

Every interactive surface checks `ctx.mode` / `ctx.hasUI`:

- `tui` mode: full board component, dialogs, widgets.
- `rpc` mode: dialogs/notifications only; `/cards` prints a text board
  (`ctx.ui.custom()` returns `undefined` there).
- `print`/`json` modes: tools and `/cards` text output only; no prompts —
  execution flows take all answers from flags (YOLO defaults, §6.5).

### 5.4 Actor identity

Same chain as the CLI/TUI: extension setting `actor` → `CARDS_USER` →
`USER` → workspace `default_user`. An optional `actorSuffix` setting (e.g.
`foz` → `foz-pi`) lets board readers tell pi-driven writes from human ones.
Card execution (§6.5) always claims with the resolved actor; batch runs use
one actor per run. Auto-register the actor (`POST /v1/users`, `kind:
agent`) on first use — registration is open by design (card `b9661aef`).

### 5.5 Write honesty (non-negotiable)

Mirrors philosophy §9 and the API's coordination contract:

- Every mutation sends an `Idempotency-Key` (uuid per logical operation,
  reused across retries of that operation).
- `PATCH` always carries the `version` read with the card. On
  `version_conflict`: re-GET, re-apply the intended delta, retry once; a
  second conflict is surfaced verbatim (structured error body, including
  `valid_options` when present) — never swallowed, never force-overwritten.
- Create/update tools accept `dry_run` and pass it through.
- Tool results return the server's updated card (write responses include
  it), not a locally assumed state.

## 6. Feature spec

### 6.1 `/cards` command (G1)

`pi.registerCommand("cards", …)` with subcommand args:

| Invocation | Behavior |
|---|---|
| `/cards` | Interactive board view (TUI mode) or text board otherwise. |
| `/cards board <id>` | Switch the viewed board (default: workspace default board). |
| `/cards show <id>` | Print one card (title, status, owner, fields, links, recent comments) as markdown. |
| `/cards use <id>` | Inject the card into LLM context (§6.2). |
| `/cards search <q>` | FTS results as a pickable list (`ctx.ui.select`); choosing one opens `/cards show`. |
| `/cards work <id>` / `/cards work --todo` | Execute card(s) in a subagent (§6.5). |
| `/cards export` | Export `backlog.jsonl` + git commit (§6.6). |
| `/cards connect [url]` | Re-resolve backend; report HTTP vs CLI mode. |

Argument completion via `getArgumentCompletions`: board ids, card short-ids
(cached from the last list), and subcommands.

**Interactive board component** (`ctx.ui.custom`, full-screen, not overlay;
TUI mode only). Port of the Go TUI's proven keymap:

- Lanes as columns, cards as rows; `h/l` lane, `j/k` card, `enter` detail
  view (scrollable: description, fields, links, comments, worklog; `esc`
  back).
- `/` filter (FTS `q` against the backend), `s` move status — options are
  the board's **declared transitions** for that card, not free-form input
  (same enforcement as core), `o` set owner, `m` claim/release to self,
  `c` comment (`ctx.ui.editor`), `n` new card (type picker → title →
  required-fields wizard), `e` edit a field (schema-driven input: enum →
  select, date → input, text → editor).
- `w` work this card (§6.5), `g` export to git (§6.6), `?` key help,
  `q` close.
- HTTP mode: an SSE subscription refreshes the lanes live (reconnect with
  `Last-Event-ID`); CLI mode shows a "press `r` to refresh" hint and never
  claims liveness.

Rendering uses `@earendil-works/pi-tui` components (`Text`, `Box`,
`Container`) and the pi theme (`theme.fg("accent"|"muted"|"dim", …)`); no
colors outside the theme.

### 6.2 Cards as context (G2)

- **`/cards use <id>`** sends the card as a persistent custom message
  (`pi.sendMessage({ customType: "cards-card", display: true, … })`) so it
  participates in LLM context; `pi.registerMessageRenderer("cards-card", …)`
  renders it compactly in the transcript (type badge, short-id, title,
  status/owner, description, acceptance criteria, links).
- **`#<short-id>` autocomplete**: `ctx.ui.addAutocompleteProvider` triggered
  on `#`, completing against the workspace's cards (short-id + title, the
  `github-issue-autocomplete.ts` pattern). An `input` transform expands a
  bare `#<short-id>` mention to `[#<short-id> "<title>"]` before the prompt
  is sent, so the model sees the reference without a tool call; the LLM can
  always fetch the full card via `cards_get`.
- **Search/tagging from chat**: covered by the `cards_search` tool and the
  update tool's `tags` patch (§6.3); no separate UI.

### 6.3 LLM tools (G3, G6)

`pi.registerTool`, names prefixed `cards_`. v1 surface (deliberately a
subset — progressive disclosure, philosophy §4):

| Tool | Maps to | Notes |
|---|---|---|
| `cards_list` | `GET /cards?board_id=&status=&owner=&type_id=` | Board-centric listing; compact rows (short-id, title, status, owner, tags), not full cards. |
| `cards_get` | `GET /cards/:id` | Full card: fields, links, comments, `version`. Accepts short-id (**leading-8 hex prefix**, `core.ResolveCard` — errata 2026-07-18: this spec originally said last-8; falsified against the real resolver). |
| `cards_search` | `GET /cards?q=` | FTS5 search; compact rows. |
| `cards_create` | `POST /cards` | `type_id`, `title`, `fields`, `tags`; `dry_run` supported. Structured validation errors returned verbatim (`valid_options`) so the model self-corrects in one retry. |
| `cards_update` | `PATCH /cards/:id` | fields/status/owner/tags with `version` + conflict retry per §5.5; `dry_run` supported. |
| `cards_comment` | `POST /cards/:id/comments` | Markdown body; actor attributed. |
| `cards_append_entry` | `POST /cards/:id/fields/:field/append` | Worklog/evidence entries on repeating fields. |
| `cards_claim` / `cards_release` | `POST /cards/:id/claim`, release | Claim sets owner to the resolved actor; 409 surfaces the current owner. **release is HTTP-only** (errata 2026-07-18: no CLI verb exists and `patch --owner ""` is a no-op); the CLI backend throws `unsupported_backend`. |
| `cards_take_next` | `POST /cards/take-next` | The coordination-loop primitive; filters `status`/`type_id`/`board_id`. |

Every tool: `promptSnippet` (one-line "Available tools" entry) +
`promptGuidelines` that name the tool (per pi docs), output truncated with
`truncateHead` (50KB/2000-line discipline), errors thrown on failure (pi
sets `isError` and reports to the LLM with the structured cards error body
included). `renderCall`/`renderResult` give compact styled rows in the TUI
(accent title, dim short-ids); default shell.

**Later (phase 2):** generated `cards_create_<type>` /
`cards_update_<type>` per card type from `GET /workspace` (the MCP pattern,
registered on `session_start` when the backend is reachable), dynamic tool
loading via a loader tool (`pi.setActiveTools`, additive), and
`cards_history` / `cards_breaches` / `cards_events` for resume and
coordination debugging.

### 6.4 Task-definition assistance (G6)

No new machinery — a usage pattern over §6.3 plus one prompt surface:

- A `cards-draft` prompt template (shipped in the package's `prompts/`):
  interview the user about a piece of work, then call `cards_create` with a
  well-formed description + acceptance criteria, splitting large asks into
  linked cards (`blocks`/`related`).
- Refinement: "tighten card #a1b2c3" → `cards_get` → rewrite →
  `cards_update` (version-checked). The structured `valid_options` errors
  make field/enum mistakes self-correcting.

### 6.5 Card execution (G4)

`/cards work <id>` (command) and a `cards_work` tool for the LLM. One card
run:

1. **Preflight**: `cards_get`; refuse (showing the card state) if already
   owned by another actor or in a terminal status, unless `--force`.
2. **Claim**: `cards_claim` as the resolved actor; set status per the
   board's in-progress transition when one exists.
3. **Prompt assembly**: title, type, description, acceptance criteria,
   comments, link context (one hop, titles only). The subagent already gets
   repo conventions (`CLAUDE.md`/`AGENTS.md`) from pi itself — the prompt
   stays card-focused.
4. **Subagent run**: spawn per pi's `subagent/` example — `pi.exec("pi",
   ["-p", prompt, ...])` in the current cwd, streaming progress to
   `onUpdate` and a `ctx.ui.setStatus("cards", "working #a1b2c3…")` line.
   The subagent inherits the extension's `cards_*` tools, so it can append
   evidence as it goes.
5. **Close-out**: append a `work_log` entry when the type declares one
   (`{author, notes, commit_hash?, timestamp}` — the shape card `b9661aef`
   uses), add a completion comment summarizing the diff (`git diff --stat`
   since claim), and transition: success → the board's review transition
   (never auto-`done` unless `work.autoDone` and acceptance is checkable
   and passes); failure → leave status, comment the failure, release the
   claim unless `--keep`.

`/cards work --todo` (batch): loop `cards_take_next` with `status=todo`
(+ optional type/board filter), run sequentially, one actor per run, stop
on N (default 3) or first hard failure; in TUI mode a confirm dialog shows
the candidate list first; in print mode `--yes` is required.

Concurrency honesty: claims are the CAS primitive (no double-claim, §2.3);
a lost race just moves to the next candidate. Runs never share a card with
a human owner — preflight refuses.

### 6.6 Git persistence of board state (G5)

`sync.ts` implements `/cards export` and an optional auto mode:

- Runs `cards export --state-only --out <workspace>/backlog.jsonl`, then
  `git add backlog.jsonl && git commit -m "cards: board sync (<n> cards)"`
  — same semantics as `scripts/board.sh export`, which it shells out to
  when the workspace repo provides it.
- Auto mode (`autoExport: "shutdown" | "idle" | "off"`, default `off`):
  `session_shutdown` hook (the `auto-commit-on-exit.ts` pattern) or a
  debounced `agent_settled` hook after any `cards_*` mutation. Never
  auto-pushes; commit only, and only when the export actually changed
  (`git diff --quiet` guard).
- Honors the repo rule: **never** rewrite `backlog.jsonl` by hand — only
  the CLI exporter writes it.

### 6.7 Worklog & comment activity

- While a card is claimed by this session, `turn_end` (debounced) appends
  interim `work_log` notes when commits landed since the last entry
  (`git log --oneline` since claim) — visible progress on the board without
  waiting for close-out. Opt-in via `worklogOnTurn: true` (default off;
  close-out entries from §6.5 are always on for `cards_work` runs).
- Handoff comments: when a batch run stops (limit/failure), comment on the
  *next* unstarted card why it wasn't started (one comment per batch, not
  per card).

## 7. Configuration

Extension settings read from `.pi/cards.json` (project) and
`~/.pi/agent/cards.json` (user), project winning; env overrides where noted:

| Key | Default | Meaning |
|---|---|---|
| `workspace` | resolution chain §5.2 | Workspace dir (or project root containing `.cards/`). |
| `url` | `$CARDS_URL`, else probe `http://127.0.0.1:8787` | Force HTTP backend + base URL. |
| `actor` / `actorSuffix` | chain §5.4 / `""` | Write identity. |
| `board` | workspace default | Board for `/cards` and list defaults. |
| `autoExport` | `"off"` | `shutdown` / `idle` git export cadence. |
| `worklogOnTurn` | `false` | Interim worklog entries while a card is claimed. |
| `work.batchLimit` | `3` | Max cards per `--todo` run. |
| `work.autoDone` | `false` | Allow direct transition to `done` on verified acceptance. |

## 8. Testing strategy

- **Unit**: `CardsClient` HTTP backend against a node `http` mock serving
  canned `/v1` responses (fixtures exported from the demo workspace); CLI
  backend against a fake `cards` shim on `PATH`. Cover conflict-retry,
  idempotency-key reuse, short-id resolution, structured-error passthrough.
- **Integration**: a fixture workspace (`definitions/` + seeded db) in the
  pi-cards repo; drive the real `cards` binary serverless (no server
  needed) through list/create/update/claim/work-preflight.
- **Manual dogfood**: `pi -e ./src/index.ts` against this repo's demo
  workspace; then install the package and use it to work its own dogfood
  card.
- CI (pi-cards repo): `tsc --noEmit`, unit + integration under the node
  test runner; no cards-repo changes required.

## 9. Open questions

1. **autoServe**: should the extension offer to spawn `cards serve` when a
   workspace exists but no server answers? (Process ownership, port
   collisions, and the one-workspace-per-process rule argue for opt-in
   only.)
2. **Typed tools now or later**: register `cards_create_<type>` /
   `cards_update_<type>` on `session_start` in v1 (MCP parity) or keep
   generic create/update and add typed tools in phase 2? Spec says phase 2;
   revisit after the first dogfood week.
3. **Subagent depth**: pi's subagent mechanisms are evolving (built-in
   `Agent` tool vs `pi -p` exec). The extension isolates spawning behind
   one function so the mechanism can move without touching the flow.
4. **SSE in the board component vs widget-only**: a footer/widget board
   summary (`ctx.ui.setWidget`) updated by SSE may be enough ambient
   awareness; the full-screen component could open on demand only. Decide
   with usage.
5. **Cross-repo link**: should the cards repo's `examples/` carry a minimal
   vendored copy (or a docs pointer) for the docs site? Leaning: link only
   — vendoring invites drift.

## 10. Phasing

| Phase | Deliverable | Exit check |
|---|---|---|
| P0 — skeleton | Repo + package scaffold, `CardsClient` (HTTP + CLI), `/cards` text board, `cards_list`/`cards_get`/`cards_search` | `pi -e` session lists and prints demo-workspace cards, both backends |
| P1 — writes | `cards_create`/`cards_update`/`cards_comment`/`cards_claim`/`cards_release`/`cards_take_next`, conflict-retry + idempotency, `.pi/cards.json` config | Round-trip a card from chat; forced 409 handled visibly |
| P2 — board UI | Interactive component (§6.1), SSE live refresh in HTTP mode, `/cards show`, `#id` autocomplete, `cards-card` message renderer | Daily-driver replacement for quick board checks |
| P3 — execution | `cards_work` + `/cards work [--todo]`, prompt assembly, close-out worklog/comment/transition | One real demo-workspace card completed end-to-end by a subagent |
| P4 — persistence & polish | `/cards export` + autoExport, worklog-on-turn, typed tools + dynamic loading, docs example | Board state rides in git; extension used to build its own remaining cards |

Follow-ups in **this** repo when P0 lands: file the dogfood card
(resurrecting the `97cec658` intent), add the `docs/extensions/index.md`
example pointer, and reference the extension in roadmap §11 worked examples.
