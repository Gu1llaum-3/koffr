package scheduler

import (
	"fmt"
	"time"
)

// Window is the span of the day during which a backup may start (EF-093).
//
// It is not the same thing as a schedule, and the difference matters. A cron
// spec says when to *try*; the window says when trying is allowed at all. A
// source on @every 6h with a 22:00-06:00 window runs at night and stays off the
// link during the day, which one cron spec cannot express.
//
// The zero value allows everything, so a configuration that says nothing about
// windows behaves as though the feature did not exist.
type Window struct {
	// start and end are minutes since midnight. A window may cross midnight,
	// which for backups is the normal case and the one a naive
	// start <= t <= end gets wrong.
	start, end int
	set        bool
}

// ParseWindow reads "22:00" to "06:00".
func ParseWindow(start, end string) (Window, error) {
	if start == "" && end == "" {
		return Window{}, nil
	}
	s, err := parseClock(start)
	if err != nil {
		return Window{}, fmt.Errorf("scheduler: window start: %w", err)
	}
	e, err := parseClock(end)
	if err != nil {
		return Window{}, fmt.Errorf("scheduler: window end: %w", err)
	}
	if s == e {
		// Ambiguous rather than obvious: it reads as "all day" to one person
		// and "never" to the next, and a backup window nobody agrees on is
		// worse than none.
		return Window{}, fmt.Errorf(
			"scheduler: a window from %s to %s is empty or endless depending on who reads it; "+
				"leave both out to allow any time", start, end)
	}
	return Window{start: s, end: e, set: true}, nil
}

// Allows reports whether a job may start at t.
//
// The start is inclusive and the end is not, so 22:00-06:00 and 06:00-22:00
// partition the day between them with no minute belonging to both.
func (w Window) Allows(t time.Time) bool {
	if !w.set {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if w.start < w.end {
		return m >= w.start && m < w.end
	}
	// Crosses midnight: inside means after the start or before the end.
	return m >= w.start || m < w.end
}

// String renders the window the way it was configured.
func (w Window) String() string {
	if !w.set {
		return "any time"
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.start/60, w.start%60, w.end/60, w.end%60)
}

// IsSet reports whether a window was configured at all.
func (w Window) IsSet() bool { return w.set }

func parseClock(s string) (int, error) {
	var h, m int
	if n, err := fmt.Sscanf(s, "%d:%d", &h, &m); n != 2 || err != nil {
		return 0, fmt.Errorf("%q is not a time of day; write it as HH:MM", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("%q is not a time of day; hours are 0-23 and minutes 0-59", s)
	}
	return h*60 + m, nil
}
