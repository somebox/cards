# Roadmap

Forward-looking work, captured out of the engineering board's backlog so the
board stays focused on near-term, actionable cards. Each item notes its status
and — where it came from a board card — the card's short id for traceability.

Status tags: **built** (shipped) · **partial** (some of it ships, rest open) ·
**proposed** (designed in docs, unbuilt) · **needs-design** (open questions to
resolve before building) · **deferred** (intentionally not now).

Framing note: Work Cards is deliberately a **local-first, single-tenant,
trusted-environment** tool. Authentication, permissions/ACLs, and alternative
storage backends are explicit non-goals for v1 and are pushed to the host
(reverse proxy / auth extension). The items below are the considered future,
not commitments for the current milestone.

---

## 1. Authentication & identity — *needs-design*

**No authentication exists today, by design.** Actor identity is trusted and
unauthenticated: the `X-Work-Cards-Actor` header → `CARDS_USER` env →
workspace `default_user`, honored verbatim across HTTP/UI/CLI/MCP. There is an
open user registry (`POST /v1/users`, no auth); a registered user id is
required only when assigning `owner` (claim / take-next). Docs treat auth as a
host/reverse-proxy/extension concern (`PHILOSOPHY.md`, `ARCHITECTURE.md`,
`SPEC-EVENTS-HISTORY.md §12`), and "strong identity/ACL" is a deferred item in
`NOTES.md`.

This needs **investigation and design thinking before any build**, not a
mechanical ticket. Open questions to resolve first:

- **Threat model / deployment tiers.** What are we actually defending against,
  and in which deployments? Keep localhost fully trusted; design auth for the
  "exposed beyond localhost" tier only.
- **Where the boundary lives.** Baked-in vs. an auth *extension* vs. a
  documented reverse-proxy recipe (e.g. proxy sets a verified actor header the
  core is told to trust). The current architecture leans toward the last.
- **Identity binding.** Today any client can claim any actor. Options range
  from signed actor tokens / per-user keys to delegating entirely to the proxy.
  How does this interact with agent (non-human) actors?
- **Authorization is separate from authentication.** See §2 — there is no
  permission model at all, and hooks cannot broker one. Decide whether authz is
  even in scope, or stays a non-goal.
- **Blast radius.** Auth touches the actor resolution chain in
  `httpapi/middleware.go`, MCP's session-bound actor, and the CLI client — a
  design doc should precede code.

Suggested next step: a short design RFC (`docs/design/AUTH.md`) covering the
above, reviewed before any implementation card is created.

---

## 2. Permissions / authorization — *deferred (non-goal in v1)*

There is **no** permission / ACL / role / policy concept anywhere, and the hook
system **cannot** provide one:

- `internal/hooks` is an **observe-only, post-hoc, at-most-once** supervisor.
  It subscribes to the event bus *after* a mutation is committed, spawns a
  subprocess with the event JSON on stdin, and only logs the result. A non-zero
  exit does **not** roll anything back (`EXTENSIONS.md`).
- Brokering authorization would require a **new synchronous pre-action hook
  point that does not exist** — a hook could only react to a completed change
  (e.g. issue a compensating write-back), never prevent it.
- `transition_rejected` is a workflow-rule **audit event**, not an authz
  decision; `ArtifactPolicy: local|uri` governs path confinement, not users.

"Jira-grade permissions, ACLs, SSO" are listed as non-goals (`SPEC-API-SURFACE.md`).
Revisit only if a multi-tenant deployment is actually scheduled; it would ride
on the auth design in §1 and (probably) a pre-commit hook seam.

---

## 3. Storage — *well-defined; SQLite is the sole backend*

SQLite is the only backend, but the seam is clean. `core.Store`
(`internal/core/store.go`) is a consumer-side interface; core/service depend
only on it, and the concrete `*sqlite.Store` is confined to `cmd/cards/`. An
in-memory `EventLog` fake already passes the same conformance suite.

- **Concrete near-ish work — SQLite read pool** *(proposed, card `87903967`)*.
  Today `MaxOpenConns(1)` serializes everything through one connection (single
  writer). Add a separate read pool for concurrent reads while keeping the
  single-writer discipline for mutations.
- **Alternative backends (e.g. Postgres)** *(deferred)*. Feasible for the
  service layer, but two things are SQLite-coupled: the query/filter DSL
  compiler emits SQLite SQL (`json_extract`, `internal/sqlite/filter.go`), and
  `export`/`import` are typed against the concrete store. `NOTES.md`
  deliberately declines to extract a backend-neutral filter AST until a second
  backend is actually scheduled. **Do not pre-build.**
