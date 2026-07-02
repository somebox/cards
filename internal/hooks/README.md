# Hooks Package

The `hooks` package handles system-level extensions and hook execution. It implements the work card extension model, specifically watching for events and scheduling subprocess hooks.

## Extension Supervisor

The `cards run-extensions` command spins up this supervisor. It reads extension declarations and monitors the internal event bus.

### Responsibilities:
- Read `definitions/extensions.{yaml,yml,json}`.
- Subscribe to the internal event bus.
- Run `kind: hook` extensions matching fired events. Spawns hook subprocesses with the event JSON piped on stdin.
- Log stdout/stderr of subprocesses to `.cards/logs/<extension_id>.log`.
- Isolate workspace execution securely.

See also: [Extensions Design & Reference](../../docs/extensions/EXTENSIONS.md)
