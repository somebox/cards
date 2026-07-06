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

The theming *contract* (`docs/architecture/DESIGN.md`) is otherwise sound: a
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
  protocol, no parser, no build step.
- **Stable contract preserved.** `--role-*` + stable hooks are the public
  API; extraction just changes *where* a theme's bytes live, not *what* a
  theme is.

## Related

- `docs/architecture/DESIGN.md` — theming contract, `--role-*`, stable hooks.
- `docs/design/STYLE-FIELD.md` — enum-value → icon/colour mapping (independent
  of where the theme CSS lives, but shares the `data-icon` hook).
- Theme-contract card (`card_440a2bed`) — `html[data-theme]` hook + named
  themes (done).
- Style-field card (`card_8b3e83d9`) — `presentation.style_field` (backlog).
