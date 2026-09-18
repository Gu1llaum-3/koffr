# koffr

An autonomous database backup agent for self-hosted PostgreSQL, MySQL and MariaDB fleets.

A single static Go binary. No Docker requirement, no mandatory central server, no service
dependency: local state lives in SQLite, archives are compressed with zstd and encrypted with
[age](https://age-encryption.org), and every archive stays readable with the standard `age` tool
alone — without koffr.

## Status

**Early development.** Lot 0 (skeleton, tooling and the tool-linking spike) is in progress; no
backup is taken yet. The `version` command is the only thing this binary does today.

## Requirements

- Linux (`amd64`, `arm64`) or macOS (`arm64`) — Windows is out of scope
- Go 1.27 to build from source
- The dump and restore tools of the engines you back up, either on the host, managed by koffr, or
  reachable in a container

## Build

```sh
mise install          # Go and golangci-lint, pinned in mise.toml
mise run build        # static binary, CGO_ENABLED=0
mise run verify       # vet, lint, tests and build — green before any commit on main
```

## Configure

`examples/koffr.yaml` is a complete, working configuration — the one koffr's own
tests are run against, so it cannot quietly drift from what koffr accepts.

```sh
cp examples/koffr.yaml /etc/koffr/koffr.yaml
koffr config validate              # reads the secrets it points at
koffr config validate --offline    # checks the shape only, on a machine that holds none
koffr config show                  # prints it with every secret masked
```

Parsing is strict: an unknown key is an error naming the key, its line and its section.
Every sensitive field takes three forms — a literal value, `*_env`, or `*_file`.

## Documentation

The product speaks English; the project is steered in French (ADR-0003). Contributor and steering
documents are therefore in French:

| Document | What it holds |
| --- | --- |
| [`ROADMAP.md`](ROADMAP.md) | One line per lot, its scope and its exit criterion |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Package layout and the dependency rules the lint enforces |
| [`METHODE.md`](METHODE.md) | How the project is run: cycle, registers, definition of done |
| [`docs/adr/`](docs/adr/) | Frozen decisions; changed by a new ADR, never worked around in code |
| [`docs/cdc/`](docs/cdc/) | The specification and its `E-nnn` requirement register |
| [`docs/README.md`](docs/README.md) | Index of the documentation and table of registers |

## License

Apache-2.0 — see [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
