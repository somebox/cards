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

## 1. Authentication & identity — *design frozen / impl parked*

**No authentication exists today, by design.** Actor identity is trusted and
unauthenticated: the `X-Work-Cards-Actor` header → `CARDS_USER` env →
workspace `default_user`, honored verbatim across HTTP/UI/CLI/MCP. There is an
open user registry (`POST /v1/users`, no auth); a registered user id is
required only when assigning `owner` (claim / take-next). Docs treat auth as a
host/reverse-proxy/extension concern (`philosophy.md`, `index.md`,
`events-history.md §12`), and "strong identity/ACL" is a deferred item in
`design-notes.md`.

**The design pass has landed and is frozen.** [`docs/design/auth.md`](design/auth.md)
(frozen at `c6cb17e`, 2026-07-10) is the RFC: token mode for the
"exposed beyond localhost" tier, proxy-delegated identity as the recommended
boundary, authorization explicitly out of scope (§2). It becomes normative
only when a first implementation lands, and reopening it requires
implementation discovering reality or an owner re-open — **not** another
investigation pass. The questions below are kept as the historical record of
what the frozen design resolved. Implementation is **parked** on
`card_350b1bac` (bearer-token reference impl — its own future XL, never to be
stacked with the read pool); the design-review obligation (`card_61040a3e`) was
discharged in the 2026-07-18 sprint P1 closeout.

<details><summary>Historical: the open questions the frozen RFC resolved</summary>

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

</details>

---

## 2. Permissions / authorization — *deferred (non-goal in v1)*

There is **no** permission / ACL / role / policy concept anywhere, and the hook
system **cannot** provide one:

- `internal/hooks` is an **observe-only, post-hoc, at-most-once** supervisor.
  It subscribes to the event bus *after* a mutation is committed, spawns a
  subprocess with the event JSON on stdin, and only logs the result. A non-zero
  exit does **not** roll anything back (`index.md`).
- Brokering authorization would require a **new synchronous pre-action hook
  point that does not exist** — a hook could only react to a completed change
  (e.g. issue a compensating write-back), never prevent it.
- `transition_rejected` is a workflow-rule **audit event**, not an authz
  decision; `ArtifactPolicy: local|uri` governs path confinement, not users.

"Jira-grade permissions, ACLs, SSO" are listed as non-goals (`api-surface.md`).
Revisit only if a multi-tenant deployment is actually scheduled; it would ride
on the auth design in §1 and (probably) a pre-commit hook seam.

---

## 3. Storage — *well-defined; SQLite is the sole backend*

SQLite is the only backend, but the seam is clean. `core.Store`
(`internal/core/store.go`) is a consumer-side interface; core/service depend
only on it, and the concrete `*sqlite.Store` is confined to `cmd/cards/`. An
in-memory `EventLog` fake already passes the same conformance suite.

- **Concrete near-ish work — SQLite read pool** *(proposed, card `57e1bde9`)*.
  Today `MaxOpenConns(1)` serializes everything through one connection (single
  writer). Add a separate read pool for concurrent reads while keeping the
  single-writer discipline for mutations.
- **Alternative backends (e.g. Postgres)** *(deferred)*. Feasible for the
  service layer, but two things are SQLite-coupled: the query/filter DSL
  compiler emits SQLite SQL (`json_extract`, `internal/sqlite/filter.go`), and
  `export`/`import` are typed against the concrete store. `design-notes.md`
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
the board SSE stream. Design intent (`design-notes.md`, `index.md`):
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
  [`docs/design/subscribers.md`](design/subscribers.md) and **deferred** by the
  Phase-6 go/no-go ([`docs/design/outbox-gonogo.md`](design/outbox-gonogo.md)):
  the log is now crash-safe (foundation gate passed), but no relied-upon consumer
  was observed losing events that matter, so building it now would be
  infrastructure ahead of need. Re-open on a real go signal.
- **`card_ready` on DAG progress** *(proposed, card `2898a658`)*. Fires when
  *all* dependencies are satisfied — distinct from the built `card_unblocked`.
- **Priority / rank + reprioritize-on-`lane_drained`** *(proposed, card
  `b3e0914b`)*. A rank field feeding `take-next` ordering. Also the prerequisite
  for UI drag-drop reordering (§9).
