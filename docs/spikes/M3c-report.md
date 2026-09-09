# M3c spike report — the binary log, from the server to a point in time

Run 2026-09-08, on the way to EF-032 and EF-082: archive a MariaDB binary log
continuously from a Koffr host that has no access to the database host, and
recover a database to the second before a mistake -- with Koffr, and without it.

**Summary: it works, and every one of the eight findings below was found by
running the real thing, not by a unit test.** Three of them would have restored
the wrong data while reporting success. The design borrowed from Databasus's
WAL streamer (closed files only, derived continuity, supervision that tells an
accident from a symptom) held without amendment; ADR-0007 records it.

| # | Question | Answer |
|---|---|---|
| P-301 | Is the file the server is writing ever safe to archive? | **No.** Judged by size it is; judged by its successor's existence it is not, and only the second is right. |
| P-302 | Does a byte position that moved mean the database was written to? | **No.** An idle MariaDB moves it anyway (checkpoint events). Rotation is judged on the GTID. |
| P-303 | Does `--stop-datetime` mean what the manifest says? | **Not on a host in Paris.** Read in local time; two hours short, silently. `TZ=UTC` is pinned. |
| P-304 | Does `--start-position` apply to the whole replay? | **To the first file only.** Handing an earlier file replays the tables' creation again. |
| P-305 | Does `--database` on the client send the replay where it says? | **No.** A `USE` in the stream wins; on the source's own server that is the live database. `--rewrite-db` does. |
| P-306 | Does the rewrite use the right name under `--target`? | **Not at first.** The CLI handed the target server's database as origin. Found by a namesake decoy. |
| P-307 | Does a daemon with no cron and one archiving source start? | **Not at first.** It refused for lack of a schedule while it had a receiver to run. |
| P-308 | Does `prune` decide the archive's fate? | **Not at first.** Planned, tested, and never called -- the M1 audit's defect, again. The linter said so. |

## Test rig

`mariadb:11.4` in containers on `linux/arm64`, one with `--log-bin` and
`--max-binlog-size=65536` so that files rotate every few hundred rows, one
without, plus a third bare `mariadb:11.4` image holding only `age`, `zstd` and
the server's own client for the PD-001 run. The client is `mariadb-binlog`
12.3 from Homebrew on the Koffr host, which is the mixed-version case an
operator is most likely to have.

Tests, all in the tree and all run on this rig:

| Test | What it proves |
|---|---|
| `internal/binlog` `TestSupervisor_*` | closed files only; survives the link being cut behind a severing proxy; stops on a crash loop and says so; pauses at the spool's high mark and resumes below the low one; rotates only when a GTID advanced |
| `internal/binlog` `TestPITR_*` | recovers to the second before a `DELETE`, under another name, next to a namesake that stays untouched; refuses to cross a hole; refuses a target before the backup |
| `test/e2e` `TestPointInTimeRecoveryWithoutKoffr` | the generated `RESTORE.md`, executed on the bare machine, crosses two files and stops at the second asked |
| mutation battery, 9 guards | each guard, removed or inverted, is caught by exactly the test written for it |
| `internal/binlog` `TestSupervisor_ResumesPastAPurgeAndSaysSo` | after a real `PURGE BINARY LOGS`, one `binlog.gap` and archiving resumes from the server's oldest file |
| `internal/binlog` `TestPITR_RefusesAPreambleTheTargetCannotRun` | a session variable the target lacks is a refusal before anything is restored |

## P-301 — closed files

A binary log file is complete when the server has opened the next one, and at
no other moment. The receiver writes the current file in place; its size
reaches `max_binlog_size` and then keeps growing by the rotation event, so any
size-based rule is wrong at least once per file. The archiver takes a file
only when a higher sequence number exists in the spool. The open file is
therefore never in the repository, and `koffr check` reports its size as
"not yet recoverable" rather than pretending.

## P-302 — an idle server moves its position

The first version of forced rotation compared the byte position between two
ticks. It rotated an idle server on every tick: MariaDB writes a
`Binlog_checkpoint` event into a log that nobody wrote to. The GTID
(`@@gtid_binlog_pos`) advances only on a committed transaction, so that is
what rotation is judged on. `TestSupervisor_RotatesOnlyWhenSomethingWasWritten`
holds the line.

## P-303 — the time zone

`mariadb-binlog --stop-datetime="2026-09-08 11:41:00"` is read in the client's
local zone. The events carry UTC seconds. The first PITR run on this machine
(Europe/Paris, UTC+2) stopped at 09:41 UTC without a word and left the target
short by everything written in between. The replay pins `TZ=UTC` in the
client's environment and formats the target in UTC; the generated `RESTORE.md`
prefixes the command with `TZ=UTC` and says why.

## P-304 — the first file only

`--start-position` applies to the first file given on the command line. The
first version of the PD-001 test handed every archived file, including one
from before the backup, and the replay failed on `Table 'orders' already
exists` -- a kind failure. Had the earlier file held only data, it would have
doubled rows and said nothing. `RESTORE.md` names the file to start from and
the test now follows it.

## P-305 and P-306 — where the replay lands

`mariadb --database=shop_copy` only sets a default; the stream carries
`USE shop` and every statement after it goes to `shop`. The replay passes
`--rewrite-db=shop->shop_copy` to `mariadb-binlog`, which rewrites both
statement and row events. The test puts a namesake `shop` with a marker row on
the target server and asserts the marker survives; removing the rewrite kills
the test (the namesake is emptied, the copy stays at 1000 rows).

The real run then failed anyway: under `--target lab`, the CLI passed the
target source's database as the origin of the rewrite, so nothing was rewritten
and the replay went to the target server's `shop` -- where the decoy stopped
it with `Table 'shop.orders' doesn't exist`. The origin is now the backup's
own source. A decoy that stops a wrong replay is cheap; put one on the target.

