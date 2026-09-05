# Spikes — throwaway probe code (milestone M0)

Everything in this directory answered a question and is now kept only as
evidence of how the answer was obtained. It is excluded from the build, from the
linter and from the coverage requirement.

**None of this code is reused.** A probe that works is not a starting point: it
was written without tests, without error handling, and without the concurrency
invariants the real pipeline depends on. Reusing it would smuggle all three
omissions into the codebase.

The findings, with measurements and their consequences for the specification,
are in [`../docs/spikes/M0-report.md`](../docs/spikes/M0-report.md). **The
report is what matters; this directory is how it was produced.**

## Results

| Probe | Question | Answer |
|---|---|---|
| P-001 | Does `mariabackup --backup --stream=xbstream` write to the MariaDB host's disk? | No — zero bytes, at both 1.9 and 4.3 GiB |
| P-002 | Can `backup_manifest` be reconstructed by walking the tar in flight? | Yes, byte-identical to PostgreSQL's own |
| P-003 | Is streaming age encryption a throughput bottleneck? | No — 1314 MiB/s alone, 1621 MiB/s for the full chain |
| P-004 | Does an SSH tunnel break TLS verification or `.pgpass` resolution? | No, provided `.pgpass` is written after the tunnel is bound |
| P-005 | Does `pg_basebackup --incremental` accept a reconstructed parent manifest? | Yes, and the recombined cluster starts with correct data |
| P-006 | What are the limits of restoring a `-Fc` dump from stdin? | Only parallel restore; beware SIGPIPE on the decompressor |
| P-007 | What does `pg_basebackup --pgdata=-` do with several tablespaces? | Explicit refusal — needs a capability check at `Probe` |

## Running the rig

```sh
docker compose up -d pg17 mariadb11

docker compose exec -T pg17 psql -U postgres -d probe -v scale_mb=512 \
    -f /dev/stdin < seed/pg-seed.sql
docker compose exec -T mariadb11 mariadb -uroot -pprobe probe < seed/maria-seed.sql

# P-004 needs a bastion, a hidden database and throwaway key material.
./setup-keys.sh
docker compose --profile p004 up -d --build
```

Key material and probe output are generated, never committed: see
`setup-keys.sh` and the `.gitignore` entries.

## Contents

| Path | Probe |
|---|---|
| `p001-footprint.sh` | P-001 — measures local writes via `/proc/<pid>/io` |
| `p002/` | P-002, P-005, P-007 — walks a base backup tar |
| `build-manifest.py` | P-002, P-005 — reassembles PostgreSQL's manifest format |
| `p003/` | P-003 — pipeline throughput benchmark |
| `p004/` | P-004 — SSH tunnel over `x/crypto/ssh` |
| `seed/` | reference datasets |
