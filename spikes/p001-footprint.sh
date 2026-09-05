#!/bin/sh
# P-001: does `mariabackup --backup --stream=xbstream` write to the MariaDB
# host's disk, and how much?
#
# Runs inside the MariaDB container. The stream goes to /dev/null so only LOCAL
# writes are measured.
#
# Sampling directory sizes proved too coarse: a fast backup finishes in a
# handful of samples and a transient file can slip between two of them. The
# authoritative figure is /proc/<pid>/io:
#
#   wchar       every byte passed to write(), the stream to /dev/null included
#   write_bytes bytes actually sent to storage -- this is the local footprint
#
# The difference between the two is exactly what streaming buys.
#
# Usage: p001-footprint.sh <label>
set -e

LABEL="${1:-run}"
TARGET="/probe-$LABEL"
TMPD="/probetmp-$LABEL"
OUT="/out/p001-$LABEL.samples"

rm -rf "$TARGET" "$TMPD"
mkdir -p "$TARGET" "$TMPD"
: > "$OUT"

DATADIR_BYTES=$(du -sb /var/lib/mysql | cut -f1)

mariabackup --backup --stream=xbstream \
    --target-dir="$TARGET" --tmpdir="$TMPD" \
    --user=root --password=probe \
    > /dev/null 2> "/out/p001-$LABEL.stderr" &
BPID=$!

while kill -0 "$BPID" 2>/dev/null; do
    if [ -r "/proc/$BPID/io" ]; then
        WCHAR=$(awk '/^wchar:/ {print $2}' "/proc/$BPID/io" 2>/dev/null || echo 0)
        WBYTES=$(awk '/^write_bytes:/ {print $2}' "/proc/$BPID/io" 2>/dev/null || echo 0)
        T=$(du -sb "$TARGET" 2>/dev/null | cut -f1)
        P=$(du -sb "$TMPD" 2>/dev/null | cut -f1)
        echo "${WCHAR:-0} ${WBYTES:-0} ${T:-0} ${P:-0}" >> "$OUT"
    fi
    sleep 0.05
done
wait "$BPID" || echo "mariabackup exited non-zero" >&2

awk -v d="$DATADIR_BYTES" '
    { if ($1>w) w=$1; if ($2>b) b=$2; if ($3>t) t=$3; if ($4>p) p=$4; n++ }
    END {
        printf "samples=%d\n", n
        printf "datadir            = %12d B (%8.1f MiB)\n", d, d/1048576
        printf "wchar (streamed)   = %12d B (%8.1f MiB)\n", w, w/1048576
        printf "write_bytes (disk) = %12d B (%8.1f MiB)\n", b, b/1048576
        printf "peak target-dir    = %12d B\n", t
        printf "peak tmpdir        = %12d B\n", p
        printf "local writes / datadir = %.4f%%\n", b*100/d
    }' "$OUT"
