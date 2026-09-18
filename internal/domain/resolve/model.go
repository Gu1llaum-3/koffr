package resolve

import (
	"fmt"
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

func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}

	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// IsZero reports whether no version was read at all.
func (v Version) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0 && v.Raw == ""
}

// ParseVersion reads the leading numbers of a version string. It keeps what it
// was given: "11.4.8-MariaDB-ubu2404" parses to 11.4.8 and remembers the whole.
func ParseVersion(raw string) Version {
	version := Version{Raw: strings.TrimSpace(raw)}

	fields := strings.FieldsFunc(version.Raw, func(r rune) bool {
		return r < '0' || r > '9'
	})
	for i, field := range fields {
		if i > 2 {
			break
		}

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
}
