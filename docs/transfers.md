# Transfers: what an interruption costs

Two settings on an object-store destination decide what happens when the network
goes away, and how large an artifact can ever be. Both have defaults that work;
this is here for when they do not.

```yaml
destinations:
  main:
    type: s3
    bucket: backups
    region: eu-west-3
    retry_window: 2m       # default
    part_size_mib: 16      # default
    max_parts: 10000       # default without an endpoint; 1000 with one
```

Neither means anything for a `type: fs` destination, and setting them there is
refused rather than ignored — a setting that is accepted and never read tells
you your transfers are tuned when they are not.

## A backup cannot be resumed

This is the constraint everything else follows from.

The bytes come from `pg_dump`, streaming. Running `pg_dump` again produces a
*different* dump: different timestamps, different internal ordering, a different
snapshot. So no part already uploaded can ever be matched to a second attempt.
There is no offset to restart from, and there never will be for a logical
backup.

What a transfer can do is survive the interruption without giving up. What it
cannot do is pick up where it left off after the process has died.

(Physical backups may lift this later — a base backup is a file set, and a file
set can be resumed file by file. Nothing here forecloses it.)

## `retry_window`

How long an interruption a transfer rides out.

Left to the AWS SDK's own settings, a **five-second** outage was enough to end a
backup outright — measured, against a proxy that severed the connection
mid-upload. For a database that takes hours to dump, that is a five-second blip
costing a night's work.

The default window is two minutes. Measured against the same proxy, outages of
5 s, 30 s, 90 s and 110 s all recover; four minutes correctly fails.

Set it to `0` to fail on the first interruption. That is a real choice: an
endpoint failing for a reason retrying cannot fix is better failed fast, and
told about.

**The upper bound is 2m29s, and the reason is not obvious.** Giving up costs the
window *twice*: the cleanup that abandons the half-written upload goes through
the same retryer, so a 30-second window took 1 m 24 s to fail under a long
outage. Meanwhile a retry sends no bytes to storage, which is exactly what the
stall watcher (EF-095) ends a job for, at five minutes. A window past half of
that means the watcher kills the transfer the retry was about to rescue — and
blames the wrong actor while doing it. The configuration refuses it at load
time rather than letting you find out during an incident.

## `part_size_mib`

The size of each multipart chunk — and, less obviously, a ceiling on how large
one artifact can be.

The upload manager picks a part size for itself only when it knows the total
length, which it cannot when reading a pipe. So part size times part count fixes
the largest object that can ever be written — and the part count is not the same
everywhere. See `max_parts` below.

| `part_size_mib` | With 10000 parts (AWS) | With 1000 parts | Heap held by the storage layer |
|---|---|---|---|
| 8 | 80 GiB | 8 GiB | — |
| **16** (default) | **156 GiB** | **15.6 GiB** | — |
| 32 (maximum) | 312 GiB | 31 GiB | 228 MiB |

The maximum is 32, and it comes from memory rather than from S3. The upload
manager holds about six parts at once, so part size is a memory setting: 64 MiB
parts were measured at 452 MiB for the storage layer *alone*, which fits inside
ENF-001's 512 MiB budget only if nothing else in the process needs memory — and
the source, zstd and age all do.

If a dump outgrows the ceiling, the upload fails **at the last part**, after
everything before it has been sent. There is no way to see it coming: the length
is not known until the stream ends. Koffr says what to change:

```
koffr: upload "sources/prod/logical/01J.../dump.pgdump.zst.age": this artifact is
larger than 156 GiB, which is all 16 MiB parts can carry (S3 allows 10000 parts
and the size cannot change once an upload has started): raise part_size_mib on
this destination and run the backup again
```

A compressed logical dump above 156 GiB means a database in the terabytes. On a
provider capped at a thousand parts the same sentence reads 15.6 GiB, which is
an ordinary database. If you are near either figure, raise the settings before
you get there, not after.

## `max_parts`

How many parts one multipart upload may have. **The provider decides this, and
there is no way to ask it.**

- AWS S3 allows **10 000**.
- Scaleway Object Storage, and several other S3-compatible services, allow
  **1 000**.

Left unset, Koffr follows the only signal available: a destination with no
`endpoint` is AWS and gets 10 000; a destination with an `endpoint` is a service
whose limit cannot be known and gets 1 000. Raise it when yours allows more —
MinIO and Cloudflare R2 both take 10 000:

```yaml
    endpoint: http://minio.internal:9000
    max_parts: 10000
```

Koffr enforces the number rather than letting the service refuse part 1001. That
is deliberate: a provider that refuses does so in its own words, which Koffr
cannot translate, so you would get a raw S3 error at the end of an upload that
had already run for hours. Refusing it ourselves keeps the message the one
above, on every backend.

Being conservative by default costs an artificial failure with an actionable
message. Being optimistic costs a provider error nobody can act on. The first is
the better way to be wrong.

## What an interrupted job leaves behind

A job that fails cleanly abandons its own upload. A job that is *killed* cannot
— and what it leaves is billed and shows up in no listing at all. See
[Orphans](retention.md#orphans): `koffr prune --orphans` finds and releases
them, and a bucket lifecycle rule with `AbortIncompleteMultipartUpload` is worth
having as a second line of defence.
