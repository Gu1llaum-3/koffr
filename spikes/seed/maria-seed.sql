-- Reference dataset for the MariaDB probes.
--
-- Usage: mariadb -uroot -pprobe probe < maria-seed.sql
-- Adjust @rows_random / @rows_wide to change the volume.
--
-- Same reasoning as the PostgreSQL seed: a mix of incompressible and highly
-- compressible bytes, so throughput and footprint figures mean something.
-- RANDOM_BYTES caps at 1024 bytes per call, hence the concatenation.

-- MariaDB's knob is max_recursive_iterations, not MySQL's
-- cte_max_recursion_depth, and it defaults to 1000.
SET SESSION max_recursive_iterations = 100000000;
SET @rows_random = 128000;   -- x 4 KiB  ~= 512 MiB
SET @rows_wide   = 1400000;  -- x ~200 B ~= 280 MiB

DROP TABLE IF EXISTS probe_random, probe_wide;

CREATE TABLE probe_random (
    id      BIGINT PRIMARY KEY,
    payload VARBINARY(4096) NOT NULL
) ENGINE=InnoDB;

CREATE TABLE probe_wide (
    id   BIGINT PRIMARY KEY,
    tag  VARCHAR(32)  NOT NULL,
    note VARCHAR(255) NOT NULL
) ENGINE=InnoDB;

INSERT INTO probe_random (id, payload)
WITH RECURSIVE seq AS (
    SELECT 1 AS n
    UNION ALL
    SELECT n + 1 FROM seq WHERE n < 128000
)
SELECT n,
       CONCAT(RANDOM_BYTES(1024), RANDOM_BYTES(1024),
              RANDOM_BYTES(1024), RANDOM_BYTES(1024))
FROM seq;

INSERT INTO probe_wide (id, tag, note)
WITH RECURSIVE seq AS (
    SELECT 1 AS n
    UNION ALL
    SELECT n + 1 FROM seq WHERE n < 1400000
)
SELECT n,
       CONCAT('tag-', n % 50),
       REPEAT('the quick brown fox jumps over the lazy dog ', 4)
FROM seq;

SELECT table_name,
       table_rows,
       ROUND((data_length + index_length) / 1024 / 1024) AS mb
FROM information_schema.tables
WHERE table_schema = 'probe'
ORDER BY data_length DESC;
