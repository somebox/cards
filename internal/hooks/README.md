# Hooks Package

Bimodal extension supervisor (`kind:hook` + `kind:service`). See
[`LIFECYCLE-SCHEMA.md`](../../docs/architecture/LIFECYCLE-SCHEMA.md).

## Responsibilities

- **Hooks:** subscribe to the in-process event bus; on filter match, spawn
  `run[]` with event JSON on stdin (at-most-once).
- **Services:** when `autostart: true`, start after the listener-ready gate;
  restart per `restart_policy` with bounded backoff (min-healthy-uptime);
  drain with SIGTERM → grace → SIGKILL process group. No event feeding —
  children dial `/v1/events/stream` via `CARDS_URL`. After a successful
  workspace reload, `Reconcile` applies the decision table in
  [`RELOAD.md`](../../docs/architecture/RELOAD.md) (unchanged decls ⇒ zero
  churn).
- Log stdout/stderr under `.cards/logs/<extension_id>.log`.

Supported home: `cards serve --run-extensions` (shared construction with
standalone `cards run-extensions` via `cmd/cards/supervisor.go`).

See also: [Extensions Design & Reference](../../docs/extensions/index.md)
