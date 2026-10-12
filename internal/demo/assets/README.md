# Cards demo workspace

A sample workspace to explore Cards with: an **engineering** board with six
card types and a review lane, a **welcome** board, the `jeeruh` theme, and two
example extensions. `cards demo` installed it here and filled it with sample
cards and users. Open it:

```bash
cards serve --workspace .
open http://127.0.0.1:8787/ui/boards/engineering
```

Everything is yours to change. The definitions under `definitions/` are plain
JSON — edit a card type or board and restart the server to see the model
change everywhere (web UI, CLI, MCP, API). The live database is
`work-cards.db`; `cards export --state-only` writes a portable snapshot of it.

When you are done, delete this directory. To start a real board in a project,
run `cards init` there instead.

## Extensions

`definitions/extensions.json` declares two extensions that react to the
**same** event — a card reaching `review` on the engineering board — as a
deliberate hook-vs-service illustration (see
[Extensions](https://somebox.github.io/cards/extensions/)):

- **`review-notify` (hook)** — the core spawns `.cards/ext/notify.sh` per
  matching event with the event JSON on stdin; it appends a line to
  `.cards/logs/notify.log`.
- **`review-bot` (service)** — a supervised long-running worker
  (`.cards/ext/review-bot.mjs`) that dials `/v1/events/stream` itself, and on
  `status_changed → review` claims the oldest unowned review card via
  `POST /v1/cards/take-next` and comments as actor `review-bot`. Reconnects
  send `Last-Event-ID`, so a server restart mid-stream replays the gap.

Services start only under the supervisor:

```bash
cards serve --workspace . --run-extensions
```

### Runtime requirement: Node

`review-bot` requires **Node.js on `PATH`** (stdlib `fetch` + a hand-rolled
SSE reader — no npm dependencies, nothing to install). The declared launcher
`.cards/ext/review-bot.sh` gates the runtime: on a Node-less machine it logs

```
node: not found — service review-bot skipped
```

and exits 0, so `restart_policy: on-failure` leaves the service down instead
of restart-looping. The hook extension is unaffected (it needs only `bash`).
