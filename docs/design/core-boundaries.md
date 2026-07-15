# Core Boundaries — what belongs in cards, what belongs in a client

**Status:** exploration · **Updated:** 2026-07-10 (revised after review)
**Contributors:** Jeremy (vision), Claude (audit + revision)
**Not normative.** This document is a working analysis to guide iteration
toward v0.5. When any section here promotes to spec, it moves into
`docs/spec/SPEC-*.md` or into a dedicated proposed doc under `docs/design/`.
Auth details have already extracted to [`auth.md`](auth.md); §4 below is a
short pointer.

---

> **Overlay principle.** Core guarantees an **honest, schema-visible
> coordination memory**. It does not guarantee a complete product surface
> or a complete trust boundary. Hosts and clients supply the rest. The
> bundled ones (the web UI, the token-based auth reference, `hooks`
> supervisor) are examples that dogfood the guarantees.

---

## 0. Purpose

Cards has drifted, in small ways, from what it says it is. This document
inventories the drift, ties each item to a code location or a card on the
engineering board, and proposes where the seam between *core framework* and
*bundled reference client* should actually sit. It is written to be re-read
in three months and still be executable — every claim is anchored to a
file, a card ID, or a quoted principle.

## 1. Sharpened vision (project owner, 2026-07-10)

Quoted verbatim from the working session that prompted this doc:

> Cards is a minimalist pragmatic app — a framework that others can integrate
> or build on top of. The web UI can be considered almost a separate app: it's
> a client for cards. It ships with it, but it's not the only one that could
> exist. It's a showcase that helps us dogfood and allows users to get a
> usable experience out of the box.
>
> The cards **core** should not have strong opinions or rules about
> transitions, user profiles, or frontend components. Those are closer to
> the web app and could even be extensions.
>
> Our auth implementation is only meant to be an example: minimalist auth
> enforces defined users that can own cards or have auditable comments.
> Requiring auth means we avoid mistakes with attribution or username drift…
> The most basic thing we can offer is a way to register a user, get a
> unique id and issue an access credential, and allow distinct users to be
> identified. I don't see granular roles being important for the core, but
> we should provide building blocks so if someone needed that there is a
> path for them.

Two consequences of that framing:

