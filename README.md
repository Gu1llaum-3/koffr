# koffr

An autonomous database backup agent for self-hosted PostgreSQL, MySQL and MariaDB fleets.

A single static Go binary. No Docker requirement, no mandatory central server, no service
dependency: local state lives in SQLite, archives are compressed with zstd and encrypted with
[age](https://age-encryption.org), and every archive stays readable with the standard `age` tool
alone — without koffr.

## Status

**Early development.** koffr takes real backups: `koffr backup <db>` dumps a PostgreSQL, MySQL or
MariaDB database, compresses it, encrypts it and writes it to a filesystem destination. Not yet
here: S3 and SFTP destinations, archive verification, the manifest, retention, restore, the
scheduler and the web interface. What a command cannot do yet, it says.

## Requirements

- Linux (`amd64`, `arm64`) or macOS (`arm64`) — Windows is out of scope
- Go 1.27 to build from source
### The dump and restore tools

koffr calls `pg_dump`, `mysqldump` and `mariadb-dump`; it does not reimplement them, and **it does
not install them**. Install the client package of each engine you back up, in a version **at least
as recent as the server**:

| Engine | Debian, Ubuntu | RHEL, Rocky, Fedora | Alpine |
| --- | --- | --- | --- |
| PostgreSQL | `postgresql-client-17` | `postgresql17` | `postgresql17-client` |
| MySQL | `mysql-client` | `mysql` | `mysql-client` |
| MariaDB | `mariadb-client` | `mariadb` | `mariadb-client` |

Several PostgreSQL client versions can be installed side by side; koffr picks the closest one that
is at least as recent as each server, and never crosses the MySQL and MariaDB families.

For a database running in a container, you can skip all of this and let koffr use the client inside
that container:

```yaml
databases:
  - id: erp
    engine: mariadb
    tools: { strategy: exec, container: erp-mariadb }
```

`koffr doctor` tells you, for each database, which tool it would use — and says what is missing when
there is none.

#### One machine cannot hold both MySQL and MariaDB clients

On Debian, Ubuntu and their derivatives, the two conflict:

```
mariadb-client-core : Conflicts: virtual-mysql-client-core
E: Unable to satisfy dependencies.
```

Installing one removes the other, and `/usr/bin/mysqldump` then belongs to whichever stayed. koffr
**will not** use MariaDB's tool for a MySQL database, or the reverse — that would produce an archive
nobody can restore — so on a mixed fleet one of the two is simply unserved.

Three ways out, all of which koffr supports:

- one koffr machine **per family**;
- host clients for one family, and `tools: { strategy: exec, container: … }` for the other;
- `exec` for everything, when the databases run in containers.

See ADR-0015.

## Build

```sh
mise install          # Go and golangci-lint, pinned in mise.toml
mise run build        # static binary, CGO_ENABLED=0
mise run verify       # vet, lint, tests and build — green before any commit on main
```

## Configure

`examples/koffr.yaml` is a complete, working configuration — the one koffr's own
tests are run against, so it cannot quietly drift from what koffr accepts.

```sh
cp examples/koffr.yaml /etc/koffr/koffr.yaml
koffr config validate              # reads the secrets it points at
koffr config validate --offline    # checks the shape only, on a machine that holds none
koffr config show                  # prints it with every secret masked
```

Parsing is strict: an unknown key is an error naming the key, its line and its section.
Every sensitive field takes three forms — a literal value, `*_env`, or `*_file`.

## Back up

```sh
koffr keygen                       # a key pair, shown once and never written
koffr backup boutique              # dump, compress, encrypt, write
koffr backup boutique --dry-run    # what it would do, without writing a byte
```

An archive lands at a path you can read and rebuild without koffr. Its name says what it
is, in the order you undo it — `age --decrypt`, then `zstd -d`, then a `.pgc` — and its
identifier is a ULID, so it sorts by time:

```
boutique/2026/09/boutique_20260922T020003Z_01K5X8QJ4T7N2M9VWZ3RBGH6CD.pgc.zst.age
```

### Two keys, always

koffr holds **public** keys only. It cannot read its own archives, which is what makes a stolen
agent give away the databases as they are now rather than the history of what they were.

That cuts the other way too: **lose the private key and every archive encrypted for it is
unreadable, forever.** So `recipients.txt` takes two keys — the operational one and an **escrow**
key kept somewhere else entirely, offline, by somebody who is not the person running koffr.

```sh
koffr keygen >>notes-operational.txt   # keep the private key off this machine
koffr keygen >>notes-escrow.txt        # and this one somewhere else again
# put both public keys, one per line, in /etc/koffr/recipients.txt
```

koffr warns at every start when it finds a single key. It does not refuse: a fleet with one key is
a fleet that is still being backed up. But the warning does not go away.

### What koffr does not back up

A dump is taken with `--no-owner --no-privileges`, so an archive restores into **any** cluster —
including one rebuilt after a total loss, where the original roles no longer exist. The price is
stated rather than discovered on the day:

- **roles, passwords and grants** are not in it, nor are tablespaces and cluster-wide extensions;
- restoring gives every object to whoever runs the restore.

For a faithful disaster recovery the industry pairs this with `pg_dumpall --globals-only`. koffr
does not take that second artefact yet — it is the next thing on the list, and until then koffr
produces archives that are **portable**, not a byte-for-byte cluster.

Each database is encrypted for the fleet's recipients, unless it declares its own
`recipients_file` — which then **replaces** the fleet list rather than adding to it, so that one
can say for whom an archive is encrypted by looking at one place.

### Inventory a repository without koffr

Every archive has a manifest beside it, unencrypted and holding no credential. A repository is
therefore inventoried with `jq` alone — no koffr, no private key, no database:

```sh
jq -r '[.database_id, .started_at, .size_stored,
        (if .verified.checksum and .verified.structure then "verified" else "NOT VERIFIED" end)]
       | @tsv' /srv/backups/*/*/*/*.json
```

A manifest says what the archive is, how it was written and what can read it back: the engine and
its version, the tool with its version and its arguments, the pipeline, both checksums, and the
public keys it was encrypted for. It says nothing that opens a database — no host, no user, no
password. That is the whole point: a stolen repository gives up metadata and nothing else.

### Open an archive without koffr

This is the point of the format, and it is checked by `mise run e2e` on every build. Two standard
tools, in this order — the archive is compressed **and then** encrypted:

```sh
age --decrypt -i identity.txt boutique_20260922T020003Z_01K5X8QJ4T7N2M9VWZ3RBGH6CD.pgc.zst.age | zstd -d >dump.pgc

pg_restore --list dump.pgc                       # what is inside it
pg_restore -d boutique_restored dump.pgc         # PostgreSQL
mariadb boutique_restored <dump.sql              # MySQL and MariaDB
```

Nothing above runs koffr. If this repository disappeared tomorrow, your archives would still open.

## Documentation

The product speaks English; the project is steered in French (ADR-0003). Contributor and steering
documents are therefore in French:

| Document | What it holds |
| --- | --- |
| [`ROADMAP.md`](ROADMAP.md) | One line per lot, its scope and its exit criterion |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Package layout and the dependency rules the lint enforces |
| [`METHODE.md`](METHODE.md) | How the project is run: cycle, registers, definition of done |
| [`docs/adr/`](docs/adr/) | Frozen decisions; changed by a new ADR, never worked around in code |
| [`docs/cdc/`](docs/cdc/) | The specification and its `E-nnn` requirement register |
| [`docs/README.md`](docs/README.md) | Index of the documentation and table of registers |

## License

Apache-2.0 — see [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
