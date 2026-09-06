# M1 spike report — interruption, and what a multipart upload costs

Four probes, run 2026-09-06, to answer one question left open by EF-041:
what does "resume after a network interruption" mean for a backup whose bytes
come from a pipe?

**Summary: it cannot mean resuming.** The stream comes from `pg_dump`, and a
second `pg_dump` produces different bytes, so parts already uploaded can never
be matched to a second attempt. What an interrupted transfer can do is ride the
interruption out, and what an interrupted job leaves behind can be found and
released. Both were broken; both now work. The requirement is amended below.

| Probe | Question | Answer |
|---|---|---|
| P-101 | Does a killed job leave anything on the object store? | **Yes, and no listing shows it.** |
| P-102 | How long an outage does an upload survive? | **Five seconds.** |
| P-103 | Do MinIO and AWS answer `ListMultipartUploads` the same way? | **No. It is not even scoped to the bucket.** |
| P-104 | What is the largest artifact that can be written? | **80 GiB on AWS, 8 GiB elsewhere, and the refusal arrives last.** |

## Test rig

MinIO `RELEASE.2025-04-22T22-12-26Z` and PostgreSQL 17 in containers on
`linux/arm64` (Apple Silicon). Probe code, all throwaway:

| Probe code | What it asks |
|---|---|
| `spikes/mpu` | does an abandoned upload survive, and who can see it |
| `spikes/netcut` | how long an outage an upload survives, behind a severing proxy |
| `spikes/mpupfx` | is `Prefix` honoured on `ListMultipartUploads` |
| `spikes/mpuabort` | what is returned for aborting an upload that is gone |
| `spikes/mpupage` | is `MaxUploads` honoured, and what does paging cost |
| `spikes/mpuscope` | is the listing scoped to the bucket it names |
| `spikes/mpudiff` | is an upload with no parts abandoned like any other |

---

## P-101 — what a killed job leaves behind

**Question**: a job killed mid-transfer cannot run its own cleanup. Does the
service keep anything, and can an operator see it? (EF-041, ENF-010)

**Why it matters**: `retention.FindOrphans` finds objects a dead job left with no
manifest, and it works by listing the repository. If interruption also leaves
something a listing does not show, then a repository can be tidied and still be
paid for, and nothing in Koffr would say so.

### Method

Two ways round. First, killing a real `koffr backup` mid-transfer -- which
turned out to be hard to time: the first two attempts killed a job that had
already finished, and a watcher polling from the host cost more per sample than
the whole upload took. Second, and conclusively, a probe that creates a
multipart upload, sends one 8 MiB part, and exits without completing or
aborting: exactly the state a SIGKILL leaves, without the race.

### Measurements

```
  8 MiB envoyes, upload id NThiZDFkODYt...
  ListObjectsV2 (ce que voit l'operateur) : 8 objets     <- unchanged
  ListMultipartUploads (ce que facture le service) : 1
    sources/shop/logical/01ABANDONED/dump.pgdump.zst.age
```

A clean failure is different: the upload manager aborts on its way out
(`LeavePartsOnError` defaults to false), and the probe confirmed nothing is left
behind when a transfer fails rather than being killed.

**Answer**: a killed job leaves a multipart upload that is stored, billed, and
absent from every listing -- so invisible to `koffr ls` and to the orphan sweep
alike.

**Impact**: new `storage.MultipartMaintainer`, implemented by `storage/s3` and
not by `storage/fs`, which has nothing to leak.
`retention.FindIncompleteUploadsOlderThan` applies the same 24-hour grace period
the orphan sweep uses, for the same reason: from outside, an upload in flight
and one abandoned last month are the same thing, and aborting the wrong one
kills a running backup. `koffr prune --orphans` reports and releases them.

---

## P-102 — how long an outage a transfer survives

**Question**: with no configured retry policy, what interruption is enough to
end a backup? (EF-041, ENF-095)

**Why it matters**: there is no resuming. Whatever a transfer does not survive,
it restarts from nothing -- and for a large database "from nothing" is hours.

### Method

`spikes/netcut`: a TCP proxy in front of MinIO, severing every connection for a
chosen span mid-upload, then letting traffic flow again. The first version
proved nothing: it refused new connections only, and the SDK reuses pooled ones,
so an upload in flight sailed through a 90-second "outage" untouched. Severing
had to tear down live connections as well.

