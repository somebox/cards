# FTS5 vs LIKE — disposition (POC scale)

**Status:** decided · keep FTS5 (already shipped)
**Card:** `card_ded1a923` — *Evaluate SQLite FTS5 vs LIKE for POC scale*
**Scope:** card full-text (`q` / `search_cards`), not id lookup and not the
filter-DSL `$contains` operator (those stay on `LIKE` by design).

---

## Hypothesis (original)

> LIKE is sufficient for <10k cards and avoids the FTS5 build/CGO complexity.

Two claims, independently falsifiable:

1. **Complexity:** FTS5 implies CGO / a painful build.
2. **Sufficiency:** at POC scale (<10k cards) a `title||body LIKE '%q%'`
   table scan is good enough that an inverted index is not worth it.

---

## Finding 1 — the CGO premise is false

The store already uses `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`). That
build **includes FTS5**. Architecture already states this explicitly
(`docs/architecture/index.md`: "pure-Go, no CGO, FTS5 supported").

There is no FTS5-driven CGO tax, no dual-driver story, and no special build
tag. The "avoid FTS to stay CGO-free" half of the hypothesis does not apply
to this codebase.

Verified: `CGO_ENABLED=0 go test ./internal/sqlite/ -run TestListCardsFilterAndSearch`
passes; the live schema creates `USING fts5(...)`.

---

## Finding 2 — FTS5 is already the production path

Code (not aspiration):

| Piece | Where |
|---|---|
| Virtual table | `internal/sqlite/sqlite.go` — `CREATE VIRTUAL TABLE fts_cards USING fts5(card_id UNINDEXED, title, body)` |
| Index maintain | `upsertFTS` on insert/update/claim; delete on card delete |
| Query | `buildCardWhere`: `c.id IN (SELECT card_id FROM fts_cards WHERE fts_cards MATCH ?)` when `CardQuery.Q != ""` |
| Sanitization | `ftsQuery` — per-token double-quoting so user input cannot inject FTS operators |
| Failure discipline | FTS upsert errors roll the write txn back (`TestClaimAtomicFTSFailureRollsBack`) |
| Surface tests | `TestListCardsFilterAndSearch`, `TestFTSSearch` (HTTP `?q=`) |

`LIKE` is still used for two *different* jobs, correctly:

- **Short/full id match** (`CardQuery.IDLike`) — prefix/substring over
  `c.id` / `substr(c.id, 6, 8)`, with `likeEscape` so `%`/`_` in the
  pattern cannot widen the match. OR-combined with FTS when both `Q` and
  `IDLike` are set.
- **Filter DSL `$contains` on string paths** — intentional case-insensitive
  substring, documented in `docs/spec/query-dsl.md`. Not a substitute for
  `q`.

So the live design is already "FTS for free-text, LIKE for id/DSL", not an
either/or.

---

## Finding 3 — measured cost at POC scale

Ad-hoc file+WAL microbench (modernc driver, single conn, tempdir; 50
rounds after one warmup; `COUNT(*)` only so fetch cost is equal). Synthetic
rows: title + ~1 line body, 10 rotating topic tokens, one of which is the
needle. Shape is closer to "index the title + searchable text" than to a
huge document corpus.

| N cards | FTS5 `MATCH` (per query) | `LIKE` title/body | `lower(...) LIKE` |
|--------:|-------------------------:|------------------:|------------------:|
| 1,000   | ~25 µs                   | ~0.6 ms           | ~0.8 ms           |
| 10,000  | ~160 µs                  | ~6.6 ms           | ~9.2 ms           |
| 50,000  | ~675 µs                  | ~32 ms            | ~48 ms            |

Takeaways:

- At the hypothesis ceiling (10k) FTS is ~40× faster than a case-sensitive
  `LIKE` scan and ~60× faster than the case-insensitive form UI search
  actually wants. Absolute LIKE cost (~7–9 ms) is still "fine" for a
  single human keystroke on a quiet laptop — which is what made the
  hypothesis *plausible*.
- LIKE scales with N (full scan); FTS scales with hit cardinality /
  index height. By 50k, LIKE is into tens of milliseconds **before**
  row materialization, JSON field extract, or concurrent writers — the
  regime the single-writer + FTS-in-write-txn design already cares about
  (see sprint 07-12 P2/P3 notes on write-tx hold time).
- Seed/import upsert cost for FTS was ~0.1 s at 10k and ~0.7 s at 50k in
  this microbench — noise next to definition load and HTTP bring-up.

POC scale does **not** require FTS for correctness of a demo. It *does*
prefer FTS once search is a first-class API (`q`, MCP `search_cards`,
UI `/ui/search`, TUI `/`) that agents and the web UI hit repeatedly, and
the implementation cost is already paid.

---

## Residual gaps (out of scope for the go/no-go, tracked as scrub)

These do not reopen the FTS-vs-LIKE decision; they are polish on the
chosen path:

1. **`searchable_fields` is declared but not applied.** Card-type JSON
   and `core.CardType.SearchableFields` carry the list; `upsertFTS`
   currently concatenates *every* value in `card.Fields` via
   `fmt.Sprint`. Effect: enums, branch names, work-log blobs, etc. get
   indexed too. Harmless for recall at POC scale; noisier ranking and
   larger index than the type author opted into. Fix when search quality
   matters: look up the type's `SearchableFields` (and always `title`)
   inside `upsertFTS`.
2. **No ranking.** Results ride the default `ListCards` order
   (`updated_at, id`), not `bm25(fts_cards)`. Acceptable while `q` is a
   filter; revisit if search becomes a primary navigation surface
   (`docs/design/benchmark-suite.md` already parks "FTS ranking quality"
   as at-most-one sample).
3. **Tokenization is the FTS5 default** (unicode61). Good enough for
   English identifiers and prose; no custom tokenizer.

---

## Verdict

| Claim | Result |
|---|---|
| LIKE avoids CGO complexity | **False** here — `modernc.org/sqlite` already ships FTS5 with `CGO_ENABLED=0`. |
| LIKE is "enough" at <10k | **True for absolute latency on a cold demo**, false as a reason to rip out or avoid an index that is already integrated with write atomicity, MCP, UI, and API. |
| **Decision** | **Keep FTS5** as the implementation of `q`. Keep `LIKE` for id-likeness and DSL `$contains` only. Do not add a LIKE fallback for `q`. |

The one-line card conclusion stands, with evidence:

> FTS5 chosen and implemented: `fts_cards` virtual table with `MATCH`;
> `upsertFTS` maintains the index. LIKE is not the free-text path at POC
> scale (and the CGO objection does not apply).

No code change required for the decision itself. Optional follow-up:
honor `searchable_fields` in `upsertFTS` (small, additive).
