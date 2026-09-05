# M0 spike report

Seven probes, run 2026-09-05, to confirm the architecture holds or to amend it
while amending is still cheap.

**Summary: the architecture holds.** Six probes returned their best case. One,
P-007, found a real restriction that needs a specification amendment and a
capability check. One unplanned finding forces a Go version bump.

| Probe | Question | Answer |
|---|---|---|
| P-001 | Does `mariabackup --stream=xbstream` write to the database host's disk? | **No. Zero bytes.** |
| P-002 | Can `backup_manifest` be reconstructed by walking the tar? | **Yes, byte-identical.** |
| P-003 | Is streaming age encryption a bottleneck? | **No, 1314 MiB/s.** |
| P-004 | Does an SSH tunnel break TLS verification or `.pgpass`? | **No, with one ordering constraint.** |
| P-005 | Does `--incremental` accept a reconstructed manifest? | **Yes, and the result restores correctly.** |
| P-006 | What are the limits of restoring a `-Fc` dump from a pipe? | **Only parallel restore. Plus a SIGPIPE trap.** |
| P-007 | What happens with several tablespaces on stdout? | **Explicit refusal. Needs a capability check.** |

## Test rig

`spikes/docker-compose.yml`. PostgreSQL 17.11, MariaDB 11.8.9, on
`linux/arm64` (Apple Silicon, 10 cores, 24 GiB).

Reference dataset, roughly 60% incompressible and 40% highly compressible, so
that throughput and ratio figures mean something: purely random data gives a
pessimistic figure, purely repetitive text an unreachable one.

- PostgreSQL: 702 MB logical / 1.6 GB data directory. 78,643 rows of 4 KiB
  random `bytea` with `STORAGE EXTERNAL`, 1,073,766 narrow repetitive rows, plus
  100 small tables to give the manifest a realistic entry count.
- MariaDB: 1.9 GB then 4.3 GB data directory, same composition.

**Deviation from plan.** The plan called for ~5 GiB per engine. The probes ran
at 0.7–4.3 GB. Every question here is about correctness or about a ratio, and
P-001 — the only size-sensitive one — was run at two sizes specifically so a
constant footprint could be told apart from a proportional one.

---

## P-001 — `mariabackup --stream=xbstream` local footprint

**Question**: does it write to the MariaDB host's disk, and how much? (OPEN-004)

**Why it matters**: PD-003 claims no backup writes an artifact to the database
host. If the footprint grew with the database, that claim would be false for
MariaDB physical backups and the specification would have to say so.

### Method

Sampling directory sizes proved useless on the first attempt: the backup
finished in three samples, and a transient file can slip between two of them.
The authoritative figure is `/proc/<pid>/io`:

- `wchar` — every byte passed to `write()`, the stream to `/dev/null` included;
- `write_bytes` — bytes actually sent to storage, which is the local footprint.

The gap between the two is exactly what streaming buys.

### Measurements

| Run | Data directory | `wchar` (streamed) | `write_bytes` (disk) | Peak `--target-dir` | Peak `--tmpdir` |
|---|---|---|---|---|---|
| A | 1945 MiB | 1079 MiB | **0 B** | 0 B | 0 B |
| B | 4312 MiB | 2502 MiB | **0 B** | 0 B | 0 B |

Both runs ended `completed OK!`.

### Answer

**Zero bytes reach local storage.** The footprint is not merely constant, it is
nil: the streamed volume grew by 2.3× between runs while local writes stayed at
zero.

**Impact**: OPEN-004 is closed. PD-003 needs no amendment, and CT-002 needs no
free-space warning for the backup path. `--prepare` at *restore* time still
requires the uncompressed backup on disk (CT-005), which is unchanged.

---

## P-002 — Reconstructing `backup_manifest`

**Question**: is a manifest reconstructed by walking the tar identical to the
one PostgreSQL produces? (EF-013)

