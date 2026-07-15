# Lifecycle schema — extension kinds, Autostart, RestartPolicy

ADR for Phase 5a (`card_abb13ae0`, sprint 2026-07-11). Authored **before**
the service supervisor (P5b) so the shared `config.Extension` vocabulary is
stable. Implementation of process lifecycle is out of scope here; this note
is the contract P5b/P5c must follow.

Companion: [`index.md`](../extensions/index.md),
[`reload.md`](reload.md) (supervisor ↔ generation provenance).

---

## Decision summary

1. **`autostart` is the enable-gate; `restart_policy` is the recovery shape.**
   They are not synonyms and do not collapse into one field.
2. **`restart_policy` is legal only on `kind: service`.** Present on
   `hook` or `run` → load-time rejection (fail-loud, same tier as P2b).
3. **`service` vs `run`:** long-running supervised process vs one-shot CLI
   invocation. Different lifetimes; do not unify them.
4. **The supervisor is deliberately bimodal.** Hooks get in-process bus
   delivery + subprocess-per-event. Services get pure process lifecycle and
   **no** event feeding — they dial `/v1/events/stream` as ordinary API
   clients. This split is intentional; do not "clean up" by unifying later.
5. **`expose` (port/protocol) stays parsed but unconsumed.** No reverse
   proxy, no port registry in this phase.
6. **Supported home for supervision:** `cards serve --run-extensions`
   (shared construction path with standalone `cards run-extensions`).

---

## Three kinds (lifetime contract)

| Kind | Lifetime | Who starts it | Event input |
|------|----------|---------------|-------------|
| `hook` | One-shot subprocess per matching event | Supervisor on bus match | Event JSON on stdin |
| `run` | One-shot subprocess | `cards do <id>` | CLI `--param` argv |
| `service` | Long-running child | Supervisor when `autostart: true` (after listener-ready) | None from supervisor — child dials HTTP/SSE itself |

`service` is the only kind the supervisor keeps alive across crashes.
`run` is never restarted by policy; invoke it again via `cards do`.
`hook` failures are at-most-once (logged, not retried by the core).

---

## Autostart × RestartPolicy

### `autostart` (bool, existing)

Enable-gate for `kind: service` only (meaningful semantics):

- `true` → supervisor should start the child once the HTTP listener is
  accepting (listener-ready gate is P5b).
- `false` / omitted → supervisor does not start it; operators may still run
  the same command under systemd/compose/by hand.

`autostart` on `hook` / `run` is ignored by the supervisor today and is not
a load-time error (bool zero-value is common). Prefer omitting it.

### `restart_policy` (string, additive omitempty)

JSON/YAML key: `restart_policy`. Allowed values (exact strings):

| Value | Meaning |
|-------|---------|
| `on-failure` | Restart only when the child exits non-zero or is killed by signal (not clean exit 0). |
| `always` | Restart on any exit, including 0. |
| `never` | Do not restart after the first exit. |

**Default when omitted on a service:** `on-failure` (applied by the
supervisor at runtime; the loaded struct may still show `""`).

**Load-time validation** (`internal/config.validateExtensions`):

- Unknown value → reject (`extension <id>: unknown restart_policy %q`).
- Non-empty value on `kind` other than `service` → reject
  (`extension <id>: restart_policy is only valid on kind:service`).

Empty / absent is always fine (including on hooks and runs).

### How the two compose

```
autostart=false  → never started by supervisor; restart_policy unused
autostart=true, restart_policy=never      → start once; stop after first exit
autostart=true, restart_policy=on-failure → start; restart only on failure
autostart=true, restart_policy=always     → start; restart on every exit
```

RestartPolicy never implies Autostart. Setting `restart_policy` without
`autostart: true` is valid JSON (policy is recorded) but the supervisor
still will not start the process until something else does — or until
reconcile/autostart rules in P5b/P5c say otherwise. Prefer pairing them
explicitly in declarations.

---

## Bounded backoff shape (supervisor defaults; not schema fields yet)

