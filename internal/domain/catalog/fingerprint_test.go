package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// CAT-01 — the catalogue keeps a snapshot of the **resolved** configuration of
// each database, with a fingerprint, so that a change can be noticed (E-028).
// Two identical configurations give the same fingerprint.
func TestCAT01TheSameConfigurationGivesTheSameFingerprint(t *testing.T) {
	first, second := aDatabase(), aDatabase()

	if first.Fingerprint() != second.Fingerprint() {
		t.Errorf("two identical configurations fingerprint differently:\n %s\n %s",
			first.Fingerprint(), second.Fingerprint())
	}
	if first.Fingerprint() == "" {
		t.Error("the fingerprint is empty")
	}
}

// CAT-01 — and any change to what matters changes it. A backup taken against a
// configuration nobody can reconstruct is a backup nobody can explain.
func TestCAT01AnyChangeThatMattersChangesTheFingerprint(t *testing.T) {
	changes := map[string]func(*catalog.Database){
		"the port":        func(d *catalog.Database) { d.Port = 5433 },
		"the host":        func(d *catalog.Database) { d.Host = "10.0.3.13" },
		"the user":        func(d *catalog.Database) { d.User = "someone_else" },
		"the engine":      func(d *catalog.Database) { d.Engine = "mariadb" },
		"the destination": func(d *catalog.Database) { d.Destinations = []string{"offsite"} },
		"the staging":     func(d *catalog.Database) { d.Staging = "stream" },
		"the schedule":    func(d *catalog.Database) { d.Schedule = "0 4 * * *" },
		"the container":   func(d *catalog.Database) { d.Container = "erp-mariadb" },
	}

	reference := aDatabase().Fingerprint()

	for what, change := range changes {
		t.Run(what, func(t *testing.T) {
			changed := aDatabase()
			change(&changed)

			if changed.Fingerprint() == reference {
				t.Errorf("changing %s did not change the fingerprint", what)
			}
		})
	}
}

// CAT-01, E-115 — the snapshot is stored as JSON, and **no credential is in
// it**. The test looks for the values, and the emitted field names are
// enumerated: a field added later that carried a secret would fail here.
func TestCAT01TheSnapshotCarriesNoCredential(t *testing.T) {
	const password = "hunter2-the-database-password"

	database := aDatabase()
	snapshot := database.Resolved()

	if strings.Contains(snapshot, password) {
		t.Fatalf("a credential is in the snapshot:\n%s", snapshot)
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(snapshot), &fields); err != nil {
		t.Fatalf("the snapshot is not JSON: %v\n%s", err, snapshot)
	}

	allowed := map[string]bool{
		"id": true, "engine": true, "host": true, "port": true, "database": true,
		"user": true, "destinations": true, "staging": true, "schedule": true,
		"retention": true, "container": true,
	}

	for name := range fields {
		if !allowed[name] {
			t.Errorf("the snapshot carries %q, which is not on the declared list.\n"+
				"  Adding to it is a decision: E-115 keeps credentials and secret paths out.", name)
		}
	}

	// And a password field, even empty, has no business being there.
	for _, forbidden := range []string{"password", "password_file", "password_env", "recipients_file"} {
		if _, present := fields[forbidden]; present {
			t.Errorf("the snapshot declares %q", forbidden)
		}
	}
}

func aDatabase() catalog.Database {
	return catalog.Database{
		ID: "boutique", Engine: "postgresql", Host: "10.0.3.12", Port: 5432,
		Name: "boutique", User: "koffr_backup",
		Destinations: []string{"local"}, Staging: "auto", Schedule: "0 2 * * *",
		Retention: catalog.Retention{Last: 7, Daily: 7, Weekly: 4, Monthly: 6},
	}
}
