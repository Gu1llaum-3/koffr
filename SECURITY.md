# Security Policy

## Reporting a vulnerability

Please report security issues privately through GitHub's private vulnerability
reporting on this repository, rather than by opening a public issue.

Include the affected version, a description of the impact, and the minimal steps
needed to reproduce the problem. You will get an acknowledgement within a few
days.

## Scope

Koffr holds credentials for the databases it backs up and, depending on the
configured key model, may hold a key able to decrypt stored backups. The
following are considered security issues:

- any path that writes a credential to a log, an error message, a process
  argument list, or a world-readable file;
- any path that stores backup data unencrypted when encryption is configured;
- any path that lets a backup be silently truncated, corrupted, or reported as
  successful when it is not restorable;
- any retention or pruning path that deletes a backup still required to restore
  another one;
- bypassing SSH host key verification, or TLS certificate verification, without
  the operator explicitly asking for it.

## Design assumptions

These are documented properties rather than vulnerabilities:

- A host running a verification restore must be able to decrypt backups. That
  host is a trust boundary.
- The web UI has no authentication of its own. It binds to the loopback
  interface and delegates access control to SSH or to a reverse proxy.
- An operational key stored on the Koffr host is readable by anyone who
  compromises that host. This is why configuring an offline recovery recipient
  is mandatory.
