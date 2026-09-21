package resolve

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Family is an engine family. MySQL and MariaDB are two families, not two
// versions of one: E-041 makes never confusing them a hard point of the
// product, because their dump tools are not interchangeable.
type Family string

// The three families of the § 3 scope (ADR-0004).
const (
	PostgreSQL Family = "postgresql"
	MySQL      Family = "mysql"
	MariaDB    Family = "mariadb"
)

// Version is a server or tool version. The compatibility matrix of § 5.2
// reasons in major versions; the rest is kept for the operator to read.
type Version struct {
	Major int
	Minor int
	Patch int

	// Raw is what the server or the tool actually said, kept verbatim so that
	// an error message can quote it rather than a reconstruction.
	Raw string
}

// String renders the numbers, which is what a column and a comparison need.
// What the tool actually announced is in Raw, for an error that wants to quote
// it — "mariadb-dump from 12.0.2-MariaDB, client 10.19 for osx10.20 (arm64)"
// belongs in a message, not in a table.
func (v Version) String() string {
	if v.Patch != 0 {
		return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	}

	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// IsZero reports whether no number could be read. Raw is not part of the
// question: it holds what was announced, precisely so that an error can quote
// the unreadable thing.
func (v Version) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0
}

// versionNumbers matches the first dotted number group of a version string.
// Reading *every* number instead would turn "16.10 (Debian 16.10-1.pgdg13+1)"
// into 16.10.16, because the build metadata is full of digits.
var versionNumbers = regexp.MustCompile(`\d+(?:\.\d+){0,2}`)

// ParseVersion reads the version out of what a server or a tool announces, and
// keeps the whole announcement. "11.4.8-MariaDB-ubu2404" is 11.4.8 and stays
// quotable; "pg_dump (PostgreSQL) 15.4" is 15.4.
func ParseVersion(raw string) Version {
	version := Version{Raw: strings.TrimSpace(raw)}

	for i, field := range strings.Split(versionNumbers.FindString(version.Raw), ".") {
		number, err := strconv.Atoi(field)
		if err != nil {
			break
		}

		switch i {
		case 0:
			version.Major = number
		case 1:
			version.Minor = number
		case 2:
			version.Patch = number
		}
	}

	return version
}

// Target is a database to reach. It is built from the configuration by the
// wiring; the domain never reads a file to obtain one.
type Target struct {
	// Engine is what the operator declared. The probe reports what the server
	// actually is, which is what E-041 makes authoritative.
	Engine   Family
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

// String never prints the password: a Target ends up in error messages and log
// lines, and E-115 wants neither to carry a credential.
func (t Target) String() string {
	return fmt.Sprintf("%s://%s@%s:%d/%s", t.Engine, t.User, t.Host, t.Port, t.Database)
}

// ServerInfo is what a probe learned about a server. Nothing here is taken from
// the configuration: the family and the version come from the server itself.
type ServerInfo struct {
	Reachable bool
	Family    Family
	Version   Version

	// MyISAMTables are the tables of this database that MyISAM holds.
	// --single-transaction promises nothing about them: they are dumped
	// outside the snapshot, so the archive can be inconsistent (E-056).
	MyISAMTables []string

	// DatabaseBytes is how much this database occupies on the server, as the
	// server reports it. It is the fallback of E-061: with no previous backup
	// to extrapolate from, it is all koffr has to decide whether staging would
	// fill the volume. Zero means koffr could not tell.
	DatabaseBytes int64

	// MyISAMUnknown says why koffr could not tell, when it could not — an
	// account that cannot read the catalogue, say. Silence about a question
	// koffr failed to ask is not an answer.
	MyISAMUnknown string
}

// MyISAMWarning is what an operator has to be told about the consistency of the
// archives of this database, and the empty string when there is nothing to
// tell. A warning that is always there is a warning nobody reads.
func (s ServerInfo) MyISAMWarning() string {
	switch {
	case s.MyISAMUnknown != "":
		return "koffr could not check this database for MyISAM tables, so the consistency of " +
			"its archives is unknown: " + s.MyISAMUnknown

	case len(s.MyISAMTables) > 0:
		return fmt.Sprintf("%d table(s) of this database use MyISAM (%s): --single-transaction "+
			"does not cover them, so an archive may catch them mid-change",
			len(s.MyISAMTables), strings.Join(s.MyISAMTables, ", "))

	default:
		return ""
	}
}
