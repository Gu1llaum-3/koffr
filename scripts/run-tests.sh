#!/usr/bin/env bash
#
# The tests, with the race detector when the machine can run it.
#
# `go test -race` needs cgo, and cgo needs a C compiler. A fresh Linux machine
# that meets koffr's own prerequisites — mise, no Go — has none, so Go sets
# CGO_ENABLED=0 and `-race` fails outright. Blocking someone on that is a poor
# trade: the tests themselves run fine without it (A-01).
#
# So the detector is used when it is available, skipped loudly when it is not,
# and REQUIRED where the guarantee is actually measured: the CI sets
# KOFFR_REQUIRE_RACE=1, which turns a missing detector into a failure (N-2).
set -euo pipefail

# The detector needs both: cgo enabled, and a compiler Go can actually reach.
# Checking only CGO_ENABLED is not enough — it can be exported by hand on a
# machine that has no compiler at all.
race_is_available() {
  [ "$(go env CGO_ENABLED)" = "1" ] || return 1

  local compiler
  compiler=$(go env CC)

  command -v "$compiler" >/dev/null 2>&1
}

main() {
  if race_is_available; then
    echo "race detector: on ($(go env CC))" >&2

    exec go test ./... -race "$@"
  fi

  if [ "${KOFFR_REQUIRE_RACE:-0}" = "1" ]; then
    echo "race detector: REQUIRED here and unavailable." >&2
    echo "  CGO_ENABLED=$(go env CGO_ENABLED), CC=$(go env CC)" >&2
    echo "  Install a C toolchain (build-essential on Debian and Ubuntu)." >&2

    exit 1
  fi

  echo "race detector: off — no C toolchain, so cgo is disabled and -race cannot run." >&2
  echo "  The tests below run without it. The CI runs them with it, and fails without." >&2
  echo "  To get it here: install a C toolchain (build-essential on Debian and Ubuntu)." >&2

  exec go test ./... "$@"
}

main "$@"
