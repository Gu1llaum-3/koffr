# Running Koffr as a service

`koffr schedule` runs in the foreground until told to stop. systemd is what
keeps it running, restarts it, and gives `systemctl reload` a meaning.

The unit in `deploy/systemd/koffr.service` was not written from systemd's
documentation. It was run: a container with a read-only root filesystem, an
unprivileged user, no capabilities, no `HOME` and a private `/tmp` — the
closest approximation of those directives available without a Linux host.
Backups, reload and shutdown all work under it.

## Install

```sh
install -m 0755 koffr /usr/local/bin/koffr
useradd --system --home /var/lib/koffr --shell /usr/sbin/nologin koffr
install -d -o koffr -g koffr -m 0750 /var/lib/koffr /var/log/koffr /etc/koffr

install -o koffr -g koffr -m 0640 koffr.yml /etc/koffr/koffr.yml
install -o koffr -g koffr -m 0600 koffr.env /etc/koffr/koffr.env

install -m 0644 koffr.service /etc/systemd/system/koffr.service
systemctl daemon-reload
systemctl enable --now koffr
```

Two files, and the split matters. `koffr.yml` holds references (`env:NAME`) and
is meant to be committable (EF-103). `koffr.env` holds what they refer to, at
0600, and is not.

## The machine needs the PostgreSQL client tools

Koffr shells out to `pg_dump`, `pg_dumpall`, `pg_restore` and `psql`; it cannot
embed them, and `pg_dump` must be at least the server's major version (CT-001).

```sh
apt-get install postgresql-client-17     # or the major you back up
```

`koffr check` refuses a source whose client tools are missing or too old, so run
it once after installing:

```sh
sudo -u koffr koffr --config /etc/koffr/koffr.yml check
```

If you back up several majors, install each toolchain and set `bin_dir` per
source.

## Applying a change

```sh
systemctl reload koffr
```

SIGHUP rereads the configuration and says nothing to a backup in progress: work
already half done is not thrown away because a file changed (EF-104). A
configuration that no longer loads keeps the previous timetable and logs why —
a typo should not cost a night.

`systemctl restart` also works and is heavier: it cancels running backups.

## Stopping

SIGTERM cancels what is running, which is what actually kills `pg_dump` rather
than leaving it holding a connection. `TimeoutStopSec=300` gives a large upload
time to finish first; raise it if your backups are bigger than your patience.

## What the hardening costs you

`ProtectSystem=strict` makes the whole filesystem read-only. Every path Koffr
writes to has to be named in `ReadWritePaths`:

- the catalog (`catalog.path`)
- the log file (`log.path`)
- a filesystem destination, if you use one

Add the destination to `ReadWritePaths` yourself. A backup that cannot write
where it was told to is the failure you will discover on the first night.

`ProtectHome=yes` makes `/home` and `/root` unreachable, and `HOME` points
nowhere useful. That matters in one place: an SSH source with no
`known_hosts_file` falls back to `~/.ssh/known_hosts`, which is not there.
**Set `known_hosts_file` explicitly for every SSH source.** Koffr refuses the
connection rather than skipping the host key check, and says so, but at 2 AM
rather than now if you leave it.

`PrivateTmp=yes` gives Koffr a `/tmp` of its own. This is where the temporary
`.pgpass` goes, so it is required rather than optional: the credentials file
should not share a directory with everything else on the machine.

## Health and monitoring

If `http.listen` is set, systemd is not what watches Koffr — your supervisor is:

```sh
curl -s localhost:9633/readyz | jq
curl -s localhost:9633/api/v1/status | jq '.sources[] | {source, age_seconds, stale}'
```

Point Uptime Kuma at `/readyz` for "can it work", and alert on `stale` or
`age_seconds` from `/api/v1/status` for "did it work".

Better still, configure `notify.dead_mans_switch`. It is the only signal that
survives Koffr being stopped, crashed, or uninstalled by someone tidying up: the
alarm comes from a ping that never arrives.

## Retention

Nothing is deleted until a policy is written and a schedule applies it. See
[retention.md](retention.md) — particularly the section on versioned buckets, if
the destination is S3: deleting a backup there frees no space until a lifecycle
rule expires the versions, and Koffr will tell you so rather than report a
number your bill will not match.

## Logs

Structured JSON on stderr, which journald captures:

```sh
journalctl -u koffr -f
journalctl -u koffr -o cat | jq 'select(.level=="ERROR")'
```

Set `log.path` as well if you want a file that outlives the journal's retention.
It rotates by size and keeps a bounded number of files — a daemon that fills the
disk takes the backups down with it.
