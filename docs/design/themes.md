# Themes — extraction, modularity, and installability

Status: **exploration** (2026-07-06). No code. Prompted by the observation that
named themes (`journal`, `labels`) are CSS blocks appended to the embedded
`style.css`, so a theme cannot be added or shared without rebuilding the
binary. This doc defines the basic solution and sequences the work.

## The problem

Today every theme is one `html[data-theme="<name>"] { … }` block at the end of
`internal/httpapi/templates/style.css`, which is `//go:embed`-ed into the
binary and served verbatim at `/ui/style.css`. Consequences:

- A new or customized theme requires editing source and rebuilding — it
  cannot live in a workspace, be `git`-tracked per-project, or be dropped in
  by someone who only has the binary.
- The `labels` theme currently **branches the shared `card_body` template**
  (`{{if eq $.Theme "labels"}}` in `card_modal.html`) for its two-row detail
  header. That is the one place a theme is *not* CSS-only — and it is the real
  blocker: until a theme can express that layout without markup changes, no
  extraction mechanism is safe.

The theming *contract* (`docs/architecture/design-system.md`) is otherwise sound: a
theme is a token remap (`--role-*`, `--modal-*`, palette) plus scoped rules on
stable hooks (`html[data-theme]`, `.card[data-type]`, `[data-icon]`). The
`--role-*` typography system already lets a theme retune the whole hierarchy
from one block. So themes are modular **conceptually**; they are not modular
**physically**.

## Non-goals

- Not a plugin marketplace or a packaging format. A theme is a CSS file (and
  optionally a font link), nothing more.
- Not per-user theme authoring in the UI. Themes are workspace content,
  authored as files, same as card-type definitions.
