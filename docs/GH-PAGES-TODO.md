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
    - **Concepts:** Vocabulary (`CONCEPTS.md`), Philosophy (`PHILOSOPHY.md`).
    - **Specification:** Main Spec (`SPEC.md`), Data Model (`SPEC-DATA-MODEL.md`), API (`SPEC-API-SURFACE.md`), Events (`SPEC-EVENTS-HISTORY.md`), Query DSL (`SPEC-QUERY-DSL.md`), Schemas (`SPEC-CARDTYPE-EXAMPLES.md`).
    - **Architecture:** Go Core & Runtime (`ARCHITECTURE.md`), Styling / Design System (`DESIGN.md`).
    - **Reference:** Workspace Configuration (`DEVELOPER-REFERENCE.md`), Schema Rules (`DEVELOPER-REFERENCE-SCHEMA-AUTHORING.md`), Field Types (`DEVELOPER-REFERENCE-TYPES-EXAMPLES.md`), `cards` CLI (`DEVELOPER-REFERENCE-CLI.md`), Drift Audit (`INTEGRATOR-REFERENCE.md`).
    - **Events:** Mutation Bus (`EVENTS.md`), SSE Monitors & Conditions (`INTEGRATION.md`).
    - **Extensions:** Subprocess Hooks (`EXTENSIONS.md`), Model Context Protocol (`MCP.md`).
    - **Walkthroughs:** Landing Guide (`LIFECYCLE-EXAMPLES.md`) + Software Delivery (`LIFECYCLE-EXAMPLES-SOFTWARE.md`), Shop Floor (`LIFECYCLE-EXAMPLES-SHOPFLOOR.md`).
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
