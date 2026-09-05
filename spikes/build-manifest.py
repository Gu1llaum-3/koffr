#!/usr/bin/env python3
"""P-005: assemble a backup_manifest from a tar walk, byte-for-byte in
PostgreSQL's own writer format.

Usage:
  build-manifest.py WALK.jsonl SYSTEM_ID TIMELINE START_LSN END_LSN > backup_manifest

The exclusion rule was established by P-002: WAL segment files carried in the
tar are absent from the manifest (WAL-Ranges covers them instead), while
everything else under pg_wal/, archive_status included, is present.

The manifest checksum is SHA-256 of every byte preceding the
"Manifest-Checksum" key, which P-002 confirmed empirically.
"""
import hashlib
import json
import re
import sys

WAL_SEGMENT = re.compile(r"^pg_wal/[0-9A-F]{24}(\.partial)?$")


def is_wal_segment(path: str) -> bool:
    return bool(WAL_SEGMENT.match(path))


def main() -> int:
    walk_path, system_id, timeline, start_lsn, end_lsn = sys.argv[1:6]

    entries = []
    for line in open(walk_path, encoding="utf-8"):
        e = json.loads(line)
        if e["type"] != 48:  # regular files only
            continue
        if is_wal_segment(e["path"]):
            continue
        entries.append(e)

    out = []
    out.append('{ "PostgreSQL-Backup-Manifest-Version": 2,\n')
    out.append(f'"System-Identifier": {system_id},\n')
    out.append('"Files": [\n')
    for i, e in enumerate(entries):
        sep = "" if i == len(entries) - 1 else ","
        out.append(
            '{ "Path": %s, "Size": %d, "Last-Modified": "%s", '
            '"Checksum-Algorithm": "SHA256", "Checksum": "%s" }%s\n'
            % (json.dumps(e["path"]), e["size"], e["modified"], e["checksum"], sep)
        )
    out.append('],\n')
    out.append('"WAL-Ranges": [\n')
    out.append(
        '{ "Timeline": %s, "Start-LSN": "%s", "End-LSN": "%s" }\n'
        % (timeline, start_lsn, end_lsn)
    )
    out.append('],\n')

    body = "".join(out).encode("utf-8")
    digest = hashlib.sha256(body).hexdigest()

    sys.stdout.buffer.write(body)
    # PostgreSQL terminates the file with a newline after the closing brace.
    sys.stdout.buffer.write(f'"Manifest-Checksum": "{digest}"}}\n'.encode("utf-8"))
    print(f"entries: {len(entries)}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
