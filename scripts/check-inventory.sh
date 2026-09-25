#!/usr/bin/env bash
#
# Le critère de sortie du lot 3 : un dépôt d'archives s'inventorie **sans koffr
# et sans la clé privée**, à partir des seuls manifestes (`E-059`, `E-114`).
#
# C'est la promesse que personne ne peut vérifier avec nos propres outils : le
# test Go dépose un vrai dépôt, et ce script l'inventorie avec `jq` seul — le
# même marché que `N-8` du lot 2, employé pour la cinquième fois.
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v jq >/dev/null 2>&1; then
  if [ "${KOFFR_REQUIRE_AGE:-0}" = "1" ]; then
    echo "inventory: REQUIRED here and jq is missing." >&2
    echo "  E-059 says a repository is inventoried without koffr; jq is how we check it." >&2

    exit 1
  fi

  echo "inventory: skipped — jq is not installed (apt install jq)." >&2

  exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "inventory: on (jq $(jq --version))" >&2

go test -C "$ROOT" ./internal/cli/ -run TestWriteTheInventoryFixture \
  -count=1 -timeout 15m -args -fixture-dir="$work" >/dev/null

if [ ! -d "$work/postgresql" ]; then
  if [ "${KOFFR_REQUIRE_DOCKER:-0}" = "1" ]; then
    echo "inventory: REQUIRED here and no repository was produced (Docker?)." >&2

    exit 1
  fi

  echo "inventory: skipped — the tests produced no repository (no Docker?)." >&2

  exit 0
fi

# The inventory itself: no koffr, no key, just the manifests.
manifests=$(find "$work" -name '*.json' | sort)
count=$(echo "$manifests" | grep -c . || true)

[ "$count" -ge 2 ] || { echo "  only $count manifest(s) found, want one per engine" >&2; exit 1; }

echo "  the repository, inventoried without koffr:" >&2
printf '  %-12s %-28s %10s  %s\n' DATABASE ARCHIVE STORED VERIFIED >&2

while read -r manifest; do
  line=$(jq -r '[.database_id, .backup_id, (.size_stored|tostring),
                 (if .verified.checksum and .verified.structure then "yes" else "NO" end)] | @tsv' \
           "$manifest")
  # shellcheck disable=SC2086
  printf '  %-12s %-28s %10s  %s\n' $line >&2

  # Every manifest must carry enough to decide what to restore.
  for field in backup_id database_id engine started_at size_stored sha256_stored format pipeline recipients; do
    jq -e --arg f "$field" 'has($f)' "$manifest" >/dev/null ||
      { echo "  $manifest has no $field" >&2; exit 1; }
  done

  # E-114 — metadata, and nothing that opens a database.
  for forbidden in password user host port secret_key private; do
    jq -e --arg f "$forbidden" 'has($f)' "$manifest" >/dev/null 2>&1 &&
      { echo "  $manifest declares $forbidden" >&2; exit 1; }
  done
done <<<"$manifests"

# And the archive it describes is there, at the size it announces.
while read -r manifest; do
  archive="${manifest%.json}"
  [ -f "$archive" ] || { echo "  $manifest describes an archive that is not there" >&2; exit 1; }

  announced=$(jq -r '.size_stored' "$manifest")
  actual=$(wc -c <"$archive" | tr -d ' ')
  [ "$announced" = "$actual" ] ||
    { echo "  $manifest announces $announced bytes, the archive has $actual" >&2; exit 1; }
done <<<"$manifests"

echo "  ok    $count archives inventoried with jq alone, without koffr and without a private key" >&2
