// Package manifest describes what a backup contains.
//
// Two artifacts, deliberately split (EF-055):
//
//	manifest.json      plaintext: identity, timings, sizes, digests, recipients.
//	                   Enough to list, chain and prune without holding any key,
//	                   which is what lets a write-only node apply retention and
//	                   what lets a lost catalog be rebuilt (EF-143).
//	details.json.age   encrypted: database and relation names, i.e. everything
//	                   that says something about the content.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// FormatVersion is bumped only when an older Koffr could not read a manifest
// correctly. A newer one adding a field does not need a bump: decoding ignores
// unknown keys.
const FormatVersion = 1

// Manifest is the plaintext description of one backup.
//
// Field order here is the field order in the file. Adding a field means
// deciding it is safe in plaintext; TestManifestJSON_TopLevelKeysAreDeliberate
// exists to force that decision to be conscious.
type Manifest struct {
	FormatVersion int    `json:"format_version"`
	BackupID      string `json:"backup_id"`
	SourceID      string `json:"source_id"`
	Engine        string `json:"engine"`
	ServerVersion string `json:"server_version"`
	Kind          string `json:"kind"`

	// ParentID is the backup an incremental depends on, null otherwise.
	// Retention walks it and refuses to break a chain (EF-062).
	ParentID *string `json:"parent_id"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`

	Objects []Object `json:"objects"`
	Tool    Tool     `json:"tool"`

	KoffrVersion string `json:"koffr_version"`
}

// Object is one stored artifact.
type Object struct {
	Key string `json:"key"`
	// SizeBytes is the size as stored, after compression and encryption.
	SizeBytes int64 `json:"size_bytes"`
	// SHA256 covers the CIPHERTEXT, so integrity can be checked without any
	// key at all (EF-053).
	SHA256     string   `json:"sha256"`
	Codec      string   `json:"codec"`
	Encryption string   `json:"encryption"`
	Recipients []string `json:"recipients"`
}

// Tool records which binary produced the stream.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// ArgsDigest replaces the arguments themselves. A command line can carry a
	// revealing path, a hostname, or a credential someone passed the wrong way;
	// a digest is enough to notice that a backup was taken with different
	// options from its predecessors.
	ArgsDigest string `json:"args_digest"`
}

// ToolFrom builds a Tool, digesting the arguments rather than keeping them.
func ToolFrom(name, version string, args []string) Tool {
	// NUL separation so ["a", "bc"] and ["ab", "c"] do not collide.
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return Tool{
		Name:       name,
		Version:    version,
		ArgsDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

// Details is the encrypted companion: everything that describes the content.
type Details struct {
	Databases []string   `json:"databases,omitempty"`
	Relations []Relation `json:"relations,omitempty"`
}

// Relation is one table or index in the backup.
type Relation struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// requiredFields are the keys without which a manifest cannot be interpreted.
// Their absence names the missing key, because the operator reading the error
// is usually looking at the file.
var requiredFields = []string{
	"format_version", "backup_id", "source_id", "engine", "kind", "started_at",
}

// Encode writes a manifest. Timestamps are normalised to UTC: a local offset
// would sort wrongly against sibling manifests and confuse a PITR target.
func Encode(w io.Writer, m Manifest) error {
	m.StartedAt = m.StartedAt.UTC()
	m.FinishedAt = m.FinishedAt.UTC()

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// Decode reads a manifest, rejecting a format it cannot interpret and a file
// missing a field it needs.
func Decode(r io.Reader) (Manifest, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}

	// Presence is checked separately from decoding: a missing field and a field
	// set to its zero value are different problems, and only one is an error.
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	for _, f := range requiredFields {
		if _, ok := present[f]; !ok {
			return Manifest{}, fmt.Errorf("manifest is missing required field %q", f)
		}
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if m.FormatVersion > FormatVersion {
		return Manifest{}, fmt.Errorf(
			"manifest format version %d was written by a newer Koffr; this build understands up to %d",
			m.FormatVersion, FormatVersion)
	}
	return m, nil
}

// EncodeDetails writes the encrypted companion's plaintext form. The caller is
// responsible for sealing it (EF-055).
func EncodeDetails(w io.Writer, d Details) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode details: %w", err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write details: %w", err)
	}
	return nil
}

// DecodeDetails reads the encrypted companion's plaintext form.
func DecodeDetails(r io.Reader) (Details, error) {
	var d Details
	if err := json.NewDecoder(r).Decode(&d); err != nil {
		return Details{}, fmt.Errorf("parse details: %w", err)
	}
	return d, nil
}

// Validate checks the invariants that make a manifest usable for a restore.
//
// It is not schema validation: it asserts the two properties whose absence is
// silently fatal, namely that integrity can be checked and that the objects can
// be opened at all.
func (m Manifest) Validate() error {
	if len(m.Objects) == 0 {
		return fmt.Errorf("manifest %s lists no objects", m.BackupID)
	}
	for _, o := range m.Objects {
		if len(o.SHA256) != 64 || !isHex(o.SHA256) {
			return fmt.Errorf("object %q has digest %q, want 64 hex characters", o.Key, o.SHA256)
		}
		if o.Encryption != "" && o.Encryption != "none" && len(o.Recipients) == 0 {
			return fmt.Errorf("object %q is encrypted but lists no recipients, so it cannot be opened", o.Key)
		}
	}
	return nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
