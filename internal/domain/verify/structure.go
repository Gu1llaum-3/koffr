package verify

// Structure is what a structural check concluded about a dump.
//
// The three states are deliberate. A check koffr could **not run** is not a
// success, and it is not a failure of the archive either: `P4` says an archive
// is valid only once it has been checked, so "not checked" and "checked and
// bad" must never collapse into one another.
type Structure struct {
	// Checked says whether koffr was able to look at all.
	Checked bool

	// OK says what it found, and means nothing when Checked is false.
	OK bool

	// Detail is the table of contents when it went well, and why not when it
	// did not. It goes into the manifest and the journal, so it never carries a
	// credential.
	Detail string
}

// Unchecked is the verdict of a check that could not run, with its reason.
func Unchecked(why string) Structure {
	return Structure{Detail: why}
}

// Sound is the verdict of a dump the engine still recognises as one.
func Sound(detail string) Structure {
	return Structure{Checked: true, OK: true, Detail: detail}
}

// Refused is the verdict of a flow that is not a dump, or not a whole one.
func Refused(why string) Structure {
	return Structure{Checked: true, Detail: why}
}