### Measurements

| Setting | Outage | Result |
|---|---|---|
| SDK default | 5 s | **failed** after 8 s |
| SDK default | 30 s | **failed** after 10 s |
| 10 attempts, 30 s cap | 30 s | recovered in 44 s |
| 10 attempts, 30 s cap | 90 s | recovered in 1 m 51 s |

A first attempt at deriving attempts from a window failed its own test: a window
advertised as two minutes survived 30 seconds and failed at 90. The cause is the
SDK's exponential backoff *with jitter* -- each delay is drawn between zero and
the cap, so a count of attempts does not convert into a span of time. With a
fixed interval the arithmetic is exact:

| `retry_window` | Outage | Result |
|---|---|---|
| 2 m | 5 s | recovered in 14.6 s |
| 2 m | 30 s | recovered in 34.6 s |
| 2 m | 90 s | recovered in 1 m 34.6 s |
| 2 m | 110 s | recovered in 1 m 54.6 s |
| 2 m | 4 m | failed, correctly |

**Unplanned finding.** Giving up costs the window *twice*. A 30-second window
under a four-minute outage took 1 m 24 s to fail -- because the cleanup that
abandons the half-written upload goes through the same retryer. That decides the
validation bound: `retry_window` must be under half the pipeline's 5-minute
stall budget, or the stall watcher ends the job the retry was about to rescue,
and attributes the failure to the wrong actor.

**Answer**: five seconds, before this work. Two minutes, after it, with the
window meaning what it says.

**Impact**: `retry_window` per destination, defaulting to 2 m; zero disables
retrying, which is a legitimate ask. Validation refuses anything at or above
2 m 29 s.

---

## P-103 — MinIO and AWS do not answer the same

**Question**: can `ListMultipartUploads` be given a prefix and a page size?

**Why it matters**: the sweep is a safety control, and a control that reports
"nothing to clean up" when it cannot see is worse than no control, because it
produces confidence instead of protection.

### Measurements

```
  sans prefixe          2 televersement(s)
  prefixe sources/      0 televersement(s)
  prefixe sources/shop/ 0 televersement(s)
  prefixe + delimiter   0 televersement(s)

  MaxUploads=1    -> 2004 rendus, tronquee=false, NextKeyMarker=""
  MaxUploads=1000 -> 2004 rendus, tronquee=false, NextKeyMarker=""
```

Three divergences: MinIO returns **nothing** when given a `Prefix` where AWS
filters on it; MinIO ignores `MaxUploads`; MinIO never truncates and never
returns a marker. A fourth: aborting an upload that does not exist is a success
on MinIO and `NoSuchUpload` on AWS.

### The one that mattered

Found by running the real command rather than by reading anything. `koffr prune
--orphans --confirm` reported two thousand unfinished uploads and released none
of them, four passes running, with no error. Chasing that produced the finding:

```
  listage de scope-a-1788719082315026000 : 1 a lui, 2008 a quelqu'un d'autre
  listage de scope-b-1788719082315026000 : 1 a lui, 2008 a quelqu'un d'autre
```

Two buckets created seconds earlier, one upload each. **MinIO answers
`ListMultipartUploads` with every upload it holds, whichever bucket is named.**

That is not merely noisy. Every Koffr repository uses the same key layout, so
the `sources/` prefix does not separate one repository from another either. Two
repositories sharing an endpoint would each report the other's backup paths --
and on a service whose abort were as loose as its listing, one could abort the
other's *running* backup. The abort turned out to be scoped on this instance,
which is why nothing was destroyed; that is a thin thing to depend on.

`ListParts` **is** scoped, measured on the same instance: asked about a foreign
upload it refuses with `NoSuchUpload`. So it is asked about every candidate, and
what comes back settles ownership and gives the size at the same time -- the
number that tells an operator what they are being charged for. On a healthy
repository it costs nothing, because an unfinished upload is an accident rather
than a state.

Verified afterwards against the same instance: MinIO claimed 2009 unfinished
uploads for the destination bucket, Koffr reported **none**, then reported
exactly the three that were genuinely there.

