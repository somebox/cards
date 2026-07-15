# Users & auth

Two questions come up early and deserve straight answers: *what are users
for*, and *where's the login page*. Short version: users exist for
attribution, and there is no login page.

## What users are for

Users in Cards handle **ownership, authorship, and actor context** — not
identity, and not permissions:

- **Ownership** — `owner` is the assignment field. `claim` and `take-next`
  set it; `owner=me` filters on it.
- **Authorship** — every write records its actor: who created the card, who
  moved it, who commented, who appended the work-log entry. That's what makes
  the history reviewable.
- **Actor context** — `me` in filters resolves to the calling actor; boards
  can save "my open cards" filters that work for whoever is looking.

A user is a registered id with a kind:

```console
$ cards users register --id agent-claude --kind agent --display-name "Claude"
$ cards users register --id jeremy --kind human
```

The actor for a write comes from `CARDS_USER` (CLI/MCP), `--as` (CLI), or the
`X-Work-Cards-Actor` header (HTTP). `user`-typed schema fields must reference
a registered id, so attribution stays consistent.

## Agents are users

The `kind: agent` distinction exists because agents come and go. Give each
harness its own actor id (`agent-claude`, `agent-pi`, `ci-bot`) rather than
sharing one: the board then shows *which* agent did what, `take-next` assigns
work to the right worker, and when an agent is replaced its history remains
attributed. Registering a new agent is one command; nothing else to
provision.

## There is no auth, on purpose

Cards was not designed to be a public website. The default deployment is a
single process on `localhost`, serving one workspace, trusting its caller —
the same trust model as your shell. Declaring an actor is attribution, not
authentication: nothing stops a caller from claiming to be someone else, the
same way nothing stops a git commit with a borrowed author line. For a local,
single-tenant tool, adding accounts and permissions would add ceremony
without adding real security.

This is [philosophy #7](../concepts/PHILOSOPHY.md#7-local-and-trusted-by-default):
isolation belongs to the host.

## When you need more

The boundary is extensible — outside the core:

- **Share on a LAN / with a team** — put the server behind what you already
  trust: an SSH tunnel, a VPN (Tailscale works well), or a reverse proxy
  with HTTP basic auth or SSO in front of it. The proxy decides who gets in;
  Cards keeps recording who did what.
- **Multiple isolated groups** — run multiple workspaces, one process each.
  The isolation boundary is the process, which a host can enforce properly.
- **Auditing** — the event log already records every mutation with its
  actor; an [extension](../extensions/EXTENSIONS.md) subscribed to the event
  stream can ship that wherever your audit trail lives.

What Cards won't grow is a built-in account system, role model, or
permission matrix — a tracker that fits in your repo shouldn't need a user
database of its own.
