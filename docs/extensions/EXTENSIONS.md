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
| `hook` | An event matches its filter | One-shot subprocess | Event JSON on stdin (supervisor bus delivery) |
| `service` | When `autostart: true` under `--run-extensions` **[built]**; otherwise started externally | Long-running supervised process | None from supervisor — dials HTTP/SSE itself |
| `run` | When called via `cards do <id>` | One-shot subprocess | Args from CLI |

All three communicate with the core via the same HTTP API. **Event delivery is
bimodal by design** (see [`LIFECYCLE-SCHEMA.md`](../architecture/LIFECYCLE-SCHEMA.md)):

- **Hooks** receive events from the in-process bus as stdin JSON
  (subprocess-per-event).
- **Services** are not fed events by the supervisor. A service that needs the
  stream dials `/v1/events/stream` (with `Last-Event-ID` replay) as an ordinary
  API client — same contract as any external integrator.

Do not unify these paths later; the split keeps long-running peers crash-isolated
from bus fan-out. (The `command` kind was renamed to `run` to avoid colliding
with the removed `command` field type — see `NOTES.md` D2/D18.)

## Declaration

Workspaces declare extensions in `definitions/extensions.{yaml,yml,json}`:

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
    restart_policy: on-failure
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

### Lifecycle fields (`autostart` × `restart_policy`)

Normative detail: [`LIFECYCLE-SCHEMA.md`](../architecture/LIFECYCLE-SCHEMA.md).

| Field | Applies to | Role |
|-------|------------|------|
| `autostart` | `service` (enable-gate) | Whether the supervisor should start the process after the HTTP listener is ready |
| `restart_policy` | `service` only | Recovery shape after exit: `on-failure` (default when omitted), `always`, or `never` |

`restart_policy` on `hook` or `run` is a **load-time rejection** (not a silent
no-op). Unknown values are rejected the same way. `expose` remains parsed but
unconsumed (no reverse proxy / port registry).

The core runs declared `hook` and `run` extensions when asked; `service`
supervision is **[built]** (P5b):

```bash
cards serve --run-extensions --workspace ./.work-cards   # supported home
cards run-extensions --workspace ./.work-cards           # standalone
```

`serve --run-extensions` binds the HTTP listener first, then starts the
supervisor so autostart services see an accepting port. Children receive
`CARDS_URL` (loopback base, e.g. `http://127.0.0.1:8787/v1`),
`CARDS_WORKSPACE`, and `CARDS_USER`. Restart uses `restart_policy` with
bounded backoff (min-healthy-uptime before streak reset). Shutdown is
SIGTERM → grace → SIGKILL of the process group.

On successful definition reload, the supervisor **reconciles** service
declarations (P5c): identity is the extension id; a change to run/args/env
(and cwd / restart_policy) is "same service, changed declaration" and
triggers drain+restart of that child only. Unchanged decls (including a
routine board-create reload) leave running services alone. Hook/run
declarations stay frozen for the supervisor's lifetime.

## Event contract for hooks

When a hook fires, the core spawns the configured command with:

- Event JSON on **stdin**.
- Environment variables: `CARDS_URL`, `CARDS_WORKSPACE`, `CARDS_USER`,
  `CARDS_EVENT_ID`, `CARDS_EVENT_TYPE`.
- Working directory: workspace root (unless `cwd:` is set in the declaration).

Event JSON shape:

```json
{
  "id": 4217,
  "type": "status_changed",
  "card_id": "card_auth_api",
  "actor": "coder-agent",
  "at": "2026-06-25T14:30:00Z",
  "workspace_id": "demo",
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
> implemented** (see [`ARCHITECTURE.md`](../architecture/ARCHITECTURE.md)'s planned
> integrations). Until it ships, substitute raw HTTP calls to the `/v1/*`
> API using `requests`/`httpx` (Python) or `fetch` (Node), as shown in
> [`INTEGRATION.md`](../events/INTEGRATION.md). **Example 5** uses only the CLI and is
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
be a one-file script. For detailed CLI commands, distribution patterns, and running the extension supervisor, refer to the [hooks package documentation](../../internal/hooks/README.md).

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
session (see [`MCP.md`](../extensions/MCP.md)). MCP is a transport for agents that prefer
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
| [`PHILOSOPHY.md`](../concepts/PHILOSOPHY.md) | Why the system stays small |
| [`SPEC.md`](../spec/SPEC.md) | Normative API and event types |
| [`ARCHITECTURE.md`](../architecture/ARCHITECTURE.md) | Go core, packaging, supervisor |
| [`LIFECYCLE-SCHEMA.md`](../architecture/LIFECYCLE-SCHEMA.md) | Autostart × RestartPolicy × kinds; bimodal supervisor |
| [`DEVELOPER-REFERENCE.md`](../reference/DEVELOPER-REFERENCE.md) | Schema authoring |
| [`LIFECYCLE-EXAMPLES.md`](../examples/LIFECYCLE-EXAMPLES.md) | End-to-end lifecycles |
