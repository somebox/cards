# Design Decisions Log

A record of design decisions and why they were made. This is a **historical
rationale log**, not a status report — for current implementation status see
[`index.md`](spec/index.md) and [`index.md`](events/index.md). Other docs cite the
D-numbered entries below (D1–D18) for rationale not restated elsewhere; those
anchors are stable and must not be renumbered.

---

## v0.4 design pass

What changed in the v0.4 design pass and why. These decisions are now
**implemented** — the core kernel, HTTP API, CLI, MCP server, web UI, and
hook supervisor are built and dogfooded. Read alongside [`index.md`](spec/index.md)
v0.4. These notes capture decisions; the normative text lives in the spec.

## Theme

Trim the core to what serves the **agent coordination loop** — introspect →
take-next → work → append evidence → transition → comment — and keep
everything else generic enough to live in an extension. The system should be
obviously small before it is obviously featureful.

## Decisions

### D1 — Single workspace per instance; multi-instance is easy

One `cards serve` process serves **exactly one workspace** (which may be
assembled from multiple context files via merge). One workspace = one SQLite
file. Running several workspaces means running several processes on different
ports/paths; the binary, CLI, and clients all take `--workspace`/`--url`, so
this is trivial. No multi-tenant router in the kernel.

- **Why:** removes the ambiguous "workspace ids" (plural) in the health
  endpoint, removes a routing dimension, and keeps the data model honest
  (cards belong to exactly one workspace).
- **Affected:** `index.md` (health endpoint singular; context merge
  produces one workspace), `index.md` §3.

### D2 — Field catalog trimmed

Core v1 field types: `string`, `text`, `number`, `date`, `enum`, `tags`,
`user`, `card_link`, `repeating`, `artifact`. That is the load-bearing set.

Dropped from core (extension territory):

| Dropped | Why | Extension path |
|---|---|---|
| `markdown` | `text` is already markdown-rendered; a separate type is noise. | `text` fields render as markdown. |
| `json` / `yaml` | Structured-payload validation is open-ended and belongs to whoever defines the payload. | Store as `text`; an extension validates against its own schema and posts findings as a comment or `repeating` entry. |
| `path` | Path-traversal validation is a security surface, and the only core need (file references) is covered by `artifact`. | Store arbitrary paths as `string`; an extension validates workspace confinement. |
| `command` | Executable-intent metadata is risky (injection) and overlaps the `command` *extension kind*. | Extensions own execution contracts (argv-only, no shell, env allowlist, timeout). The core never executes. |

- **Why:** each removed type needs validation, UI rendering, MCP typing, and
  tests. None is essential to the coordination loop. `artifact` stays because
  posting links to artifacts is a stated core use case.
- **Affected:** `index.md` §6, `workspace-and-boards.md` §3/§6/§8,
  `index.md` (examples no longer use removed types).

### D3 — Link direction fixed: `blocks` → `blocked-by`

The old `blocks` type ("source blocks target") was wired backwards in the
lifecycle example and is a trap for agents. Replaced with **`blocked-by`**:
the *source* is the blocked card, the *target* is the blocker. The blocked
card owns its own "what am I waiting on?" edge — consistent with `depends-on`
(source depends on target), which is also stored on the waiting card.

- `depends-on`: source waits for target (ordering).
- `blocked-by`: source is hard-blocked by target.
- `blocked=true` query: a card is blocked iff it has an outgoing `blocked-by`
  (or `depends-on`) link to a non-`done` card.
- **Why:** storage location now matches intent; a card's outgoing edges answer
  "what's blocking me?" without a reverse lookup.
- **Affected:** `workspace-and-boards.md` §2/§5, `index.md` §7,
  `index.md` A2/A3.

### D4 — Actor is normative

Every write supplies an actor via the `X-Work-Cards-Actor` header (or
`actor` body field as an alias). Resolution: header → `CARDS_USER` env →
workspace `default_user` → `403 actor_required`. The server sets `created_by`
and event `actor` from this; clients cannot forge arbitrary actors beyond
their configured identity in a trusted-local model. (Stronger identity binding
is an extension/host concern; see `index.md`.)

- **Why:** `claim`, `take-next`, and event attribution all depend on an actor,
  but it was nowhere defined.
- **Affected:** `index.md` §11 + new §12 (Actors and authorization).

### D5 — Concurrency: `version` canonical, `If-Match` alias

Optimistic concurrency uses `version` in the request body / `--version` CLI
flag as canonical. `If-Match: <version>` header was proposed as an alias. One
mechanism, two spellings; pick the body form in examples.

> **Status.** The `If-Match` header alias was **never implemented** —
> `version` in the request body is the only concurrency mechanism in the HTTP
> layer. `index.md` and `workspace-and-boards.md` state this correctly.

- **Affected:** `index.md` §11, `index.md` (examples use
  `--version`).

### D6 — Repeating entries have stable ids