- Not a build step or asset pipeline (PHILOSOPHY #10 — boring tech). No
  Sass, no Tailwind, no bundler.

## Proposed solution

### 1. A theme is one CSS file (+ optional font sidecar)

```
definitions/
  themes/
    labels.css          # the html[data-theme="labels"] { … } block, verbatim
    labels.json         # { "fonts": "https://fonts.googleapis.com/css2?family=Sono:wght@200;400;600&display=swap" }
    journal.css
    journal.json
```

- `themes/<name>.css` is the exact block currently appended to `style.css`,
  moved to its own file. The embedded `style.css` keeps only `:root` +
  components + the dark block; named themes are layered on top.
- `themes/<name>.json` (optional) carries the web-font `<link>` that
  `layout.html` currently hardcodes per theme. The server reads it and emits
  the `<link>` when `.Theme == <name>`, so a theme can declare its own fonts
  without touching `layout.html`.
- No template overrides. A theme that needs markup changes is a design-system
  change first (add a stable hook), a theme second.

### 2. Workspace-declared themes (primary path)

`internal/config` loads `definitions/themes/*.css` like it loads card types
and boards: merge, validate (CSS is served as `text/css` — it cannot escalate
beyond styling, same trust boundary as a declared hook command). The server
concatenates embedded `style.css` + loaded theme files into the response at
`/ui/style.css`, and registers each found name so `resolveTheme` honours
`?theme=<name>` and `settings.theme`.

This matches PHILOSOPHY #2 ("files where they help"), #6 ("extensions over
plugins"), and #8 ("stable, documented contracts"). A `git pull` of a
workspace gets a new theme; no install step. Two workspaces can share a theme
by committing it; they don't need a package registry.

### 3. `--themes-dir` overlay (deferred)

A directory of CSS files layered on top of the workspace themes, for sharing
themes across workspaces without committing them to each. Same shape as
`--workspace` for definitions. Deliberately deferred — it's a "read a dir,
concatenate CSS" addition, and nobody needs it until they actually share a
theme across workspaces. Build it when the first user asks.

### Selection (already solved)

`?theme=<name>` (sticky cookie) and `settings.theme` resolve in
`httpapi.resolveTheme`. A loaded theme just needs its name registered so the
selector resolves; no new selection mechanism.

## The constraint: kill the template branch first

The `labels` theme's `{{if eq $.Theme "labels"}}` branch in `card_modal.html`
is the one place a theme touches markup. Before any extraction is useful,
that branch must go — otherwise an extracted `labels.css` alone cannot
reproduce the theme. Two ways to remove it:

1. **Promote the labels header layout to stable hooks.** Emit the header as
   icon / title / actions / meta with stable data attributes and classes in
   the shared template (for *every* theme), and let `labels.css` lay them out
   as the two-row attribute table while the default theme lays them out as
   today's single meta line. The default theme's appearance must not change.
2. **Or accept a CSS-only contract and drop the layout divergence**, reverting
   the labels detail header to the shared structure restyled by CSS. Cheaper,
   but loses the layout the user just designed.

Option 1 is the right one: it makes the theme contract honestly CSS-only,
which is the whole point. It is the real modularity unlock — once a theme is
"one CSS file, zero markup," extracting it is a file move plus a loader.

## Sequencing

1. **Remove the template branch** (prerequisite). Make the labels detail
   header achievable through stable hooks + role tokens in the shared
   template; default theme appearance unchanged. *This card.*
2. **Move `journal`/`labels` blocks out of `style.css`** into
   `definitions/themes/*.css` + `.json`, and teach `internal/config` +
   `httpapi.uiStylesheet` to load and concatenate them. Register names with
   `resolveTheme`. *Follow-up card — link depends-on this one.*
3. **Defer `--themes-dir`** until requested.

## Why this fits the architecture

- **Same seam as card types and boards.** Definitions already drive every
  surface from files in `definitions/`; themes become another subdirectory of
  the same workspace, loaded the same way.
- **Same trust boundary as extensions.** A declared theme file is trusted
  workspace content, exactly like a declared hook command
  (`extensions.json`). CSS-as-text can't execute; the risk is styling only.
- **Boring tech.** Read CSS files, concatenate, set `text/css`. No new
  protocol, no build step. The one addition is a *minimal* contract-checking
  scanner (`internal/themecss`, ~200 LOC, brace-match + scope + `@import`/remote-
  `url()` checks) — deliberately not a full CSS parser; see "Load-time contract"
  below.
- **Stable contract preserved.** `--role-*` + stable hooks are the public
  API; extraction just changes *where* a theme's bytes live, not *what* a
  theme is.

## Load-time contract (step 2 precursors — landed ahead of the loader)

Two precursors ship before the loader itself so step 2 is a small, safe wiring
change rather than a redesign:

**1. Per-generation stylesheet stamp (`httpapi.Server.assetStamp`).** The
`/ui/style.css?v=<stamp>` cache-buster is now instance state minted once per
`httpapi.New()` — and reload builds a fresh `Server` — so the URL rotates on
every reload while staying `Cache-Control: public, max-age=86400`. This is what
makes install-by-reload safe: dropping a theme file and calling
`POST /v1/workspace/reload` changes the served CSS *and* its URL, so returning
tabs refetch instead of holding stale bytes. (Pinned by
`cmd/cards` `TestReloadRotatesStylesheetStamp`.)

**2. The validator (`internal/themecss.Validate`).** Every workspace theme file
is checked at load time against the guarantees below; the built-ins pass the
same checks. See the package doc for the full threat model — in short, it is a
*contract* check, not a security sandbox (theme files are operator-trusted,
git-backed definitions), and it enforces:

- **Braces balance** — an unterminated rule swallows every later rule.
- **Every rule is scoped** under `html[data-theme="<name>"]` — including
  catching a balanced scope-escape (`html[data-theme="x"]{…}` followed by an
  unscoped `body{…}`). `@media`/`@supports` wrappers are transparent; their
  inner rules must also be scoped.
- **No `@import`** (pulls unbounded external CSS at load).
- **No remote `url()`** (`http:`, `https:`, protocol-relative `//`). Relative
  and `data:` URLs pass.

A theme that fails validation is **rejected, not served**: the reload returns
`422` whose body names `{theme, file, line, rule, message}` for each violation,
and the rest of the workspace keeps serving (contract guarantee 3 — a broken
theme degrades to "absent," never to "error page").

### Theme resolution & precedence (target chain for step 2)

Today `httpapi.resolveTheme` resolves, highest first:

1. `?theme=<name>` — explicit, persisted in the `wc_theme` cookie so it sticks
   across navigation; `?theme=default` clears it.
2. the `wc_theme` cookie.
3. `settings.theme` (workspace default).

Step 2 inserts **board-presentation theme** between the cookie and the
workspace default, so the chain becomes:

> `?theme=` (sticky cookie) → `wc_theme` cookie → **`board.presentation.theme`**
> → `settings.theme` → built-in default

Rationale: "assign a theme to a board to try it out" — a board can adopt a theme
without changing the workspace default or requiring every visitor to pass
`?theme=`. An explicit `?theme=` still wins (an operator overriding to compare),
and a per-visitor cookie still beats a board default.

### `Board.Theme` layering (two distinct hooks — keep them separate)

There are two board-level theming hooks and they do **not** collide:

- **`[data-board="<id>"]` inline tokens** (existing). `board.Theme` is a
  whitelist of hue tokens emitted as inline custom properties on the board
  wrapper (`boardStyle`). It *tweaks tokens* within whatever named theme is
  active — it is not a theme and never emits rules.
- **`board.presentation.theme`** (step 2). Names a full theme that sets
  `html[data-theme]` for that board via the precedence chain above.

They compose: the named theme sets the token baseline; the board's inline
tokens override specific hues on top. Neither requires markup changes.

### Back-compat (non-negotiable)

- The embedded `journal`/`labels` themes keep working unchanged; extraction to
  files (step 2) keeps them embedded as defaults so a bare `cards init` still
  has them.
- `?theme=` and the `wc_theme` cookie are unchanged.
- An unknown theme name still passes through to `html[data-theme]` and
  harmlessly matches no CSS (already pinned by an httpapi test) — a stale cookie
  or a not-yet-installed shared theme never errors.

### Workspace-font policy

The validator forbids remote `url()` *inside* theme CSS, but themes still need
web fonts. The reconciliation: a theme declares fonts **only** in its `.json`
manifest (`fonts` → a stylesheet href), the same reviewed, explicit channel the
built-ins use today (`themeFonts`). The manifest URL is an intentional,
git-reviewed declaration; an inline `url()` buried in CSS is not. So:

- **Allowed:** `fonts` href in the theme manifest (e.g. a Google Fonts URL),
  and `data:`/relative `url()` in the CSS.
- **Rejected:** any remote `url()` in the CSS body.

This keeps "where does this theme fetch from" answerable by reading one
manifest field, not by scanning stylesheet bytes.

## The theme contract, v1 (2026-07-07)

Step 1 landed (templates are theme-blind, pinned by test), which makes the
contract the load-bearing artifact: a theme is safe to add, remove, or change
**only because** the surface it styles is enumerated and stable. This section
names that surface. `docs/architecture/design-system.md` remains the reference for
tokens and typography roles; this is the catalog of everything else a theme
may target, grouped by surface.

### Guarantees (what makes themes modular)

1. **The default theme is complete.** Base CSS renders every surface fully
   with no named theme present. A theme is pure override — deleting every
   theme file leaves a working UI.
2. **Themes are scoped.** Every rule in a theme is prefixed
   `html[data-theme="<name>"]`; base CSS never references a theme name.
   *Enforced by `TestThemeRulesAreScoped` — an unscoped theme rule fails
   `go test ./...`.*
3. **Unknown names degrade to default.** Selecting a theme that doesn't
   exist (`?theme=nope`, a stale cookie, an uninstalled shared theme) renders
   the default: `data-theme="nope"` simply matches no rules. *Enforced by
   render test.*
4. **Markup is never theme-conditional.** Pinned by
   `TestTemplatesAreThemeBlind`. A theme that needs new structure is a
   design-system change first (add a hook to the contract), a theme second.
5. **CSS stays parseable.** `TestStyleCSSBalanced` (brace balance) — a single
   dropped brace once silently swallowed a whole theme.

### Element hooks (stable classes + data attributes, by surface)

- **Board**: `.board`, `.lane`, `.lane__head`, `.lane__count`, `.lane__add`,
  `.lane__body[data-status]`, `.board-controls`, `.filter-chips`,
  `.chip-filter`.
- **Board card**: `.card[data-type][data-icon]` (+ inline `--card-stock`,
  `--card-stock-bg` from the type/option theme), `.card__type-mark`,
  `.card__title`, `.card__meta`, `.card__preview`, `.card__secondary` +
  `__secondary-item(--status|--owner|--tag|--preview|--updated)`,
  `.card__stats` + `.card__stat[data-stat=blocked|comments|out|in]`,
  `.card__artifacts` (`__artifact-thumb`, `__artifact-file`), `.chip`
  (`--tag`, `--owner`).
- **Detail/modal header** (shared by every theme since step 1):
  `.modal__head[data-type][data-icon]`, `.modal__type-icon`,
  `.modal__head-main`, `.card-title__view`, `.modal__meta`,
  `.modal__meta-field` (editable status/owner: `.modal__meta-key` +
  `.field__view`), `.modal__meta-tail`,
  `.modal__meta-item[data-meta=id|version|updated]`
  (`.modal__meta-key`/`.modal__meta-value`), `.id-copy`, `.modal__close`.
- **Modal body**: `.modal`, `.modal__body`, `.modal__scroll`,
  `.modal__footer`, `.field` (label|value anatomy: `.field__label` + ONE
  value child — `.field__view`/`.field__val`/wrapper; themes may grid this),
  `.alert`, `.req`, `.field-error`, `.field-hint`.
- **Editing anatomy**: `[data-field]` with `[data-view]`/`[data-edit]`
  (click-to-edit); `.input`, `.select`, `.textarea`, `.btn`
  (`--primary|--ghost|--danger|--sm`), `.icon-btn` (+`--primary`) — the ONE
  style for micro-actions (+ add, ✎ edit, × remove/cancel, ✓ save/submit).
- **Feeds**: `.entry` (`__head`, `__author`, `__time`, `__actions`,
  `__grid`, `__key`, `__val`, `__body`), `.feed`, `.stack`,
  `.entry-form` (`__row`, `__label`, `__actions`, `__status`),
  `.comment-composer` (`__bar`, `__status`), `.entries-box`,
  `.comments-box`, `.entries-toolbar`.
- **Creation modals**: `.create-modal`, `.type-picker` (`__opt`),
  `.create-form`, `.check-grid`, `.check-item` (board create).
- **Uploads**: `.artifact-upload` (`__zone`, `__input`, `__cta`, `__hint`,
  `__status`), `.artifact-thumb`.
- **Identity**: `[data-type="<id>"]` per card type, `[data-icon="<name>"]`
  glyphs (`card star bug check flask target code pen wrench` — adding one is
  a single CSS mask alias), `.card__type-badge`.

### State hooks (what "actions" a theme can style)

- `.artifact-upload[data-state=idle|dragover|uploading|success|error]` — the
  upload state machine.
- `.is-invalid` on inputs, `.is-error` on status lines, `.alert` blocks.
- `.is-dragging` on cards, `.is-drag-over` on lanes (column move).
- `.entry:hover / :focus-within → .entry__actions` reveal.
- `.is-empty` on empty field views; `[hidden]` respected everywhere.
- `data-stat="blocked"` — must remain visibly text, never colour-only.
- Focus: `:focus-visible` outlines derive from `--c-accent`.

Contract changes are **additive within v1**: hooks may be added; renaming or
removing one is a v2 and must update every built-in theme in the same change.

## Sharing themes (the GitHub story)

The unit of sharing is deliberately tiny — two files, no packaging:

```
my-theme.css    # every rule scoped html[data-theme="my-theme"]
my-theme.json   # {"name":"my-theme","contract":1,"fonts":"https://…",
                #  "description":"…","source":"https://github.com/…"}
```

- **Publish**: put them in any repo/gist. A theme *is* its CSS — reviewable
  at a glance, no build step, nothing executable.
- **Install**: drop both files into `definitions/themes/` (themes.md step 2
  loads that directory) — by hand, `curl -O`, or a git submodule. A `git
  pull` of a workspace brings its themes along.
- **Select**: `?theme=my-theme` to try it (sticky cookie),
  `settings.theme` to default the workspace, and — new —
  `board.presentation.theme` to assign it to ONE board:
  precedence `?theme` cookie → board presentation → workspace settings.
  "Assign it to a board to try it out" is exactly the middle tier.
- **Safety**: the manifest's `"contract": 1` lets the loader warn on a theme
  written against a future contract; an unknown or broken theme file can at
  worst mis-style — guarantee 3 means it can never take the UI down.

## Related

- `docs/architecture/design-system.md` — theming contract, `--role-*`, stable hooks.
- `docs/design/style-field.md` — enum-value → icon/colour mapping (independent
  of where the theme CSS lives, but shares the `data-icon` hook).
- Theme-contract card (`card_440a2bed`) — `html[data-theme]` hook + named
  themes (done).
- Style-field card (`card_8b3e83d9`) — `presentation.style_field` (backlog).