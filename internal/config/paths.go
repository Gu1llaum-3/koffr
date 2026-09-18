package config

import "path/filepath"

// The layout E-026 prescribes, as the specification § 4.3 draws it and ADR-0001
// renames it. These are defaults: --config and --state-dir move them, which is
// how koffr is developed on a machine where /etc/koffr does not exist (N-11).
const (
	DefaultConfigFile = "/etc/koffr/koffr.yaml"
	DefaultStateDir   = "/var/lib/koffr"
	DefaultLogDir     = "/var/log/koffr"
)

// Names of what hangs from those directories.
const (
	recipientsName = "recipients.txt"
	databaseName   = "koffr.db"
	toolsName      = "tools"
	tmpName        = "tmp"
	logName        = "koffr.log"
)

// Paths says where koffr reads and writes. Everything derives from three
// directories, so that overriding one moves what belongs to it and nothing
// else.
type Paths struct {
	ConfigFile string
	StateDir   string
	LogDir     string
}

// DefaultPaths is the production layout of E-026.
func DefaultPaths() Paths {
	return Paths{
		ConfigFile: DefaultConfigFile,
		StateDir:   DefaultStateDir,
		LogDir:     DefaultLogDir,
	}
}

// RecipientsFile is the list of age public keys, next to the configuration.
func (p Paths) RecipientsFile() string {
	return filepath.Join(filepath.Dir(p.ConfigFile), recipientsName)
}

// DatabaseFile is the local SQLite state.
func (p Paths) DatabaseFile() string {
	return filepath.Join(p.StateDir, databaseName)
}

// ToolsDir holds the dump tools koffr installed itself. A bundle is patched for
// the absolute path it will run from, so this directory is not one to move
// without rebuilding what is in it (spike E-130).
func (p Paths) ToolsDir() string {
	return filepath.Join(p.StateDir, toolsName)
}

// TmpDir is the working space of the jobs, cleared when the state opens.
func (p Paths) TmpDir() string {
	return filepath.Join(p.StateDir, tmpName)
}

// LogFile is the application log, rotated by koffr itself (E-121).
func (p Paths) LogFile() string {
	return filepath.Join(p.LogDir, logName)
}
