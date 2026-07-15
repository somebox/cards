# Work Cards GitHub Pages Site & Integration Roadmap

This document outlines the TODO items and technical design for building our GitHub Pages site, the necessary automation workflows, and architectural guides on connecting outside productivity tools (Obsidian, Jira, GitHub Issues) into the Work Cards ecosystem.

---

## Part 1: GitHub Pages Site Plan & Setup

To serve static documentation from our structured workspace, we will target a Markdown-driven documentation generator like **MkDocs** (with the `mkdocs-material` theme) or **Docusaurus**. Both compile static HTML beautifully, map onto our topical folders, and run seamlessly as a GitHub Actions workflow.

### TODO List: Site Deployment

- [ ] **Configure MkDocs Configuration (`mkdocs.yml`)**
  - Define theme: `material` with search, copy-to-clipboard, and dark/light toggles.
  - Set up navigation tree matching our unified `docs/` topical folders:
    - **Home (Landing Page):** General overview, key value propositions, quick setup.
    - **Concepts:** Vocabulary (`index.md`), Philosophy (`philosophy.md`).
    - **Specification:** Main Spec (`index.md`), Data Model (`data-model.md`), API (`api-surface.md`), Events (`events-history.md`), Query DSL (`query-dsl.md`), Schemas (`card-types.md`).
    - **Architecture:** Go Core & Runtime (`index.md`), Styling / Design System (`design-system.md`).
    - **Reference:** Workspace Configuration (`workspace-and-boards.md`), Schema Rules (`card-definitions.md`), Field Types (`card-type-examples.md`), `cards` CLI (`cli.md`), Drift Audit (`implementation-status.md`).
    - **Events:** Events Index (`index.md`), Core Event Contract (`core.md`), Rollout History (`rollout.md`), SSE Monitors & Conditions (`integration.md`).
    - **Extensions:** Subprocess Hooks (`index.md`), Model Context Protocol (`mcp.md`).
    - **Walkthroughs:** Landing Guide (`index.md`), Shared Setup (`setup.md`), Software Delivery (`software-delivery.md`), Shop Floor (`shop-floor.md`).
- [ ] **Draft the Landing Page (`docs/index.md`)**
  - High-level value propositions (small binary, zero-config launch, SQLite/Git backed, agent-native typed tools).
  - Quick-start guide (installing pre-built binary, executing `cards init`, running UI, connecting an agent).
- [ ] **Create GitHub Actions Workflow (`.github/workflows/deploy-pages.yml`)**
  - Automatically deploy to the `gh-pages` branch on push to `main`.
  - Workflow blueprint:
    ```yaml
    name: Build & Deploy GitHub Pages
    on:
      push:
        branches:
          - main
    permissions:
      contents: write
    jobs:
      deploy:
        runs-on: ubuntu-latest
        steps:
          - uses: actions/checkout@v4
          - name: Set up Python
            uses: actions/setup-python@v5
            with:
              python-version: '3.x'
          - name: Install MkDocs Material
            run: |
              pip install mkdocs-material
          - name: Compile and Deploy Site
            run: |
              mkdocs gh-deploy --force
    ```

---

## Part 2: Integration Roadmap

Connecting external interfaces lets Work Cards function as the central "coordination terminal" that agents and human developers share.

### 0. The Primary Use Case: Bidirectional, Lossless Migration & Open Collaboration

> **Captured here as a use case and a future architectural direction — not a TODO to build today.**

The most important reason to build robust import/export adapters for Cards is that project state should be **owned by the team, not by a vendor's hosted database.** Once a team starts tracking work in Cards, that history should remain a portable, git-versioned artifact, with a clear way out if needs change later.

**Why this matters:**

