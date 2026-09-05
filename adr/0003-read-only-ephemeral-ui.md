# ADR-0003 — Read-only ephemeral web UI, no user accounts

- **Status**: Accepted
- **Date**: 2026-09-05
- **Relates to**: PD-004, DEC-003, EF-120, EF-122, HP-005

## Context

A web UI makes backup state legible in a way a terminal does not. The obvious
implementation — a permanently running service with user accounts, sessions and
role-based access, as Databasus does — puts an authentication system in front of
a process that holds the credentials of every database it protects.

That means sessions, password hashing and reset, CSRF protection, rate limiting,
account lockout and an audit trail of logins: a large attack surface, built to
reimplement, less well, an authentication mechanism the operating system already
provides.

plakar takes the opposite approach: `plakar ui` starts an ephemeral server on
the loopback interface and stops when the browser tab closes.

## Decision

`koffr ui` starts an ephemeral, read-only server bound to `127.0.0.1`, opens the
browser with a single-use token, and exits when the session ends. There are no
user accounts, no persistent sessions and no RBAC. Remote access is `ssh -L`:
the authentication is SSH's.

Destructive operations — restore, prune, configuration changes — are CLI-only.

## Consequences

- The attack surface of the UI is a loopback listener with a single-use token.
- Access control, key management and audit are SSH's and the operating system's,
  which are already the trust boundary for anyone who can reach the Koffr host.
- No credential is ever exposed by the API, not even masked; a test enumerates
  every route and inspects full payloads to keep it that way.
- **The price**: no multi-user story, no per-team visibility, no "give the
  on-call engineer read access without a shell account". An operator who needs
  that must put `koffr serve` behind a reverse proxy that provides
  authentication.
- Anyone who can reach the loopback interface of the Koffr host can read backup
  metadata. On a shared host, that matters — which is why `koffr serve` is
  disabled by default and documented as needing a proxy.

## Alternatives rejected

- **Full application with accounts and RBAC** — the attack surface described
  above, for a tool holding database credentials.
- **Read-write UI with the configuration file still authoritative** — guarantees
  divergence between what the UI shows and what the file says (see ADR-0005).
- **No UI at all** — the CLI covers every need, but legibility at a glance is a
  real part of why operators trust a backup system.
