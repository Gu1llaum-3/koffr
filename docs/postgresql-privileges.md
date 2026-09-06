# The PostgreSQL role Koffr needs

Koffr reads. It never writes to the database it backs up, and it does not need
superuser.

Every statement below was derived by running Koffr against a role built exactly
this way, not from reading the PostgreSQL documentation. Two of them are here
because the first attempt failed: the role the obvious grants produce cannot
read sequences, and it cannot read role passwords.

## The grants

```sql
CREATE ROLE koffr LOGIN PASSWORD 'choose-something';

GRANT CONNECT ON DATABASE shop TO koffr;

-- One per schema you want backed up. Repeat for each.
GRANT USAGE ON SCHEMA public TO koffr;
GRANT SELECT ON ALL TABLES    IN SCHEMA public TO koffr;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO koffr;

-- Tables created after those grants would not be covered by them. Without
-- this, a table added next month is a table missing from next month's backup.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT ON TABLES TO koffr;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT ON SEQUENCES TO koffr;
```

Then check it, before trusting a schedule to it:

```sh
koffr check
```

`koffr check` refuses a role that cannot read everything, rather than letting
`pg_dump` produce a partial dump (EF-019). If it passes, the backup will not
fail on privileges.

## Why sequences are on that list

`GRANT SELECT ON ALL TABLES` does not cover sequences, and `pg_dump` reads
`last_value` from every one of them. A role with the table grants alone passes a
naive check and then fails partway through with `permission denied for
sequence`. Koffr's check covers sequences for exactly that reason.

## Row-level security

A table with RLS enabled is dumped through the policies, and `pg_dump` refuses
to emit a table it can only see part of. If any table in the database uses RLS:

```sql
ALTER ROLE koffr BYPASSRLS;
```

`koffr check` names the tables that need it.

`BYPASSRLS` is a real privilege: it lets the role read rows the policies were
written to hide. If that is not acceptable, the alternative is a policy granting
the role full visibility on each table, which is more work and easier to get
wrong. Koffr does not need it on a database with no RLS.

## What is not backed up, and why

**Role passwords.** `pg_dumpall` reads them from `pg_authid`, which is
superuser-only, so requiring them would mean requiring superuser. Koffr passes
`--no-role-passwords`: the roles come back, their passwords do not, and you set
them again after restoring. A password hash sitting in a backup repository is a
liability nobody asked for, so this is the better answer twice over.

**Anything outside the database named in the configuration.** A logical backup
covers one database. Roles and tablespaces come from the cluster and are stored
alongside it as `globals.sql`; other databases are separate sources.

## Restoring needs a different role, on purpose

The role above cannot restore. It cannot create a database, and it cannot write
to one. That is the point: the credential a schedule uses every night, sitting
in an environment file on a server, should not be able to change anything.

Configure the restore target as a source of its own, with a role that can write,
and name it when restoring:

```yaml
sources:
  shop:                 # what the schedule backs up, read-only
    engine: postgresql
    host: db.internal
    user: koffr
    password: env:KOFFR_PG_PASSWORD
    database: shop
    schedule: "0 2 * * *"
    destinations: [main]

  shop-restore-target:  # only used by koffr restore --target
    engine: postgresql
    host: db.internal
    user: koffr_restore
    password: env:KOFFR_RESTORE_PASSWORD
    database: postgres
    destinations: [main]
```

```sh
koffr restore <backup-id> \
    --into shop_recovered --target shop-restore-target --create --no-owner
```

`--no-owner` is not optional here, and the reason is worth knowing. The dump
records who owned each table, and `pg_restore` tries to restore that with
`ALTER TABLE ... OWNER TO`. A role that is not a member of the original owner
cannot do it, and the restore stops on the first table. `--no-owner` leaves
everything owned by the restoring role, which is what you want for a test
restore and what you fix afterwards for a real one.

The restoring role needs `CREATEDB` if you use `--create`. Give it nothing else,
and give it a password that is not in the nightly environment file.

A restore is also the moment to check the other half of PD-001: everything the
`--target` flag does can be done by hand with `pg_restore`, following the
`RESTORE.md` stored beside the backup. The role above is not needed for that
either.

## If you would rather use a superuser

It works, and nothing here objects. But the role above is enough, and a
credential that can only read is a credential worth less to whoever finds it.

## Checking what a role can actually see

```sql
-- Tables the role cannot read
SELECT n.nspname, c.relname
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','p','m','f','S')
  AND n.nspname NOT IN ('pg_catalog','information_schema')
  AND NOT has_table_privilege('koffr', c.oid, 'SELECT');
```

This is the query `koffr check` runs, minus the parts that make its message
readable.