## P-307 — a daemon with a receiver to run

`koffr schedule` refused a configuration in which no source had a schedule,
which is right for a daemon with nothing to do and wrong for one with a binary
log to receive. It now starts when any source archives; the message names both
ways to give it work.

## P-308 — planned and never called

`prune` had a binlog plan, an apply step, six unit tests, and no caller. The
linter's `unused` finding is what said so. It is wired on the destination that
holds the archive, after the backups' fate is decided, because the floor is
the oldest backup kept (EF-063).

## The three verifications asked for before commit

Run 2026-09-08, after the état des lieux, on the user's request.

| # | Question | Answer |
|---|---|---|
| P-309 | Does the binlog leg hold on MariaDB 10.6 and 12.0? | **12.0 yes. 10.6 no, until it did:** the 12.3 client's preamble names a variable 10.6 lacks; the replay died after the dump was loaded. |
| P-310 | Does the receiver work through the SSH tunnel? | **Yes.** A server with no published port, reachable only through a bastion: receiver, dump, `check` and a recovery all went through it. |
| P-311 | What happens when the server purged the resume file? | **A crash loop, then a stop, and a message with no reason in it.** Now: one `binlog.gap`, and archiving resumes from the server's oldest file. |
| P-312 | Does the daemon stop cleanly when the receiver went through the tunnel? | **No.** "stopped" was logged and the process stayed: the tunnel's Close waited for a dump thread that never hangs up. Found under P-311, once the harness's ghosts were gone. |

### P-309 — a client newer than the target

`mariadb-binlog` prints `SET @@session.foreign_key_checks=1, …,
@@session.system_versioning_insert_history=0` before the first event. MariaDB
10.6 has no such variable, and the replay stopped at line 28 -- after the dump
had been restored, which is the wrong order to learn it in. A version
comparison would be the wrong check: the same 12.3 client works against 11.4.
The check is now the failure itself, tried first: the preamble is decoded from
the first planned file and run on the target through the same client and
session the replay will use; a refusal is `ErrReplayIncompatible`, naming the
variable, before anything is restored. On the 10.6 rig the recovery test now
records the refusal and skips; `make verify-mariadb-matrix` covers the binlog
tests and the PD-001 run on all three majors.

### P-310 — through the tunnel

Bastion built from `internal/executor/ssh/testdata/sshd`, database on a private
Docker network with no published port. `koffr check`, the receiver, two dumps,
and `restore --until` all reached it; sshd logged seven accepted connections,
and the recovery was exact (500 / 160 / no row 999).

### P-311 — the purge, and a lesson about the test harness

The receiver asked the server for a file it no longer had, exited at once,
and the supervisor -- correctly, by its own rule -- called five such exits a
crash loop and stopped. Two things were wrong with that. The stop message
carried no reason, because the stderr tail was read before the copy had
finished; both the supervisor and the replay now wait for it. And the stop
itself was wrong: the files are lost whether or not the receiver retries, and
every file the server still has is worth archiving. `startFile` now compares
the resume point with the server's oldest file and, when the server has moved
past it, reports the hole once and resumes from what is there. Tested end to
end against a real `PURGE BINARY LOGS`.

The verification also produced a false alarm worth recording. A receiver kept
surviving the daemon's SIGTERM and a fresh receiver kept dying a second after
starting. Both were the harness: the daemon was launched through a shell
function, so `$!` named the subshell, `kill` killed the subshell (exit 143)
and left four daemons from earlier runs alive, each restarting its receiver
after every `pkill`, each colliding with the new one on the same `server_id`.
Koffr's own child dies within 300 ms of the signal, measured. The lesson is in
the operator documentation: two instances archiving one server need distinct
`server_id`s, and what looks like a receiver that will not stay up may be a
neighbour you forgot.

### P-312 — a tunnel that would not close

`Forwarder.Close` stopped listening and waited for the connections in flight to
end. A connection ends when both sides close; a `--stop-never` dump thread on
the far side never does, so once the receiver had been killed the daemon's
shutdown waited on the server -- "stopped" in the log, the process still
there, `wait` never returning. `pg_dump` connections end by themselves, which
is why two milestones of tunnelled dumps never saw it. Close now cuts every
connection it still carries before waiting; the session that owns the tunnel
is closed after its client has exited or been killed, so nothing legitimate is
cut. `TestForward_CloseCutsAConnectionTheFarSideKeepsOpen` holds it, and the
mutation that stops cutting hangs the test.

It surfaced only after the ghosts of P-311 were gone: their duplicate
`server_id` made the server close the dump thread itself, which masked the
hang on every earlier stop. A finding hidden by an artefact of the harness is
the strongest argument for cleaning the harness before reading the results.

## Measurements

| What | Value |
|---|---|
| archive latency, file closed to object stored | 5 s tick, 1 s in the tests |
| receiver restart after a cut link | one backoff step, 1 s min, 60 s max |
| spool gate | stop above `spool_high`, restart below `spool_low` = high / 5 |
| replay on the real run | 1 file from `binlog.000007:379`, 60 orders back, the `DELETE` gone |
| PD-001 e2e, two files, bare machine | 11 s end to end |
| `TestSupervisor_*` + `TestPITR_*` | 14 s, two containers |

## What changed in the specification

PD-003 gains its third exception, the bounded spool. EF-031, EF-032, EF-063 and
EF-082 are marked delivered with their limits: recovery by time or by
`file:position`, not by GTID; rotation optional and off; a hole refuses. See
the dated amendments in the specification and ADR-0007.
