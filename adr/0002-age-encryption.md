# ADR-0002 — Use age for encryption

- **Status**: Accepted
- **Date**: 2026-09-05
- **Relates to**: PD-001, PD-004, DEC-002, EF-050, EF-051

## Context

Backups leave the machine that produced them and land on storage the operator
does not fully control. They must be encrypted, and the encryption must still be
reversible years later, possibly by someone who no longer has Koffr.

The reference projects differ: wal-g supports libsodium, OpenPGP and KMS
envelopes; Databasus uses AES-256-GCM of its own construction.

A hand-rolled AES-GCM envelope means implementing key wrapping, multiple
recipients and chunked authenticated streaming — which is to say, reimplementing
age, with no independent implementation to fall back on.

## Decision

Encrypt with age: X25519 key agreement, ChaCha20-Poly1305, STREAM construction.
Every object is encrypted for **at least two recipients** — the operational key
held by Koffr, and an offline recovery key Koffr never holds. Configuring a
single recipient is a configuration error, not a warning.

## Consequences

- `age -d -i key.txt < base.tar.zst.age | zstd -d | tar -x` works anywhere, with
  no Koffr binary. This is what makes PD-001 true for encrypted backups.
- Envelope encryption and multi-recipient support come from the format; neither
  is implemented here.
- STREAM marks the final chunk, so a truncated object is detected on read rather
  than silently accepted as a short backup.
- Hardware-backed recovery keys work through the existing age plugins
  (`age-plugin-yubikey`, `age-plugin-fido2-hmac`) with no code of our own.
- **The price**: Koffr holds a key able to decrypt backups, because verification
  by real restore requires it. Compromising the Koffr host exposes the backups
  it can read. The mandatory offline recipient is what survives that compromise.
- Compression must happen before encryption; ciphertext does not compress.

## Alternatives rejected

- **Hand-rolled AES-256-GCM** — reimplements age, worse, with no interoperable
  decryptor.
- **OpenPGP** — interoperable, but a large and awkward format for streaming, and
  a much bigger implementation surface.
- **Asymmetric-only, with Koffr holding no private key** — the strongest option,
  and it makes automated verification by real restore impossible. Kept as an
  open question (OPEN-003) for a write-only repository mode.
- **Encryption delegated to the storage backend (SSE)** — the provider holds the
  key, which defeats the purpose.
