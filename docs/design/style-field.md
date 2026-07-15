# Style field — board-chosen enum drives card visuals

Status: **shipped** (P4b, 2026-07-11). OptionThemes render through the same
`data-icon` / `--card-stock` hooks as TypeTheme. Filter-by-accent is deferred.

Prompted by the `labels` theme review: on the engineering board ~all cards are
`programming-task`, so the type-driven color/icon key renders every card
identically and carries zero information.

## The problem

The UI has standardized on **card type** as the visual differentiator:
`TypeTheme{icon, accent, muted}` on the type feeds the corner mark / spine
color / badge, `[data-type]` CSS tokens, and the board header's TYPE filter.
That works when a board mixes types — and fails on the common board that has
exactly one type. A team's real categories ("feature vs bug vs design task")
then live in an enum field or a tag, invisible to the eye.

The alternative of dogfooding 3+ near-identical card types (feature/bug/task)
solves visuals by **type explosion**. Type should stay what it is (a field
contract); category is data.

## Normative: define / activate split

Two layered pieces; presentation metadata never branches write paths
(CORE-BOUNDARIES §3.2):

**1. Define — enum options carry a theme** on the field definition
(workspace-level, type-global — consistent with "the definition drives every
surface"):

```json
{
  "id": "kind", "type": "enum",
  "options": ["feature", "bug", "design", "infra"],
  "option_themes": {
    "feature": { "icon": "star",   "accent": "#005bd3", "muted": "#d9e8ff" },
    "bug":     { "icon": "bug",    "accent": "#b00000", "muted": "#ffd9d6" },
    "design":  { "icon": "pen",    "accent": "#8a5200", "muted": "#ffe8bf" },
    "infra":   { "icon": "wrench", "accent": "#596469", "muted": "#e0e4e5" }
  }
}
```

`FieldDef.OptionThemes` is the user-facing value→icon/color map. Icon names
resolve through the design-system `data-icon` aliases (`card`, `star`, `bug`,
`check`, `flask`, `target`, `code`, `pen`, `wrench`). Declaring OptionThemes
alone does **not** change any board's render — boards must opt in.

Each themed option requires **icon + accent + muted** together (meaning is
never color-alone). The enum value remains ordinary field text in the
modal/detail body.

**2. Activate — a board opts in** via `BoardPresentation.StyleField`:

```json
"presentation": { "style_field": "kind" }
```

The board names which enum field's OptionThemes to use for card accent/icon.
It does not redefine the map. The same card can therefore render differently
on two boards (one with `style_field` set, one without — or two boards naming
different fields). This is intentional: presentation is board-scoped; the
field contract is type-scoped.

`BoardPresentation.StyleField` replaces the unused `card_accent_field` name
(deleted; unknown JSON keys remain ignored — no `DisallowUnknownFields`).

## Normative: resolution precedence

One precomputed per-card theme (`httpapi.resolveCardTheme` → `ViewData.CardTheme`
/ `CardView` TypeIcon·Accent·Muted), consumed by board cards and modal/detail
`card_head`:

```
option theme (card's style_field value)  →  TypeTheme (card's type)  →  CSS [data-type] defaults
```

- **Option theme wins** when the board has `style_field`, the card has that
  field set, and `option_themes[value]` is present. Missing keys fall through.
- **Type theme** (`CardType.type_theme`, plus legacy flat `icon`) fills any
  unset Icon/Accent/Muted.
- **CSS defaults** (`[data-type]` / `[data-icon]` in `style.css`) apply when
  Accent/Muted/Icon remain empty after the merge.

Without `style_field`, resolution is TypeTheme → CSS only (today's behavior).
Cards without the field, or with an unthemed value, fall through — nothing
breaks; `style_field` is pure opt-in.

Type-level identity still works without any style_field:

```json
{
  "id": "programming-task",
  "type_theme": { "icon": "code", "accent": "#0f5d78", "muted": "#d9edf2" }
}
```

With `style_field`, the same `TypeTheme` shape is reused for enum values so the
card root, modal badge, and detail header all receive the effective `data-icon`,
`--card-stock`, and `--card-stock-bg`.

## Load-time validation (P2b seam)

Misconfiguration fails workspace load with a structured error (field path +
value + allowed set / required floor):

| Check | Where |
|---|---|
| `option_themes` only on single-value enum | `validateField` |
| keys ⊆ `options` | `validateField` |
| icon ∈ 9-alias set (`internal/core/icons.go`) | `validateField` |
| every themed option has icon + accent + muted | `validateField` |
| accent-on-muted contrast ≥ 4.5:1 | `validateField` via `core.MeetsContrastFloor` |
| `style_field` names an enum field on ≥1 board type | `validateBoard` |
| that field is not `multiple` | `validateBoard` / `validateField` |

## Accessibility — contrast decision (card_8fea3fc0 A/B/C)

Badge text paints author **accent** on author **muted** wash
(`.card__type-badge` / `--badge-ink` on `--badge-wash`). Soft pairs fail WCAG
AA (e.g. legacy `frontend-task` `#cf7b00` on `#f6e7cc` ≈ 2.65:1).

**Decision C (hybrid), enforced for option accents:**

- **B for `option_themes`:** load-time hard reject below 4.5:1 accent-on-muted.
  Authored pairs are applied inline (`--card-stock` / `--card-stock-bg`) and
  do not remap under `prefers-color-scheme`, so one floor check covers light
  and dark. Hex only (`#RGB` / `#RRGGBB`).
- **A deferred for legacy `TypeTheme`:** existing soft type accents stay as
  author identity this sprint (runtime text-shade residual for
  `card_8fea3fc0`); they are not hard-rejected so demo workspaces keep
  booting.

**The floor covers accent-on-muted pairs only — not theme-composed
surfaces.** Two follow-up rules close the gaps found post-merge:

- **Washed cards under a dark scheme:** `journal`/`labels` use the wash as the
  full card background while text stays on the theme ink token, which remaps
  near-white in dark mode. A `prefers-color-scheme: dark` override (end of
  style.css) keeps the theme surface as card background there — the wash is
  decorative; accent stock elements, icon, and text carry the identity.
- **Icon-on-accent:** themes paint a fixed light ink (`#fffdf5`) on the accent
  surface. A load-time **warning** (not rejection) flags option accents below
  the 3:1 graphics floor against that ink.

**Board context:** card links pass `?board=<id>` to the modal, so a card
shared by two boards keeps the opener board's option theming; without the
param the fallback board choice is deterministic (sorted board ids).

Icon is mandatory with color; the enum value stays readable as field text.
No filter-by-accent affordance ships with style_field.

## Why this fits the architecture

- **Same seam as today.** TypeTheme merging, inline `--card-stock` vars, and
  `[data-type]` already exist; option themes add one lookup in front, not a
  second theming system.
- **Definition-driven.** The enum def drives API validation, CLI/MCP schema,
  *and* visual identity — one source, every surface.
- **Kills type-explosion pressure.** feature/bug/task becomes one card type +
  a `kind` enum.

## Open questions (filter UX — not blocking)

1. **Filter semantics.** Replace the TYPE dropdown or sit beside it? Deferred
   — no generic enum-filter affordance exists yet.
2. **Tags overlap.** Enum = exactly-one-of, drives identity; tags = any-of,
   never drive identity.
3. **`card_preview` interplay.** If the style field is promoted to color/icon,
   it should probably drop out of the preview line automatically (redundant),
   like status already is in the labels theme. Engineering dogfood leaves
   `kind` out of `card_preview` for that reason.

## Non-goals

- No new field types, no per-card colors, no user-set colors on arbitrary
  fields. Only `enum` gets themes: bounded values, bounded palette.
- Not a replacement for `TypeTheme` — mixed-type boards keep working unchanged.
- No multi-field color, tag-driven visuals, or per-card custom colors.
- Filter-by-accent-click is out of scope for the style_field legibility ship.

## Demo (engineering board)

`programming-task` declares optional `kind` with OptionThemes;
`boards/engineering.json` sets `"style_field": "kind"`. Cards without `kind`
keep their type/CSS look (golden fixtures included). Cards with `kind` set
render per-option color+icon.

```bash
scripts/dev-server.sh
# or: go build -o cards ./cmd/cards && ./cards serve --workspace ./examples/demo-workspace --port 8787 --seed
open 'http://127.0.0.1:8787/ui/boards/engineering?theme=labels'
```

Set `kind` on a card via the modal enum field (or `cards patch`) to see the
option theme; clear it to fall back to type theme.
