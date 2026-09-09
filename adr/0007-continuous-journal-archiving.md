# ADR-0007 — Continuous journal archiving: closed files, derived continuity, a bounded spool

- **Status**: Accepted
- **Date**: 2026-09-08
- **Relates to**: EF-031, EF-032, EF-063, EF-082, PD-001, PD-003, PD-006

## Context

A point-in-time recovery needs the database's own journal -- the binary log for
MariaDB, WAL for PostgreSQL -- archived continuously, so that a backup taken at
02:00 can be carried forward to 15:41. The journal is produced as files by the
server's own client (`mariadb-binlog --raw --stop-never`, `pg_receivewal`), which
writes into a directory; there is no pipe to stream it through, and the file
being written is incomplete until the server rotates to the next one.

Databasus has done this for PostgreSQL WAL and recorded what it learned. wal-g
does it for MySQL and stopped short of automatic recovery for MariaDB. Neither
has a MariaDB binary log archive; the shape of the problem is the same.

Two things in this design are architecture borrowed from Databasus's WAL
streamer and reimplemented, which is what this record exists to say.

## Decision

1. **Only closed files are archived.** A file is closed when the one after it
   exists; never judged by size. The file being written stays in the spool and
   never reaches the repository.
2. **Continuity is derived from the archive's names on every check, never
   stored.** A hole is reported once, deduplicated, and a recovery that would
   cross one is refused rather than replayed around it.
3. **The spool is bounded with hysteresis.** Above a high mark the receiver is
   stopped; it restarts only once the archiver has drained the spool below a
   low mark, a fifth of the high mark by default.
4. **The receiver is supervised, not merely restarted.** A run that lasted is
   an accident and is relaunched with a bounded backoff; several short runs in
   a row against a server that answers are a symptom, and the supervisor stops
   and says so. A server that cannot be reached at all counts against nothing.
5. **Recovery replays by file and position, and records GTID.** A fresh server
   has no GTID state to reconcile, so the position is exact and sufficient;
   the GTID travels in the manifest for the operator and for a future replica.
6. **Forced rotation is optional and off**, and rotates only when a transaction
   was committed since the last rotation, judged on the GTID.

## Consequences

**Easy.** Each archived file is one object: `age -d | zstd -d` gives back the
file the server wrote, and `mariadb-binlog` replays it -- PD-001 holds with no
tool of ours. The archive is the truth (ADR-0004): a listing says what is
archived, and the catalog has nothing to rebuild. A gap cannot hide, because
nothing remembers "intact". A killed archiver loses at most the index of one
object, which is rebuilt from the object.

**The price.** The spool is a complete artifact on the Koffr host's disk,
which PD-003 forbade without exception. It is now the one exception the
principle admits -- bounded, checked at load time, emptied as it goes -- and
the same exception M3b uses on the database host. A principle with an exception
attracts the next one; this record is where the next one has to argue.

A quiet database's newest changes sit in an open file that never fills. With
rotation off, that is the recovery point, and `koffr check` says how far back
it is rather than letting anyone assume. Turning rotation on costs the RELOAD
privilege and one near-empty file per interval while the server is written to.

Replay by time is cut at whole seconds by the tool, and `--stop-datetime` is
read in the client's time zone: the zone is pinned to UTC in the replay's
environment and in the generated procedure, because the first run of this
stopped two hours early on a host in Paris and said nothing.

## Alternatives rejected

- **Archive the file being written, under a key overwritten each time.** A
  mutable object in the repository, with a lifecycle and a merge on restore.
  Databasus considered and rejected the same thing (their ADR-0011); a
  conveyor that only moves finished pieces has nothing to reconcile.
- **Store "chain intact" in the catalog.** It can be wrong. The sequence of
  names cannot.
- **Restart the receiver on every exit, with backoff.** A revoked grant or a
  changed password then produces one alert a minute for a night. Databasus's
  supervisor draws the line between an accident and a symptom by uptime and
  by whether the server answers at all; so does this one.
- **Speak the replication protocol natively, as wal-g's binlog-server does.**
  No spool, no client binary -- and a large dependency on a path where a
  subtle mistake corrupts recoveries. The server's own client is the
  implementation that is always right about the format (PD-002), and EF-032
  names it.
- **Replay by GTID.** It is where wal-g gave up on MariaDB: reconciling
  `gtid_slave_pos` with a restored server's state. On a fresh server the
  position is exact and has nothing to reconcile. The GTID is kept for the day
  a replica is the target.
- **Rotate on a timer regardless of writes.** Archives an empty file every
  interval on a server that takes one write a day.