P5b implements restart delays with these **process-global defaults**
(not yet per-extension knobs — keep the schema small):

| Knob | Default (P5b) | Role |
|------|---------------|------|
| Initial delay | 200ms | Wait before first restart after a crash |
| Multiplier | 2× | Exponential growth of the delay |
| Max delay | 30s | Cap so crash-loops do not wait forever, but also do not pin CPU |
| Min healthy uptime | 10s | Uptime that must elapse before the backoff counter resets; a child that runs briefly then crashes escalates toward the cap instead of resetting every time |

Exact values are process-global defaults in `internal/hooks` (overridable in tests via `SetBackoff`). The schema contract
here is only: RestartPolicy selects *whether* to restart; backoff selects
*when*. Per-extension backoff fields are deferred until a real need appears.

Drain on stop/shutdown: SIGTERM → configurable grace (`SetDrainTimeout`, default 5s) → SIGKILL
process group (hooks procgroup helpers).

---

## Event-delivery boundary (bimodal supervisor)

**Intentional decision — do not unify.**

| Mode | Kind | Event path |
|------|------|------------|
| Bus + spawn | `hook` | Supervisor subscribes to the in-process bus; on match, spawns argv with event JSON on stdin |
| Lifecycle only | `service` | Supervisor starts/stops/restarts the process; it does **not** write events to the child's stdin or otherwise feed the bus into the child |

Services that need the event stream dial `GET /v1/events/stream` (with
`Last-Event-ID` on reconnect) as ordinary HTTP clients — same contract as
any external integrator ([`index.md`](../extensions/index.md),
[`integration.md`](../events/integration.md)).

Why bimodal stays:

- Hooks are reactive one-shots; bus delivery + stdin is the smallest
  contract and matches at-most-once spawn semantics.
- Services are long-lived peers of the API; feeding them from the bus
  would duplicate SSE, invent a second delivery protocol, and couple
  supervisor lifecycle to event fan-out.
- Extensions-over-plugins: a service crash must not require the core to
  "own" that service's event cursor.

A later cleanup that "makes services look like long-running hooks" would
be a contract break, not a refactor.

---

## `expose` — out of scope

`expose.port` / `expose.protocol` remain parsed and stored on
`config.Extension` / `config.Expose` and are **not consumed**. Phase 5 does
not open a reverse-proxy or port-registry door. Validation of expose values
is unchanged.

---

## Entry points

| Entry | Role |
|-------|------|
| `cards serve --run-extensions` | **Supported home** — HTTP listener + bimodal supervisor in one process |
| `cards run-extensions` | Standalone supervisor (hooks today; services once P5b shares construction) |

Both must share one supervisor-construction path (P5b). Reconcile-on-reload
(P5c) applies the identity key + decision table in [`reload.md`](reload.md)
§"Service reconcile-on-reload". Hook/run declarations remain frozen.

---

## Schema field shape (normative)

```go
// on config.Extension — additive omitempty
RestartPolicy string `json:"restart_policy,omitempty"`
```

Example declaration:

```yaml
- id: dropbox
  kind: service
  autostart: true
  restart_policy: on-failure
  run: ["node", ".cards/ext/dropbox.mjs"]
```

Rejected:

```yaml
- id: review-notify
  kind: hook
  on: status_changed
  restart_policy: on-failure   # load error: only valid on kind:service
  run: ["bash", ".cards/ext/notify.sh"]
```

---

## Status

| Piece | Status |
|-------|--------|
| Vocabulary + ADR | **[built]** this document |
| `config.Extension.RestartPolicy` + load validation | **[built]** with this card |
| Service process supervisor (listener-ready, backoff, drain) | **[built]** P5b (`internal/hooks` services + shared `cmd/cards` construction) |
| Reconcile-on-reload | **[built]** P5c — identity = extension id; fingerprint = run+env+cwd+restart_policy; decision table in [`reload.md`](reload.md); board-create reload ⇒ zero service churn |
| `expose` consumption | **[proposed]** deferred; door closed |
