# AUTH — Identity and attribution

**Status:** proposed · **Updated:** 2026-07-10
**Extracted from:** `docs/design/CORE-BOUNDARIES.md §4`
**Supersedes draft:** card `card_61040a3e` and `card_350b1bac` in their
current wording (both cards are to be reconciled against this doc once it is
accepted).
**Not normative until first implementation lands.** At that point a
"Security / Actor" subsection promotes into `docs/spec/SPEC-API-SURFACE.md`
and this document either shrinks to a pointer or moves to `docs/spec/`.

## 0. Overlay principle

> **Core guarantees an honest, schema-visible coordination memory. It does
> not guarantee a complete product surface or a complete trust boundary.
> Hosts and clients supply the rest.**

Two consequences that shape everything below:

1. **Identity attribution** — who is credited with each write, verifiably
   — is a core concern. The event log must not lie about who did what.
2. **Access control** — who can reach the process at all — is the host's
   concern. Caddy, Tailscale, firewall. Cards does not ship permission
   theater on top of it.

## 1. Modes — the auth matrix

`cards serve --auth <mode>` where `mode ∈ { none, token, proxy }`.

| Mode | Actor source | Verified? | Default | Intended for |
|---|---|---|---|---|
| `none` | `X-Work-Cards-Actor` header → `CARDS_USER` env → `WorkspaceSettings.DefaultUser` | No (trust ambient) | ✓ | Solo local dogfood; demo workspaces; single-user CLI |
| `token` | `Authorization: Bearer <token>` → users-table lookup | Yes (credential compare) | | Agents, CLI over network, multi-writer workflows |
| `proxy` | Trusted request header from a loopback/subnet reverse proxy → users-table lookup | Yes (network trust) | | Homelab behind Caddy/Tailscale; OIDC/SSO fronted deployments |

**None is the documented default**, not a prototype mode. All three are first-class.

**Precedence under `token`:**
1. `Authorization: Bearer` verified against the credential store → identity.
2. Unverified actor hints (`X-Work-Cards-Actor`, `CARDS_USER`) are ignored
   for attribution purposes; they may still be logged as claimed-but-rejected
   in structured logs.
3. `WorkspaceSettings.DefaultUser` is **not consulted** under `token`.

**Precedence under `proxy`:**
1. Trusted header (name declared per-workspace; e.g. `X-Forwarded-User`) →
   identity, only if the request arrived over loopback or a configured
   trusted CIDR. Otherwise 401 without consulting the header.
2. Same downgrade rules as `token` for unverified hints.

**Precedence under `none`:**
1. `X-Work-Cards-Actor` → `CARDS_USER` → `DefaultUser`.
2. No verification. The registry `POST /v1/users` is open.

**Anti-spoof (normative under `token` and `proxy`):**
`X-Work-Cards-Actor` MUST NOT override authenticated identity. If a
request under `token` carries valid Bearer credentials AND a mismatched
`X-Work-Cards-Actor`, the write commits with the **authenticated** actor;
the mismatched hint is dropped (and MAY be logged).

**Reads vs. writes under `token`/`proxy`:**

- v0 default: **authentication is required for writes**; **reads are open**
  if the caller can reach the port. Host access control (`--host`, firewall,
  reverse-proxy allowlists) is still the first line of defense for read
  confidentiality.
- Optional later: a `--auth-require-read` flag that additionally requires a
  valid credential for `GET`/SSE. Not in v0. Rationale: writes-only auth
  fixes the event-log honesty gap immediately; requiring auth for reads
  is a separate confidentiality decision an operator makes.
- Public bind + open reads is a real data-leak surface even with authed
  writes; the bind warning (§6) covers the loud case.

**Proxy mode tightenings (all normative when `--auth proxy`):**

- **Unregistered header user → 401 by default.** Auto-provision is
  opt-in via an explicit `proxy_auto_register: true` workspace setting.
  Default is fail-closed because silent auto-provision under a
  misconfigured proxy is worse than a login error.
- **Trust CIDR:** the reverse-proxy trust list is workspace config.
  `0.0.0.0/0` (or any non-loopback default) triggers an explicit startup
  WARN — the operator has just said "trust the whole internet's forwarded
  headers," which is almost never what they meant. One line, once, on
  startup.
- **Single trust header per workspace.** No stacking / rewrite logic.
  If the front-end is Caddy, name Caddy's header; if Cloudflare, name
  Cloudflare's. An extension can normalize before we see the request.
