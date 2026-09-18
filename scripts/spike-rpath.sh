#!/usr/bin/env bash
#
# Spike E-130 — can koffr ship a dump tool with its shared libraries and run it
# on a host that has neither the tool nor a matching libc?
#
# The question behind E-043 (managed tools): koffr downloads a bundle, unpacks
# it under /var/lib/koffr/tools/, and executes it. Nothing is installed by the
# package manager, so the bundle must carry every library the binary needs and
# be told where to find them.
#
# The method, per bundle:
#   1. take the binary out of an official image (glibc, Debian or Ubuntu);
#   2. copy every library ldd reports, plus the dynamic loader;
#   3. patchelf --set-interpreter to the bundled loader, and a runpath on the
#      binary AND on every bundled library (DT_RUNPATH is not inherited);
#   4. run `--version` inside Debian 12, Rocky 9 and Alpine 3.20.
#
# Usage: scripts/spike-rpath.sh [arm64|amd64|all]
set -euo pipefail

# The prefix is absolute on purpose: PT_INTERP is resolved by the kernel and
# does not expand $ORIGIN, so a bundle only runs from the path it was patched
# for. In production that path is /var/lib/koffr/tools/<tool>/<version>.
readonly PREFIX=/opt/koffr/tool

readonly WORK="${SPIKE_WORK:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.spike}"
readonly RESULTS="$WORK/results.tsv"

# tool name | source image | path of the binary inside that image
readonly TOOLS=(
  "pg_dump|postgres:16|/usr/lib/postgresql/16/bin/pg_dump"
  "mariadb-dump|mariadb:11.4|/usr/bin/mariadb-dump"
)

# distributions the bundle must run on, and what their libc is
readonly TARGETS=(
  "debian12|debian:12|glibc"
  "rocky9|rockylinux/rockylinux:9|glibc"
  "alpine320|alpine:3.20|musl"
)

log() { printf '\n=== %s\n' "$*" >&2; }

record() { printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" >>"$RESULTS"; }

# bundle <arch> <tool> <image> <binary path>
bundle() {
  local arch="$1" tool="$2" image="$3" binary="$4"
  local out="$WORK/$arch/$tool"

  log "bundle $tool ($arch) from $image"
  rm -rf "$out"
  mkdir -p "$out"

  docker run --rm --platform "linux/$arch" \
    -v "$out:/out" -e "BINARY=$binary" -e "PREFIX=$PREFIX" \
    "$image" bash -c '
      set -euo pipefail
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -qq
      apt-get install -y -qq --no-install-recommends patchelf >/dev/null

      mkdir -p /out/bin /out/lib
      cp -L "$BINARY" /out/bin/

      # ldd prints "name => /path (addr)" and the loader as a bare /path.
      ldd "$BINARY" | tr " " "\n" | grep "^/" | sort -u | while read -r lib; do
        cp -L "$lib" /out/lib/
      done

      interpreter=$(patchelf --print-interpreter "$BINARY")
      cp -L "$interpreter" /out/lib/

      name=$(basename "$BINARY")
      patchelf \
        --set-interpreter "$PREFIX/lib/$(basename "$interpreter")" \
        --set-rpath "\$ORIGIN/../lib" \
        "/out/bin/$name"

      # DT_RUNPATH applies to the object that declares it and is NOT inherited
      # by its dependencies: patching only the binary leaves libpq.so.5 looking
      # for libssl.so.3 in the system paths. Every bundled library therefore
      # gets its own runpath. The loader is left alone: patching it breaks it.
      for lib in /out/lib/*.so*; do
        case "$(basename "$lib")" in
          ld-linux*|ld64*|ld-musl*) continue ;;
        esac
        patchelf --set-rpath "\$ORIGIN" "$lib"
      done

      echo "--- bundled $name"
      patchelf --print-interpreter "/out/bin/$name"
      patchelf --print-rpath "/out/bin/$name"
      ls /out/lib | wc -l | xargs echo "libraries:"
    ' 2>&1 | sed 's/^/    /'
}

# probe <arch> <tool> <target name> <target image> <libc>
probe() {
  local arch="$1" tool="$2" target="$3" image="$4" libc="$5"
  local out="$WORK/$arch/$tool"

  log "run $tool ($arch) on $target ($libc)"

  local output status
  set +e
  output=$(docker run --rm --platform "linux/$arch" \
    -v "$out:$PREFIX:ro" "$image" "$PREFIX/bin/$tool" --version 2>&1)
  status=$?
  set -e

  printf '    exit=%s %s\n' "$status" "$output" >&2

  if [ "$status" -eq 0 ]; then
    record "$arch" "$tool" "$target" "ok" "${output//$'\n'/ }"
  else
    record "$arch" "$tool" "$target" "FAILED" "${output//$'\n'/ }"
  fi
}

# connect <arch> — the linking test only proves the loader is satisfied.
# A real dump resolves a host name, and glibc resolves names through NSS
# modules it dlopens at run time: those are NOT in the ldd closure, and a musl
# host has neither them nor /etc/nsswitch.conf. This probe therefore dumps a
# real server over TCP, by name, from each target.
connect() {
  local arch="$1"
  local out="$WORK/$arch/pg_dump"
  local network=koffr-spike
  local server=koffr-spike-pg

  log "connect pg_dump ($arch) to a real server, by host name"

  docker network create "$network" >/dev/null 2>&1 || true
  docker rm -f "$server" >/dev/null 2>&1 || true
  docker run -d --rm --platform "linux/$arch" --name "$server" \
    --network "$network" -e POSTGRES_PASSWORD=spike postgres:16 >/dev/null

  local ready=1
  for _ in $(seq 1 60); do
    if docker exec "$server" pg_isready -q -U postgres >/dev/null 2>&1; then
      ready=0
      break
    fi
    sleep 2
  done
  if [ "$ready" -ne 0 ]; then
    record "$arch" "pg_dump" "connect-setup" "SKIPPED" "the server never became ready"
    docker rm -f "$server" >/dev/null 2>&1 || true
    return
  fi

  for target in "${TARGETS[@]}"; do
    IFS='"'"'|'"'"' read -r name timage libc <<<"$target"

    local output status
    set +e
    output=$(docker run --rm --platform "linux/$arch" --network "$network" \
      -v "$out:$PREFIX:ro" -e PGPASSWORD=spike "$timage" \
      "$PREFIX/bin/pg_dump" -h "$server" -U postgres -d postgres --schema-only 2>&1)
    status=$?
    set -e

    printf '"'"'    connect %s: exit=%s\n'"'"' "$name" "$status" >&2
    if [ "$status" -eq 0 ]; then
      record "$arch" "pg_dump" "$name/connect" "ok" "dumped $(printf '"'"'%s'"'"' "$output" | wc -l | tr -d '"'"' '"'"') lines over tcp, by name"
    else
      record "$arch" "pg_dump" "$name/connect" "FAILED" "${output//$'"'"'\n'"'"'/ }"
    fi
  done

  docker rm -f "$server" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}

run_arch() {
  local arch="$1"

  for entry in "${TOOLS[@]}"; do
    IFS='|' read -r tool image binary <<<"$entry"
    bundle "$arch" "$tool" "$image" "$binary"

    for target in "${TARGETS[@]}"; do
      IFS='|' read -r name timage libc <<<"$target"
      probe "$arch" "$tool" "$name" "$timage" "$libc"
    done
  done

  connect "$arch"
}

main() {
  mkdir -p "$WORK"
  : >"$RESULTS"

  case "${1:-all}" in
    arm64) run_arch arm64 ;;
    amd64) run_arch amd64 ;;
    all) run_arch arm64; run_arch amd64 ;;
    *) echo "usage: $0 [arm64|amd64|all]" >&2; exit 2 ;;
  esac

  log "results"
  column -t -s $'\t' "$RESULTS"
}

main "$@"
