# Recovering a MariaDB database to a point in time

A backup is a moment. The binary log is everything that happened after it. With
both, a database can be brought back to any second in between -- to 15:41, just
before the `DELETE` at 15:42 -- rather than to last night.

## What it takes

**On the server**

```ini
[mariadb]
log_bin = binlog
server_id = 1
```

`log_bin` needs a restart. Without it nothing is archived and `koffr check` says
so. `expire_logs_days` (or `binlog_expire_logs_seconds`) decides how long the
server keeps its own files: it has to be longer than any outage you expect
Koffr to ride out, or a file will be gone before it was archived and the
archive will have a hole.

**For the backup account**

```sql
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'koffr'@'%';
GRANT RELOAD ON *.* TO 'koffr'@'%';   -- only if rotate_every is set
```

`RELOAD` is also what lets a backup record its own position (`--master-data`),
which is what a recovery starts from. Without it the backup still happens, and
`koffr show` shows no `binlog` line for it.

**In the configuration**

```yaml
binlog:
  spool_dir: /var/lib/koffr/binlog-spool   # under ReadWritePaths in the unit
  spool_high: 2G                            # default
  spool_low: 400M                           # default: a fifth of high

sources:
  shop:
    engine: mariadb
    binlog:
      enabled: true
      # destination: main       default: the source's first destination
      # server_id: 4242         default: derived from the source id, stable
      # rotate_every: 5m        default: off -- read the next section first
```

`koffr schedule` then runs a receiver for every source that archives.

## The recovery point of a quiet database

The server's client hands over a binary log file only once the server has moved
on to the next one. A busy database rotates every few minutes. A quiet one --
ten rows an hour -- keeps its open file for hours or days, and everything in it
is **not yet recoverable**.

`koffr check` says how much:

```
ok  binlog  shop  on, writing binlog.000042 @ 1048576; archived through binlog.000041;
                 1 file(s) / 1.0 MiB not yet recoverable; rotation off, so a quiet
                 database's newest changes stay unrecoverable until its file fills
```

`rotate_every: 5m` asks the server to close its file every five minutes, but
only if a transaction was committed since the last time -- an idle server is
never rotated. It costs `RELOAD`, and it is **off by default** because it
touches the server. Decide with the figure above in view.

## Recovering

```sh
koffr restore 01M1... --into shop --until 2026-09-08T15:41:00Z
```

Koffr restores the backup, then replays every archived file from the backup's
position to the target. Before a single event is replayed it checks that no
file is missing between the two: **a hole is a refusal**, never a recovery that
stops short and says "restored".

The target is an RFC 3339 time, cut at whole seconds, or an exact
`<file>:<position>` when a second is not fine enough:

```sh
koffr restore 01M1... --into shop --until binlog.000042:198734
```

## Without Koffr

Every archived file is `age` + `zstd` around the file the server wrote. The
generated `RESTORE.md` of a backup taken with the log on ends with the two
commands: decrypt the files from the backup's own onwards, then

```sh
TZ=UTC mariadb-binlog --start-position=<pos> <files...> --stop-datetime="2026-09-08 15:41:00" \
  | mariadb --defaults-file=restore.cnf --protocol=TCP
```

`TZ=UTC` is not decoration. `mariadb-binlog` reads the target in the client's
local time zone, and a client two hours from UTC stops two hours early without
a word. The first run of Koffr's own replay did exactly that.

## The client has to be able to talk to the target

`mariadb-binlog` decodes events into SQL, and it opens that SQL with a `SET
@@session.…` line naming every variable **it** knows. A client newer than the
target server names one the server does not have -- a 12.3 client against a
10.6 server sets `system_versioning_insert_history`, and the replay dies on
the first statement.

Koffr tries that preamble on the target **before restoring anything**, and
refuses with the variable's name and nothing changed on the server:

```
restore: this mariadb-binlog produces SQL the target cannot run: … Unknown system
variable 'system_versioning_insert_history'. The mariadb-binlog on this host is
newer than the target server; point bin_dir at a client of the target's version
and run the recovery again. Nothing was restored
```

The rule of thumb is the same as for `pg_dump`, reversed: **replay with a
client of the target's version.** A client of the same major is always safe; a
newer one may work, and Koffr checks rather than guesses.

## Two Koffr hosts, one server

The receiver registers with the server as a replica, under a `server_id`
derived from the source id. Two Koffr instances archiving the same server under
the same source id present the same `server_id`, and the server disconnects
one of them every time the other connects -- what you see is a receiver that
exits a second after starting, over and over. Give each instance its own
`binlog.server_id`, or archive from one host only.

## When the server purged before Koffr archived

An outage longer than `expire_logs_days` leaves the server without the file
the archive would resume from. Koffr does not sit in a crash loop asking for
it: it reports the hole once (`binlog.gap`, naming the files that are gone and
the one it resumes from), and archives everything the server still has. The
files are lost either way; the recoveries that need them are refused
(EF-063), and the next backup starts a whole chain again.

## Retention

A backup is only worth keeping if the log from its position onwards is kept
too. `koffr prune` never deletes an archived file at or after the position of
the oldest backup it keeps -- and if any kept backup has no position, it
deletes none at all. The archive outlives any one backup on purpose: the files
written since the last backup are the only copy of what the next one will need.

## What can go wrong, and what you will see

| Event | Notification | Meaning |
|---|---|---|
| `binlog.gap` (warning) | a file is missing from the archive | the server purged it before it was archived; recoveries cannot cross it. Check `expire_logs_days` |
| `binlog.crashloop` (error) | the receiver died several times at once against a server that answers | a changed password, a revoked grant. Nothing retries; fix and restart |
| link down | nothing | the receiver restarts by itself; the server keeps the files |

## What this is not

Not GTID-based recovery onto an existing replica. Koffr replays by file and
position onto a **fresh** server, which is exact and has nothing to reconcile;
the GTID is recorded in the manifest for the day a replica is the target.
