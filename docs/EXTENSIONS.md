# Extensions

Extensions are independent processes that add behavior to Work Cards. The core
exposes typed cards, events, and an HTTP API. Extensions consume those to
implement everything else: CI dispatchers, file watchers, report builders,
agent dispatchers, UI backends, integrations, custom validators.

Extensions are **not plugins**. The core does not load extension code.
Extensions can be written in any language and crash without affecting the
kernel.

**Status legend:** **[built]** available today · **[proposed]** planned but
not yet implemented. Individual sections are tagged below.

## Why this model

- **Language-agnostic.** Use Python for ML and data, Node for browser and
  HTTP, Go/Rust for performance-sensitive work, Bash for glue.
- **Crash-isolated.** A failing extension does not take down the core.
- **Composable.** Multiple extensions can coexist; each does one thing.
- **Inspectable.** Each extension has its own logs, process, and source.
- **Replaceable.** Fork or swap an extension without touching the kernel.

## Extension kinds

| Kind | When the core invokes it | Lifetime | Input |
|------|--------------------------|----------|-------|
| `hook` | An event matches its filter | One-shot subprocess | Event JSON on stdin |
| `service` | When started (autostart or manual) | Long-running | Reads its own API/SSE |
| `run` | When called via `cards do <id>` | One-shot subprocess | Args from CLI |