- **Portable format (built).** `cards export` / `import` round-trip
  workspace + users + cards + links + comments + full event log as JSONL
  (`cmd/cards/portable.go`). This is a **backup/migration/git-portability**
  format — the system operates *from* SQLite, and `import` is a full-snapshot
  restore into a fresh empty DB (refuses if cards already exist). You cannot run
  without SQLite today; JSONL is not an alternate runtime source.
- **Migrations (built).** Schema-on-open with small self-gating migrations
  (`PRAGMA table_info` checks); no versioned migration framework. Fine for now.

---

## 4. Attachments — *built* — card `dafd0873`

An `artifact` field type and `internal/artifacts` (a content-addressed store —
SHA-256 dedup, MIME sniffing, symlink-safe path confinement) are now **wired
end-to-end** (Sprint A, Phase 4). Cards hold metadata only:
`{uri, mime, size, sha256}`.

Shipped: `Service.AddArtifact` (validates the field is a local-policy artifact
field, ingests via `Manager.Put`, records the metadata, emits `artifact_added`);
`Service.OpenArtifact` (serve path with `Resolve` confinement); the composition
root (`openWorkspace`) builds one `artifacts.Manager` per workspace at
`<dir>/artifacts` so every surface shares it. Surfaces: `POST
/v1/cards/{id}/artifacts/{field}` (raw body) + `GET /v1/artifacts/*` (confined,
traversal → 404); `cards attach <id> <field> <file>`; MCP `attach_artifact` /
`get_artifact` (base64, deliberate stdio asymmetry); and `/ui` card detail
renders an inline thumbnail (images) or download link, with `artifact_added` on
the board SSE stream. Design intent (`NOTES.md`, `ARCHITECTURE.md`):
`path/json/yaml/command/markdown` field types were deliberately dropped;
`artifact` (link-to-bytes) was kept because posting links to artifacts is a
stated core use case.

