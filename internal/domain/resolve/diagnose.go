package resolve

import "context"

// Diagnosis is what koffr knows about one database before anything goes wrong.
// § 5.12 makes this the point of doctor: today an operator discovers these
// problems in the logs of a night job.
type Diagnosis struct {
	// ID is the identifier the operator wrote in the configuration.
	ID string

	Target Target

	// Server is what the probe learned, and Unreachable says why it learned
	// nothing.
	Server      ServerInfo
	Unreachable error

	// Tool is the one that would run, and NoTool says why none would.
	Tool   Candidate
	NoTool error
}

// Healthy reports whether this database could be backed up right now.
func (d Diagnosis) Healthy() bool {
	return d.Unreachable == nil && d.NoTool == nil
}

// Subject is one database to diagnose, with the identifier its operator gave
// it. A slice rather than a map on purpose: the configuration has an order, and
// a diagnosis that comes back shuffled is a diagnosis nobody can compare to the
// last one — or to the file they wrote.
type Subject struct {
	ID     string
	Target Target
}

// Diagnose walks a fleet and reports on every database, including the ones that
// did not answer. It never stops at the first problem: a diagnosis that hides
// the second failure is worth half of nothing. The order of the answers is the
// order of the subjects.
//
// It opens no connection and runs no binary of its own: the probe and the
// finder do, and they are ports (AR-01).
func Diagnose(ctx context.Context, probe ServerProbe, finder ToolFinder, subjects []Subject) []Diagnosis {
	diagnoses := make([]Diagnosis, 0, len(subjects))

	for _, subject := range subjects {
		diagnoses = append(diagnoses, diagnoseOne(ctx, probe, finder, subject.ID, subject.Target))
	}

	return diagnoses
}

func diagnoseOne(ctx context.Context, probe ServerProbe, finder ToolFinder, id string, target Target) Diagnosis {
	diagnosis := Diagnosis{ID: id, Target: target}

	server, err := probe.Probe(ctx, target)
	if err != nil {
		diagnosis.Unreachable = err

		// Without the server's version there is no compatibility question to
		// answer: koffr says what it could not do, and does not guess the rest.
		return diagnosis
	}
	diagnosis.Server = server

	candidates, err := finder.Find(ctx, server.Family, Dump)
	if err != nil {
		diagnosis.NoTool = err

		return diagnosis
	}

	tool, err := Choose(candidates, server, Dump)
	if err != nil {
		diagnosis.NoTool = err

		return diagnosis
	}
	diagnosis.Tool = tool

	return diagnosis
}
