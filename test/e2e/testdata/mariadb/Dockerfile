# The machine a MariaDB backup is restored on when Koffr is not available.
#
# It has exactly what PD-001 promises is enough -- age, zstd and the database's
# own client -- and nothing of Koffr's. If a restore needs anything else, this
# image is where that shows up.
FROM mariadb:11.4

RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends age zstd \
 && rm -rf /var/lib/apt/lists/*

# Where the operator unpacks the backup.
RUN mkdir -p /restore