Each appended repeating entry gets a server-generated stable `entry_id`.
Mutate/address by `entry_id`, not array index. Events (`item_appended`,
`item_updated`, `item_removed`) carry `entry_id`. Index-based addressing was a
concurrency hazard (stale views + concurrent append/delete shifted indices).

- **Affected:** `index.md` §6/§8/§11, `workspace-and-boards.md` §3,
  `index.md`.

### D7 — `take-next` fully specified

`POST /cards/take-next` picks the oldest matching unowned card
(`updated_at ASC, id ASC`), atomically sets `owner` (+ optional `status`) via
the same compare-and-set as `claim`, and returns it. Returns `200` with
`card: null` when nothing matches (not an error — agents retry on a schedule).
On a concurrent claim race, `409` → client retries. Idempotent retries with
the same `Idempotency-Key` return the *same* card (not a new pick).

- **Why:** "atomic pick one" was undefined on ordering, empty result, and
  retry semantics — all load-bearing for multi-agent fleets.
- **Affected:** `index.md` §11.

### D8 — Schema versioning: pure versioned snapshots

Each `schema_version` is an immutable snapshot of the field list. A card pins
a version and validates against that snapshot. A field removed in v2 is simply
*absent* from v2; v1 cards keep it because they validate against v1. The
`deprecated` flag is optional **within the current version** for advance
warning only — it is not how removal works. No "legacy, readable but not
writable" muddle.

- **Why:** the old text mixed snapshot-versioning with in-place deprecation.
- **Affected:** `index.md` §5, `workspace-and-boards.md` §6.

### D9 — Event `diff` shapes are normative

`index.md` §8 now enumerates the exact `diff` object for every event type.
Hooks receive this JSON on stdin; the contract must be stable and precise.

### D10 — Error catalog

`index.md` §10 now lists error types and the fields each carries
(`validation_failed`, `unknown_enum`, `unknown_tag`, `unknown_user`,
`unknown_field`, `transition_illegal`, `version_conflict`, `not_found`,
`target_card_missing`, `schema_version_mismatch`, `actor_required`).
Agents self-correct by programming against these.

### D11 — SSE resumability via `Last-Event-ID`

`GET /events/stream` supports `Last-Event-ID` (and `since=`) for replay from a
cursor. A dropped connection no longer means a missed `failed` transition.

### D12 — Hook guarantees stated

Hooks are **at-most-once** by default (non-zero exit is logged, not retryped).
Hook *spawn* is ordered with the event; hook *completion* is async and may
overtake earlier hooks. Critical paths should use a `service` extension with
its own retry/idempotency. Documented in `index.md`.

### D13 — Mirror import is version-gated

`cards import --mirror` treats each file as a PATCH: the file's frontmatter
must declare the `version` it was edited from; stale imports are `409`
rejected. Prevents a human git edit from clobbering agent updates silently.

### D14 — History vs. retention reconciled

The **materialized card (including repeating fields) is the durable work
product.** The **event log is the audit/coordination layer** and may be
trimmed via `event_retention_days`. Trimming events never loses structured
work product (work logs, sources, status updates) — that lives in the card.
The original "history is the work record" thesis now refers to the *event
stream as coordination memory*, not as the only copy of results.

### D15 — Agent coordination loop is a first-class concept

Named loop: **introspect → take-next → work → append evidence → transition →
comment.** Drives MCP tool grouping, skills, and the lifecycle examples.
Documented in `index.md` §13 and `mcp.md`.

### D16 — MCP surface defined

New [`mcp.md`](extensions/mcp.md): one create tool per card type (typed input from the
schema), plus generic tools (`claim`, `take-next`, `append`, `link`,
`comment`, `upgrade-schema`, `events`). Tool inputs embed `version` for
concurrency. Generated from `GET /workspace`.

### D17 — Link types may constrain source/target card types

`LinkType` optionally declares `source_types` / `target_types` (card type ids).
Mismatched links are rejected with the valid set echoed. Stops an agent from
`sent-to`-linking a research card to a printer.

### D18 — Minor consistency fixes

- `index.md` no longer references "v0.2"; pinned to v0.4.
- `epic-of` either added to default `link_types` or removed from the
  common-ids table — here removed from defaults (keep boards explicit).
- Board `card_type_ids` is sugar merged into `default_filter`; documented.
- `created_at`/`updated_at`/event `at` are **server-set only**.
- `command` extension kind renamed to `run` (was `command`), removing the
  collision with the dropped `command` field type. `cards do <id>` unchanged.

## Deferred (not in v0.4 core)

- Definition-of-Done checklist gating (`enforce_dod`) — candidate extension.
- Per-board tag subsets.
- Cross-workspace links.
- Outbound signed webhooks (SSE covers v1).
- Nested repeating fields.
- Strong identity/ACL model.

## Backlog notes (from the 2026-07 debt pass)

- **`fieldSchema` duplication (mcp/openapi).** Both `internal/mcp` and
  `internal/openapi` map `core.FieldDef` → JSON Schema with genuinely different
  signatures (mcp handles `required`/`x-required`). Conceptual duplication, not
  copy-paste; consolidate only as part of a deliberate shared schema-builder
  package decision.