1. The *identity* system is a core concern (attribution honesty).
   The *authorization* system is not (that's an extension or host concern).
2. The bundled web UI is a **reference client**, not the product surface.
   Its opinions do not belong in the core service layer; declarative
   *hints* it publishes in workspace JSON, however, may live alongside
   type/board schemas — see §3.2.

## 2. Where philosophy.md already aligns

`docs/concepts/philosophy.md` was written with much of this in mind:

- **§1 "Small core, big composition"** — *"The core does cards, fields,
  events, links, comments, columns, and storage. Everything else —
  dispatchers, agents, UIs, sync, reports, validations — is an external
  process."* The framework/showcase seam in principle.
- **§3 "Schemas, not magic"** — behavior comes from *explicit typed
  schemas the agent or human can introspect*. This is directly load-bearing
  for the transitions discussion in §3.3.
- **§5 "Hooks, not engines"** — no automation engine, no rules language.
  Constrains where behavior may live.
- **§6 "Extensions over plugins"** — extensions are independent processes
  over HTTP+events. Constrains the shape any new seam may take.
- **§7 "YOLO defaults"** — *"Authentication and isolation are the host's
  responsibility."*

The vision above is compatible with these principles. It sharpens two of
them (§1's UI-is-external and §7's what-auth-means-for-us) without
contradicting any. Draft PHILOSOPHY edits are in §5.

## 3. Where the tree drifts today

Ordered by severity. Each item cites a file (or card) and names the
honest form.

### 3.1 The `User` type has no credential; `X-Work-Cards-Actor` is trusted blind

**Location:** `internal/core/types.go:133` (`// User is an open identifier
(no auth in v1).`) and `internal/httpapi/api.go:197` (`apiRegisterUser`).

**State:** `POST /v1/users` registers `{id, display_name, kind}` and stores
it. Nothing verifies the actor header against the registry. There is no
credential column, no token issuance, no `Authorization`/`Bearer` handling
anywhere in the tree (grep-verified: zero hits in `internal/httpapi`,
`internal/cli`, `internal/core`).

**Symptom observed:** during this session an agent (via curl) passed
`X-Work-Cards-Actor: jeremy` and the event log accepted it. The engineering
board now carries agent-authored comments attributed to a human.

**Honest form:** covered in full by [`auth.md`](auth.md). Short summary:
`--auth <none|token|proxy>` mode matrix; `POST /v1/users` issues an opaque
token; writes carry `Authorization: Bearer`; adapters cover HTTP, CLI, MCP
(env-at-spawn), and hooks (pass-through). Bootstrap via serverless CLI.

**Related cards:** `card_61040a3e`, `card_350b1bac` — both to be
reconciled against auth.md.

### 3.2 Presentation metadata in core — real, but not "drift"

*This section was rewritten after review. The earlier framing overshot.*

**Location:**
- `internal/core/types.go:169` — `BoardPresentation`
- `internal/core/types.go:75` — `TypeTheme` on card types

**State:** grep-verified that `Presentation`, `CardPreview`, and
`LaneGroupBy` are consumed only by `internal/httpapi/render.go` and
`internal/httpapi/ui.go`. Zero consumers in CLI, MCP, or elsewhere.

**Two views, both partly right:**

| View | Implication |
|---|---|
| Strict package purity | Move types out of `internal/core`; use `json.RawMessage` or a namespaced `Extensions` bag; presentation lives in httpapi |
| Portable workspace-as-data | Board and type JSON in git *are* the product artifacts; icons, accent, theme names are **optional presentation metadata** any rich client may honor. A TUI ignores unknown keys. JSON with fields a client doesn't use is not pollution; it is progressive disclosure. |

**Recommended framing (supersedes the earlier "drift" wording):**

> Core **may** carry optional presentation metadata declared in board/type
> definitions. Core **must not** require it for correctness of card
> operations, and **must not** branch write paths on it. Clients may ignore
> unknown keys.

Under this framing the current shape is defensible: `TypeTheme` in
particular lives next to *type schema* — a place human authors edit — and
belongs in the schema-authoring vocabulary, not banished to a UI package.
`FieldDef.OptionThemes` (define) and `BoardPresentation.StyleField` (activate)
follow the same rule: optional presentation metadata; boards opt in; write
paths ignore them. Precedence is normative in `docs/design/style-field.md`.

**What we should still do (small, cheap):**
- Add a one-line type comment on `BoardPresentation` and `TypeTheme`
  clarifying they are optional presentation metadata that clients may
  ignore — makes the seam obvious to a code reader.
- Add one paragraph to `docs/reference/workspace-and-boards.md` documenting
  the "clients may ignore" contract, so a future TUI author knows their
  obligations.

**What we should not do (dropped from earlier plan):**
- Move `BoardPresentation` / `TypeTheme` out of `internal/core/`. Not a
  drift bug; not worth the cost to export/import, MCP introspection, and
  schema-authoring UX. Revisit only if maintainability actually hurts.

### 3.3 Transitions — data vs. enforcement vs. observation

*Rewritten after review. The earlier framing conflated three layers.*

There are three separate concerns to keep straight:

| Layer | Today | Should it live in core? |
|---|---|---|
| **Declarative graph** — `Board.Transitions map[string][]string` | Data in board JSON (`internal/core/types.go:222`) | **Yes.** It is a typed constraint boards declare, same family as columns and WIP limits. `schemas, not magic.` |
| **Enforcement** — Service refuses writes that violate the graph | `internal/core/service.go:751, :1456` | **Open question.** Batteries-included dogfood default vs. seam refactor. |
| **Observation** — `transition_rejected` event | `internal/core/events.go:283` | **Yes**, wherever enforcement lives. |

> **Bridging the vision quote and this table.** The §1 quote — *"the cards
> core should not have strong opinions or rules about transitions"* — we
> read as banishing **programmable** transition rules from core (DSLs,
> scripts, multi-step engines). It does not banish a **declarative,
> introspectable** transitions map living in board JSON. Without this
> distinction, the quote and the table look contradictory; with it, they
> reinforce each other.

**What the vision statement rules out** — a *programmable workflow engine*
(DSL, scripts, multi-step orchestration) inside core. That's §5 territory.

**What the vision statement does NOT rule out** — a declarative transitions
map in workspace JSON. Introspectable, exportable, agent-visible, versioned
in git. That's exactly `schemas, not magic`; banishing it would overshoot
into "column semantics are data, workflow edges must not be."

**Sharper boundary (recommended wording for the RFC):**

> Core stores and (optionally) evaluates **pure, declarative board
> constraints** that are fully visible in workspace JSON. Core does **not**
> host programmable workflow (DSL, scripts, multi-step engines).
> Programmable vetoes live at a **validator boundary** (subprocess). Moving
> *declarative* enforcement out of core is optional; killing declared
> transitions wholesale is not the goal.

This preserves take-next, force-move, `transition_rejected`, and WIP
interaction without a philosophical purge.

**Force flag — revised after review.**

Force is a **per-write request field** (`force: true` in a PATCH body;
`req.Force` in the service layer; surfaced as `--force` in the CLI) — not
a serve-level mode. Today a forced status patch bypasses
`EnforceTransitions` in `internal/core/service.go:751`. An earlier draft
of this doc proposed
tightening force so it could not break the declarative graph, only the
board-callback subprocess. That would have been a *behavior change* — the
drag-and-force-confirm UX in the web UI relies on today's permissive
force, and a demo/engineering board dogfooder recovering a mis-transitioned
card at 11pm shouldn't have to edit board JSON to do it. YOLO force is
part of the batteries.

**Revised normative shape for `--force` on status writes:**

- **Force skips BOTH the builtin declarative rules AND the board-callback
  validator.** This matches today's behavior; changing it would break
  force-move UX for real dogfood.
- **Force NEVER skips identity verification.** Under `token`/`proxy`, a
  forced write still requires a valid credential; force is a policy
  override, not an authentication override. Attribution of the forced write
  is the resolved (verified) identity.
- **No new event type.** The forced-ness rides as `status_changed` with
  `diff.forced: true` (or an equivalent field on the existing diff shape).
  Consumers who care can filter by that field; consumers who don't see the
  same event they always did. Adding a new `EventType` (`transition_forced`)
  would cost SPEC + bus filter + INTEGRATOR-REFERENCE churn without
  evidence of consumer demand — not worth it.
- **Optional board opt-out:** a board may declare `settings.allow_force:
  false` in its JSON to make its declarative graph a hard floor for that
  board (no force override there). This is *data*, same family as
  `enforce_transitions` — not a rules language, not code. Strict boards
  set the flag; the engineering board leaves it default (permissive).

Rationale: force stays a YOLO escape hatch by default (matches current
behavior and web-UI flow), but boards that want strict workflow policy
have a declarative opt-out. Nothing programmable in core.

**Related card:** `card_f570b35b` — already backlogged. Update its body to
adopt this three-layer framing and the revised force-flag semantics before
code starts. The `allow_force` field is a small SPEC addition to
`Board.Settings` (see `data-model.md` §Board Settings).

### 3.4 philosophy.md §7 conflates access control with attribution

**Location:** `docs/concepts/philosophy.md:52` — *"Authentication and
isolation are the host's responsibility."*

**Why this drifts:** the sentence is right for access control (network
reachability, TLS termination, ACLs) and wrong for attribution:

- **Access control** (who can *reach* the port) → the host's job. Caddy,
  Tailscale, iptables. Core does not own this.
- **Identity attribution** (who *made* this write, verifiably) → the
  core's job. Otherwise the event log lies.

**Honest form:** split §7 into two sentences, one for each concern. Draft
language in §5.

### 3.5 Minor: `WorkspaceSettings.DefaultUser` is a YOLO fallback

**Location:** `internal/core/types.go:147`.

**State:** a workspace-level fallback actor for requests with no identity.
Correct behavior under `--auth none`. Under `--auth token` it becomes a
hazard (silent misattribution on a missing token).

**Honest form:** normative note in auth.md — *"`DefaultUser` is consulted
only when auth is `none`."* (Already in [`auth.md §1`](auth.md#1-modes--the-auth-matrix).)

### 3.6 Minor: docs undersell "the web UI is one client"

**Grep-verified:** `showcase`, `reference client`, and `example client`
appear zero times in `docs/`. `philosophy.md §1` says UIs are external
processes in principle, but a reader looking at the code (a single binary
serving `/v1` and `/ui/`) will not conclude that the `/ui/` is
architecturally optional. One sentence per doc is enough to close this —
draft language in §5.

## 4. Auth — pointer to `auth.md`

The auth direction has extracted into [`docs/design/auth.md`](auth.md)
(`status: proposed`). Rather than duplicate content, this section carries
only the high-level shape and the header names, so a reader hitting
CORE-BOUNDARIES can orient without a jump.

**Mode matrix:** `--auth <none|token|proxy>`.

- `none` — trust ambient hints (`X-Work-Cards-Actor` → `CARDS_USER` →
  `DefaultUser`). YOLO default; not a prototype mode.
- `token` — `POST /v1/users` issues an opaque token; writes carry
  `Authorization: Bearer`. Anti-spoof rule: unverified hints MUST NOT
  override authenticated identity.
- `proxy` — trusted header from a loopback/subnet reverse proxy, mapped
  to a registered user. For homelab-behind-Caddy and OIDC-fronted
  deployments.

**Adapters:** HTTP middleware, CLI (`~/.cards/credentials` or `$CARDS_TOKEN`),
MCP (`CARDS_TOKEN` env at process start, not per-tool-call), hooks
(pass-through — hooks do not authenticate).

**Bootstrap:** serverless `cards users register` always trusted
(filesystem-authoritative). Under `token`, HTTP registration is gated by
an existing valid Bearer; v0 stopgap treats the first CLI-registered user
as "admin."

Everything else — the interface signatures, identity-before-idempotency
rule, force-flag semantics, anti-spoof tests, non-loopback bind warning —
lives in [`auth.md`](auth.md). This section stays intentionally short so
CORE-BOUNDARIES and auth.md cannot fork.

## 5. Draft language for the two PHILOSOPHY edits

**PHILOSOPHY §1, add at the end:**

> The bundled web UI at `/ui/` is a **reference client** — a showcase and
> dogfood surface — not the only product boundary of the HTTP + event
> contracts. A third-party client that speaks the same contracts is a
> first-class citizen. The reference client stays in-repo and first-class
> for dogfood, even if architecturally replaceable.

**PHILOSOPHY §7, rewrite:**

> The default deployment is local-only, single-tenant, trusted environment;
> the default auth mode is `none`. We do not ship permission theater.
>
> **Access control** — who can reach the process at all — is the host's
> responsibility (Caddy, Tailscale, firewall).
> **Identity attribution** — who is credited with each write — is ours.
> Under `token` or `proxy` auth modes, cards verifies the claimed identity
> before writing the event; the event log's honesty is a core guarantee,
> not a host concern.
>
> *Modes and verification mechanics: `docs/design/auth.md` (proposed).
> This footer stays until auth.md promotes to SPEC.*

These edits land **with the first `token`-mode implementation**, not before
— landing "we verify" language while nothing verifies would create a
normative vacuum. They are quoted here so future iterations can grade
drift against them, and so [`auth.md`](auth.md) can refer to them without
pre-empting the actual doc change.

## 6. Transitions and triggers — pointer

The transition-enforcement drift is covered by `card_f570b35b`. This
document does not duplicate that RFC's content. Three cross-links only:

- The three-layer framing in §3.3 (declarative graph = data = keep;
  enforcement = open question; observation = keep) is the correction the
  RFC card body should adopt.
- The **hooks/validators covenant** in `card_f570b35b` is the load-bearing
  distinction: hooks observe, validators veto. Both live at the boundary.
- **Force flag semantics** are decided in §3.3 (revised): force skips
  BOTH declarative rules AND callback (matches today's YOLO); never skips
  identity verification; rides `status_changed` with `diff.forced: true`
  rather than a new event type; optional per-board `settings.allow_force:
  false` for strict boards. auth.md carries only the identity invariant
  (forced writes still attribute the verified actor); the mutation policy
  lives here and in the transitions RFC.

## 7. Core-vs-client boundary — what would a second client look like?

A useful thought experiment. Imagine a TUI client, a Slack bot, or a
mobile widget for cards. What does each need? What does each *not* need?

| Need | Provided by core today? | Comment |
|---|---|---|
| List cards, filter, paginate | ✓ `GET /v1/cards` | Aligned |
| Create / patch / claim | ✓ `/v1/cards` write verbs | Aligned |
| Attributed events | ✓ event log with `actor` | **Drift — actor is unverified today (§3.1)** |
| Live updates | ✓ `/v1/events/stream` (SSE) | Aligned |
| Schemas for card types | ✓ `GET /v1/workspace` | Aligned |
| Board membership / columns | ✓ `Board.CardTypeIDs`, `Columns` | Aligned |
| Rendering hints (icons, colors) | ✓ `Board.Presentation`, `TypeTheme` | **Optional metadata — clients may ignore (§3.2)** |
| Enforce workflow rules | ✓ `EnforceTransitions` (declarative graph in board JSON) | **Aligned as data.** Enforcement is the open question (§3.3) |
| Register + authenticate | ✗ (no credential path) | **Missing — [`auth.md`](auth.md)** |

Two drift rows + one missing row + two neutral rows.

**Reference client stays first-class.** Explicit for the doc's future
readers: the bundled web UI at `/ui/` is architecturally replaceable, but
it is not architecturally *disposable*. It stays in-repo and continues to
receive first-class dogfood attention. Otherwise the feedback loop reverses:
UI stops teaching core about real use.

## 8. Decisions still open

Not decisions this document makes — decisions the next iteration should
close.

1. **Enforcement seam.** Move `EnforceTransitions` behind the validator
   interface from `card_f570b35b`, or leave it as the default in-core
   evaluator. Both preserve the declarative graph. Recommend seam refactor
   (matches the RFC's chain-order model), but keep in-core evaluate as the
   built-in first link in the chain. Force-flag semantics: **decided** in
   §3.3 (revised) — permissive by default, per-board `allow_force: false`
   opt-out, no new event type.
2. **Bootstrap admin distinction.** v0 treats the first CLI-registered
   user as "admin" for HTTP registration. That's a stopgap. Long-term
   options: an explicit `--admin` flag on `users register`, a workspace
   config field, or delegate to an extension. Decide before v0.5 spec
   promotion.
3. **`CARDS_TOKEN` vs. `--token` vs. credentials file.** All three shapes
   are ergonomic; recommend all three exist, with credentials file as the
   preferred long-lived form (matches `gh`).
4. **Historical misattribution.** Do not rewrite; accept the honesty gap;
   date the flag day in `design-notes.md`. Decide if the demo workspace's seeded
   Welcome cards should be re-authored under the flag-day actor (product
   call, tied to P1b of sprint 07-10).
5. **Presentation metadata contract.** Land the "clients may ignore"
   note in DEVELOPER-REFERENCE. Do **not** file a "move BoardPresentation
   out of core" card — dropped from earlier plan (§3.2).
6. **`Kind` field usage.** `human` vs `agent` is useful for
   display/filtering. Confirmation: never branch *authorization* on
   `Kind` in core — that would drag us into RBAC by the back door.

## 9. Follow-up ledger (updated at the 2026-07-10 freeze)

This document and `auth.md` were **frozen as the accepted direction on
2026-07-10** (see `docs/design-notes.md` freeze entry). From that date they change
only via (a) impl PRs that discover reality or (b) an explicit re-open from
the owner.

**Done at the freeze:**

1. ✓ `card_61040a3e` reconciled — now carries only review request +
   ROADMAP §1 link; auth.md is the single source of truth for spec content.
2. ✓ `card_350b1bac` reconciled — retitled token-primary (*"AUTH reference
   impl: register issues bearer token; `--auth token` verifies writes"*);
   Basic adapter is a deliberate later option; serverless bootstrap called
   out.
3. ✓ `card_f570b35b` updated — adopted the three-layer framing from §3.3
   and is now the **durable home of the force policy** (permissive default,
   never skips identity verification, `diff.forced` on `status_changed`,
   per-board `allow_force: false` opt-out).

**Still to do (each is small; none in scope for sprint 07-10):**

4. **philosophy.md edits** — apply the §5 language. Lands **with the first
   `token`-mode implementation**, not before.
5. **Deprecate `WorkspaceSettings.DefaultUser` under `--auth != none`** —
   trivial; the normative rule is already in auth.md §1; enforce in the
   reference impl (`card_350b1bac` acceptance covers it).
6. **Optional presentation metadata note in DEVELOPER-REFERENCE** — one
   paragraph; matches §3.2's recommendation. Plus the one-line type
   comments on `BoardPresentation` / `TypeTheme`.
7. **design-notes.md flag-day entry** — one bullet when auth activation lands,
   dating the honest-attribution boundary.

Deliberately **not** filed: the "Move `BoardPresentation` + `TypeTheme`
out of `internal/core/`" card. Rationale in §3.2.

## 10. Cross-references

**Cards on the engineering board:**

- `card_3f225267` — sprint 07-10 tracker
- `card_61040a3e` — Auth interface RFC (to be reconciled against `auth.md`)
- `card_350b1bac` — reference auth implementation (to be reshaped —
  token-primary, not htpasswd-primary)
- `card_fa239a92` — non-loopback bind warning (unchanged)
- `card_c7a70b64` — P2 code-review batch (already aligned)
- `card_f570b35b` — transitions-as-callbacks RFC (to be updated with the
  three-layer framing and force-flag semantics)
- `card_86515fd2` — UI wave tracker (orthogonal)

**Repository docs referenced:**

- `docs/design/auth.md` (`status: proposed`; extracts §4)
- `docs/concepts/philosophy.md` (§§1, 3, 5, 6, 7, 10 — §5 above drafts the
  edits)
- `docs/concepts/index.md`
- `docs/spec/api-surface.md` (§§ users, actor; gains
  "Security / Actor" subsection when auth.md's reference impl lands)
- `docs/spec/data-model.md` (User, WorkspaceSettings)
- `docs/architecture/index.md`
- `docs/architecture/design-system.md` (the web UI's own design contract — the
  reference client's charter)
- `docs/reference/workspace-and-boards.md` (gains the "optional
  presentation metadata; clients may ignore" note)

**Source anchors:**

- `internal/core/types.go:75, 133, 143, 147, 169, 222`
- `internal/core/service.go:751, 1456`
- `internal/core/events.go:283`
- `internal/httpapi/api.go:197`
- `internal/httpapi/server.go:275`
- `internal/hooks/hooks.go:2-4, 140`
