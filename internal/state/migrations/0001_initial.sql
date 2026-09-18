-- 0001 — the seven tables of E-028, as § 4.4 names them.
--
-- Conventions fixed by ADR-0006 and applied throughout:
--   * STRICT tables, so that a size or a duration written as text is refused
--     by SQLite instead of being stored and read back as nonsense;
--   * timestamps are TEXT in RFC 3339 UTC, checked by
--     GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'
--     — the trailing Z is the point: one representation in the database, the
--     conversion to agent.timezone happens on display and in the manifest;
--   * statuses are TEXT constrained by CHECK, spelled out, never integers;
--   * sizes are bytes, durations are milliseconds, both INTEGER, never REAL;
--   * created_at and updated_at on every table.
--
-- Five of these seven tables stay empty until lot 2 and later. They are created
-- now because E-028 is a requirement of lot 0 and the specification describes
-- the whole schema (N-9).

-- schema_migrations is created by the runner: it has to exist before the
-- first migration can record itself.

-- A snapshot of the resolved configuration of each database, kept so that a
-- change can be noticed (§ 4.4). The identifier is the one the operator wrote.
CREATE TABLE databases (
    id          TEXT    NOT NULL PRIMARY KEY,
    engine      TEXT    NOT NULL CHECK (engine IN ('postgresql', 'mysql', 'mariadb')),
    host        TEXT    NOT NULL,
    port        INTEGER NOT NULL,
    "database"  TEXT    NOT NULL,
    "user"      TEXT    NOT NULL,
    fingerprint TEXT    NOT NULL,
    resolved    TEXT    NOT NULL,
    created_at  TEXT    NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    updated_at  TEXT    NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z')
) STRICT;

-- One row per run: what it was, on which database, how it ended (§ 4.4).
CREATE TABLE jobs (
    id          TEXT    NOT NULL PRIMARY KEY,
    kind        TEXT    NOT NULL CHECK (kind IN ('backup', 'verify', 'restore', 'retention')),
    database_id TEXT    NOT NULL REFERENCES databases (id),
    status      TEXT    NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    started_at  TEXT             CHECK (started_at IS NULL OR started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    finished_at TEXT             CHECK (finished_at IS NULL OR finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    duration_ms INTEGER          CHECK (duration_ms IS NULL OR duration_ms >= 0),
    exit_code   INTEGER,
    created_at  TEXT    NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    updated_at  TEXT    NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z')
) STRICT;

CREATE INDEX jobs_by_database ON jobs (database_id, created_at DESC);

-- Structured log lines attached to a job (§ 4.4). Their growth is not handled
-- here: Q-21 is open and belongs to the lot that serves them.
CREATE TABLE job_logs (
    id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    job_id     TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    at         TEXT    NOT NULL CHECK (at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    level      TEXT    NOT NULL CHECK (level IN ('debug', 'info', 'warn', 'error')),
    message    TEXT    NOT NULL,
    fields     TEXT,
    created_at TEXT    NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    updated_at TEXT    NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z')
) STRICT;

CREATE INDEX job_logs_by_job ON job_logs (job_id, id);

-- The catalogue. It is an index, never the source of truth: everything a
-- restore needs also lives in the manifest next to the archive (ADR-0006).
CREATE TABLE backups (
    id            TEXT    NOT NULL PRIMARY KEY,
    database_id   TEXT    NOT NULL REFERENCES databases (id),
    job_id        TEXT             REFERENCES jobs (id),
    started_at    TEXT    NOT NULL CHECK (started_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    finished_at   TEXT             CHECK (finished_at IS NULL OR finished_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    size_bytes    INTEGER          CHECK (size_bytes IS NULL OR size_bytes >= 0),
    stored_bytes  INTEGER          CHECK (stored_bytes IS NULL OR stored_bytes >= 0),
    sha256_raw    TEXT,
    sha256_stored TEXT,
    manifest      TEXT,
    verified      TEXT    NOT NULL DEFAULT 'none' CHECK (verified IN ('none', 'checksum', 'structure', 'failed')),
    verified_at   TEXT             CHECK (verified_at IS NULL OR verified_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    created_at    TEXT    NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    updated_at    TEXT    NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z')
) STRICT;

CREATE INDEX backups_by_database ON backups (database_id, started_at DESC);

-- One row per archive and destination, with the remote path and the size
-- actually written there (§ 4.4).
CREATE TABLE backup_locations (
    backup_id      TEXT    NOT NULL REFERENCES backups (id) ON DELETE CASCADE,
    destination_id TEXT    NOT NULL,
    status         TEXT    NOT NULL CHECK (status IN ('pending', 'uploading', 'stored', 'failed', 'deleted')),
    remote_path    TEXT,
    size_bytes     INTEGER          CHECK (size_bytes IS NULL OR size_bytes >= 0),
    stored_at      TEXT             CHECK (stored_at IS NULL OR stored_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    created_at     TEXT    NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    updated_at     TEXT    NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    PRIMARY KEY (backup_id, destination_id)
) STRICT;

-- The effective schedule of a database, where it came from, and the due dates
-- that survive a restart (§ 4.4, E-089).
CREATE TABLE schedules (
    database_id TEXT NOT NULL PRIMARY KEY REFERENCES databases (id) ON DELETE CASCADE,
    expression  TEXT NOT NULL,
    origin      TEXT NOT NULL CHECK (origin IN ('local', 'server')),
    last_run_at TEXT          CHECK (last_run_at IS NULL OR last_run_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    next_run_at TEXT          CHECK (next_run_at IS NULL OR next_run_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    created_at  TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    updated_at  TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z')
) STRICT;

-- The events that were emitted, kept for suppression and for history (§ 4.4).
-- The nine names are the ones § 5.10 fixes; a tenth is a migration, not a
-- string invented at the call site.
CREATE TABLE alerts (
    id             TEXT NOT NULL PRIMARY KEY,
    event          TEXT NOT NULL CHECK (event IN (
                       'backup_missed', 'backup_failed', 'verify_failed',
                       'database_unreachable', 'destination_failed', 'tool_missing',
                       'retention_blocked', 'uplink_lost', 'remote_command_rejected')),
    state          TEXT NOT NULL CHECK (state IN ('firing', 'recovered')),
    database_id    TEXT          REFERENCES databases (id),
    destination_id TEXT,
    emitted_at     TEXT NOT NULL CHECK (emitted_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    reminded_at    TEXT          CHECK (reminded_at IS NULL OR reminded_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    recovered_at   TEXT          CHECK (recovered_at IS NULL OR recovered_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    details        TEXT,
    created_at     TEXT NOT NULL CHECK (created_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z'),
    updated_at     TEXT NOT NULL CHECK (updated_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z')
) STRICT;

CREATE INDEX alerts_by_event ON alerts (event, state, emitted_at DESC);
