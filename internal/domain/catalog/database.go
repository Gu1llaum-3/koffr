package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Database is the snapshot of a **resolved** configuration that the catalogue
// keeps, so that a change can be noticed later (E-028, § 4.4).
//
// It carries no credential, and it is not meant to: what it is for is to say
// what koffr was told to back up, and to fingerprint it. `E-115` keeps
// passwords, secret paths and key files out of the local state as well as out
// of the manifest.
type Database struct {
	ID     string `json:"id"`
	Engine string `json:"engine"`
	Host   string `json:"host"`
	Port   int    `json:"port"`

	// Name is the database on the server. The field of the schema is called
	// "database"; here it is Name, because catalog.Database.Database reads
	// like a mistake.
	Name string `json:"database"`
	User string `json:"user"`

	Destinations []string  `json:"destinations"`
	Staging      string    `json:"staging"`
	Schedule     string    `json:"schedule"`
	Retention    Retention `json:"retention"`

	// Container is set when this database resolves its tool inside one — the
	// exec strategy, which changes what a backup runs (ADR-0015).
	Container string `json:"container,omitempty"`
}

// Retention is the grandfather-father-son policy, kept in the snapshot because
// changing it changes what a backup is worth.
type Retention struct {
	Last    int `json:"last"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// Resolved is the snapshot as it is stored: JSON, deterministic — the fields
// come out in the order of the type, never from a map — and without a secret.
func (d Database) Resolved() string {
	written, err := json.Marshal(d)
	if err != nil {
		// A struct of strings and integers does not fail to marshal; returning
		// an empty snapshot would be worse than saying so in the fingerprint.
		return "{}"
	}

	return string(written)
}

// Fingerprint is the SHA-256 of that snapshot. Two identical configurations
// give the same one, and any change that matters changes it.
func (d Database) Fingerprint() string {
	sum := sha256.Sum256([]byte(d.Resolved()))

	return hex.EncodeToString(sum[:])
}
