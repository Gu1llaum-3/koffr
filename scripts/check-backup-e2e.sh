#!/usr/bin/env bash
#
# Scenario 6 of § 8, and the exit criterion of the lot 2: an archive koffr wrote
# opens **without koffr**, and what comes out is a real dump.
#
# The chain is `age --decrypt | zstd -d`, in that order, because § 4.1 compresses
# and then encrypts. This is the procedure the README documents for an operator
# who has lost the agent and kept the key — so it is checked with the real
# binaries, not with our own libraries.
#
# The Go test starts the servers, runs `koffr backup` and lays out the pieces;
# this script executes age, zstd and pg_restore over them (`N-8`, `N-15`).
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

missing=()
for tool in age zstd; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done

if [ ${#missing[@]} -gt 0 ]; then
  if [ "${KOFFR_REQUIRE_AGE:-0}" = "1" ]; then
    echo "backup end to end: REQUIRED here and missing ${missing[*]}." >&2
    echo "  Scenario 6 cannot be checked without them (apt install age zstd)." >&2

    exit 1
  fi

  echo "backup end to end: skipped — missing ${missing[*]}." >&2
  echo "  Scenario 6 says an archive must open without koffr, and only the real" >&2
  echo "  tools can show that. The CI runs this with KOFFR_REQUIRE_AGE=1." >&2

  exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "backup end to end: on ($(age --version 2>&1 | head -1), zstd $(zstd --version | sed 's/.*v\([0-9.]*\).*/\1/'))" >&2

# koffr backs up a real PostgreSQL and a real MariaDB, and leaves the archives
# and one private key here.
go test -C "$ROOT" ./internal/cli/ \
  -run 'TestBackupOfAReal(PostgreSQL|MariaDB)WritesAnEncryptedArchive' \
  -count=1 -timeout 15m -args -fixture-dir="$work" >/dev/null

if [ ! -d "$work/postgresql" ]; then
  if [ "${KOFFR_REQUIRE_DOCKER:-0}" = "1" ]; then
    echo "backup end to end: REQUIRED here and no archive was produced." >&2
    echo "  The tests need Docker to start the servers they back up." >&2

    exit 1
  fi

  echo "backup end to end: skipped — the tests produced no archive (no Docker?)." >&2

  exit 0
fi

for engine in postgresql mariadb; do
  cd "$work/$engine"

  # The procedure of the README, exactly: decrypt, then decompress.
  age --decrypt -i identity.txt archive.age | zstd -d >dump

  [ -s dump ] || { echo "  $engine: the archive opened to nothing" >&2; exit 1; }

  table=$(cat expected-table.txt)

  case "$engine" in
    postgresql)
      # And the dump is a real archive: pg_restore lists what is inside it.
      if ! pg_restore --list dump | grep -q "$table"; then
        echo "  $engine: pg_restore does not see the table $table" >&2
        pg_restore --list dump >&2 || true

        exit 1
      fi

      echo "  ok    age and zstd opened the archive, and pg_restore read $table in it" >&2
      ;;

    *)
      if ! grep -q "$table" dump; then
        echo "  $engine: the dump does not contain the table $table" >&2

        exit 1
      fi

      echo "  ok    age and zstd opened the archive, and the dump declares $table" >&2
      ;;
  esac
done
