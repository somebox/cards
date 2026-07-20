# Demo workspace

A frozen example Cards workspace used by the docs, the screenshots under
`media/`/`docs/assets/`, and the integration test
[`scripts/review-bot_test.sh`](../../scripts/review-bot_test.sh). Point a
server at it:

```bash
cards serve --workspace ./examples/demo-workspace --port 8787 --seed
open http://127.0.0.1:8787/ui/boards/engineering
```

`--seed` inserts demo users/cards into an empty DB. The live
`work-cards.db` is gitignored and machine-local; `backlog.jsonl` is the
committed, portable snapshot (`cards export --state-only` / `cards import`).

## Extensions

`definitions/extensions.json` declares two extensions that react to the
**same** event — a card reaching `review` on the engineering board — as an
intentional hook-vs-service illustration (see
[`docs/extensions/index.md`](../../docs/extensions/index.md)):

- **`review-notify` (hook)** — the core spawns `.cards/ext/notify.sh` per
  matching event with the event JSON on stdin.
- **`review-bot` (service)** — a supervised long-running worker
  (`.cards/ext/review-bot.mjs`) that dials `/v1/events/stream` itself, and on
  `status_changed → review` claims the oldest unowned review card via
  `POST /v1/cards/take-next` and comments as actor `review-bot`. Reconnects
  send `Last-Event-ID`, so a server restart mid-stream replays the gap.

Services start only under the supervisor:

```bash
cards serve --workspace ./examples/demo-workspace --run-extensions
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

## Automated proof

```bash
scripts/review-bot_test.sh    # or: go test ./cmd/cards -run TestReviewBotScript
```

copies this workspace to a temp dir, starts `cards serve --run-extensions`,
drives a card to `review`, and asserts the bot's comment (by author
`review-bot`), SSE resumption across a mid-stream kill/restart, and
supervisor stability. `REVIEW_BOT_SUBSCRIBE_TIMEOUT` (default 10s) tunes the
subscribe deadline for loaded machines.
