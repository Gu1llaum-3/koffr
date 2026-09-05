#!/usr/bin/env bash
# Refuse to commit key material.
#
# This guard is not theoretical. During milestone M0 the probe rig generated an
# SSH private key and a TLS key under spikes/, and a `git add -A` staged both of
# them for a public repository. They were caught by reading `git status` output,
# which is not a control.
#
# Koffr holds database credentials and age identities by design, so the cost of
# one leaked key is high and the cost of this check is a few milliseconds.
#
# Scanning content rather than filenames is deliberate: a key pasted into a YAML
# file has no telltale name.
set -euo pipefail

# Patterns are anchored on the armour that key formats actually emit, so a
# document *about* keys does not trip them.
patterns=(
    '-----BEGIN [A-Z ]*PRIVATE KEY-----'
    'AGE-SECRET-KEY-1[0-9A-Z]{20,}'
    'PuTTY-User-Key-File-[0-9]'
    'aws_secret_access_key[[:space:]]*='
)

# Files whose whole purpose is to describe these patterns. Kept to an explicit
# list rather than a marker comment in the file: a marker anyone can add is a
# second --no-verify, and one escape hatch is enough.
allowlist_files=(
    'scripts/check-secrets.sh'
    'test/hooks/hooks_test.go'
)

staged=$(git diff --cached --name-only --diff-filter=ACM)
[ -z "$staged" ] && exit 0

found=0
while IFS= read -r file; do
    [ -z "$file" ] && continue

    skip=0
    for allowed in "${allowlist_files[@]}"; do
        [ "$file" = "$allowed" ] && skip=1
    done
    [ "$skip" -eq 1 ] && continue

    # Read the staged blob, not the working tree: they can differ.
    content=$(git show ":$file" 2>/dev/null || true)
    [ -z "$content" ] && continue

    for pattern in "${patterns[@]}"; do
        # -e is not optional: BSD grep reads a pattern starting with "-" as
        # an option, which made this check silently pass on exactly the armour
        # it exists to catch.
        if printf '%s' "$content" | grep -qE -e "$pattern"; then
            echo "refusing to commit $file: it contains something shaped like a private key"
            echo "    pattern: $pattern"
            found=1
        fi
    done
done <<< "$staged"

if [ "$found" -ne 0 ]; then
    cat >&2 <<'EOF'

No key material belongs in this repository.

If the file is throwaway probe material, add it to .gitignore; spikes/setup-keys.sh
regenerates everything the test rig needs.

If you are certain this is a false positive, commit with --no-verify and say why
in the commit message.
EOF
    exit 1
fi