**Why it matters**: server-side compression stops `pg_basebackup` from injecting
the manifest, and `--incremental` needs the parent's manifest. Without reliable
reconstruction, EF-014 falls.

### Method

Comparing two separate backup runs would be meaningless: volatile files differ
between any two runs. Instead, a single run wrote **both** `base.tar` and its
own `backup_manifest`; the walk of that tar was compared against that manifest.

A ~90-line Go program (`spikes/p002`) reads the tar from stdin with
`archive/tar` and emits path, size, SHA-256 and mtime per regular file.
`spikes/build-manifest.py` reassembles PostgreSQL's exact writer format.

### Findings

**1. The reconstruction is byte-identical.** 336 KiB, 1680 entries, `cmp` clean
against PostgreSQL's own file — including the `Manifest-Checksum`, which proves
every preceding byte matches.

The checksum is SHA-256 of every byte preceding the `"Manifest-Checksum"` key,
determined empirically. The file ends with a newline after the closing brace.

**2. One exclusion rule, and it is narrow.** WAL *segment* files carried in the
tar are absent from the manifest — `WAL-Ranges` covers them instead. Everything
else under `pg_wal/`, `archive_status/*.done` included, **is** present. A first
walk showed 1678 entries against 1677: the single difference was
`pg_wal/000000010000000000000035`.

**3. Server-side compression does not alter tar content.** Comparing an
uncompressed directory backup against a `server-zstd` stream to stdout, taken
back to back: 1681 entries each, and only three differences — the current WAL
segment, `backup_label` (different LSN) and `global/pg_control` (different
checkpoint). **Zero relation files differ.**

**4. Files above 8 GiB round-trip correctly.** USTAR encodes size in a 12-byte
octal field that caps at 8 GiB. A 9 GiB file (9,663,676,416 B) was placed in
`PGDATA`: size and SHA-256 matched the manifest exactly. Go's `archive/tar`
reads PostgreSQL's encoding without help.

**5. Long filenames are refused by PostgreSQL itself, not by us.** A 185-character
filename in `PGDATA` aborts the backup:

```
pg_basebackup: error: backup failed: ERROR:  file name too long for tar format: "aaaa...conf"
```

PostgreSQL emits strict USTAR (magic `ustar\0` + `00`) and does not fall back to
PAX or GNU extensions. The plan treated long tar headers as a risk for our
walker; it is not, because the case cannot reach us. It is, however, a real
operational failure: any user file with a long name in `PGDATA` — a certificate,
an included configuration file — breaks tar-format backups entirely. It is not
detectable remotely, so it surfaces at runtime with that clear message.

**6. Do not trust Go's `Header.Format`.** It reported `<unknown>` for all 1681
entries although the archive is strict USTAR. Cosmetic — every checksum matched
— but the field must not be used for logic.

### Answer

**Yes, exactly.** EF-013 confirmed. Compression code should never rely on
`Header.Format`, and the reconstruction must skip `pg_wal/<24 hex chars>`
entries.

---

## P-003 — age throughput

**Question**: is streaming age encryption a bottleneck? (EF-050, ENF-003)

### Method

4 GiB pushed through each stage (`spikes/p003`), on the 60/40 mix. The specific
hypothesis under test: age's STREAM works in 64 KiB chunks, so writes smaller
than a chunk might multiply calls for nothing — hence each encrypting case runs
twice, direct and behind a 1 MiB `bufio.Writer`. Encryption is for **two**
recipients, as the real configuration always is.

### Measurements

| Case | MiB/s | out/in |
|---|---|---|
| Raw copy (ceiling) | 47,624 | 1.000 |
| zstd level 1 | 4,914 | 0.600 |
| zstd level 3 | 4,813 | 0.600 |
| zstd level 9 | 2,745 | 0.600 |
| **age only** | **1,314** | 1.000 |
| age only + 1 MiB bufio | 1,297 | 1.000 |
| **zstd3 → age (real chain)** | **1,621** | 0.600 |
| zstd3 → 1 MiB bufio → age | 1,519 | 0.600 |

