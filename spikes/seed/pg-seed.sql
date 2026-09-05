-- Reference dataset for the PostgreSQL probes.
--
-- Usage: psql -v scale_mb=512 -f pg-seed.sql
--
-- The mix matters. Measuring compression on purely random data gives a
-- pessimistic figure, and on purely repetitive text an unreachable one. Roughly
-- 60% of the on-disk bytes are incompressible and 40% compress well:
--
--   probe_random  bytea, STORAGE EXTERNAL so PostgreSQL does not compress it
--                 itself; this is what stays incompressible in the tar stream.
--   probe_wide    short repetitive text living uncompressed in the heap, which
--                 is what a backup stream actually gets to compress.
--
-- The 100 small tables exist to give the reconstructed manifest a realistic
-- number of entries to compare (P-002), not to hold data.

\set ON_ERROR_STOP on

CREATE EXTENSION IF NOT EXISTS pgcrypto;

DROP TABLE IF EXISTS probe_random;
DROP TABLE IF EXISTS probe_wide;

CREATE TABLE probe_random (
    id      bigint PRIMARY KEY,
    payload bytea NOT NULL
);
-- EXTERNAL means out-of-line and uncompressed: the bytes stay random on disk.
ALTER TABLE probe_random ALTER COLUMN payload SET STORAGE EXTERNAL;

CREATE TABLE probe_wide (
    id       bigint PRIMARY KEY,
    tag      text NOT NULL,
    note     text NOT NULL,
    created  timestamptz NOT NULL
);

-- ~4 KiB per row, 60% of the target.
-- pgcrypto caps gen_random_bytes at 1024 bytes per call, hence the concatenation.
INSERT INTO probe_random (id, payload)
SELECT g, gen_random_bytes(1024) || gen_random_bytes(1024)
        || gen_random_bytes(1024) || gen_random_bytes(1024)
FROM generate_series(1, (:scale_mb * 0.60 * 256)::bigint) AS g;

-- ~200 B per row, 40% of the target, deliberately repetitive.
INSERT INTO probe_wide (id, tag, note, created)
SELECT g,
       'tag-' || (g % 50),
       repeat('the quick brown fox jumps over the lazy dog ', 4),
       timestamptz '2020-01-01' + (g % 100000) * interval '1 minute'
FROM generate_series(1, (:scale_mb * 0.40 * 5243)::bigint) AS g;

-- 100 small relations: file count, not volume.
DO $$
DECLARE i int;
BEGIN
    FOR i IN 0..99 LOOP
        EXECUTE format(
            'DROP TABLE IF EXISTS probe_t%1$s;
             CREATE TABLE probe_t%1$s (id int PRIMARY KEY, label text);
             INSERT INTO probe_t%1$s
                 SELECT g, ''row-'' || g FROM generate_series(1, 50) g;',
            lpad(i::text, 3, '0'));
    END LOOP;
END $$;

ANALYZE;

SELECT pg_size_pretty(pg_database_size(current_database())) AS database_size;
