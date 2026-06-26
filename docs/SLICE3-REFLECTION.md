# Slice 3 Dogfooding Reflection

Used the Work Cards system itself to track Slice 3 (CLI + MCP) work: 7
`programming-task` cards created, linked with `depends-on`, claimed,
worked (with `work_log` appends + comments), and walked through the
`engineering` board to `done`. This is the agent coordination loop
(introspect → take-next → work → append evidence → transition → comment)
performed by a human, against the HTTP API and then the freshly-built CLI.

## Bugs found

### B1 — `X-Work-Cards-Actor` header was silently ignored (shipped, fixed)
The most serious finding. Every API write was attributed to
`workspace.settings.default_user` regardless of the `X-Work-Cards-Actor`
header. Root cause: `withActor` middleware stored the resolved actor via
`core.WithActor` (key `core.actorCtxKey{}`), but handlers read it via
`s.actorFromCtx` which looked up the httpapi-local `actorKey{}` — a
different context key. The fall-through returned `default_user`.

Why the test suite missed it: the existing actor test sent *no* header and
expected the default user, so it passed trivially; the lifecycle test used
the header but never asserted `created_by`. I only noticed because a
`claim` came back with `owner: local-dev` instead of `foz`.

Fixed: `actorFromCtx` now calls `core.ActorFromCtx`. Added
`TestAPIActorHeaderRespected` regression test. **Lesson: structured
errors make failures loud, but a silent misattribution is invisible
unless you assert the actor on the response.**

## Friction points (UX)

### F1 — `take-next` ignores dependency readiness
`take-next` picks the oldest unowned matching card by `updated_at`. With
seeded cards present, it grabbed a seeded "Add OpenAPI spec" card instead
of my `cli-framework` root — even though the framework had no blockers and
5 other cards `depends-on` it. There's a `blocked=true` filter but no
`unblocked`/`ready` complement, and no way to say "pick a card whose
dependencies are all `done`." For a dependency-aware work queue this is the
single biggest gap.

### F2 — No "release/unclaim" primitive
After accidentally claiming a seeded card, I couldn't put it back: the
enforced board allows `in_progress → review` only, so `in_progress → todo`
is `transition_illegal`. Clearing `owner` + reverting status requires
walking an illegal edge. A `release` operation (clear owner, return to a
prior status, exempt from transitions) would make claim/take-next safe to
retry.

### F3 — Enforced transitions force linear walking; no skip
To mark a small task done I had to walk `todo → in_progress → review →
done` one PATCH at a time, each needing a fresh `--version`. For a solo
dev knocking out trivial tasks this is ceremony. A `--to <status>` that
auto-walks the transition graph (or a board setting for "fast-forward
through review") would help humans without weakening the contract for
agents.

### F4 — `--version` churn from link/comment mutations
Adding a `depends-on` link or a comment bumps the card `version`, so a
`--version` captured from an earlier `get` goes stale and the next patch
returns `409 version_conflict`. Correct behavior, but the CLI ergonomics
hurt: there's no "fetch current version then patch in one call." A
`--if-match latest` or a `cards patch --fetch-version` mode would smooth
this.

### F5 — `unknown_enum` for status is slightly misleading
Creating a card with `status: backlog` returns `unknown_enum` with
`valid_options` = the **workspace** columns (`backlog, todo, ...`) — but
the real reason is the **type's** `allowed_columns` excludes `backlog`.
The valid_options echoed are the workspace set, not the type's subset, so
the error lists `backlog` as "valid" right after rejecting it. Should echo
the type's `allowed_columns` when that's the failing layer.

### F6 — Workspace authoring trap: board has a column the type forbids
The `engineering` board defines a `backlog` column, but the
`programming-task` type's `allowed_columns` is `[todo, in_progress,
review, done]`. So `backlog` is unreachable for programming tasks. I hit
this immediately trying to create my work cards in `backlog`. Either the
type should allow `backlog`, or the board/type column sets should be
validated for consistency at config-load time.

### F7 — No test isolation; MCP/CLI smoke tests mutated the real workspace
Running `cards mcp` against the live workspace to smoke-test `take_next`
claimed one of my actual work cards (`mcp-tools`), changing its owner and
status. There's no "scratch workspace" or transactional test mode. A
`--workspace :memory:` option or a `cards mcp --dry-run` would let tools
exercise the loop without side effects.

## What worked well

- **Structured errors with `valid_options`** were genuinely
  self-correcting. Every bad status/enum came back with the allowed set;
  the CLI surfaced them readably to stderr. This is the system's strongest
  property and it held up.
- **The coordination loop is real.** `claim` → append `work_log` →
  transition → comment → `history` mapped 1:1 to how I actually worked,
  and the `history` timeline faithfully reconstructed what happened.
- **`take-next` atomicity** is correct — once I had the right filter, it
  picked and claimed in one call, and concurrent claims would 409.
- **Repeating entries with stable `entry_id`** worked cleanly for
  `work_log`; addressing by id (not index) felt safe even though I only
  appended.
- **The typed MCP tools** (`create_programming-task` with a field schema)
  rejected bad enums with `isError: true` + `valid_options` — exactly the
  anti-hallucination guarantee the design targeted.
- **`depends-on` + `blocked=true`** correctly identified the 5 cards
  waiting on the framework/scaffold. The dependency graph query worked
  first try.
- **Idempotency** (`Idempotency-Key`) replayed the same card on retry —
  useful for the flaky-network agent case.

## Concrete improvements (ranked)

1. **`ready` / `unblocked` filter** for `take-next` and `list` (cards whose
   `blocked-by`/`depends-on` targets are all `done`). Highest leverage for
   dependency-driven work.
2. **`release`/`unclaim` operation** that clears owner and reverts status,
   exempt from transition enforcement.
3. **Fix `unknown_enum` for status** to echo the type's `allowed_columns`
   when that's the failing validation layer (and validate board/type
   column-set consistency at config load — F6).
4. **CLI `--to <status>` auto-walk** for enforced boards, and a
   `--fetch-version` mode so `patch` doesn't need a manual `get` first.
5. **`history` should be the CLI/UI default for "what happened"** — it's
   excellent and underexposed (no UI for it yet).
6. **Test isolation**: support `:memory:` workspaces for `cards mcp`/CLI
   smoke tests, or a `--dry-run` that runs the full validation loop without
   persisting (we have `dry_run` on create/patch; extend to claim/take-next).

## Takeaway

The kernel's contracts (typed validation, structured errors, events,
optimistic concurrency, the coordination loop) are sound and survived
real use. The friction is almost entirely in **operational ergonomics**
(release, ready-filter, version churn, linear walking) and **one real
attribution bug** — which dogfooding caught exactly because I had to read
the actual response bodies rather than assert "200 OK." Building the CLI
mid-dogfood was immediately useful: the second half of the card
transitions were far less painful than the curl-driven first half.