- **TLS client certificates, mTLS, SPIFFE, and network-cert schemes**
  are explicitly out of scope for `proxy` mode. They belong at the
  reverse proxy (which then sets a trusted user header) or as a future
  extension. `proxy` mode consumes a header, not a certificate.

## 2. Bootstrap — the chicken and the egg

If every write requires Bearer, who mints the first user?

**Answer: the CLI in serverless mode.**

`cards users register` invoked without `CARDS_URL` (or via
`--workspace <dir>`) runs in-process against local SQLite. It does not go
over HTTP. It is always trusted — the operator has filesystem access to the
workspace DB, which is a higher trust level than any HTTP credential could
gate. It returns the new user's `{id, kind, token}`.

**HTTP `POST /v1/users` behavior by mode:**

| Mode | HTTP registration open to |
|---|---|
| `none` | Anyone who can reach the port (matches YOLO) |
| `token` | Requires existing valid Bearer. **v0 stopgap:** the first user registered via the CLI is treated as "admin"; their token gates further HTTP registration. Document as v0 — future work may replace with a proper admin flag or delegate to an extension. |
| `proxy` | Gated by proxy identity (the proxy decides). |

**CLI ergonomics:**

```bash
cards users register --id claude --kind agent --display-name "Claude"
# → prints the token to stdout so agents can capture

cards users register --id claude --kind agent --save
# → writes {id, token} into ~/.cards/credentials (like gh's ~/.config/gh/hosts.yml)

cards users token rotate <id>  # v0 posture: revoke-and-reissue only
```

`--save` uses the same file the CLI reads to send `Authorization: Bearer`
on subsequent commands. Path override via `CARDS_CREDENTIALS_FILE`.

**Token storage — normative:**

- The database stores a **hash of the token**, not the token itself. Hash
  algorithm: at rest, whatever the reference impl picks (recommend
  `argon2id` or `sha256(pepper || token)`; the SPEC promotion will pin one).
- There is **no `GET /v1/users/{id}/token` endpoint**. The token is shown
  once, at registration (and re-shown on rotation). If the operator loses
  it, they rotate — never dig it out of the DB.
- **Rotation = revoke + reissue.** `cards users token rotate <id>`
  invalidates the current hash and mints a new token. No refresh tokens,
  no scoped derivations, no side-channel recovery.
- Token compare on the request path uses **constant-time comparison**
  (`crypto/subtle.ConstantTimeCompare`) and returns 401 with a generic
  body on any failure — no user enumeration, no timing side channel.
- The token is **opaque** — an unstructured, high-entropy string.
  Consumers must not parse it. Choice of JWT-vs-opaque is deferred to §11
  ("Not in this doc") and is an extension path.

## 3. Interface — Authenticator

```go
type Authenticator interface {
    Resolve(ctx context.Context, r IdentityRequest) (Identity, error)
    // ok=false → caller maps to 401/403 without invoking Service writes.
}

type IdentityRequest struct {
    Headers map[string]string // allowlist: Authorization, X-Work-Cards-Actor,
                              //             X-Forwarded-User, X-Real-IP
    Remote  string            // host:port peer, for proxy trust and warnings
    Envs    map[string]string // CLI/MCP adapters populate this
}

type Identity struct {
    Actor  string   // required if err == nil; see mode-dependent rules below
    Kind   string   // "human" | "agent"; from the User registry when verified
    Groups []string // shipped as a field but UNUSED BY CORE IN v0
    Source string   // "none" | "token" | "proxy" — for audit
}
```

