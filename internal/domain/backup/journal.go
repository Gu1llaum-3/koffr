package backup

import "strings"

// Journal records what a job did, step by step. The domain declares the port
// and knows nothing of slog: `internal/cli` writes it to the file of E-026
// (`N-1`, AR-01).
//
// It exists because the acceptance run of the lot 2 found 21 log lines for 21
// commands, all of them "command started" — nothing about what a backup wrote,
// how big it was, or where it broke. For an agent launched by cron at two in
// the morning, that file is the only trace that survives (A-18).
type Journal interface {
	Step(entry JobStep)
}

// JobStep is one line of that trace.
type JobStep struct {
	Job      string
	Database string
	Step     Step

	// Failed is what went wrong, and nil when nothing did. A step that failed
	// is the last one journalled: the trace stops where the job stopped.
	Failed error

	// Facts are what the step produced, **in a fixed order**: two runs of the
	// same database have to be comparable line by line, and a map would shuffle
	// them. They never carry a secret (BKP-21, E-115).
	Facts []Fact
}

// Fact is one thing worth keeping about a step.
type Fact struct {
	Name  string
	Value any
}

// factLimit is how long a fact may be. A journal line is read by a person, and
// the file of E-026 is the only trace a job run by cron at two in the morning
// leaves behind: one conclusion per line, never a transcript.
const factLimit = 200

// Conclusion makes a fact of something a sub-process said. It keeps the first
// line and bounds it, so that a component which answers with a listing cannot
// pour it into the journal (`BKP-26`, `A-29`).
//
// The engines summarise what they conclude; this is the domain refusing to
// carry a transcript whatever they decide to say next.
func Conclusion(name, said string) Fact {
	if cut := strings.IndexByte(said, '\n'); cut >= 0 {
		said = said[:cut]
	}

	if len(said) > factLimit {
		said = said[:factLimit] + "…"
	}

	return Fact{Name: name, Value: said}
}

// silentJournal is what a Service without a journal uses. Nothing in the domain
// has to check for nil.
type silentJournal struct{}

func (silentJournal) Step(JobStep) {}