- **Temporal coverage in `/breaches`** — **built for projection**
  (2026-07-18, card `e3c63f21`). `Service.Breaches` cold-projects
  `status_timeout` / `card_idle` past-due cards with the same deadline math
  as rebuild/verify (`internal/core/breaches.go`), read-only, additive
  `status`/`since`/`max`/`threshold` fields. **Residual:** item scans inherit
  the `ListCards` 500 ceiling — the report echoes `limit` + `truncated`
  (partial catch-up, honestly tagged); the unclamped deep-wash path stays
  open below.
- **>500-card column census limitation** *(partial)*. WIP/lane counts are
  uncapped (`CountCards`); **blocked + temporal item scans** still inherit
  the `ListCards` 500 ceiling (surfaced as `truncated` on `/v1/breaches`).
  Fix: an unclamped iterator path for item scans rather than raising the
  ceiling. Also: census counts by *type* membership, so a board scoped by
  `DefaultFilter` (not type) isn't counted correctly.

---

## 6. Integration — webhooks *(designed, gated — deferred; card `a9f78958`)*

A first-class `webhook` extension kind: push events to external URLs with
retry + HMAC signing + cursor replay. Today only a shell `hook` can react to an
event (and only observe-post-hoc, per §2). Built on the outbox (§5), so it
shares the same deferral and design: see
[`docs/design/subscribers.md`](design/subscribers.md) — which pins the
`Consumer` model, reject-at-registration deliverability, the additive
(non-republishing) tailer topology, and the **enforced default-deny egress
allowlist** (registration + dispatch time) that any runtime `/v1/subscribers`
registration must ship with.

---

## 7. API surface — *partial* (reload + card delete built; rest proposed)

- **`View` type — orphaned** *(proposed)*. The type is declared in
  `internal/core` ("read-only in v1") but has **no** `Workspace.Views` field,
  route, CLI verb, or service wiring. Read-only parameterized card views
  (`/views/:id/cards`); a real feature to design or explicitly drop.
- **Workspace reload** — **built**. `POST /v1/workspace/reload` re-runs the
  definitions loader and swaps the composition without a restart
  (`cmd/cards/reload.go:104-112`), emitting `definition_reloaded`
  (`internal/core/types.go:328`); `POST /v1/boards` writes + validates a board
  definition through the same path.
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

- **MCP parity** *(partial)*. MCP now covers release (`internal/mcp/mcp.go:265`),
  remove-entry (`:296`) and remove-link (`:309`), edit-comment (`:319`),
  upgrade-schema with dry-run preview (`:324`), card history (`:401`), and the
  durable event feed (`:411`). Remaining gaps: user registration,
  idempotency-key support, general mutation dry-run, and SSE subscribe
  (deliberate — stdio). Reconcile `mcp.md` with reality and close the gaps
  that matter.
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

- **Service-kind supervision** — **built**. The hook supervisor runs
  long-running `service` extensions with autostart, backoff restart, and
  reconcile-on-reload (identity = extension id, fingerprint =
  run/env/cwd/restart_policy; `internal/hooks/`, decision table in
  `docs/architecture/reload.md`).
- **Extensions design review** *(needs-design, card `79bfbebc`)*. Review the
  extension model's design, use cases, and integration points as a whole
  (overlaps the auth/permissions seams in §1–§2).

---

## 11. Docs, examples & research

- **GitHub Pages microsite** — **built**: MkDocs site (`mkdocs.yml`) deployed
  via `.github/workflows/deploy-pages.yml` — landing + reference + examples.
  (Plan history: `docs/archive/gh-pages-plan.md`.)
- **Lease/lock modeling pattern** *(proposed, card `4b5fbdc9`)* — document
  modeling a lease/lock as a card (`expires_at` + integrator reclaim).
- **Worked examples** *(proposed)*: CSV → todos with link enrichment
  (`078bf24a`); YouTube pipeline yt-dlp → srt → summarize → classify → rank
  (`7f56d7c8`); pi-agent extension developing code from a spec
  (`97cec658`) — **built** as [pi-cards](https://github.com/somebox/pi-cards)
  (spec `docs/design/pi-extension.md`; the 8-card pi-cards series on this
  board tracked its own construction).
- **Research** *(research, card `cf1b96ac`)* — survey agent resume-from-history
  patterns.
