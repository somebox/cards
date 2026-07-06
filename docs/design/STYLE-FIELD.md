# Style field — board-chosen enum drives card visuals (exploration)

Status: **exploration** (2026-07-06). No code. Prompted by the `labels` theme
review: on the engineering board ~all cards are `programming-task`, so the
type-driven color/icon key renders every card identically and carries zero
information.

## The problem

The UI has standardized on **card type** as the visual differentiator: 
`TypeTheme{icon, accent, muted}` on the type feeds the corner mark / spine
color / badge, `[data-type]` CSS tokens, and the board header's TYPE filter.
That works when a board mixes types — and fails on the common board that has
exactly one type. A team's real categories ("feature vs bug vs design task")
then live in an enum field or a tag, invisible to the eye and absent from the
filter dropdown.

The alternative the board currently gestures at (backlog card `cb9e2ca`:
"dogfood 3+ card types — feature/bug/task") solves the *visuals* by **type
explosion**: three near-identical schemas to maintain, `allowed_columns`
duplicated, migrations tripled — schema mechanics spent on what is really a
one-word categorization. Type should stay what it is (a field contract);
category is data.

## Proposal sketch

Two small, layered pieces:

**1. Enum options can carry a theme** (field definition, workspace-level —
consistent with "the definition drives every surface"):

```json
{
  "id": "kind", "type": "enum",
  "options": ["feature", "bug", "design", "infra"],
  "option_themes": {
    "feature": { "icon": "star",   "accent": "#005bd3", "muted": "#d9e8ff" },
    "bug":     { "icon": "bug",    "accent": "#e21f26", "muted": "#ffd9d6" },
    "design":  { "icon": "pen",    "accent": "#cf7b00", "muted": "#ffe8bf" },
    "infra":   { "icon": "wrench", "accent": "#596469", "muted": "#e0e4e5" }
  }
}
```

This is the user-facing value→icon/color mapping: the enum value names the
category, and `option_themes[value]` supplies the icon and card colours for that
category. Icon names resolve through the design-system `data-icon` aliases
(currently `card`, `star`, `bug`, `check`, `flask`, `target`, `code`, `pen`, and
`wrench`; adding a new icon means adding one CSS mask alias, not changing card
markup).

**2. A board opts in** to using that field as its visual + filter key:

```json
"presentation": { "style_field": "kind" }
```

Resolution chain, one place (`httpapi.cardView` / modal data):

```
option theme (card's style_field value)  →  TypeTheme (card's type)  →  CSS [data-type] defaults
```

Today, without `style_field`, users can already specify type-level identity in a
card type:

```json
{
  "id": "programming-task",
  "type_theme": { "icon": "code", "accent": "#0f5d78", "muted": "#d9edf2" }
}
```

With `style_field`, the same `TypeTheme` shape is reused for enum values so the
card root, modal badge, and detail header all receive the effective `data-icon`,
`--card-stock`, and `--card-stock-bg`.

- Cards without the field, or with an unthemed value, fall through — nothing
  breaks, `style_field` is pure opt-in.
- The board header's TYPE dropdown becomes the style-field dropdown when one
  is declared (options come from the field def — already introspectable);
  filtering maps to the existing `fields.kind=bug` query surface.
- Themes (default/labels/journal) need no changes: they already render from
  `--card-stock`/`--card-stock-bg`/icon, which the resolution chain feeds.

## Why this fits the architecture

- **Same seam as today.** `TypeTheme` merging (`typeTheme()`, inline
  `--card-stock` vars, `[data-type]`) already exists; this adds one lookup in
  front of it, not a second theming system.
- **Definition-driven.** The enum def drives API validation, CLI/MCP schema,
  *and now* the visual identity — one source, every surface, per the
  philosophy.
- **Kills the type-explosion pressure.** `cb9e2ca`'s feature/bug/task split
  becomes one `programming-task` type + a `kind` enum — cheaper to author,
  migrate, and query.

## Open questions (decide before building)

1. **Where do option themes live** — decided for the proposed contract: on the
   enum field definition (`option_themes`), because the value's identity should
   be consistent across boards. A board opts into *using* the field via
   `presentation.style_field`; it does not redefine the icon/color map.
2. **Filter semantics.** Replace the TYPE dropdown or sit beside it? On a
   single-type board "replace" is obviously right; on mixed boards both keys
   are meaningful. Cheapest honest answer: show the style-field dropdown when
   declared, keep TYPE in saved filters.
3. **Tags overlap.** Tags are also lightweight categorization. Rule of thumb
   worth documenting: enum = exactly-one-of, drives identity; tags = any-of,
   never drive identity.
4. **`card_preview` interplay.** If the style field is promoted to color/icon,
   it should probably drop out of the preview line automatically (it would be
   redundant), like status already is in the labels theme.

## Non-goals

- No new field types, no per-card colors, no user-set colors on arbitrary
  fields. Only `enum` gets themes: bounded values, bounded palette.
- Not a replacement for `TypeTheme` — mixed-type boards keep working unchanged.
