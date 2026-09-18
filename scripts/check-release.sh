#!/usr/bin/env bash
#
# E-117, as ADR-0011 amended it: a static binary built with CGO_ENABLED=0, for
# linux/amd64, linux/arm64 and darwin/arm64, each under 30 MB.
#
# What this checks, and what each check is worth:
#   1. no dependency pulls in cgo, and mattn/go-sqlite3 is nowhere near — that
#      is E-027 and the reason CGO_ENABLED=0 holds at all;
#   2. every target builds with CGO_ENABLED=0;
#   3. every binary is under the size the threshold allows — the CI fails here,
#      which is the only way a size limit means anything;
#   4. every LINUX binary carries no ELF interpreter, which is what "static"
#      means: it runs on a host with no libc at all, musl included.
#
# darwin is deliberately exempt from check 4. macOS has no stable syscall ABI,
# so a Go binary always links /usr/lib/libSystem.B.dylib whatever CGO_ENABLED
# says. "Static" there means "no cgo", not "no dynamic library" (N-17).
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUT="${ROOT}/dist"
readonly PACKAGE="github.com/Gu1llaum-3/koffr/internal/build"

# 30 MB, per E-117. Overridable so that the threshold itself can be tested.
readonly MAX_BYTES="${KOFFR_MAX_BINARY_BYTES:-31457280}"

readonly TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/arm64"
)

fail() { printf '\n  FAIL  %s\n' "$*" >&2; exit 1; }
ok()   { printf '  ok    %s\n' "$*"; }

# 1 — nothing in the dependency graph needs cgo.
#
# The graph is listed for the RELEASE build, not for this machine: with cgo
# enabled — the default on a Linux host that has a compiler — net pulls
# runtime/cgo for the system resolver, and the check would fail on a repository
# that is perfectly fine. What matters is the graph koffr actually ships.
check_dependencies() {
  printf '\ndependencies\n'

  local target os arch deps
  for target in "${TARGETS[@]}"; do
    os="${target%%/*}"
    arch="${target##*/}"

    deps=$(cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go list -deps ./cmd/koffr)

    if grep -q 'mattn/go-sqlite3' <<<"$deps"; then
      fail "$target: mattn/go-sqlite3 is in the dependency graph — E-027 forbids it and it needs CGO"
    fi
    if grep -qx 'runtime/cgo' <<<"$deps"; then
      fail "$target: runtime/cgo is in the release graph — something requires CGO"
    fi
    ok "$target: no mattn/go-sqlite3, no runtime/cgo"
  done
}

# 4 — a static ELF has no PT_INTERP. Read straight out of the file, so that the
# check works for an architecture this machine cannot execute.
has_elf_interpreter() {
  python3 - "$1" <<'PY'
import struct, sys

with open(sys.argv[1], "rb") as binary:
    header = binary.read(64)
    if header[:4] != b"\x7fELF":
        sys.exit(2)  # not an ELF: the caller decides what that means

    little = header[5] == 1
    endian = "<" if little else ">"

    # e_phoff, e_phentsize, e_phnum for the 64-bit layout
    phoff = struct.unpack_from(endian + "Q", header, 32)[0]
    phentsize = struct.unpack_from(endian + "H", header, 54)[0]
    phnum = struct.unpack_from(endian + "H", header, 56)[0]

    binary.seek(phoff)
    for _ in range(phnum):
        entry = binary.read(phentsize)
        p_type = struct.unpack_from(endian + "I", entry, 0)[0]
        if p_type == 3:  # PT_INTERP
            sys.exit(0)

sys.exit(1)
PY
}

build_and_check() {
  local target="$1"
  local os="${target%%/*}" arch="${target##*/}"
  local binary="${OUT}/koffr-${os}-${arch}"

  printf '\n%s\n' "$target"

  local version commit date
  version="${KOFFR_VERSION:-dev}"
  commit="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -C "$ROOT" \
    -trimpath \
    -ldflags "-s -w -X ${PACKAGE}.version=${version} -X ${PACKAGE}.commit=${commit} -X ${PACKAGE}.date=${date}" \
    -o "$binary" ./cmd/koffr \
    || fail "$target does not build with CGO_ENABLED=0"
  ok "builds with CGO_ENABLED=0"

  local size
  size=$(wc -c <"$binary" | tr -d ' ')
  printf '  size  %s bytes (%s MiB), threshold %s bytes\n' \
    "$size" "$(python3 -c "print(f'{$size/1048576:.1f}')")" "$MAX_BYTES"
  if [ "$size" -gt "$MAX_BYTES" ]; then
    fail "$target is $size bytes, over the threshold of $MAX_BYTES (E-117)"
  fi
  ok "under the threshold"

  if [ "$os" = "linux" ]; then
    if has_elf_interpreter "$binary"; then
      fail "$target carries an ELF interpreter: it is dynamically linked, not static"
    fi
    ok "no ELF interpreter: runs on a host with no libc, musl included"
  else
    ok "darwin links libSystem by design, whatever CGO_ENABLED says (N-17)"
  fi
}

main() {
  mkdir -p "$OUT"
  check_dependencies

  for target in "${TARGETS[@]}"; do
    build_and_check "$target"
  done

  printf '\nthree targets built, all under %s bytes\n' "$MAX_BYTES"
}

main "$@"