- **Open collaboration:** A Cards workspace is a directory of plain files (JSON, Markdown, SQLite). That means the *entire project state* (issues, epics, comments, history) can live in a public or shared git repository. Anyone — a contributor, an auditor, a future maintainer — can `git clone` and see the full context of how the work evolved, with no proprietary lock-in.
- **Cards as living documentation:** Because cards are git-tracked, they double as project history. You can read the `boards.json` and `cards.jsonl` diffs to understand *why* a decision was made, not just *what* was decided. The README points to the spec; the spec references the cards; the cards themselves become browsable narrative.
- **Lossless escape hatch:** A team that started a project as a Cards workspace (e.g. for a hackathon, a class, an internal R&D spike) should be able to move that work to Jira, GitHub Issues, Linear, or Trello on day one of "this became a real product." Conversely, a team moving *off* Jira should be able to export their full history into Cards, drop it in a git repo, and continue working without losing the prior narrative.
- **Multi-source sync / federation:** Many real workflows span multiple trackers simultaneously — a single project might be tracked in both Jira (for the org's reporting) and GitHub Issues (for the OSS community), or mirrored across two Cards workspaces for a distributed team. A "common portable project format" (e.g. the existing `cards export` / `cards import` JSONL envelope) is the natural pivot point for keeping these aligned.

**What this implies for Cards' architecture (future, not now):**

- The portable export/import format should be treated as a first-class public contract, not just a CLI convenience. Today it is a newline-delimited envelope over the same shape the SQLite store uses; promoting it to a stable, versioned schema (a "Common Portable Project Format") would let adapters for Jira, GitHub Issues, Trello, Linear, Notion, etc. all live outside the core repo.
- Such adapters would naturally be implemented as Work Cards *extensions* (or thin external daemons) reading the SSE event stream and writing back to the external tracker, rather than baked into the core. That keeps Cards small and lets the community build the adapters they need.
- The README and the GitHub Pages site should explicitly call out "your data is git-tracked and portable" as a headline value proposition, alongside "small binary" and "typed agent tools."

**Concrete examples to capture in the docs site (when we get to writing the Examples section):**

- "Hackathon $\rightarrow$ Real product": start a Cards workspace for a weekend project, push it to a public git repo, then export the JSONL and import into Jira when the team gets funded.
- "Jira $\rightarrow$ OSS liberation": a team exports their Jira board to Cards, opens a git repo with the workspace checked in, and migrates the issue tracker to GitHub Issues for community contribution.
- "Two Cards workspaces, one team": a distributed org has separate Cards workspaces per region; a nightly diff-and-merge job keeps a shared subset of cards in sync, all via the portable format.
- "Read-only mirror": a Cards workspace mirrors GitHub Issues for low-friction local querying and agent-driven analysis, without ever writing back.

None of this is built today. The point of this section is to keep the *idea* alive in the docs so that when someone asks "could Cards be the migration target?" or "could we run Cards alongside Jira?" the answer is documented and the path is obvious.

---

### 1. Obsidian Markdown Integration (`cards` $\leftrightarrow$ Vault)
**Concept:** Since Work Cards workspace configurations, definitions, and outputs are plain files, and Obsidian is a local folders-of-markdown tool, we can easily sync them.

- [ ] **Implementation Strategy (The "Bases" Approach)**
  - Configure the local `.cards/` directory inside or adjacent to an Obsidian Vault.
  - Create a custom daemon or watch-service (or run-extension) that maps Obsidian notes into Work Cards:
    - Whenever a `.md` card file is modified inside the Obsidian Vault, a status update parser extracts frontmatter fields (e.g. `title:`, `status:`, `owner:`) and syncs it back to SQLite via the CLI client.
    - Conversely, when events are dispatched by the Work Cards event bus (SSE), we run a hook that updates the local Markdown files inside the Obsidian folder.
  - **Key Benefit:** Enables absolute visual representation of board workflows using Obsidian Canvas or Obsidian dataview queries, acting as a rich offline-first GUI.

### 2. Jira Import & Synchronization System (ETL)
**Concept:** Map enterprise issues (Jira) onto lightweight Work Cards boards for zero-latency local browsing, script query, and agent-driven updates.

- [ ] **Implementation Strategy (Jira API $\rightarrow$ Cards Scheme)**
  - Map Jira statuses (e.g. "To Do", "In Progress", "Code Review", "Done") to Work Cards list columns defined in `boards.json`.
  - Compile custom python client script (`sync_jira_to_cards.py`):
    1. Authenticate with Jira API; run JIRA JQL search payload to download focused JSON representations of cards/tasks.
    2. Map Jira issue fields to card schema configurations:
       - `key` (e.g., ENG-101) $\rightarrow$ card `id`
       - `fields.summary` $\rightarrow$ card `title`
       - `fields.status.name` $\rightarrow$ card `status`
       - `fields.assignee.displayName` $\rightarrow$ card `owner`
       - `fields.description` $\rightarrow$ card `text` (rendered as Markdown)
    3. Push mapped payloads to the local database using `cards create` or `PATCH /v1/cards` over HTTP.
  - **Deduplication:** Enable idempotency using the Jira `issue_key` (or write custom UUID mapping) to avoid double-processing during delta-sync intervals.

### 3. Read-Only GitHub Issues Board
**Concept:** Bring public/private repository issue states into Work Cards, allowing project managers and developer agents to run MCP tools or local search CLI directly over open/closed issue backlogs.

- [ ] **Implementation Strategy (GitHub Webhooks & Actions sync)**
  - Define custom card-type `github-issue` carrying fields: `issue_url`, `prs_associated`, `labels`.
  - Set up standard polling script via Git Action run on timer, or configure an incoming Work Cards hook service that acts as a web-endpoint listening to GitHub issue Webhooks:
    - Listening to action `issues.opened`, `issues.closed`, `issues.assigned`.
    - Automatically updating the local memory DB/workspace to reflect repository state.
  - **Security / Read-Only Safety:** By wrapping status actions in validations (using custom extension validation layers in Work Cards), we verify that local agents cannot edit status flags on Github issues without passing GitHub token access review.
