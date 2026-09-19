#!/usr/bin/env bash
#
# E-075 — an archive koffr wrote must open with the standard `age` tool, with no
# koffr anywhere. "No format of ours" is a promise that can only be kept by
# something that is not ours, so this runs the real binary.
#
# The Go test writes the pieces (AR-07 reserves os/exec to internal/engine);
# this script is what executes age over them (N-8). Same bargain as the race
# detector and the containers: skipped loudly when age is missing, and required
# where it matters — KOFFR_REQUIRE_AGE=1 turns its absence into a failure.
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v age >/dev/null 2>&1; then
  if [ "${KOFFR_REQUIRE_AGE:-0}" = "1" ]; then
    echo "age interoperability: REQUIRED here and the age binary is missing." >&2
    echo "  E-075 cannot be checked without it. Install age (apt install age)." >&2

    exit 1
  fi

  echo "age interoperability: skipped — the age binary is not installed." >&2
  echo "  E-075 says an archive must open without koffr, and only the real tool" >&2
  echo "  can show that. The CI runs this with KOFFR_REQUIRE_AGE=1." >&2
  echo "  To check it here: apt install age, or brew install age." >&2

  exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "age interoperability: on ($(age --version 2>&1 | head -1))" >&2

# koffr writes the archive, its plaintext and a private key.
go test -C "$ROOT" ./internal/pipeline/ -run TestWriteTheInteropFixture -count=1 \
  -args -interop-dir="$work" >/dev/null

for needed in archive.age identity.txt expected.txt; do
  [ -s "$work/$needed" ] || { echo "the fixture is missing $needed" >&2; exit 1; }
done

# And age opens it, knowing nothing about koffr.
age --decrypt -i "$work/identity.txt" "$work/archive.age" >"$work/decrypted.txt"

if ! diff -q "$work/expected.txt" "$work/decrypted.txt" >/dev/null; then
  echo "age opened the archive and read something else:" >&2
  diff "$work/expected.txt" "$work/decrypted.txt" >&2 || true

  exit 1
fi

echo "  ok    the standard age tool read an archive koffr wrote" >&2