### Answer

**Not a bottleneck.** age alone sustains 1314 MiB/s — about 10 Gb/s — far above
any realistic network or source disk. The full chain is *faster* than age alone
because compression removes 40% of the bytes before they reach it.

**The buffering hypothesis is wrong.** A 1 MiB buffer changes nothing on age
alone (−1%) and makes the full chain slightly worse (−6%): zstd already emits
large blocks. **Do not add buffering to the pipeline for this reason.**

Two caveats on the figures. `klauspost/compress` parallelises across
`GOMAXPROCS`, so the zstd rows are multi-threaded while age is single-threaded;
and these are Apple Silicon numbers, so a Linux x86 server will differ. Neither
changes the conclusion, which rests on an order of magnitude.

---

## P-004 — SSH tunnel and libpq

**Question**: does redirecting libpq through a tunnel break TLS verification or
`.pgpass` resolution? (EF-002, EF-007)

**Why it matters**: this is what Koffr offers over wal-g. If the redirection
forced TLS verification off, we would be trading a weakness for a feature, which
PD-004 forbids.

### Method

Topology matching production: `pg-hidden` publishes no port and sits on an
`internal` network — the client container cannot even resolve its name. A
bastion straddles both networks. A Go program (`spikes/p004`, `x/crypto/ssh`)
opens a `direct-tcpip` channel and serves a listener on `127.0.0.1:0`. All of it
runs from a client container attached to the public network only, which is
exactly where Koffr sits.

### Result

```
port local : 34767
pg_dump -h pg-hidden -p 34767  (PGHOSTADDR=127.0.0.1, PGSSLMODE=verify-full)
exit=0
```

Three negative controls, because a passing test proves nothing unless failure is
also possible:

| Control | Result |
|---|---|
| Wrong CA | Fails — `SSL error: certificate verify failed` |
| Wrong `-h` value | Fails — `server certificate for "pg-hidden" does not match host name "wrong-name"` |
| Wrong port in `.pgpass` | Fails — `fe_sendauth: no password supplied` |

### Answer

**It works, and verification stays real.** Control B is the important one: libpq
verifies the certificate against the **`-h` value**, not against `PGHOSTADDR`.
The real hostname keeps working for verification while the connection is
redirected.

**One ordering constraint, from control C.** `.pgpass` matches on host **and
port**. The tunnel port is chosen by the kernel and unknown until the tunnel is
up, so **the password file must be written after the listener is bound**. Any
implementation that writes credentials before opening the tunnel will fail with
a misleading "no password supplied".

---

## P-005 — Incremental backup on a reconstructed manifest

**Question**: does `pg_basebackup --incremental` accept the manifest rebuilt in
P-002? (EF-014)

**Why it matters**: the plan flagged a third outcome worse than rejection —
accepted but semantically wrong, which is only discovered on the day of the
restore.

### Method

Full streamed backup → manifest reconstructed from the stream → 107,376 rows
updated → incremental against that manifest → `pg_combinebackup` → start the
cluster and count.

### Result

| Step | Outcome |
|---|---|
| Incremental accepted | Yes, `base backup completed` |
| Size | **17.7 MB** against 343 MB for the full backup |
| `pg_combinebackup` | Succeeds, 767 MB combined |
| Cluster starts | Yes |
| Modified rows | **107,376** — exactly the `UPDATE` count |
| `probe_wide` / `probe_random` / small tables | 1,073,766 / 78,643 / 100 |

### Answer

**Best case.** EF-014 confirmed end to end, and the backup is genuinely
incremental at 5% of the full. The P-002 fallback — client-side compression to
keep PostgreSQL's own manifest — is not needed.

---

## P-006 — `pg_restore` from a pipe

**Question**: what are the limits of restoring a `-Fc` dump through a pipe?
(EF-018, EF-084)