- **Filter-DSL AST package.** The §9 filter compiler lives in
  `internal/sqlite/filter.go` (the store owns SQL). Extracting a
  backend-neutral `internal/filter` AST becomes the right move if and only if
  a second Store backend is actually scheduled — don't pre-build it.

## Backlog notes (from the 2026-07 reactive-coordination milestone)

- **`GET /v1/breaches` temporal items.** `Service.Breaches` (seam 3d/3e)
  reports current `wip_exceeded`/`lane_drained`/`card_blocked` state but not
  `status_timeout`/`card_idle` — timeboxed out of the milestone. Natural
  extension: reuse `rebuildStatusTimeout`/`rebuildCardIdle`'s scans, checking
  each candidate's deadline against `now` instead of arming it.
- **>500-card column census.** `ListCards` caps a page at 500
  (`internal/sqlite/clampCardLimit`). The condition census / breach queries
  (`evaluateColumn`, `Breaches`, blocked lookups) pass `Limit: 500` and are now
  honored up to that ceiling — but a single column/board holding **more than
  500 matching cards** would still under-count. Not a concern at coordination
  scale (typically <100k cards, few hundred per column); if it ever matters,
  give the census an unclamped `CountCards`/iterator path rather than raising
  the ceiling further. Export already sidesteps this by cursor-paginating.

## Design-doc freeze (2026-07-10)

- **`docs/design/auth.md` (proposed) and `docs/design/core-boundaries.md`
  (exploration) are frozen as the accepted direction** for identity/attribution
  and core-vs-client boundaries, after two adversarial review passes. From this
  date, these two documents change only via (a) an implementation PR that
  discovers reality diverging from the doc, or (b) an explicit re-open note
  from the project owner naming the section. Drive-by prose passes are not a
  reason to edit. Board cards `card_f570b35b`, `card_61040a3e`, and
  `card_350b1bac` were reconciled against the frozen shape the same day.
- Decisions frozen with them: auth mode matrix `none|token|proxy` with
  attribution (not access control) as the core concern; opaque bearer token as
  the reference credential; force = per-write escape hatch that skips
  declarative rules AND board callbacks but never identity verification
  (`diff.forced` on `status_changed`, per-board `settings.allow_force: false`
  opt-out); declarative `Board.Transitions` stays core data, programmable
  vetoes live at the validator boundary; `BoardPresentation`/`TypeTheme` stay
  in core as optional presentation metadata (clients may ignore).
- The PHILOSOPHY §1/§7 edits drafted in CORE-BOUNDARIES §5 land **with the
  first `token`-mode implementation**, not before.

## Demo-workspace seed policy (2026-07-10)

**Decision (P1b of `docs/plans/sprint-2026-07-10.md`): keep the onboarding
cards.** A fresh install from source should greet the user with a working,
self-documenting board, not an empty one.

**Persona.** Someone cloning/installing cards from source and running it for
the first time (`cards init`, or the zero-config serve path that falls back to
the global workspace). Not the maintainer's dogfood board — that's a separate
surface (see below).

**Chosen option: (b) auto-seed into any empty workspace — already implemented,
no new code.** `internal/starter/SeedWelcome` (wired at
`cmd/cards/workspace.go:121`, covered by `internal/starter/starter_test.go`)
creates a five-card onboarding tutorial the first time a workspace has zero
cards: *Welcome to Cards · Make it yours · Add a board per project · Drive it
from the CLI and agents (MCP) · Back up and move your workspace*. The cards
are authored as real cards (type `task` on a `welcome` board) so the tutorial
*is* the product demonstrating itself, and they reference `docs/index.md`
for depth.

**Idempotency is the safety property.** `SeedWelcome` is a no-op the moment the
workspace holds any card, so it never re-seeds or clobbers a user's real work —
the tutorial appears once and quietly stops mattering as soon as the user
creates their own first card. `Scaffold` has the same guard on
`definitions/workspace.json`. Options (a) "demo-only fixtures" and (c) "remove
entirely" were both rejected: (a) would deny fresh installs the guided start;
(c) throws away a genuinely good first-run experience.

**Acceptance check.** A fresh `cards init <dir>` (or serving into an empty
workspace) shows the five welcome cards, they read as a tutorial rather than
scaffolding noise, and running against a workspace that already has cards adds
none. `internal/starter/starter_test.go` pins the seed + idempotency.

**Two distinct surfaces — don't conflate them.**
- **Fresh-install onboarding** (this decision): the `starter` welcome board,
  generated on demand, never committed.
- **The maintainer dogfood board** (`examples/demo-workspace/`, committed as
  `backlog.jsonl`): a real, groomed engineering board that happens to also
  carry a few onboarding-titled cards in its history. It is kept as-is — it
  doubles as a realistic example workspace shipped with the repo. If those
  specific cards ever read as clutter *on the dogfood board*, that's a board-
  grooming task, independent of the first-install onboarding policy above.
