# Retention: deciding what to delete

Koffr keeps every backup until a policy says otherwise. That is deliberate: a
setting whose mistakes cannot be undone should do nothing until someone has
written it down.

Nothing is ever deleted without `--confirm`. Running `koffr prune` without it
lists every backup, the verdict, and the rule that produced it — and that is
the supported way to use the command, not a safety net for the careless.

## Writing a policy

```yaml
sources:
  shop:
    retention:
      keep_last: 7        # the 7 most recent, whatever their age
      keep_within: 720h   # everything taken in the last 30 days
      # Grandfather-father-son: the newest backup of each period
      hourly: 24
      daily: 7
      weekly: 4
      monthly: 12
      yearly: 3
    destinations: [main]
```

**Rules are a union.** A backup any rule wants is kept. The alternative would
delete something a policy explicitly asked to keep, and between those two
mistakes only one is recoverable.

**`daily: 7` means seven days, not seven backups.** Each GFS rule keeps the
newest backup of each of that many periods *that have a backup*. A source
backed up four times a night, with `daily: 7`, keeps seven backups — one per
night.

This is restic's and borg's behaviour, deliberately: a gap in the timeline does
not spend the daily slots on days that have nothing in them. A source that was
down for a fortnight keeps seven backups spread over the days it did run.

## The rule no policy can override

**The last restorable backup is never deleted.** Whatever the policy says, and
whatever `keep_last` is set to. A source with one backup has one chance of being
restored, and no configuration should be able to spend it.

Restorable, not recorded: Koffr checks the backup is actually in the repository
before letting it count. A catalog row whose objects were lost — bit rot, a
lifecycle rule, someone tidying up — does not save the older backups that still
exist. If nothing is restorable, nothing is deleted at all.

This is stricter than restic and borg, where `--keep-last 0` will empty a
repository.

## Running it

```sh
koffr prune                       # every source, dry run, nothing touched
koffr prune shop                  # one source
koffr prune --confirm             # do it
koffr prune --orphans --confirm   # and sweep what a dead job left behind
```

On a timetable, alongside the backups:

```yaml
scheduler:
  prune: "@daily"
```

Off unless written. A repository that grows for ever is the alternative, and it
is still better than a purge that ran because nobody said it should not.

The scheduled purge does not catch up a missed run. A missed backup is a gap in
history worth making good; a missed purge is a day of extra storage, and
hurrying to delete things after an outage is the wrong instinct.

## Orphans

A backup interrupted between its first upload and its manifest leaves objects
nothing points at. The manifest is what makes a set of objects a backup, so a
prefix without one was never one: invisible to `koffr ls`, invisible to a purge
that reads the catalog, and paid for every month.

`--orphans` finds them. Anything touched in the last 24 hours is left alone — a
backup being written has objects and no manifest too, and deleting a running job
is a far worse outcome than paying for a stale prefix another day.

## Versioned and Object Lock buckets: read this one

**On a versioned or Object-Locked bucket, deleting a backup frees no space.**

S3 answers a delete on such a bucket by writing a delete marker. The object
stops being listed, and every byte of it stays — and stays billed — until a
bucket lifecycle rule expires the noncurrent versions.

Measured, on three backups with `keep_last: 1`:

| Bucket | Before | After |
|---|---|---|
| Plain | 288 KiB | 98 KiB |
| Versioned | 291 KiB | **292 KiB** |
| Object Lock | 291 KiB | **292 KiB** |

Koffr says so rather than reporting a number your bill will not match:

```
deleted 2. No space was reclaimed: main keeps previous versions of what it
deletes, so the bytes stay until a bucket lifecycle rule expires them.
```

The deletion is still worth doing — it removes the backup from view and from the
catalog. But **retention in Koffr and a lifecycle rule on the bucket are two
halves of one thing**. Koffr decides what is a backup; the bucket decides when
the bytes go.

```json
{
  "Rules": [{
    "ID": "expire-noncurrent",
    "Status": "Enabled",
    "Filter": {"Prefix": "sources/"},
    "NoncurrentVersionExpiration": {"NoncurrentDays": 7}
  }]
}
```

Seven days, not one: it is the window in which a mistaken purge can still be
undone, and it is the only thing standing between an accidental
`prune --confirm` and no backups at all. On Object Lock, deletion is impossible
until the lock expires whatever any rule says — which is the protection you
asked for when you turned it on.

## Should the repository itself be backed up?

No. A backup of a backup doubles the cost and adds a layer that can be wrong on
its own.

What protects a repository is not a copy of it:

- **A second destination**, written independently, so a bug in one write does
  not reach both. This is the 3-2-1 rule and it is worth more than a copy,
  because each destination is verified on its own terms.
- **Immutability** on at least one of them (S3 Object Lock), which is what
  survives ransomware, a wrong `prune --confirm`, and Koffr itself being wrong.
- **Versioning with a lifecycle rule**, which gives a window to undo a mistake
  without keeping every version for ever.

Retention matters more under all of these, not less: every byte Koffr keeps is
multiplied by the number of copies of it, and retention is what sets the base
number.