All three communicate with the core via the same HTTP API. Hooks and services
["can also subscribe to `/v1/events/stream` (with `Last-Event-ID` replay) if they
want richer event flow than a one-shot subprocess. (The `command` kind was
renamed to `run` to avoid colliding with the removed `command` field type —
see `NOTES.md` D2/D18.)

## Declaration

Workspaces declare extensions in `definitions/extensions.yaml` (or under an
`extensions:` key in `workspace.yaml`):

```yaml
extensions:
  - id: ci-dispatcher
    kind: hook
    description: Start CI builds when build cards reach `ready`
    on: status_changed
    filter:
      board_id: ci
      to_status: ready
    run: ["python", ".cards/ext/ci_dispatcher.py"]

  - id: dropbox
    kind: service
    description: Watch ./drop and create intake cards
    autostart: true
    run: ["node", ".cards/ext/dropbox.mjs"]

  - id: build-report
    kind: run
    description: Assemble research cards into a Markdown report
    run: ["python", ".cards/ext/report.py"]

  - id: web-ui
    kind: service
    description: Browser dashboard backend
    autostart: false
    run: ["node", ".cards/ext/web/server.mjs"]
    expose:
      port: 8788
      protocol: http
```

The core supervises declared extensions only when asked:

```bash
cards run-extensions --workspace ./.work-cards
```

Otherwise extensions can be supervised by systemd, docker compose, or by hand.
The core never requires its own supervisor.

## Event contract for hooks

When a hook fires, the core spawns the configured command with:

- Event JSON on **stdin**.
- Environment variables: `CARDS_URL`, `CARDS_WORKSPACE`, `CARDS_USER`,
  `CARDS_EVENT_ID`, `CARDS_EVENT_TYPE`.
- Working directory: workspace root (unless `cwd:` is set in the declaration).

Event JSON shape:

```json
{
  "id": "evt_01HXYZ",
  "type": "status_changed",
  "card_id": "card_auth_api",
  "actor": "coder-agent",
  "at": "2026-06-25T14:30:00Z",
  "workspace_id": "demo",
  "board_ids": ["engineering"],
  "diff": { "before": "in_progress", "after": "review" }
}
```

Hooks should be idempotent. Use `Idempotency-Key` headers on API writes so
retries are safe. A non-zero exit code marks the hook run as failed and is
logged, but does not roll back the originating event.

### Delivery guarantees and ordering

- **At-most-once by default.** A non-zero exit is logged, not retried. For
  critical paths (CI dispatch, deployments), use a `service` extension with
  its own retry/dead-letter logic, or have the hook enqueue durable work and
  return quickly.
- **Spawn is ordered, completion is not.** Hooks are spawned in event order,
  but run asynchronously and may complete out of order. If a workflow needs
  strict ordering, serialize through a single `service` consumer.
- **Idempotency is the hook's job.** The core does not redeliver; design hooks
  so the same event applied twice is harmless (e.g. guard on `commit_hash`
  or an idempotency key on the write-back).
- **Event replay for reconnect.** Services and hooks that subscribe to
  `/v1/events/stream` should send `Last-Event-ID` on reconnect to replay
  missed events; one-shot hooks receive only the triggering event.

## Worked examples

> **[proposed]** Examples 1, 2, 3, and 4 below use a planned `work_cards`
> (Python) / `@work-cards/client` (Node) convenience client that is **not yet
> implemented** (see [`ARCHITECTURE.md`](ARCHITECTURE.md)'s planned
> integrations). Until it ships, substitute raw HTTP calls to the `/v1/*`
> API using `requests`/`httpx` (Python) or `fetch` (Node), as shown in
> [`INTEGRATION.md`](INTEGRATION.md). **Example 5** uses only the CLI and is
> runnable today **[built]**.

### Example 1 — CI dispatcher (Python hook) `[proposed]`

`definitions/extensions.yaml`:

```yaml
- id: ci-dispatcher
  kind: hook
  on: status_changed
  filter:
    board_id: ci
    to_status: ready
  run: ["python", ".cards/ext/ci_dispatcher.py"]
```

`.cards/ext/ci_dispatcher.py`:

```python
import json, os, subprocess, sys
from work_cards import Cards

event = json.load(sys.stdin)
cards = Cards.connect(url=os.environ["CARDS_URL"])

card = cards.get(event["card_id"])
workflow = card["fields"]["workflow"]
ref = card["fields"]["ref"]

result = subprocess.run(
    ["gh", "workflow", "run", workflow, "--ref", ref],
    capture_output=True, text=True,
)

cards.append(card["id"], "ci_log", {
    "event": "dispatched",
    "workflow": workflow,
    "stdout": result.stdout,
    "stderr": result.stderr,
})
```

### Example 2 — File dropbox (Node service) `[proposed]`

`definitions/extensions.yaml`:

```yaml
- id: dropbox
  kind: service
  autostart: true
  run: ["node", ".cards/ext/dropbox.mjs"]
```

`.cards/ext/dropbox.mjs`:

```js
import { Cards } from "@work-cards/client";
import chokidar from "chokidar";
import { basename } from "node:path";

const cards = await Cards.connect();

chokidar.watch("./drop", { ignoreInitial: false }).on("add", async (path) => {
  await cards.create({
    type_id: "intake",
    title: basename(path),
    status: "queued",
    fields: { source_path: path },
  });
});
```

A second extension — a `hook` on `card_created` filtered to `type_id=intake`
— can then dispatch a per-type processor without the dropbox knowing anything
about the downstream work.

### Example 3 — Agent session dispatcher (Python hook) `[proposed]`

Spawning an agent session when a card reaches `ready` is a single hook.

```yaml
- id: agent-dispatcher
  kind: hook
  on: status_changed
  filter:
    board_id: engineering
    to_status: ready
  run: ["python", ".cards/ext/spawn_agent.py"]
```

`.cards/ext/spawn_agent.py`:

```python
import json, subprocess, sys
from work_cards import Cards

event = json.load(sys.stdin)
cards = Cards.connect()
card = cards.get(event["card_id"])

prompt = (
    f"Work on card {card['id']}: {card['title']}\n\n"
    f"{card['fields'].get('description', '')}"
)

subprocess.Popen([
    "pi", "--print",
    "--session", f".cards/sessions/{card['id']}.jsonl",
    prompt,
])

cards.add_comment(card["id"], body="Agent session started.")
```

The same pattern works for any harness: `claude`, `codex`, an `opencode`
binary, or your own.

### Example 4 — Research report builder (Python command) `[proposed]`

`definitions/extensions.yaml`:

```yaml
- id: build-report
  kind: run
  description: Assemble linked research cards into a Markdown report
  run: ["python", ".cards/ext/report.py"]
```

Invocation:

```bash
cards do build-report --param topic=card_topic_42 --param format=markdown
```

The core passes `--param` flags through to the declared command.

`.cards/ext/report.py`:

```python
import argparse
from work_cards import Cards

ap = argparse.ArgumentParser()
ap.add_argument("--param", action="append", default=[])
args = ap.parse_args()
params = dict(p.split("=", 1) for p in args.param)

cards = Cards.connect()
topic = cards.get(params["topic"])
sources = cards.list(filter={
    "has_link": {"$eq": "supports"},
    "link_target": {"$eq": topic["id"]},
})

print(f"# {topic['title']}\n")
print(topic["fields"]["hypothesis"])
print("\n## Sources\n")
for s in sources["items"]:
    print(f"- [{s['title']}]({s['fields']['url']})")
```

### Example 5 — Bash one-liner hook `[built]`

Not every extension needs a runtime. A status-change hook that pings Slack can
be a one-file script:

```bash
#!/usr/bin/env bash
event="$(cat)"
card_id=$(jq -r .card_id <<<"$event")
title=$(cards get "$card_id" --json | jq -r .title)
curl -sf "$SLACK_WEBHOOK" -d "{\"text\":\"$title moved to review\"}"
```

## CLI surface

- `cards extensions list` — list declared extensions and their kinds.
- `cards extensions show <id>` — show a single declaration.
- `cards run-extensions` — supervise hooks and start `autostart: true`
  services declared in the workspace.
- `cards do <id> [--param k=v ...]` — invoke a `run` extension.

## Distribution patterns

- **Workspace-local.** Extensions live in `.cards/ext/` and ship with the
  workspace (git-tracked). Most reproducible; recommended default.
- **System-installed.** Install with `pipx`, `npm i -g`, `cargo install`, then
  reference the binary in `run:`.
- **Subprocess-only.** No installation — just call `python script.py` or
  `bash script.sh` directly.

Extensions are not registered in any central index. The workspace is the
registry.

## Best practices

- Keep extensions **small and single-purpose**. One process per concern.
- Use **idempotency keys** on writes so retries are safe.
- Log to stdout/stderr; the supervisor captures both.
- Treat workspace files as the source of configuration; never store secrets
  there. Use environment variables passed via `env:` in the declaration.
- Prefer hooks over polling. Use `service` only when continuous attention is
  required (file watching, custom HTTP server, websocket bridge).
- For long-running work spawned by hooks, return quickly and let the worker
  run in the background; record progress by appending to the card.

## What about MCP?

MCP tools are generated by the core from card-type schemas, scoped per
session (see [`MCP.md`](MCP.md)). MCP is a transport for agents that prefer
typed tools to raw HTTP. **Extensions are about adding behavior** (things that
happen to cards and events), not about adding tools for agents to call. The
two mechanisms complement each other.

## Extension territory: structured-payload validation

The core field catalog is intentionally small (`string`, `text`, `number`,
`date`, `enum`, `tags`, `user`, `card_link`, `repeating`, `artifact` — see
`SPEC.md` §6). Validation of richer payloads is an extension's job:

- **JSON/YAML payloads:** store as `text`; an extension validates against its
  own schema and posts findings as a comment or a `repeating` entry.
- **Path confinement:** store arbitrary paths as `string`; an extension
  validates they stay under the workspace root before an `artifact` is created.
- **Command specs / execution:** the core never executes. An extension defines
  the execution contract (argv-array, no shell, env allowlist, `cwd` confined
  to the workspace, timeout) and reports results back onto the card.

This keeps the core small and the security surface where it belongs — in a
crash-isolated, replaceable process.

## Related documents

| Doc | Contents |
|-----|----------|
| [`PHILOSOPHY.md`](PHILOSOPHY.md) | Why the system stays small |
| [`SPEC.md`](SPEC.md) | Normative API and event types |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Go core, packaging, supervisor |
| [`DEVELOPER-REFERENCE.md`](DEVELOPER-REFERENCE.md) | Schema authoring |
| [`LIFECYCLE-EXAMPLES.md`](LIFECYCLE-EXAMPLES.md) | End-to-end lifecycles |
