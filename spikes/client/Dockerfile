# Client container for P-004: it sits where Koffr sits. It is attached to the
# public network only, so it can reach the bastion and NOT the database.
FROM postgres:17
RUN apt-get update && apt-get install -y --no-install-recommends netcat-openbsd \
 && rm -rf /var/lib/apt/lists/*
