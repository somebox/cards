---
title: A local coordination service for people and agents
hide:
  - navigation
---

# Cards

Cards is a local coordination service for defining, reviewing, and assigning
work. A project defines its card types, boards, columns, transitions, and
extensions in JSON (extensions may also be YAML). The `cards` binary loads
those definitions, stores card state and events in SQLite, and exposes the
same model through HTTP, CLI, MCP, a web UI, and a terminal UI.

It was built for projects where plain todos are too little structure and a
hosted tracker is more process than the team needs. Humans, scripts, and
agents can claim cards, update typed fields, append evidence, and resume from
the card history later. The web board and TUI are useful, but each is only one
view over the same API.

<span class="cards-badge">v0.1.x · beta</span>
&nbsp;Local-only by default · SQLite-backed · git-portable · MIT.

[Get started](get-started.md){ .md-button .md-button--primary }
[Connect an agent (MCP)](agents/mcp.md){ .md-button }
[Agent instructions](agents/instructions.md){ .md-button }

## Definitions in, tracker out

A card type defines the fields; the detail page renders from it. A board
definition picks columns, card types, and transition rules; the lanes render
from that. There is no separate UI configuration.

<div class="cards-duo" markdown>

```json title="definitions/card-types/programming-task.json"
{
  "id": "programming-task",
  "name": "Programming Task",
  "fields": [
    { "id": "description", "type": "text",
      "required": true },
    { "id": "branch", "type": "string",
      "required": true, "display": "badge" },
    { "id": "kind", "type": "enum",
      "options": ["feature", "bug", "design", "infra"] },
    { "id": "work_log", "type": "repeating",
      "display": "feed",
      "item_fields": [
        { "id": "commit_hash", "type": "string",
          "required": true },
        { "id": "notes", "type": "text" },
        { "id": "author", "type": "user",
          "required": true }
      ] }
  ],
  "allowed_columns": ["backlog", "todo",
    "in_progress", "review", "done"]
}
```

<figure markdown>
  ![The card detail page rendered from the schema](assets/img/card-detail.png){ .cards-shot }
  <figcaption>The card that schema renders: badge, enum, work-log feed, comments.</figcaption>
</figure>

```json title="definitions/boards/engineering.json"
{
  "id": "engineering",
  "columns": ["backlog", "todo",
    "in_progress", "review", "done"],
  "card_type_ids": ["programming-task",
    "research-goal", "api-task"],
  "settings": { "enforce_transitions": true },
  "wip_limits": { "in_progress": 3 },
  "transitions": {
    "backlog": ["todo"],
    "todo": ["in_progress"],
    "in_progress": ["review"],
    "review": ["done", "in_progress"]
  }
}
```

<figure markdown>
  ![The board lanes rendered from the board definition](assets/img/board-lanes.png){ .cards-shot }
  <figcaption>The columns that board draws, with per-type preview fields.</figcaption>
</figure>

</div>

<div class="grid cards" markdown>

-   :material-cube-outline:{ .ic-nav } **Local-first**

    ---

    Runs on your machine. Definitions and a SQLite database live in the
    project folder — no account and no hosted service.

-   :material-shape-outline:{ .ic-schema } **Schemas drive everything**

    ---

    A workspace is a folder of JSON definitions: card types, boards, columns,
    and tags. One card-type file is the web form, API contract, CLI surface,
    and generated MCP tools.

-   :material-source-branch:{ .ic-nav } **Git-portable**

    ---

    `cards export` writes cards, comments, and links to a `backlog.jsonl` you
    commit next to the definitions. A collaborator pulls, imports, and
    continues.

-   :material-tune-variant:{ .ic-nav } **Extensible**

    ---

    The core stays small: cards, columns, events. Themes, transitions, WIP
    limits, new card types, hooks, and extensions are optional.

</div>

## :material-source-branch:{ .ic-nav } Your work stays in your repo

If you are coordinating people and agents on a project, the useful property is
that the cards live with the code.

`definitions/` is plain JSON under version control. Live state is one SQLite
file, and `cards export --state-only` snapshots it — every card, comment, and
link — into a `backlog.jsonl` that diffs cleanly in review. This repo does
exactly that: the bundled demo workspace is the project's real backlog.

Many agent setups keep work in a private list, a vendor issue tracker, or a
markdown plan the agent rewrites each pass. Those can work inside one
ecosystem, but the board is then tied to that tool's format.

Cards keeps the same board behind an HTTP API and an MCP server over files in
your repo. Any client that speaks either one can read and write it — including
two harnesses side by side, or a `grep` of your own backlog.

→ [The workflow](using-cards.md) — a working session end to end, and
what the committed snapshot buys you

## :material-robot-outline:{ .ic-agent } A board beats a markdown plan

Multi-agent work often ends up in a `plan.md` that gets rewritten until the
diff is unreadable, and that falls apart when two agents edit it at once. A
shared board holds the same information with less collision:

- **Typed fields hold the state.** A bad write is rejected with the field, the
  value, and what was allowed.
- **Comments hold the conversation.** You can read back why a card moved, not
  just that it did.
- **Work logs and attachments hold the evidence.** A `repeating` field collects
  commit hashes, notes, and authors as a feed; `artifact` fields hold the
  files.
- **Every change is a versioned event.** Two agents writing the same card is a
  `version_conflict`, not a silent overwrite, and `take_next` assigns each
  worker its own card atomically.

From a card type, the MCP server generates typed tools; validation errors
carry the allowed values so agents correct themselves:

<div class="cards-duo" markdown>

