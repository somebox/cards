# Card lifecycle walkthroughs

Two end-to-end examples that exercise create → links (dependencies) → claim →
work (append) → status transitions → completion. Each step shows equivalent
**HTTP** (`/v1`) and **CLI** (the `cards` command).

Assumptions:

- Sidecar running: `cards serve --workspace ./demo-workspace --port 8787`
- Base URL: `http://127.0.0.1:8787/v1`
- Actor for writes: register users once; CLI uses `CARDS_USER` or `--as`.
  Every write carries the actor via the **`X-Work-Cards-Actor`** header
  (`--as` sets it). See [`events-history.md` §12](../spec/events-history.md).

CLI global flags: `--url`, `--workspace`, `--as <user_id>`, `--json` for raw
output. Concurrency: PATCH/claim/take-next pass `--version`. Pinned to
[`index.md`](../spec/index.md) v0.4.

> **Status markers.** Examples that exercise surface that is **not yet
> built** are tagged `**[proposed]**`. Examples where the HTTP endpoint exists
> but the CLI subcommand does not are tagged `**[HTTP only, no CLI]**`.

---


## Walkthrough Indices

- [Shared Setup & Cross-Cutting Behaviors](setup.md)
- [Example A — Software delivery board walkthrough](software-delivery.md)
- [Example B — Shop floor / CNC board walkthrough](shop-floor.md)
