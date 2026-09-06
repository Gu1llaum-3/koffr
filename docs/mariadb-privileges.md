# The MariaDB account Koffr needs

Koffr reads. It never writes to the database it backs up, and it does not need
a superuser account.

Every statement below was derived by running Koffr against an account built
exactly this way, not from reading the MariaDB documentation. Several of them
are here because the obvious grants were not enough, or were more than enough
and made the backup fail for it.

## The grants

```sql
CREATE USER 'koffr'@'%' IDENTIFIED BY 'choose-something';

GRANT SELECT, SHOW VIEW ON shop.* TO 'koffr'@'%';

-- Optional, and each one changes what the backup contains. See below.
GRANT TRIGGER ON shop.* TO 'koffr'@'%';
GRANT EVENT   ON shop.* TO 'koffr'@'%';

-- Optional. Only needed to record where the dump sits in the binary log,
-- which is what a future point-in-time recovery starts from.
GRANT RELOAD ON *.* TO 'koffr'@'%';

-- Optional. Without it the backup still contains all your data, but not the
-- accounts and privileges that go with it.
GRANT SELECT ON mysql.* TO 'koffr'@'%';
```

Restrict the host pattern to wherever Koffr runs. `'%'` is written above because
it is what works everywhere; it is not what you should leave in place.

## What each grant buys, and what its absence costs

| Grant | Without it |
|---|---|
| `SELECT` | Nothing works. Koffr refuses at configuration time rather than starting a dump that cannot finish (EF-019). |
| `SHOW VIEW` | Required **only if the database contains views**. A view is backed up by its definition and a table by its rows, so Koffr asks for this one only when there is a view to ask about. |
| `TRIGGER` | The backup contains no triggers. This is not a warning you can ignore: without the privilege, `mariadb-dump` does not quietly skip them, it **fails** on `SHOW TRIGGERS`. Koffr therefore reads your privileges first and tells the tool to skip them — and records in the backup that it did. |
| `EVENT` | The backup contains no scheduled events, recorded the same way. |
| `RELOAD` | The backup carries no binary-log position. It still restores perfectly; what it cannot do is serve as the starting point for a point-in-time recovery, and that position cannot be recovered afterwards. |
| `SELECT ON mysql.*` | `grants.sql` is not produced. The data comes back; the accounts that use it do not, and have to be recreated by hand. |

`LOCK TABLES` is deliberately absent. Koffr dumps with `--single-transaction`,
which takes a consistent snapshot without locking anything — so an account
holding `LOCK TABLES` has a privilege it will never use.

`PROCESS` is absent for the same kind of reason. It would only be needed to dump
tablespace definitions, which Koffr disables (`--no-tablespaces`): that read
costs a server-wide privilege a backup account has no other use for, and managed
providers routinely refuse to grant it.

## The one thing that is not a privilege

**A database with MyISAM or Aria tables cannot be backed up consistently**, no
matter what you grant.

`--single-transaction` is what makes a logical backup a snapshot, and it only
covers transactional engines. One non-transactional table is enough for the dump
to hold a state the database never had — some tables as of the moment the dump
started, others as of whenever they happened to be read. Nothing in the output
says so.

Koffr refuses such a source at configuration time and names the tables:

```
mariadb: database "shop" has 1 non-transactional table(s) (legacy_notes (MyISAM)),
so --single-transaction cannot give a consistent snapshot: converting them to
InnoDB is the fix; setting allow_inconsistent_snapshot on this source accepts
the risk instead, and every backup taken that way is marked inconsistent
```

The fix is `ALTER TABLE legacy_notes ENGINE=InnoDB`. If that is genuinely not
possible, the escape hatch is per-source and explicit:

```yaml
sources:
  shop:
    engine: mariadb
    allow_inconsistent_snapshot: true
```

An imperfect dump beats no dump. But it is written into the backup's manifest,
in the clear, so nobody discovers during a restore that the snapshot they are
holding was never a snapshot:

```json
{ "snapshot_consistent": false }
```

and `koffr show` says so before you act on it:

```
WARNING: this backup is not a consistent snapshot. Parts of it were
read at different moments, so it can hold a state the database never had.
See the restrictions in its encrypted details for which tables caused it.
```

Which tables caused it is in the encrypted details rather than the plaintext
manifest: whether a snapshot can be trusted is metadata, and the names of the
tables are content (EF-055).

## Checking it

```sh
koffr check
```

It connects as the configured account, reads what it actually holds, and reports
what will be missing from the backup before any backup is taken.