Remaining follow-ups (not blocking): browser multipart *upload* form (today
`/ui` renders but doesn't upload — attach via CLI/API); `artifact_policy: uri`
fields still take their URI via patch, not upload.

---

## 5. Events & reactive coordination

The conditions-engine milestone is **built**: all 9 condition events including
the temporal `status_timeout` / `card_idle`, the deadline scheduler,
persist-escalation, board-scoped events, resumable SSE, and `/breaches`
(instant conditions). Remaining:

- **Outbox / tailer (Step 4)** *(designed, **gated — deferred**; cards `d416cec3`,
  `180e7621`)*. Durable-log-driven dispatch that closes the
  commit-then-crash-before-dispatch live-delivery gap. Designed cold in
  [`docs/design/SUBSCRIBERS.md`](design/SUBSCRIBERS.md) and **deferred** by the
  Phase-6 go/no-go ([`docs/design/OUTBOX-GONOGO.md`](design/OUTBOX-GONOGO.md)):
  the log is now crash-safe (foundation gate passed), but no relied-upon consumer
  was observed losing events that matter, so building it now would be
  infrastructure ahead of need. Re-open on a real go signal.
- **`card_ready` on DAG progress** *(proposed, card `2898a658`)*. Fires when
  *all* dependencies are satisfied — distinct from the built `card_unblocked`.
- **Priority / rank + reprioritize-on-`lane_drained`** *(proposed, card
  `b3e0914b`)*. A rank field feeding `take-next` ordering. Also the prerequisite
  for UI drag-drop reordering (§9).
- **Temporal coverage in `/breaches`** *(partial)*. `Service.Breaches` reports
  WIP / lane / blocked only; it omits `status_timeout` / `card_idle`. Fix: reuse
  the `rebuildStatusTimeout` / `rebuildCardIdle` scans, checking each candidate
  against `now`. (`NOTES.md`.)
- **>500-card column census limitation** *(known limitation)*. `ListCards` caps
  a page at 500, so a column with >500 matching cards under-counts in
  `evaluateColumn` / `Breaches` / blocked lookups. Fix: an unclamped
  `CountCards` / iterator path rather than raising the ceiling. Also: census
  counts by *type* membership, so a board scoped by `DefaultFilter` (not type)
  isn't counted correctly.

---

## 6. Integration — webhooks *(designed, gated — deferred; card `a9f78958`)*

A first-class `webhook` extension kind: push events to external URLs with
retry + HMAC signing + cursor replay. Today only a shell `hook` can react to an
event (and only observe-post-hoc, per §2). Built on the outbox (§5), so it
shares the same deferral and design: see
[`docs/design/SUBSCRIBERS.md`](design/SUBSCRIBERS.md) — which pins the
`Consumer` model, reject-at-registration deliverability, the additive
(non-republishing) tailer topology, and the **enforced default-deny egress
allowlist** (registration + dispatch time) that any runtime `/v1/subscribers`
registration must ship with.

---

## 7. API surface — proposed / unbuilt

- **`View` type — orphaned** *(proposed)*. The type is declared in
  `internal/core` ("read-only in v1") but has **no** `Workspace.Views` field,
  route, CLI verb, or service wiring. Read-only parameterized card views
  (`/views/:id/cards`); a real feature to design or explicitly drop.
- **Workspace reload** *(proposed, card `4b507da7`)*. `POST /v1/workspace/reload`
  + `definition_reloaded` event so definition edits apply without a restart.
- **Card delete** — **built** *(card `146260d9`)*: `DELETE /v1/cards/:id` +
  `card_deleted` tombstone event + optional `?version=` guard + idempotency, with
  a `cards delete` CLI verb. Dependent cards are re-evaluated for
  `card_unblocked`. This made the board-hygiene cleanup below a first-class
  operation rather than a raw DB edit.
- **Other documented-but-unbuilt** *(proposed)*: `POST /cards/batch`,
  old-pinned-version schema serving (`GET /workspace/card-types/:id?version=`),
  markdown mirror (version-gated), `event_retention_days` trimming (schema field
  exists, no job reads it), sort-aware cursor pagination (currently `sort` and
  `cursor` are mutually exclusive), and per-card `/events` + `/history`
  pagination (no `next_cursor` today).

---

## 8. Surface parity & schema tooling

- **MCP parity** *(proposed, card `fedfacd1`)*. MCP is a strict subset of
  REST/CLI — no tools for release, remove-entry/link, edit-comment,
  upgrade-schema, event history/feed/stream, or user registration, and no
  idempotency-key or dry-run support. Reconcile `MCP.md` with reality and close
  the gaps that matter.
- **Schema authoring** *(proposed, card `9e3df3f7`)*. Author/edit card types via
  CLI/web with versioning, validation, and migration — instead of hand-editing
  JSON definition files.
- **Upgrade to older pinned versions** *(proposed, card `60305944`)*. Serve
  older card-type schemas from on-disk snapshots so pinned cards can round-trip.

---

## 9. UI

- **Design token substrate** *(proposed, card `73b7021c`)*. Adopt a standard
  token substrate (Open Props / Utopia fluid scales) under the existing design
  system.
- **Drag-drop reordering within a lane** *(proposed, card `d44d3e0d`)*. The
  configured-ordering half shipped (sort grammar + `lane_sort` + selector); this
  is the manual-rank remainder and depends on the priority/rank field (§5).
- **Card + modal polish** — the `.card` gap-spacing pass shipped; the **modal**
  half of card `72ebfbee` remains (kept in the board's `todo` column, not here).

---

## 10. Extensions

- **Service-kind supervision** *(proposed, card `39f64989`)*. Implement
  autostart/restart/expose for long-running `service` extensions, or explicitly
  mark it `[proposed]`.
- **Extensions design review** *(needs-design, card `79bfbebc`)*. Review the
  extension model's design, use cases, and integration points as a whole
  (overlaps the auth/permissions seams in §1–§2).

---

## 11. Docs, examples & research

- **GitHub Pages microsite** *(proposed, card `b8c8a7be`)* — landing +
  reference + examples. (See also `docs/GH-PAGES-TODO.md`.)
- **Lease/lock modeling pattern** *(proposed, card `4b5fbdc9`)* — document
  modeling a lease/lock as a card (`expires_at` + integrator reclaim).
- **Worked examples** *(proposed)*: CSV → todos with link enrichment
  (`078bf24a`); YouTube pipeline yt-dlp → srt → summarize → classify → rank
  (`7f56d7c8`); pi-agent extension developing code from a spec (`97cec658`).
- **Research** *(research, card `cf1b96ac`)* — survey agent resume-from-history
  patterns.
