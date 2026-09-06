# The machine a backup is restored on when Koffr is not available.
#
# It has exactly what PD-001 promises is enough -- age, zstd, tar and the
# database's own tools -- and nothing of Koffr's. If a restore needs anything
# else, this image is where that shows up.
FROM postgres:17

RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends age zstd \
 && rm -rf /var/lib/apt/lists/*

# Where the operator unpacks the backup. Owned by postgres because that is who
# runs the restore commands.
RUN mkdir -p /restore && chown postgres:postgres /restore
