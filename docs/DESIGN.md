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

Convention: tokens, not literals. The only intentional literals are the nav
chrome colours and `#fff/#000` "max ink" hovers.

## Components

`.btn` (+ `--primary/--ghost/--danger/--sm`), `.input/.select/.textarea`,
`.chip` (+ `--tag/--owner`), `.card__type-badge` (modal/detail/home type
stamp), `.card__type-mark` (board card's corner stock tab), `.card` +
`.card__title/__meta/__preview/__stats` + `.card__stat[data-stat]`, `.lane` +
`.lane__head/__count/__body`, `.modal` + `__head/__meta/__body/__footer` and the
shared `card_body` block, `.field` (view/edit), `.rel` (relationship rows),
`.toast`, `.search`, home cards. Layout primitives:
`.stack/.row/.cluster/.grid/.between/.muted/.faint/.truncate/.vh`.

**Icons** are monochromatic `currentColor` mask-images (data-URI SVG) keyed by
`[data-type]` / `[data-stat]` — one colour, consistent size, no emoji.

## Anatomy

- **Board card** (`card_partial.html`): corner type mark (`.card__type-mark` —
  wash stock + ink icon; type name in a `.vh` label + `title`) · title · owner
  chip · preview line · stats row (updated-time left; comment / ↗ out / ↙ in
  counts right). The card root is not a `role=button`; the title `<a>` is the
  keyboard affordance.
- **Modal** (`card_modal.html` → `.modal`): a fixed **3:2 note card**
  (`min(1040px, 92vw, 135vh)`), soft shadow, lightly-dimmed board behind. Fixed
  header (title + one metadata line) and footer (actions); the body is the
  single scroll region. `.modal[open]` gates `display`.
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
| `.card[data-type="<id>"]` | one card type | CSS defaults; `CardType.type_theme` (`icon`/`accent`/`muted`) overrides inline as `--card-stock(-bg)` / `--badge-ink(-wash)` |

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

The worked reference is **`journal`** (`?theme=journal`): a hand-kept
meeting-notes look — warm paper desk, pastel sticky-note cards scattered at a
slight rotation with varied shadow depth, handwritten type (Caveat/Kalam),
rubber-stamp chips, and a lined-notebook modal with a red margin rule and
taped-on repeating entries. It demonstrates how far a theme can go on the same
tokens + components: **the default `:root` theme stays the reference for clean,
information-dense UI; `journal` is the proof the contract is expressive.**

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
