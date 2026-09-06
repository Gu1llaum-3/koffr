package notify_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/notify"
)

type pings struct {
	mu   sync.Mutex
	got  []string
	code int
}

func (p *pings) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.got = append(p.got, r.URL.Path)
	code := p.code
	p.mu.Unlock()
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
}

func (p *pings) paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.got...)
}

// EF-131. This is the inverse of every other alert and the only one that
// catches a job which never ran at all: the alarm comes from the ping *not*
// arriving, so it cannot be defeated by Koffr being broken, stopped, or
// uninstalled.
func TestDeadMansSwitch_PingsOnSuccessOnly(t *testing.T) {
	rec := &pings{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	dms, err := notify.NewDeadMansSwitch(notify.DeadMansSwitchConfig{
		URLs: map[string]string{"prod": srv.URL + "/prod-token"},
	})
	require.NoError(t, err)

	require.NoError(t, dms.Ping(t.Context(), "prod"))
	assert.Equal(t, []string{"/prod-token"}, rec.paths())

	// A source with no URL configured is not an error: the switch is opt-in
	// per source, and half an estate monitored is better than none.
	require.NoError(t, dms.Ping(t.Context(), "unmonitored"))
	assert.Len(t, rec.paths(), 1)
}

// A monitor that rejects the ping means the supervisor will raise the alarm in
// a few minutes and nobody will know why. Saying so now is the difference.
func TestDeadMansSwitch_ReportsARefusedPing(t *testing.T) {
	rec := &pings{code: http.StatusNotFound}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	dms, err := notify.NewDeadMansSwitch(notify.DeadMansSwitchConfig{
		URLs: map[string]string{"prod": srv.URL + "/gone"},
	})
	require.NoError(t, err)

	err = dms.Ping(t.Context(), "prod")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.NotContains(t, err.Error(), "gone",
		"a Healthchecks.io URL is a credential in its path, so it stays out of the message")
}

func TestNewDeadMansSwitch_RefusesABadURL(t *testing.T) {
	_, err := notify.NewDeadMansSwitch(notify.DeadMansSwitchConfig{
		URLs: map[string]string{"prod": "wat"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prod")
}

func TestNewDeadMansSwitch_Empty(t *testing.T) {
	dms, err := notify.NewDeadMansSwitch(notify.DeadMansSwitchConfig{})
	require.NoError(t, err)
	require.NoError(t, dms.Ping(t.Context(), "anything"))
}

func TestRedactedURLKeepsTheHost(t *testing.T) {
	rec := &pings{code: http.StatusInternalServerError}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	dms, _ := notify.NewDeadMansSwitch(notify.DeadMansSwitchConfig{
		URLs: map[string]string{"prod": srv.URL + "/a-secret-uuid"},
	})
	err := dms.Ping(t.Context(), "prod")
	require.Error(t, err)
	assert.Contains(t, err.Error(), strings.TrimPrefix(srv.URL, "http://"),
		"the host has to survive, or the operator cannot tell which monitor refused")
}