```text title="Generated MCP tools"
workspace
create_programming-task
update_programming-task
take_next
claim / release
add_comment / append_entry
history / events / breaches
```

```json title="A rejected write"
{
  "error": "invalid_field",
  "field": "status",
  "message": "\"doing\" is not an allowed column",
  "valid_options": ["backlog", "todo",
    "in_progress", "review", "done"]
}
```

</div>

Claude Code, pi, or a plain shell script — anything that speaks MCP or HTTP —
can claim a card, work it, log what it did, and move on.

→ [Connect an agent](agents/mcp.md) · [Agent instructions](agents/instructions.md)

## :material-console:{ .ic-cli } CLI and terminal UI

Every mutating operation works from the CLI — serverless against the workspace
folder, or pointed at a running server with `CARDS_URL`. Output is JSON (`-q`
prints just the id), so it pipes.

<div class="cards-term">
<div class="cards-term__bar"><span class="cards-term__dot cards-term__dot--r"></span><span class="cards-term__dot cards-term__dot--y"></span><span class="cards-term__dot cards-term__dot--g"></span></div>
<pre><span class="tp">$</span> <span class="tc">cards create --type task --title "Draft changelog" --status todo -q</span>
<span class="to">card_5f03f5f9</span>
<span class="tp">$</span> <span class="tc">cards take-next --board engineering --type programming-task -q</span>
<span class="to">card_7e090c38</span>
<span class="tp">$</span> <span class="tc">cards patch card_7e090c38 --status review --version 2 -q</span>
<span class="to">card_7e090c38</span>
<span class="tp">$</span> <span class="tc">cards comment add card_7e090c38 --body "fix pushed, PR #212" -q</span>
<span class="to">card_7e090c38</span></pre>
</div>

A bare `cards` on an interactive terminal opens the TUI against the same
workspace — no server required. Lane tabs, card list, and a markdown detail
pane share the service layer with the CLI; `q` quits. In scripts and pipes,
bare `cards` still prints usage.

```text
 Demo workspace · Engineering · my 1                                                         ● live
  Backlog 23 │ To Do 9 │ In Progress 0 │ Review 0 │ Done 146
 ─ Done · 146 cards                                                                          119/146
  Programming… Events seam 1f: Eve… ·          15d ↪2 ▾1  ╭───────────────────────────────────────╮
  Programming… Events seam 1a: ext… ·          15d ↪1 ▾2  │                                       │
  Programming… Events seam 1e: mig… ·          15d ↪2 ▾1  │   ## Events: actor/owner stream       │
▌ Programming… Events: actor/owner… ·          17d ↪1 ▾1  │   filters + GET /v1/events catch-up   │
                                                             ╰───────────────────────────────────────╯
h/← lane ← • l/→ lane → • j/↓ down • enter open • / find • ? keys • q quit
```

→ [Using Cards](using-cards.md) · [CLI reference](reference/cli.md)

## :material-palette-outline:{ .ic-board } Themes

Themes are one scoped CSS file in the workspace — no build step, no fork. A
theme that fails validation is rejected and the UI falls back to the default.
The built-in and demo themes:

<div class="cards-gallery" markdown>

<figure markdown>
  ![The journal theme](assets/img/theme-journal.png){ .cards-shot }
  <figcaption><code>journal</code> — paper background, handwritten type</figcaption>
</figure>

<figure markdown>
  ![The labels theme](assets/img/theme-labels.png){ .cards-shot }
  <figcaption><code>labels</code> — monospace type, colored card accents</figcaption>
</figure>

<figure markdown>
  ![The jeeruh theme](assets/img/theme-jeeruh.png){ .cards-shot }
  <figcaption><code>jeeruh</code> — conventional tracker styling in blue</figcaption>
</figure>

</div>

→ [Themes guide](guides/themes.md) — writing and installing your own

## :material-book-open-variant:{ .ic-nav } Documentation

<div class="grid cards" markdown>

-   ### :material-rocket-launch-outline:{ .ic-nav } [Get started](get-started.md)

    ---

    Install, create a workspace, serve the board, connect an agent. About two
    minutes.

-   ### :material-shape-outline:{ .ic-schema } [Define card schemas](reference/card-definitions.md)

    ---

    Card types, the ten field types, validation rules, and schema versioning.

-   ### :material-view-column-outline:{ .ic-board } [Workspace & boards](reference/workspace-and-boards.md)

    ---

    Columns, boards as filtered views, transitions, WIP limits, and monitors.

-   ### :material-robot-outline:{ .ic-agent } [Agents & MCP](agents/mcp.md)

    ---

    Run the MCP server, wire it into a harness, and follow the coordination
    loop.

-   ### :material-console:{ .ic-cli } [Using Cards](using-cards.md)

    ---

    Every operation documented once, with CLI, HTTP, and MCP examples and
    real responses side by side.

-   ### :material-palette-outline:{ .ic-board } [Themes](guides/themes.md)

    ---

    What themes can customize, the validation rules, and how to install and
    share one.

-   ### :material-broadcast:{ .ic-api } [Events & extensions](extensions/index.md)

    ---

    Hooks, services, and the SSE event stream — automation lives outside the
    core.

-   ### :material-file-document-outline:{ .ic-nav } [Specification](spec/index.md)

    ---

    The full contract: API surface, data model, query DSL, and the
    code-verified audit of what's built.

</div>

---

[Get started](get-started.md){ .md-button .md-button--primary }
[Read the concepts](concepts/index.md){ .md-button }
[See what's actually built](reference/implementation-status.md){ .md-button }
