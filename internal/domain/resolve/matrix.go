package resolve

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrNoCompatibleTool — nothing found can do the job. koffr never falls back on
// an incompatible tool: an archive of unknown provenance is worse than a backup
// that did not happen and said why (E-042, P3).
var ErrNoCompatibleTool = errors.New("no compatible tool")

// Fits applies the compatibility matrix of § 5.2.
//
//   - PostgreSQL, dump:    major(pg_dump) ≥ major(server)  — pg_dump backs up an
//     older server, never a newer one: it refuses and exits.
//   - PostgreSQL, restore: major(pg_restore) ≥ major(archive) — the archive
//     format moves on, and an old pg_restore cannot read a recent one.
//   - MySQL, MariaDB:      same family, and version(client) ≥ version(server) —
//     authentication plugins and options diverge between the two families, and
//     E-041 makes crossing them the one mistake koffr must never make.
//
// reference is the server for a dump, and the archive for a restore.
func Fits(candidate Candidate, reference ServerInfo, tool Tool) bool {
	if candidate.Family != reference.Family {
		return false
	}
	if candidate.Tool != tool {
		return false
	}

	if candidate.Family == PostgreSQL {
		return candidate.Version.Major >= reference.Version.Major
	}

	// MySQL and MariaDB compare more than the major: 10.6 and 10.11 are both
	// tens, and a 10.6 client does not speak for a 10.11 server.
	return compare(candidate.Version, reference.Version) >= 0
}

// WarnIfDownward reports what to tell an operator restoring into an older
// major. § 5.2 says to signal it and never to block it: the target may be a
// test instance, and that is their call to make (E-049).
func WarnIfDownward(origin, target Version) string {
	if target.Major >= origin.Major {
		return ""
	}

	return fmt.Sprintf(
		"the archive comes from PostgreSQL %d and the target runs %d: restoring into an older major is not guaranteed",
		origin.Major, target.Major,
	)
}

// Choose applies the rule the § 5.2 insists on: **compatibility comes before
// provenance**. Among the candidates that fit, the closest version from above
// wins; provenance breaks a tie and nothing else.
//
// The trap this avoids is named in the specification: the host has pg_dump 14,
// the database runs 16, koffr already installed a 16 — and an agent that
// prefers "the host tools" picks the 14.
func Choose(candidates []Candidate, reference ServerInfo, tool Tool) (Candidate, error) {
	var fitting []Candidate

	for _, candidate := range candidates {
		if Fits(candidate, reference, tool) {
			fitting = append(fitting, candidate)
		}
	}

	if len(fitting) == 0 {
		return Candidate{}, noCompatibleTool(candidates, reference, tool)
	}

	slices.SortFunc(fitting, func(a, b Candidate) int {
		// Closest from above first.
		if by := compare(a.Version, b.Version); by != 0 {
			return by
		}

		// Then, and only then, provenance: host, managed, container.
		return sourceRank(a.Source) - sourceRank(b.Source)
	})

	return fitting[0], nil
}

// sourceRank orders provenance for a tie-break, in the order § 5.2 gives.
func sourceRank(source Source) int {
	switch source {
	case Host:
		return 0
	case Managed:
		return 1
	case Container:
		return 2
	default:
		return 3
	}
}

// compare orders two versions, major then minor then patch.
func compare(a, b Version) int {
	if by := a.Major - b.Major; by != 0 {
		return by
	}
	if by := a.Minor - b.Minor; by != 0 {
		return by
	}

	return a.Patch - b.Patch
}

// noCompatibleTool builds the message an operator reads when nothing can do the
// job: what koffr expected, and what it found instead. It stops there — the
// managed install is out of the MVP (ADR-0014), so koffr has no command to
// offer, and installing a client is a prerequisite the README carries.
func noCompatibleTool(candidates []Candidate, reference ServerInfo, tool Tool) error {
	found := "none"
	if versions := versionsOf(candidates); versions != "" {
		found = versions
	}

	return fmt.Errorf(
		"%w to %s %s %s: expected a %s client of version %d or later, found %s",
		ErrNoCompatibleTool,
		tool, reference.Family, reference.Version,
		reference.Family, reference.Version.Major,
		found,
	)
}

func versionsOf(candidates []Candidate) string {
	seen := make([]string, 0, len(candidates))

	for _, candidate := range candidates {
		rendered := fmt.Sprintf("%s %s (%s)", candidate.Family, candidate.Version, candidate.Source)
		if !slices.Contains(seen, rendered) {
			seen = append(seen, rendered)
		}
	}

	return strings.Join(seen, ", ")
}
