package backup_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// BKP-20 — the seven steps of E-024 are journalled, in order, each with what it
// produced. The acceptance run found 21 log lines for 21 commands, all of them
// `command started`: nothing at all about what a backup did. For an agent
// launched by cron at two in the morning, the file of E-026 is the only trace
// that survives (A-18).
func TestBKP20TheSevenStepsAreJournalledInOrder(t *testing.T) {
	world := newWorld(t)
	journal := &recordingJournal{}
	world.journal = journal

	result, err := world.service().Run(t.Context(), request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(journal.steps(), backup.Steps) {
		t.Fatalf("journalled %v,\nwant %v — the order of E-024", journal.steps(), backup.Steps)
	}

	for _, entry := range journal.entries {
		if entry.Job != result.JobID || entry.Database != result.Database {
			t.Errorf("the step %q does not say which job it belongs to: %+v", entry.Step, entry)
		}
	}

	// Each step carries what it produced, or a line says nothing worth keeping.
	carried := map[backup.Step][]string{
		backup.StepResolution: {"engine", "tool"},
		backup.StepDump:       {"staging"},
		backup.StepWrite:      {"path", "stored_bytes", "sha256_stored"},
	}

	for step, wanted := range carried {
		facts := journal.factsOf(step)
		for _, want := range wanted {
			if !slices.Contains(facts, want) {
				t.Errorf("the step %q does not carry %q; it carries %v", step, want, facts)
			}
		}
	}

	// And the two that are not implemented say so rather than looking done.
	for _, step := range []backup.Step{backup.StepVerification, backup.StepManifest} {
		if !strings.Contains(journal.detailOf(step), "3") {
			t.Errorf("the step %q does not say which lot brings it: %q", step, journal.detailOf(step))
		}
	}
}

// BKP-20 — a step that fails is journalled **with its error**, and the steps
// after it are not journalled at all. A trace that shows seven steps for a job
// that died at the second is worse than no trace.
func TestBKP20AFailedStepIsJournalledAndStopsTheTrace(t *testing.T) {
	world := newWorld(t)
	journal := &recordingJournal{}
	world.journal = journal
	world.dumper.fail = errors.New("pg_dump exited with 1: connection refused")

	if _, err := world.service().Run(t.Context(), request()); err == nil {
		t.Fatal("a job whose dump failed reported success")
	}

	got := journal.steps()
	if !slices.Equal(got, []backup.Step{backup.StepResolution, backup.StepDump}) {
		t.Fatalf("journalled %v, want the resolution and the dump that failed", got)
	}

	failed := journal.entries[len(journal.entries)-1]
	if failed.Failed == nil {
		t.Fatal("the failing step was journalled as if it had worked")
	}
	if !strings.Contains(failed.Failed.Error(), "connection refused") {
		t.Errorf("the journal does not carry what the tool said: %v", failed.Failed)
	}
}

// BKP-21 — the journal carries an **enumerated** set of facts, and a credential
// is not one of them (E-115).
//
// Asserting "the password does not appear" would prove nothing here: the domain
// never receives it — `Resolution` carries no `Target`, which is precisely why
// it cannot leak one. What is worth guarding is the other direction: a step
// added later that journalled the target, the connection string or a secret
// path would slip through unnoticed. So the names are listed, and a name that
// is not on the list fails.
func TestBKP21TheJournalOnlyCarriesFactsThatWereDeclared(t *testing.T) {
	allowed := []string{
		"engine", "server_version", "tool", "tool_version", "tool_source",
		"staging", "staging_reason",
		"pipeline", "raw_bytes", "stored_bytes", "sha256_raw", "sha256_stored",
		"path", "destinations", "destination",
	}

	world := newWorld(t)
	journal := &recordingJournal{}
	world.journal = journal

	if _, err := world.service().Run(t.Context(), request()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(journal.entries) == 0 {
		t.Fatal("nothing was journalled")
	}

	for _, entry := range journal.entries {
		for _, fact := range entry.Facts {
			if !slices.Contains(allowed, fact.Name) {
				t.Errorf("the step %q journals %q, which is not on the declared list.\n"+
					"  Adding to it is a decision: E-115 keeps credentials, secret paths and\n"+
					"  connection strings out of the file of E-026.", entry.Step, fact.Name)
			}
		}
	}

	// And nothing the wiring holds as a secret reaches a value.
	const password = "hunter2-the-database-password"

	if strings.Contains(journal.String(), password) {
		t.Errorf("a credential is in the journal:\n%s", journal.String())
	}
}

// recordingJournal keeps what a job wrote, in order.
type recordingJournal struct {
	mutex   sync.Mutex
	entries []backup.JobStep
}

func (r *recordingJournal) Step(entry backup.JobStep) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.entries = append(r.entries, entry)
}

func (r *recordingJournal) steps() []backup.Step {
	steps := make([]backup.Step, 0, len(r.entries))
	for _, entry := range r.entries {
		steps = append(steps, entry.Step)
	}

	return steps
}

func (r *recordingJournal) factsOf(step backup.Step) []string {
	var names []string

	for _, entry := range r.entries {
		if entry.Step != step {
			continue
		}
		for _, fact := range entry.Facts {
			names = append(names, fact.Name)
		}
	}

	return names
}

func (r *recordingJournal) detailOf(step backup.Step) string {
	for _, entry := range r.entries {
		if entry.Step == step {
			return entry.Deferred
		}
	}

	return ""
}

func (r *recordingJournal) String() string {
	var written strings.Builder

	for _, entry := range r.entries {
		_, _ = fmt.Fprintf(&written, "%s %s %s %v", entry.Job, entry.Database, entry.Step, entry.Failed)

		for _, fact := range entry.Facts {
			_, _ = fmt.Fprintf(&written, " %s=%v", fact.Name, fact.Value)
		}

		written.WriteString("\n")
	}

	return written.String()
}
