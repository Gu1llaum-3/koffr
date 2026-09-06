// Package httpapi serves the health and status endpoints (EF-132 to EF-135).
//
// It is unauthenticated by design, so the rule that shapes every line here is
// that nothing it returns may be worth reading. No hostname, no database name,
// no path, no error text from a backend -- a failing S3 client will quote a
// bucket and a region into its message, and one day a connection string.
//
// What it does return is what a supervisor needs to raise an alarm: whether the
// process is alive, whether it could do its work, and how long ago each source
// last succeeded.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Health is what the endpoints report on. Every field is injected, so this
// package knows nothing about catalogs or destinations.
type Health struct {
	// Now is the clock, injected so an age is testable without waiting.
	Now func() time.Time

	// SchedulerRunning reports whether the timetable is being consulted. A
	// Koffr whose scheduler has stopped will back nothing up, however healthy
	// everything else looks.
	SchedulerRunning func() bool

	// Checks are the readiness probes, by name. The name is shown; the error
	// is not.
	Checks map[string]func(context.Context) error

	// Sources reports per-source status for EF-134.
	Sources func(context.Context) ([]SourceStatus, error)

	// CheckTimeout bounds one readiness check. Zero means five seconds: a probe
	// that hangs is a probe that fails, and a check reaching S3 has every
	// reason to hang.
	CheckTimeout time.Duration
}

// SourceStatus is one source's answer to "is this backed up".
type SourceStatus struct {
	ID       string
	Schedule string

	// LastSuccessAt is the zero time when the source has never been backed up.
	LastSuccessAt time.Time
	NextRunAt     time.Time

	// Stale means a scheduled window went by without a successful backup. It is
	// computed by the same rule the scheduler uses to decide a catch-up, so the
	// two can never disagree about whether something was missed.
	Stale bool
}

// Handler returns the endpoint router.
func Handler(h Health) http.Handler {
	mux := http.NewServeMux()

	// EF-132. Liveness answers one question -- is this process alive -- and
	// touches nothing that can be slow or down. A liveness probe that consulted
	// the catalog would restart Koffr because a disk had a bad second, which is
	// the opposite of what a restart is for.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /api/v1/status", h.status)
	return mux
}

type checkResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`

	// Error is a fixed phrase, never the underlying message. A backend error
	// quotes buckets, hosts and one day a connection string, and this endpoint
	// has no authentication in front of it (EF-135). The detail belongs in the
	// log, where whoever reads it has already been let in.
	Error string `json:"error,omitempty"`
}

// readyz is EF-133: can Koffr actually do its work.
func (h Health) readyz(w http.ResponseWriter, r *http.Request) {
	timeout := h.CheckTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	results := []checkResult{{Name: "scheduler", OK: h.SchedulerRunning == nil || h.SchedulerRunning()}}
	if !results[0].OK {
		results[0].Error = "not running"
	}

	for _, name := range sortedNames(h.Checks) {
		res := checkResult{Name: name, OK: true}
		if err := h.Checks[name](ctx); err != nil {
			res.OK = false
			res.Error = "unavailable"
		}
		results = append(results, res)
	}

	ready := true
	for _, res := range results {
		ready = ready && res.OK
	}

	status := http.StatusOK
	if !ready {
		// 503 rather than 500: this is a statement about now, and a supervisor
		// treats it as "come back shortly" rather than "this is broken".
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, struct {
		Ready  bool          `json:"ready"`
		Checks []checkResult `json:"checks"`
	}{ready, results})
}

// statusLine is the JSON shape of one source. Its field names are a contract
// with whatever alerts on them: they may be added to, never renamed.
type statusLine struct {
	ID            string  `json:"source"`
	Schedule      string  `json:"schedule,omitempty"`
	LastSuccessAt *string `json:"last_success_at"`
	AgeSeconds    *int64  `json:"age_seconds"`
	NextRunAt     *string `json:"next_run_at,omitempty"`
	Stale         bool    `json:"stale"`
}

// status is EF-134: how long ago each source last succeeded.
//
// Age is a number, not a sentence, because a supervisor alerts on it. Null
// rather than zero for a source never backed up: never is not the same as
// "in 1970", and a rule written as `age_seconds > 86400` would treat the two
// identically only by accident.
func (h Health) status(w http.ResponseWriter, r *http.Request) {
	if h.Sources == nil {
		writeJSON(w, http.StatusOK, struct {
			Sources []statusLine `json:"sources"`
		}{[]statusLine{}})
		return
	}

	sources, err := h.Sources(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, struct {
			Error string `json:"error"`
		}{"the catalog is unavailable"})
		return
	}

	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}

	lines := make([]statusLine, 0, len(sources))
	for _, s := range sources {
		line := statusLine{ID: s.ID, Schedule: s.Schedule, Stale: s.Stale}
		if !s.LastSuccessAt.IsZero() {
			at := s.LastSuccessAt.UTC().Format(time.RFC3339)
			age := int64(now.Sub(s.LastSuccessAt).Seconds())
			line.LastSuccessAt, line.AgeSeconds = &at, &age
		}
		if !s.NextRunAt.IsZero() {
			next := s.NextRunAt.UTC().Format(time.RFC3339)
			line.NextRunAt = &next
		}
		lines = append(lines, line)
	}

	writeJSON(w, http.StatusOK, struct {
		Sources []statusLine `json:"sources"`
	}{lines})
}

func sortedNames(m map[string]func(context.Context) error) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Nowhere to report it: the status line is already sent.
		_ = fmt.Sprint(err)
	}
}
