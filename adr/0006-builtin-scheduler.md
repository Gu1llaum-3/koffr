# ADR-0006 — Built-in scheduler, with every job runnable one-shot

- **Status**: Accepted
- **Date**: 2026-09-05
- **Relates to**: DEC-006, EF-090, EF-091, EF-092

## Context

Scheduling can be delegated to cron or systemd timers, or built into a daemon.
Delegation keeps the tool small; a built-in scheduler keeps behaviour consistent
and makes state observable.

The deciding factor is what the tool must know that an external scheduler cannot
tell it: whether the previous run is still going, how retention interacts with
what just succeeded, whether a verification should follow, and when the next run
is due — the last one being needed by the status endpoint and the UI.

## Decision

A scheduler built into `koffr serve`, using extended cron syntax with
timezone support. **Every scheduled job is also runnable one-shot from the CLI**,
so an operator who prefers cron or systemd can disable the scheduler entirely
and lose nothing.

## Consequences

- One job at a time per source. A run that overruns its next window is a logged
  skip, never a pile-up — two concurrent `pg_basebackup` runs against one server
  is a self-inflicted outage.
- The daemon knows when the next run is due, which is what the status endpoint
  and the UI display.
- Retry policy, backoff and the stall detector live next to the scheduler, where
  the state they need already is.
- Both audiences are served without a fork in the codebase: the one-shot path is
  the same code the scheduler calls.
- **The price**: a long-running process to supervise, and a scheduler to get
  right — timezones, DST transitions, missed windows after downtime.

## Alternatives rejected

- **cron or systemd only** — no knowledge of concurrent runs, no next-run
  information, and every operator reimplements locking and retry in shell.
- **Scheduler as a separate binary** — an extra moving part with no benefit.
- **Kubernetes CronJob only** — ties the design to one deployment model, and
  Koffr must also run as a plain binary under systemd.