### Results

| Mode | Outcome |
|---|---|
| `zstd -dc \| pg_restore -d db` | **Works.** `pg_restore` exit 0, all rows restored |
| `zstd -dc \| pg_restore -l` | **Works** — contrary to the plan's expectation |
| `zstd -dc \| pg_restore -j 4` | Fails: `parallel restore from standard input is not supported` |

**A trap worth the probe on its own.** With exit codes isolated:

```
pg_restore=0  zstd=141
```

141 is 128+13, SIGPIPE. `pg_restore` stops reading at the archive's end marker,
so the decompressor is killed while still writing. **A `RESTORE.md` using
`set -o pipefail` would report a successful restore as a failure.**

### Answer

Only parallel restore is unavailable, and the table of contents works from a
pipe after all. The generated `RESTORE.md` (EF-084) must not use `pipefail`, or
must explicitly tolerate 141 from the decompressor, and must document
decompressing to a file when parallel restore is wanted.

---

## P-007 — Several tablespaces on stdout

**Question**: what does `pg_basebackup --pgdata=-` do on a cluster with a
non-default tablespace? (EF-010, EF-005)

### Result

```
pg_basebackup: error: can only write single tablespace to stdout, database has 2
```

The middle outcome of the three the plan anticipated: an explicit refusal, not
silent truncation. **But the output file was created — 0 bytes — before the
error.** A naive implementation would store an empty object as a backup.

Detection is trivial and works remotely:

```sql
SELECT count(*) FROM pg_tablespace
WHERE spcname NOT IN ('pg_default', 'pg_global');
```

It returned 1 with the tablespace present and 0 after dropping it.

### Answer

**A real restriction: streamed physical backup only covers single-tablespace
clusters.** Amendments required:

- **EF-010** — state the restriction.
- **EF-005** — `Probe` runs the query above and refuses the configuration at
  load time (PD-006), with logical backup offered as the documented fallback.
- **ENF-010** — no object is created before the source stream has produced its
  first byte, so a failure at start cannot leave an empty artifact behind.

---

## Unplanned finding — the project needs Go 1.26

Adding `golang.org/x/crypto` for P-004 pulled v0.56.0, which requires
**Go 1.26**. The spikes module was bumped automatically.

`x/crypto/ssh` is not optional: it is how EF-002, EF-003 and EX-001 are
implemented. Pinning an older `x/crypto` to stay on Go 1.24 would mean freezing a
cryptographic library, which is the wrong trade for a tool holding database
credentials.

**Action**: move `go.mod`, the CI workflow, the Makefile and `CLAUDE.md` from
1.24 to 1.26 before M1 starts.

---

## Specification changes

| Reference | Change | Source |
|---|---|---|
| OPEN-004 | Closed: zero local footprint | P-001 |
| PD-003 | Unchanged, now evidenced | P-001 |
| EF-013 | Confirmed; add the `pg_wal/<segment>` exclusion rule | P-002 |
| EF-011 | Confirmed: server-side compression does not alter content | P-002 |
| EF-014 | Confirmed end to end | P-005 |
| EF-050 | Confirmed; **no buffering** in the pipeline | P-003 |
| EF-002, EF-007 | Confirmed; `.pgpass` written **after** the tunnel is bound | P-004 |
| EF-084 | `RESTORE.md` must not use `pipefail` | P-006 |
| **EF-010** | **Add**: streamed physical backup requires a single tablespace | **P-007** |
| **EF-005** | **Add**: tablespace count checked at `Probe` | **P-007** |
| **ENF-010** | **Add**: no object created before the first byte of the stream | **P-007** |
| ENF-030 | Go 1.24 → 1.26 | `x/crypto` |

Two failure modes are not detectable at configuration time and surface at
runtime with a clear message: a long filename in `PGDATA` (P-002), and a
tablespace added after validation (P-007, mitigated by re-probing).
