# Design system — the web UI

The `/ui` surface (`internal/httpapi/templates/`) is a server-rendered reference
consumer of the API. Its look is a single token-driven CSS system in
`templates/style.css` — no build step, htmx + a little inline JS in
`layout.html`. This doc is the contract for that system: the principles, the
tokens, the components, and the theming hooks. **The default theme (the values
in `:root`) is the reference implementation of clean UI** — every theme and
every new component is judged against it.

## Metaphor: a pre-press job board

The page is a **silver-grey press sheet** with a registration **dot-grid**. Each
card is a **white note card** printed on it. The UI that *controls* the cards
(nav, filters, modal) is a distinct **ink layer** floating above the card world.

- **Printing stays consistent, stock varies.** Black registration marks (lane
  headers, labels, the corner type mark's ink) are the constant "printing";
  colour is "stock" that a type or a board can vary.
- **Line weights carry meaning**, dividers are **dots not rules**, chips are
  **rectangles not pills**, labels are **mono, uppercase, tracked**.

## Principles (normative)

These four rules gate every change; violations are bugs, not taste.

### 1 · Containers own spacing

Spacing is **dynamic**: rhythm comes from container `gap`, never from margins
sprinkled on children.

- A component sets **no external margin**. Its parent spaces it — via `gap` on
  flex/grid containers or the `.stack/.row/.cluster` primitives.
- One scale only: `--s-1..6`. No ad-hoc pixel margins/paddings.
- **Equal air**: a container's padding equals the gap between its children's
  level — e.g. card padding (`--s-3`) = the lane's card gap (`--s-3`), so the
  space inside a card's edge and between two cards reads identical.
- Page-level containers (main padding, modal width) use `clamp()`/`min()` so
  container sizing stays proportional across viewports; components inherit the
  space, they don't hardcode it.

### 2 · Type is compact but never small

- One scale: `--t-xs..xl` (body `--t-base` .9rem/1.5; headings 1.2). Density
  comes from **spacing, not from shrinking type** — nothing renders below
  `--t-xs` (.7rem).
- Roles, not sizes: `--font-sans` for titles/body; `--font-mono` for labels,
  meta, ids, buttons, timestamps (the "typed detail" voice — uppercase +
  tracked when it's a label).
- Any text inside a control renders at the size of the display text it edits
  or sits beside (see Principle 3).

### 3 · Editing is WYSIWYG

Click-to-edit must feel like typing on the card, not opening a form.

- Every editable field is one `.field` with a `[data-view]` (display) and a
  `[data-edit]` (control). The control renders at the **same font-size,
  line-height, weight, and box padding** as the view — enforced by the
  `.field__edit .input/.select/.textarea` parity rules; extend those rules,
  never per-field overrides.
- Activating edit changes **chrome, not geometry**: a border/focus ring
  appears; width, height, and neighbours must not shift.
- The view's empty state and the control's `placeholder` use the **same
  words** ("Add a description…" both ways).
- This covers selects and enums too: they are restyled (`appearance:none`,
  masked chevron, matching type) so a value doesn't change voice when it
  becomes editable.

### 4 · Themes remap tokens, never structure

A theme is a set of token values plus (optionally) scoped rules on the public
hooks below. Themes must not require markup changes, and structural class
names are a stable API.

## Tokens (`:root`, remapped in the `prefers-color-scheme: dark` block)

Everything visual reads through custom properties; the whole system reskins by
remapping `:root`. Categories:

- **Neutrals / substrate** — `--c-flat` (press sheet), `--c-flat-dot`,
  `--c-surface` (card stock), `--c-surface-2/3`, `--c-ink`, `--c-text`,
  `--c-text-2`, `--c-muted`, `--c-faint`, `--c-border`, `--c-border-2`.
- **Label stamp** — `--c-label-bg` / `--c-label-fg` (inverts in dark). **Nav
  chrome** — `--c-nav-bg` / `--c-nav-fg` deliberately do *not* remap.
- **Accent + semantic** — `--c-accent` / `-2` / `-soft`, `--c-success`,
  `--c-danger` / `--c-danger-soft`.
- **Per-type stock** — `--type-<id>` (ink) + `--type-<id>-bg` (wash); a board
  card reads them as `--card-stock` / `--card-stock-bg` (set per `[data-type]`)
  to paint its corner type mark. Every `-bg` has a paired dark value.
- **Relationship / link hues** — `--rel-out` / `--rel-in`, `--link-<type>`.
- **Scales** — spacing `--s-1..6` (4px base), type `--t-xs..xl`, radius
  `--r-sm/md/lg/pill`, shadows `--sh-sm/hover/md/lg`.
- **Semantic line weights** — `--edge` (thick), `--stroke` (outline), `--rule`
  (hairline / dotted divider).
- **Fonts** — `--font-sans`, `--font-mono`.
- **Typography roles** — `--role-*` (see "Typography roles" below). These are
  the tokens a theme actually edits; `--font-*`/`--t-*` are the raw scale a
  role points at.
- **Modal geometry** — `--modal-width`, `--modal-height`, `--modal-ratio`;
  a theme shrinks/grows the detail card by overriding these (e.g. `labels`
  sets a compact fixed-height card).

Convention: tokens, not literals. The only intentional literals are the nav
chrome colours and `#fff/#000` "max ink" hovers.

## Typography roles (`--role-*`)

Every text-bearing rule in `style.css` reads a **role token**, never a literal
`font-size`/`font-weight`/`font-family`/`line-height`/`letter-spacing`. Roles
are the seam between "what a piece of text *is*" (a card title, a field
label, header metadata, body copy) and "how big/heavy it renders" — so a theme
retunes the whole hierarchy from one block instead of hunting selectors, and a
size/weight can never drift out of sync between two elements that are supposed
to match (e.g. the header's status/owner/id/version/updated used to each carry
their own hardcoded size before this existed; now they all read
`--role-meta-*`).

| Role | Governs | Tokens |
|---|---|---|
| Heading | `h1`–`h4`, lane header, lane count, board card type badge | `--role-heading-weight` |
| Strong/emphasis | `<strong>/<b>`, nav links, secondary chrome | `--role-strong-weight` |
| Card title | Board card title (`.card__title`) | `--role-title-card-font/size/weight/leading/tracking` |
| Detail title | Modal/detail title (`.card-title__view`, its edit `.input`) | `--role-title-detail-font/size/weight/leading/tracking` |
| Field label | `.field__label` ("TAGS", "STATUS", …) | `--role-label-font/size/weight/tracking` (+ `leading` on the default theme) |
| Header metadata | `.modal__meta` line — status/owner/id/version/updated | `--role-meta-font/size/weight/leading/tracking`, `--role-meta-value-weight` (status/owner value emphasis) |
| Body/value | `.field__val`, `.field__view`, form controls | `--role-body-font/size/weight/leading` |

**Units.** `font-size`/`line-height` are `rem` (root-relative) or unitless
(line-height) — never `vw`/`vh`. A `vw` font resizes with the *window*, which
means the same element is a different px size on every visitor's screen and a
different size than what you measure in devtools on your own window; `rem`
resizes only with the root font-size (zoom, or a deliberate `html { font-size
}` override), which is what devtools "Computed" reports and what a user's
browser zoom setting expects. `clamp(min, preferred-vw, max)` is fine for
*layout* (`.modal` width, `main` padding — Principle 1) where fluid sizing is
the point; it is deliberately not used for type roles. Hairlines/icons
(`--edge`/`--stroke`/`--rule`, mask-icon width/height) use `px`, because a
1px border must render as exactly one device pixel's worth of thickness
regardless of the root font-size.

**Tweaking in devtools.** Inspect the element, open the Styles pane, and find
the custom property the matched rule reads (e.g. `.card__title` reads
`--role-title-card-size`). That property is declared once per theme scope
(`:root` for the default theme, `html[data-theme="labels"] { ... }` for a
named theme) — edit it there, in the scope shown next to the declaration, and
every element sharing the role updates together. Don't edit the resolved
`font-size` on the specific rule; you'll fix one element and leave its
siblings inconsistent again.

**Adding a theme.** Override only the `--role-*` custom properties that need
to change (usually `--font-sans`/`--font-mono` plus a handful of
`--role-title-*`/`--role-label-*` weights); the component rules already read
those roles and need no per-selector overrides. The `labels` theme is the
worked example: it loads Sono (weights 200/400/600) and maps ExtraLight→body,
Regular→emphasis/meta, Semibold→every heading/title/label role — titles are
therefore *always* bold, by construction, not by remembering to set
`font-weight` on every title selector.

## Components

`.btn` (+ `--primary/--ghost/--danger/--sm`), `.input/.select/.textarea`,
`.chip` (+ `--tag/--owner`), `.card__type-badge` (modal/detail/home type
stamp), `.card__type-mark` (board card's corner stock tab), `.card` +
`.card__title/__meta/__preview/__secondary/__stats` + `.card__stat[data-stat]`, `.lane` +
`.lane__head/__count/__body`, `.modal` + `__head/__meta/__body/__footer` and the
shared `card_body` block, `.field` (view/edit), `.rel` (relationship rows),
`.toast`, `.search`, home cards. Layout primitives:
`.stack/.row/.cluster/.grid/.between/.muted/.faint/.truncate/.vh`.

**Icons** are monochromatic `currentColor` mask-images (data-URI SVG) keyed by
`[data-type]`, optional config-emitted `[data-icon]`, and `[data-stat]` — one
colour, consistent size, no emoji. `data-icon` wins over type defaults so users
can choose visual identity without changing CSS selectors.

## Anatomy

- **Board card** (`card_partial.html`): corner type mark (`.card__type-mark` —
  wash stock + ink icon; type name in a `.vh` label + `title`) · title · owner
  chip · preview line · compact secondary line hook (`.card__secondary`,
  hidden by default for dense named themes) · stats row (updated-time left;
  comment / ↗ out / ↙ in counts right). The card root is not a `role=button`;
  the title `<a>` is the keyboard affordance.
- **Modal** (`card_modal.html` → `.modal`): a note card sized by the
  `--modal-width` / `--modal-height` / `--modal-ratio` geometry tokens (default
  `min(1040px, 92vw, 135vh)` × `3/2`), soft shadow, lightly-dimmed board
  behind. Fixed header (title + one metadata line) and footer (actions); the
  body is the single scroll region. `.modal[open]` gates `display`. A compact
  theme overrides the geometry tokens (e.g. `labels` → `min(660px,94vw)` ×
  fixed `min(80vh,680px)` / `auto`) so detail cards can be smaller than the
  default; documented presets: standard / compact / wide.
- **Modal keyboard nav**: `Esc` closes (or reverts the field being edited);
  `←`/`→` move to the previous/next card on the board (a test affordance — only
  fires when the modal is open and no input/select/textarea is focused, so
  editing text and tabbing the board are never hijacked). The board card's
  title `<a>` remains the keyboard entry point.
- **Relationships**: outbound = blue ↗ type-label-left; inbound = brick ↙
  title-left. Direction = colour + arrow + order.

## Theming — the contract

Themes hook onto four **stable** attach points; component class names and
`data-*` attributes are a public API (renames are breaking changes):

| Hook | Scope | Set by |
|---|---|---|
| `:root` token remap | whole app | a theme stylesheet / the dark block |
| `html[data-theme="<name>"]` | named theme | `settings.theme` (workspace default), overridable per-visitor via `?theme=<name>` (sticky cookie; `?theme=default` clears). Resolved in `httpapi.resolveTheme`. |
| `[data-board="<id>"]` wrapper | one board | `Board.theme` → `httpapi.boardStyle` (whitelisted inline tokens) |
| `.card[data-type="<id>"]` + `[data-icon="<name>"]` | one card type or styled value | CSS defaults; `CardType.type_theme` `accent`/`muted` override inline — board corner mark as `--card-stock`/`--card-stock-bg`, modal/home badge as `--badge-ink` (printing: text + outline) / `--badge-wash` (stock: background). `type_theme.icon` emits `data-icon`, which overrides the monochrome `[data-type]` mask glyph. Future board `presentation.style_field` support uses the same hook for enum option themes. |

Rules:

- **Board themes** may override only non-inverting **hue** tokens
  (`boardThemeTokens` whitelist: accents, flat, label, `--type-*`, `--link-*`,
  `--rel-*`) — never neutral/ink/surface tokens, so dark mode keeps working.
  Example (`examples/demo-workspace/definitions/boards/welcome.json`):

  ```json
  "theme": { "--c-accent": "#a8623c", "--c-flat": "#e4ded3", "--type-task": "#7a5c3a" }
  ```

- Board-tinting the neutral substrate across light+dark would need a generated
  `@media` `<style>` block (inline props can't respond to
  `prefers-color-scheme`); the whitelist deliberately avoids that.
- A theme that needs a hook that doesn't exist is a design-system change first
  (add the hook + document it here), a theme second.

### Named themes (`html[data-theme]`)

A named theme is one self-contained block at the end of `style.css`:
`html[data-theme="<name>"] { … }` — a token remap plus, unlike a board theme,
**scoped component rules** (fonts, shapes, decoration) that reskin structure
without touching markup. It may override any token, including neutrals, because
it's a full stylesheet scope (not inline props), so it owns its own light/dark
story. Select it with `?theme=<name>` (sticky) or a workspace `settings.theme`
default; the conditional web-font `<link>` for a theme lives in `layout.html`
keyed on `.Theme`.

> **Workspace-loaded themes.** A named theme need not be embedded in
> `style.css` — it can be loaded from `definitions/themes/<name>.{css,json}` and
> concatenated after the base stylesheet. Loaded themes are validated at load
> time by `internal/themecss` (braces balance · every rule scoped under
> `html[data-theme="<name>"]` · no `@import` · no remote `url()`), and a failing
> theme is rejected with a `422` (naming theme/file/line/rule) rather than
> served — a broken theme degrades to "absent," never to an error. The
> resolution precedence, `board.presentation.theme` layering, back-compat, and
> the font-manifest policy are specified in
> [`docs/design/THEMES.md`](../design/THEMES.md) → "Load-time contract". The
> `/ui/style.css?v=<stamp>` cache-buster is per-composition-generation, so a
> reload that changes the served CSS also rotates the URL.

The worked reference is **`journal`** (`?theme=journal`): a hand-kept
meeting-notes look — warm paper desk, pastel sticky-note cards scattered at a
slight rotation with varied shadow depth, handwritten type (Caveat/Kalam),
rubber-stamp chips, and a lined-notebook modal with a red margin rule and
taped-on repeating entries. It demonstrates how far a theme can go on the same
tokens + components: **the default `:root` theme stays the reference for clean,
information-dense UI; `journal` is the proof the contract is expressive.**

The second reference is **`labels`** (`?theme=labels`): a compact sorting view
where each board card is small adhesive label stock with a left colour/icon
spine, heavy Sono title, and one cropped secondary metadata row. Sono is loaded
at weights 200/400/600; body copy uses 200, bold/emphasis uses 400, and
headings/labels use 600 (titles are always bold). The detail card is smaller
than the default (`--modal-width: min(660px,94vw)`, fixed height, no ratio) and
uses textured white cardstock generated with an SVG `feTurbulence`/
`feDiffuseLighting` data-URI (`baseFrequency=.2`, `numOctaves=6`,
`surfaceScale=.7`). Its header is a **two-row attribute table**: the top row
(icon · title · close · copy-id) carries the card's solid colour; the bottom
row is four label/value cells (Status · Owner · Updated · Version) with
dark-grey label cells (white text) and white value cells (black text). Only
the title is editable in the header; status/owner are editable fields in the
body (type is read-only — a card's type is immutable). Body field labels are
uniform dark-grey squares with white text, matching the header attribute
labels. It demonstrates that a theme may change hierarchy, density, modal
geometry, and field-label treatment through documented hooks. NB: the labels
detail layout is the one place a theme *does* branch the shared `card_body`
template (`{{if eq $.Theme "labels"}}` in `card_modal.html`); every other
theme shares one header + body assembly.

## Substrate & upgrade path

The token layer is hand-rolled (~600 lines, zero dependencies). The sanctioned
investment, if/when taken, is to adopt a **standard token substrate** —
Open Props (custom-property scales, no build step) and/or Utopia-style
`clamp()` fluid space/type scales — mapping our `--s-*`/`--t-*` names onto it
and keeping the component + theme layers unchanged. Utility-first frameworks
that require a build step (Tailwind et al.) are out of scope: the aesthetic is
bespoke, and the server intentionally has no asset pipeline. Tracked on the
board (substrate card).

## Quality floor

Dark mode + `prefers-reduced-motion` supported; keyboard focus visible
(`:focus-visible`); the native `<dialog>` modal (Esc / backdrop / × dismiss
identically). Timestamps render via `<time data-ago="{{iso …}}">` + the client
`refreshAgo()` helper — always emit `iso` (RFC3339), never a raw Go
`time.Time`.