**Answer**: no. Server-side filtering would have reported an empty sweep against
the backend the whole test suite runs on, and trusting the listing's bucket
would have reported -- and eventually acted on -- someone else's.

**Impact**: the prefix is filtered client-side and every candidate's ownership
is confirmed with `ListParts`. The paging loop is unreachable
against MinIO and necessary against AWS, so it is tested against a stubbed HTTP
client that fabricates a truncated listing -- which also covers the marker pair,
where advancing on the key alone loops for ever. The tolerance for an
already-vanished upload stays untested and documented as such: it turns an error
into success only for a condition that already means the work is done.

---

## P-104 — the largest artifact that can be written

**Question**: the upload manager sizes parts for itself. Does that hold for a
stream? (ENF-001, EF-041)

### Method

Reading the SDK. `upload.go:459` adjusts part size only when `u.totalSize` is
known, which requires a seekable body. Koffr's body is a pipe. At part 10 001
the manager returns `exceeded total allowed S3 limit MaxUploadParts (10000).
Adjust PartSize to fit in this limit`.

### Measurements

Heap in use, pushing through the real storage layer:

| Part size | Ceiling | Heap (storage layer alone) | Heap (whole pipeline, 2 GiB source) |
|---|---|---|---|
| 8 MiB (before) | 80 GiB | -- | 82.6 MiB at 10 GiB |
| 16 MiB | 156 GiB | -- | 128.1 MiB |
| 32 MiB | 312 GiB | 228 MiB | -- |
| 64 MiB | 625 GiB | 452 MiB | -- |

**Answer**: 80 GiB, and the refusal arrives at the last part -- after everything
before it has been uploaded, which is the worst possible moment and a breach of
PD-006 in spirit.

### The part count is not ten thousand everywhere

Found afterwards, reading Databasus as prior art: it caps itself at a thousand
parts per object, and says why -- *"that limit is provider-set (AWS 10000,
Scaleway and many S3-compatibles 1000)"*. Verified against Scaleway's own
documentation: a part number there must be between 1 and 1000.

So the ceiling above was measured against the friendlier half of the market. On
Scaleway the same 16 MiB default caps an artifact at **15.6 GiB**, and the 32 MiB
maximum at 31 GiB -- ordinary database sizes, not exotic ones. Worse, the refusal
would have come from the provider in its own words, which the translation below
does not recognise, so the operator would have got a raw S3 error hours in.

`max_parts` is therefore configurable per destination and *enforced by Koffr*
rather than left to the service, so the failure is deterministic and legible on
every backend. It defaults to 10000 when no endpoint is configured and 1000 when
one is: the only signal available is the one `wire.go` already trusts to decide
on path-style addressing. Being conservative costs an artificial failure with an
actionable message; being optimistic costs a provider error nobody can act on.

Databasus answers the same ceiling differently and better: at a thousand parts it
completes the object, opens the next, and records the spans in a `.parts`
manifest, so object count rather than memory grows with the database. That is the
right long-term answer and it stays compatible with PD-001 -- `cat` the objects
in order and the existing restore procedure is unchanged. It is a repository
format change, so it belongs to M2 and to an ADR, not here.

**Impact**: default part size raised to 16 MiB and `part_size_mib` exposed per
destination, bounded at 32 MiB. `max_parts` exposed and enforced. 64 MiB was written into the code first and then
measured at 452 MiB for the storage layer *alone*, which fits inside ENF-001's
512 MiB only if nothing else in the process needs memory -- and the source, zstd
and age all do. The manager's message is translated into one that names
`part_size_mib`, a setting that exists, rather than `PartSize`, which does not.

---

## Amendment to the specification

**EF-041, amended 2026-09-06 (P-101, P-102).** "Resume after a network
interruption" is replaced by:

> A transfer interrupted by the network is retried for a configurable window,
> bounded so that giving up still fits inside the stall budget. A transfer that
> cannot be retried is restarted from the beginning: a backup taken from a
> logical dump cannot be resumed, because re-running the dump produces different
> bytes, and no part already uploaded can be matched to the new stream. What an
> interrupted job leaves on an object store is found and released by
> `koffr prune --orphans`.

Physical backups may lift this later: a base backup is a file set, and a file
set can be resumed file by file. Nothing in this design forecloses it.
