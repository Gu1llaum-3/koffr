package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/httpapi"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func healthy() httpapi.Health {
	at := time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC)
	return httpapi.Health{
		Now:              func() time.Time { return at.Add(3 * time.Hour) },
		SchedulerRunning: func() bool { return true },
		Checks: map[string]func(context.Context) error{
			"catalog":          func(context.Context) error { return nil },
			"destination main": func(context.Context) error { return nil },
		},
		Sources: func(context.Context) ([]httpapi.SourceStatus, error) {
			return []httpapi.SourceStatus{{
				ID: "prod", Schedule: "0 2 * * *",
				LastSuccessAt: at, NextRunAt: at.Add(24 * time.Hour),
			}}, nil
		},
	}
}

// EF-132. Liveness answers one question -- is this process alive -- and must
// never depend on anything that can be slow or down. A liveness probe that
// consults a database restarts the pod when the database has a bad minute.
func TestHealthz_IsAlwaysCheapAndAlwaysAnswers(t *testing.T) {
	h := healthy()
	h.Checks = map[string]func(context.Context) error{
		"catalog": func(context.Context) error { return errors.New("catalog is gone") },
	}
	h.SchedulerRunning = func() bool { return false }

	code, body := get(t, httpapi.Handler(h), "/healthz")
	assert.Equal(t, http.StatusOK, code,
		"the process is answering, which is the whole of liveness")
	assert.Equal(t, "ok\n", body)
}

// EF-133. Readiness is the opposite: it is allowed to be expensive and it must
// say no when the work cannot be done.
func TestReadyz_ReportsEachCheck(t *testing.T) {
	code, body := get(t, httpapi.Handler(healthy()), "/readyz")
	require.Equal(t, http.StatusOK, code)

	var got struct {
		Ready  bool `json:"ready"`
		Checks []struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error,omitempty"`
		} `json:"checks"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.True(t, got.Ready)
	assert.Len(t, got.Checks, 3, "the scheduler counts as a check of its own")
}

func TestReadyz_SaysNoWhenACheckFails(t *testing.T) {
	h := healthy()
	h.Checks["destination main"] = func(context.Context) error {
		return errors.New("bucket koffr-backups: access denied")
	}

	code, body := get(t, httpapi.Handler(h), "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, code,
		"a destination Koffr cannot write to means tonight's backup will not happen")
	assert.Contains(t, body, "destination main")
}

// A scheduler that is not consulting its timetable is a Koffr that will not
// back anything up, however healthy everything else is.
func TestReadyz_SaysNoWhenTheSchedulerIsNotRunning(t *testing.T) {
	h := healthy()
	h.SchedulerRunning = func() bool { return false }

	code, body := get(t, httpapi.Handler(h), "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, body, "scheduler")
}

// EF-134. The age of the last successful backup is what a supervisor alerts
// on, so it is a number rather than a sentence.
func TestStatus_ReportsAgeAndStaleness(t *testing.T) {
	code, body := get(t, httpapi.Handler(healthy()), "/api/v1/status")
	require.Equal(t, http.StatusOK, code)

	var got struct {
		Sources []struct {
			ID            string `json:"source"`
			Schedule      string `json:"schedule"`
			LastSuccessAt string `json:"last_success_at"`
			AgeSeconds    int64  `json:"age_seconds"`
			NextRunAt     string `json:"next_run_at"`
			Stale         bool   `json:"stale"`
		} `json:"sources"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Len(t, got.Sources, 1)

	s := got.Sources[0]
	assert.Equal(t, "prod", s.ID)
	assert.Equal(t, int64(3*3600), s.AgeSeconds)
	assert.False(t, s.Stale, "backed up three hours ago against a nightly schedule")
	assert.Equal(t, "2026-03-01T02:00:00Z", s.LastSuccessAt)
}

// A source that has never been backed up is the case a supervisor most needs to
// see, and the one a naive "age since last backup" cannot express.
func TestStatus_ASourceNeverBackedUp(t *testing.T) {
	h := healthy()
	h.Sources = func(context.Context) ([]httpapi.SourceStatus, error) {
		return []httpapi.SourceStatus{{ID: "prod", Schedule: "0 2 * * *", Stale: true}}, nil
	}

	code, body := get(t, httpapi.Handler(h), "/api/v1/status")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"stale":true`)
	assert.Contains(t, body, `"last_success_at":null`,
		"never is not the same as the zero time, and a supervisor has to tell them apart")
}

// EF-135. No authentication, so nothing here may be worth reading.
func TestEndpoints_ExposeNothingSensitive(t *testing.T) {
	h := healthy()
	h.Sources = func(context.Context) ([]httpapi.SourceStatus, error) {
		return []httpapi.SourceStatus{{
			ID: "prod", Schedule: "0 2 * * *",
			LastSuccessAt: time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC),
		}}, nil
	}
	h.Checks = map[string]func(context.Context) error{
		// A backend error can quote a connection string, and this endpoint is
		// unauthenticated by design.
		"destination main": func(context.Context) error {
			return errors.New("dial postgres://koffr:" + testutil.SecretSentinel + "@db.internal:5432")
		},
	}

	handler := httpapi.Handler(h)
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/status"} {
		_, body := get(t, handler, path)
		testutil.AssertNoSecretLeak(t, body)
		assert.NotContains(t, body, "db.internal",
			"%s must not name a database host to an unauthenticated reader", path)
	}
}

func TestUnknownPath(t *testing.T) {
	code, _ := get(t, httpapi.Handler(healthy()), "/admin")
	assert.Equal(t, http.StatusNotFound, code)
}

// A probe that hangs is a probe that fails, and a readiness check reaching S3
// has every reason to hang.
func TestReadyz_BoundsASlowCheck(t *testing.T) {
	h := healthy()
	h.CheckTimeout = 20 * time.Millisecond
	h.Checks["destination main"] = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	done := make(chan int, 1)
	go func() {
		code, _ := get(t, httpapi.Handler(h), "/readyz")
		done <- code
	}()

	select {
	case code := <-done:
		assert.Equal(t, http.StatusServiceUnavailable, code)
	case <-time.After(5 * time.Second):
		t.Fatal("a slow readiness check held the probe open")
	}
}
