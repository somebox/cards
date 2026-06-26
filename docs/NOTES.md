# Design Notes — v0.4

What changed in the v0.4 design pass and why. These decisions are now
**implemented** — the core kernel, HTTP API, CLI, MCP server, web UI, and
hook supervisor are built and dogfooded. Read alongside [`SPEC.md`](SPEC.md)
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
- **Affected:** `ARCHITECTURE.md` (health endpoint singular; context merge
  produces one workspace), `SPEC.md` §3.

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
- **Affected:** `SPEC.md` §6, `DEVELOPER-REFERENCE.md` §3/§6/§8,
  `LIFECYCLE-EXAMPLES.md` (examples no longer use removed types).

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
- **Affected:** `DEVELOPER-REFERENCE.md` §2/§5, `SPEC.md` §7,
  `LIFECYCLE-EXAMPLES.md` A2/A3.

### D4 — Actor is normative

Every write supplies an actor via the `X-Work-Cards-Actor` header (or
`actor` body field as an alias). Resolution: header → `CARDS_USER` env →
workspace `default_user` → `403 actor_required`. The server sets `created_by`
and event `actor` from this; clients cannot forge arbitrary actors beyond
their configured identity in a trusted-local model. (Stronger identity binding
is an extension/host concern; see `EXTENSIONS.md`.)

- **Why:** `claim`, `take-next`, and event attribution all depend on an actor,
  but it was nowhere defined.
- **Affected:** `SPEC.md` §11 + new §12 (Actors and authorization).

### D5 — Concurrency: `version` canonical, `If-Match` alias

Optimistic concurrency uses `version` in the request body / `--version` CLI
flag as canonical. `If-Match: <version>` header is accepted as an alias. One
mechanism, two spellings; pick the body form in examples.

- **Affected:** `SPEC.md` §11, `LIFECYCLE-EXAMPLES.md` (examples use
  `--version`, note the alias).

### D6 — Repeating entries have stable ids

Each appended repeating entry gets a server-generated stable `entry_id`.
Mutate/address by `entry_id`, not array index. Events (`item_appended`,
`item_updated`, `item_removed`) carry `entry_id`. Index-based addressing was a
concurrency hazard (stale views + concurrent append/delete shifted indices).

- **Affected:** `SPEC.md` §6/§8/§11, `DEVELOPER-REFERENCE.md` §3,
  `LIFECYCLE-EXAMPLES.md`.

### D7 — `take-next` fully specified

`POST /cards/take-next` picks the oldest matching unowned card
(`updated_at ASC, id ASC`), atomically sets `owner` (+ optional `status`) via
the same compare-and-set as `claim`, and returns it. Returns `200` with
`card: null` when nothing matches (not an error — agents retry on a schedule).
On a concurrent claim race, `409` → client retries. Idempotent retries with
the same `Idempotency-Key` return the *same* card (not a new pick).

- **Why:** "atomic pick one" was undefined on ordering, empty result, and
  retry semantics — all load-bearing for multi-agent fleets.
- **Affected:** `SPEC.md` §11.

### D8 — Schema versioning: pure versioned snapshots

Each `schema_version` is an immutable snapshot of the field list. A card pins
a version and validates against that snapshot. A field removed in v2 is simply
*absent* from v2; v1 cards keep it because they validate against v1. The
`deprecated` flag is optional **within the current version** for advance
warning only — it is not how removal works. No "legacy, readable but not
writable" muddle.

- **Why:** the old text mixed snapshot-versioning with in-place deprecation.
- **Affected:** `SPEC.md` §5, `DEVELOPER-REFERENCE.md` §6.

### D9 — Event `diff` shapes are normative

`SPEC.md` §8 now enumerates the exact `diff` object for every event type.
Hooks receive this JSON on stdin; the contract must be stable and precise.

### D10 — Error catalog

`SPEC.md` §10 now lists error types and the fields each carries
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
its own retry/idempotency. Documented in `EXTENSIONS.md`.

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
Documented in `SPEC.md` §13 and `MCP.md`.

### D16 — MCP surface defined

New [`MCP.md`](MCP.md): one create tool per card type (typed input from the
schema), plus generic tools (`claim`, `take-next`, `append`, `link`,
`comment`, `upgrade-schema`, `events`). Tool inputs embed `version` for
concurrency. Generated from `GET /workspace`.

### D17 — Link types may constrain source/target card types

`LinkType` optionally declares `source_types` / `target_types` (card type ids).
Mismatched links are rejected with the valid set echoed. Stops an agent from
`sent-to`-linking a research card to a printer.

### D18 — Minor consistency fixes

- `LIFECYCLE-EXAMPLES.md` no longer references "v0.2"; pinned to v0.4.
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
