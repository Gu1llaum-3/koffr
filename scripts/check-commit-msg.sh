#!/usr/bin/env bash
# Check the mechanical parts of a commit message, and only those.
#
# Style is a matter for review; layout is not. A subject that wraps in `git log
# --oneline`, or a body glued to its subject, degrades the history for everyone
# reading it later, and neither is a judgement call.
set -euo pipefail

msg_file="$1"
[ -f "$msg_file" ] || exit 0

# Ignore comment lines and everything git appends after the scissors line.
body=$(sed '/^# ------------------------ >8 ------------------------$/,$d' "$msg_file" | grep -v '^#' || true)
subject=$(printf '%s' "$body" | head -n 1)

# A merge, revert or fixup commit has a message git wrote; leave it alone.
case "$subject" in
    Merge*|Revert*|fixup!*|squash!*|"") exit 0 ;;
esac

fail=0
err() { echo "commit message: $1" >&2; fail=1; }

if [ "${#subject}" -gt 72 ]; then
    err "subject is ${#subject} characters, keep it to 72 so it does not wrap in git log"
fi

case "$subject" in
    *.) err "subject ends with a period; it is a title, not a sentence" ;;
esac

second=$(printf '%s' "$body" | sed -n '2p')
if [ -n "$second" ]; then
    err "line 2 must be blank to separate the subject from the body"
fi

if [ "$fail" -ne 0 ]; then
    echo >&2
    echo "Subject was: $subject" >&2
    exit 1
fi
