package engine_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

// VRF-01 — the structure of a PostgreSQL dump is checked **as it streams past**,
// on the head of the flow where the table of contents lives. koffr holds only a
// public key, so this is the one moment it sees the dump in clear (ADR-0017).
func TestVRF01ARealPostgreSQLDumpHasACoherentTableOfContents(t *testing.T) {
	server := startPostgres(t, "16")
	seedPostgres(t, server)

	watcher := watcherFor(t, resolve.PostgreSQL, server)

	dumpPast(t, watcher, engine.DumpRequest{
		Target: server.target,
		Tool:   compatibleHostTool(t, server),
	})

	verdict := watcher.Conclude(t.Context())

	if !verdict.Checked {
		t.Fatalf("koffr could not look at the dump at all: %s", verdict.Detail)
	}
	if !verdict.OK {
		t.Fatalf("a real dump was refused: %s", verdict.Detail)
	}
	if !strings.Contains(verdict.Detail, seededTable) {
		t.Errorf("the table of contents does not name the table that was seeded:\n%s", verdict.Detail)
	}
}

// VRF-01 — and a flow that is not a dump is **refused**. A pg_dump that fails
// after writing two bytes produces a perfectly coherent archive of nothing;
// only this check tells the difference.
func TestVRF01AFlowThatIsNotADumpIsRefused(t *testing.T) {
	needsPgRestore(t)

	cases := map[string][]byte{
		"random bytes": randomBytes(t, 64<<10),
		"empty":        nil,
		"text":         []byte("this is not a dump, it is a sentence\n"),
	}

	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			watcher := watcherFor(t, resolve.PostgreSQL, server{})

			if _, err := io.Copy(io.Discard, io.TeeReader(bytes.NewReader(contents), watcher)); err != nil {
				t.Fatalf("copy: %v", err)
			}

			verdict := watcher.Conclude(t.Context())
			if verdict.OK {
				t.Errorf("%s was accepted as a dump:\n%s", name, verdict.Detail)
			}
		})
	}
}

// VRF-02 — for MySQL and MariaDB the marker lives at the **end**: the dump is
// complete only if `-- Dump completed` came past.
func TestVRF02ARealMariaDBDumpCarriesItsEndMarker(t *testing.T) {
	server := startMariaDB(t, "11.4")
	seedMySQLFamily(t, server.target, false)

	watcher := watcherFor(t, resolve.MariaDB, server)

	dumpPast(t, watcher, engine.DumpRequest{
		Target:    server.target,
		Tool:      containerTool(t, server.name, resolve.MariaDB),
		Container: server.name,
	})

	verdict := watcher.Conclude(t.Context())

	if !verdict.Checked || !verdict.OK {
		t.Fatalf("a real MariaDB dump was refused: %s", verdict.Detail)
	}
}

// VRF-02 — a dump cut before its marker is refused. This is the failure a
// checksum cannot see on its own: the archive is intact, and incomplete.
func TestVRF02ADumpCutBeforeItsMarkerIsRefused(t *testing.T) {
	whole := []byte("-- MariaDB dump\nCREATE TABLE x (id int);\nINSERT INTO x VALUES (1);\n" +
		"-- Dump completed on 2026-09-23 10:00:00\n")

	cases := map[string][]byte{
		"cut before the marker": whole[:40],
		"empty":                 nil,
	}

	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			watcher := watcherFor(t, resolve.MariaDB, server{})

			if _, err := io.Copy(io.Discard, io.TeeReader(bytes.NewReader(contents), watcher)); err != nil {
				t.Fatalf("copy: %v", err)
			}

			if watcher.Conclude(t.Context()).OK {
				t.Errorf("%s was accepted as a complete dump", name)
			}
		})
	}
}

// VRF-02 — and the marker is looked for at the end, not anywhere: a dump that
// merely *mentions* the marker in a row of data is not complete.
func TestVRF02TheMarkerIsLookedForAtTheEnd(t *testing.T) {
	lying := []byte("INSERT INTO notes VALUES ('-- Dump completed on 2026-01-01 00:00:00');\n" +
		strings.Repeat("INSERT INTO filler VALUES (1);\n", 200))

	watcher := watcherFor(t, resolve.MariaDB, server{})

	if _, err := io.Copy(io.Discard, io.TeeReader(bytes.NewReader(lying), watcher)); err != nil {
		t.Fatalf("copy: %v", err)
	}

	if watcher.Conclude(t.Context()).OK {
		t.Error("a dump that only mentions the marker in its data was accepted as complete")
	}
}

// E-025, tâche 1.3 — watching costs nothing to the chain: every byte reaches
// the pipeline unchanged, and what the watcher keeps stays bounded whatever the
// size of the dump.
func TestWatchingDoesNotConsumeTheStream(t *testing.T) {
	const size = 8 << 20

	source := randomBytes(t, size)

	for _, family := range []resolve.Family{resolve.PostgreSQL, resolve.MariaDB} {
		t.Run(string(family), func(t *testing.T) {
			watcher := watcherFor(t, family, server{})

			var through bytes.Buffer
			if _, err := io.Copy(&through, io.TeeReader(bytes.NewReader(source), watcher)); err != nil {
				t.Fatalf("copy: %v", err)
			}

			if !bytes.Equal(through.Bytes(), source) {
				t.Fatalf("the chain received %d bytes, want the %d that went in", through.Len(), len(source))
			}

			if kept := watcher.Kept(); kept > 8<<20 {
				t.Errorf("the watcher kept %d bytes of an %d-byte stream: it is buffering the dump",
					kept, size)
			}
		})
	}
}

func watcherFor(t *testing.T, family resolve.Family, on server) engine.StructureWatcher {
	t.Helper()

	var restore resolve.Candidate
	if family == resolve.PostgreSQL {
		restore = restoreToolOnThisMachine(t)
	}

	watcher, err := engine.WatchStructure(family, restore)
	if err != nil {
		t.Fatalf("WatchStructure(%s): %v", family, err)
	}

	_ = on

	return watcher
}

// restoreToolOnThisMachine finds pg_restore the way the resolver would.
func restoreToolOnThisMachine(t *testing.T) resolve.Candidate {
	t.Helper()

	found, err := engine.NewFinder(engine.FinderOptions{}).Find(t.Context(), resolve.PostgreSQL, resolve.Restore)
	if err != nil {
		t.Fatalf("find pg_restore: %v", err)
	}
	if len(found) == 0 {
		t.Skip("no pg_restore on this machine: the structural check needs the real tool")
	}

	return found[0]
}

func needsPgRestore(t *testing.T) {
	t.Helper()

	restoreToolOnThisMachine(t)
}

// dumpPast runs a real dump and lets the watcher see it go by, exactly as the
// pipeline will: an io.TeeReader, never a second read.
func dumpPast(t *testing.T, watcher engine.StructureWatcher, request engine.DumpRequest) {
	t.Helper()

	dump, err := engine.NewWithDocker(engine.ContainerOptions{}).Dump(t.Context(), request)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if _, err := io.Copy(io.Discard, io.TeeReader(dump, watcher)); err != nil {
		t.Fatalf("read the dump: %v", err)
	}
	if err := dump.Close(); err != nil {
		t.Fatalf("the dump failed: %v", err)
	}
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()

	made := make([]byte, size)
	if _, err := rand.Read(made); err != nil {
		t.Fatalf("random: %v", err)
	}

	return made
}
