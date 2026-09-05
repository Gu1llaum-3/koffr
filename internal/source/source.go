// Package source turns a database engine into a backup stream.
//
// A source never writes to disk and never uploads: it hands back a reader and
// lets the pipeline decide what happens to the bytes (PD-003).
package source

import (
	"context"
	"io"

	"github.com/Gu1llaum-3/koffr/internal/executor"
)

// Engine identifies a database engine.
type Engine string

const (
	EnginePostgreSQL Engine = "postgresql"
	EngineMariaDB    Engine = "mariadb"
)

// Kind identifies a backup type.
type Kind string

const (
	KindPhysical    Kind = "physical"
	KindIncremental Kind = "incremental"
	KindLogical     Kind = "logical"
	KindWAL         Kind = "wal"
	KindBinlog      Kind = "binlog"
)

// Codec identifies the compression already applied to a stream.
type Codec string

const (
	CodecNone Codec = "none"
	CodecZstd Codec = "zstd"
	CodecGzip Codec = "gzip"
)

// Source knows how to make one database engine emit a backup stream.
type Source interface {
	// Probe connects, reads the server version and settings, and reports which
	// backup kinds are actually available. Called at configuration validation
	// time so an impossible request fails early (PD-006, EF-005).
	Probe(ctx context.Context, ex executor.Executor) (Info, error)

	// Open starts the backup. The returned Stream owns a running process; the
	// caller must always Close it, which reaps that process.
	Open(ctx context.Context, ex executor.Executor, req Request) (*Stream, error)
}

// Info is what a Probe found out about a source.
type Info struct {
	Engine        Engine
	ServerVersion string

	// Kinds lists what this source can actually produce right now.
	Kinds []Kind

	// Restrictions explains, in human-readable form, why a kind is missing.
	// It is shown to the operator; it is never parsed.
	Restrictions []string

	// Databases names what a backup of this source covers. It describes the
	// content, so it goes into the encrypted details rather than the plaintext
	// manifest (EF-055).
	Databases []string
}

// Request asks for one specific backup.
type Request struct {
	Kind Kind

	// Label is recorded by the engine when it supports one.
	Label string

	// ParentManifest points at the parent backup manifest for an incremental
	// backup. Empty for every other kind.
	ParentManifest string

	// IncludeSchemas, ExcludeSchemas, IncludeTables and ExcludeTables apply to
	// logical backups only.
	IncludeSchemas []string
	ExcludeSchemas []string
	IncludeTables  []string
	ExcludeTables  []string
}

// Stream is a running backup in flight.
type Stream struct {
	// Reader yields the raw backup bytes.
	Reader io.Reader

	// Codec says what compression the engine already applied, so the pipeline
	// does not compress twice (EF-011 uses server-side compression).
	Codec Codec

	// Sidecars are small artifacts produced alongside the main stream, such as
	// a reconstructed backup_manifest. They are buffered in memory and are only
	// valid once the stream has been fully read.
	Sidecars func() (map[string][]byte, error)

	// Result is only valid after Close. It carries what only the engine knows.
	Result func() Result

	io.Closer
}

// Result carries the engine-specific positions a restore will need.
type Result struct {
	StartLSN   string // PostgreSQL
	EndLSN     string // PostgreSQL
	Timeline   int32  // PostgreSQL
	BinlogFile string // MariaDB
	BinlogPos  uint64 // MariaDB
	GTID       string // MariaDB
}