**`Actor` validity rules split by mode** (prevents "must register even under
YOLO" ambush):

- Under **`token`** or **`proxy`**: `Actor` MUST be a registered `User.ID`
  (verified by credential compare or trusted proxy header). Mismatches or
  unknown ids → 401.
- Under **`none`**: `Actor` is any non-empty string. Registration is
  optional flavor text; the actor label is what the ambient hint says it
  is. This preserves today's YOLO behavior and lets solo dogfooders keep
  using `--as somebody` without a registration step. `Kind` is
  **best-effort** under `none`: populated from the registry when the actor
  happens to be registered, empty otherwise — never a reason to reject.

**Attribution ≠ authorization.** A verified `Actor` proves *who* is
credited with the write; it does **not** restrict *what* they may write.
Any registered user under `token` can patch any card's `owner`, add
comments as themselves, and move cards subject to the board's declarative
rules — the same as today. Field-level policy, ownership transfer rules,
and per-user restrictions are extension territory (§11), not core.

**`Groups` field** is present so extensions have a path (per PHILOSOPHY §6),
but core does not branch on it. It is **not emitted on events in v0** — the
event log is coordination memory, not an IdP.

## 4. Adapters (four)

Each adapter maps a transport's identity signals into `IdentityRequest`;
`Authenticator.Resolve` is transport-agnostic.

- **HTTP** — middleware sets request-context actor from `Authenticator`.
  Under `token`, the CLI-issued Bearer arrives here.
- **CLI** — under `token`, the CLI client reads `~/.cards/credentials` (or
  `$CARDS_TOKEN`) and sends `Authorization: Bearer` on writes. Under `none`,
  falls back to `--as` / `CARDS_USER` (today, unverified). Serverless CLI
  (no `CARDS_URL`) always operates as filesystem-trusted; see Bootstrap.
- **MCP** — bearer-over-stdio is awkward. Actor is bound at **process
  start** via `CARDS_TOKEN` env (or the invoker configures the MCP client
  with the token). MCP sessions run as one actor throughout — no
  per-tool-call unlock. This matches how `mcp` sessions are already
  structured (`internal/mcp/README.md`).
- **Hooks** — post-facto and at-most-once (`internal/hooks/hooks.go`).
  Hooks do not authenticate. They **pass through** the actor from the
  already-committed event they observe. Even a "no actor propagated" answer
  is a decision worth naming.

## 5. Identity resolution comes BEFORE idempotency replay

`Idempotency-Key` is scoped per-actor. If idempotency lookup runs before
authentication, a request whose actor changes at resolution time will hit
the wrong replay bucket. **Normative order:**

1. Resolve `Authenticator.Resolve(...)` → `Identity`.
2. Read `Idempotency-Key` and look up under the resolved actor.
3. Either replay or execute the write.

This applies uniformly to `token` and `proxy`. Under `none`, actor is still
resolved (from headers/env/default) before idempotency lookup — no change
to today's replay contract.

## 6. Non-loopback bind warning

Cross-references `card_fa239a92`. Definition of "louder when auth=none" (so
it doesn't become entropy):

- On any non-loopback bind: log **one** `WARN` line.
- If additionally `--auth none`: emit **one extra remediation line**:
  `→ Use --auth token, or bind to 127.0.0.1.`
- No other conditional log lines. Delta is one line, no more.

## 7. Force flag and identity

**Force is a mutation/validator concern, not an authentication one.** Full
policy for `--force` on status writes — what it skips, what event it emits,
the optional per-board `allow_force: false` opt-out — is decided in
[`CORE-BOUNDARIES.md §3.3`](CORE-BOUNDARIES.md#33-transitions--data-vs-enforcement-vs-observation)
and will land in the transitions RFC (`card_f570b35b`).

Auth's only invariant here:

> **A forced write still attributes the resolved (verified) identity.**
> Force never disables authentication. Under `token`/`proxy`, a request
> without valid credentials is 401 regardless of whether it carried
> `force=true`.

## 8. Anti-spoof tests (required of the reference impl)

The reference implementation (renamed from "basic auth reference" to
"token reference impl" — `card_350b1bac` to be reconciled) must include:

- **Anti-spoof test.** Under `--auth token`, a request with a valid Bearer
  AND a mismatched `X-Work-Cards-Actor` header commits with the
  authenticated actor. Assert on the committed event's `actor` field.
- **No user enumeration.** Wrong password/token and unknown-user both
  return 401 with a generic body. No timing side-channel on the compare
  (constant-time comparison, `crypto/subtle.ConstantTimeCompare`).
- **Never log tokens.** Auth-failure logs record actor claim + result, no
  token material. Rate-silence brute-force attempts (log every N failures,
  not every one).
- **`--auth none` regression.** Existing httpapi tests pass unchanged under
  `--auth none`.

## 9. Definition trust ≠ card ACLs

Auth is **request identity**. Trust in `boards/*.json`, `definitions/`,
and `extensions.json` is host/filesystem ACL — not something card auth
protects. An operator who can write those files owns the workspace's
rules and validators regardless of what cards' auth mode is set to.

Say this once in the doc so people don't expect card auth to protect
against filesystem-level threats.

## 10. Historical misattribution

Events written before auth activation carry unverified actors — including
some on the engineering board where an agent posted as `jeremy` during
early sessions. The event log is **coordination memory, not truth**. Under
this doc:

- We do **not** rewrite history. Event log integrity beats retroactive
  correction.
- A single `NOTES.md` bullet dates the flag day — "honest attribution
  begins at first auth activation, 2026-MM-DD" — so future readers can
  bound the trust window.
- The demo workspace's seeded onboarding cards may be re-authored under
  the flag-day actor if we chose to; that's a P1b decision, not this doc's.

## 11. Not in this doc

- **Sessions, cookies, refresh tokens** — reference-client concern only.
  The bundled web UI at `/ui/` may mint and store a bearer in
  cookie/localStorage as its own session mechanism, mapping browser login
  onto the same token store described here. That's a **reference-client
  pattern**, not a core primitive. Third-party clients decide their own
  session shape.
- **Scoped tokens** (read-only, board-limited, time-boxed) — deliberately
  out. `Identity` does not carry a `scopes` field in v0. Once scopes
  exist, they grow ACLs by pressure. Someone who needs scoped access
  writes an extension that mints capability-shaped tokens over the same
  registry.
- **OAuth / OIDC / SAML** — extension or reverse-proxy concern. In-process
  reference is the opaque bearer token; the `proxy` mode is the seam for
  federated login.
- **Per-user authorization / RBAC / ACL / group policy** — extension
  concern. Groups ship as an inert `Identity` field so extensions have a
  path; core never branches on them.
- **Password reset UX / rotation UI / self-service** — reference-client
  concern (v0 posture: revoke-and-reissue via CLI).
- **Multi-tenant** — one workspace per process; unchanged.
- **Peer-address-based route policy** beyond the `proxy` mode's trust
  CIDR.
- **ACL DSL of any kind.**
- **Signed exports, offline log integrity, replay attestation** — honest
  attribution ≠ offline board integrity. Different problem, later doc.

## 12. Cross-references

**Cards on the engineering board:**

- `card_61040a3e` — **reconciled 2026-07-10.** Now carries only the
  remaining work (review request + ROADMAP §1 link); this doc is the
  single source of truth for the spec content.
- `card_350b1bac` — **reconciled 2026-07-10.** Retitled to *"AUTH
  reference impl: register issues bearer token; `--auth token` verifies
  writes."* Token-primary; the Basic adapter (user:token onto the same
  store) is a deliberate later option, not v0.
- `card_fa239a92` — non-loopback bind warning. Unchanged, cross-linked here.
- `card_c7a70b64` — P2 code-review batch. `Service.ResolveActor` becomes
  the seam's default fallback under `--auth none` (matches current wording
  after the last patch).
- `card_f570b35b` — transitions-as-callbacks RFC. This doc's §7 supplies
  only the identity invariant (forced writes still attribute the verified
  actor). The full force-flag policy — declarative vs. callback skip,
  `diff.forced` on `status_changed`, per-board `allow_force: false`
  opt-out — lives in
  [`CORE-BOUNDARIES.md §3.3`](CORE-BOUNDARIES.md#33-transitions--data-vs-enforcement-vs-observation)
  and will land in that RFC's body.

**Docs:**

- `docs/design/CORE-BOUNDARIES.md` — the architecture context this doc
  extracts from.
- `docs/concepts/PHILOSOPHY.md` — §7 rewrite (drafted in
  `CORE-BOUNDARIES.md §5`) makes the access-vs-attribution split
  normative at the top of the project.
- `docs/spec/SPEC-API-SURFACE.md` — receives a "Security / Actor"
  subsection when this doc's reference implementation lands.

## 13. Promotion path

1. This document lands as `status: proposed`. ✓ (2026-07-10; frozen — see
   `docs/NOTES.md` freeze entry. Changes only via impl PRs that discover
   reality or an explicit re-open from the owner.)
2. Board cards `card_61040a3e` and `card_350b1bac` are reconciled against
   this doc's shape. ✓ (2026-07-10)
3. Implementation begins against this doc (not against the earlier RFC
   card body).
4. On first impl landing, a "Security / Actor" subsection is added to
   `docs/spec/SPEC-API-SURFACE.md`. This doc's status becomes `spec` and
   it either stays as the extended narrative or shrinks to a pointer.
5. `PHILOSOPHY.md §7` rewrite lands with the reference implementation, so
   the top-of-project language and the actual behavior ship together.
