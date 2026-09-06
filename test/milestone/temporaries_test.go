//go:build milestone

package milestone_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// PD-003, criterion 4: no backup writes a complete artifact to disk, on the
// database host or on this one.
//
// Sampling rather than a before-and-after comparison, because a temporary file
// that appears and is deleted is still a temporary file. Checking only at the
// end would pass on an implementation that staged ten gibibytes and tidied up
// afterwards -- the exact implementation this principle exists to forbid.

// credentialsPrefix is the one thing Koffr is allowed to put in TMPDIR: the
// .pgpass that libpq needs, written 0600 in a 0700 directory and removed when
// the job ends (P-004, ENF-022). It is a credential, not a staged artifact, and
// it is bounded at a few hundred bytes.
const credentialsPrefix = "koffr-pgpass-"

// tolerance is what the credentials directory can weigh. Anything staging a
// backup is orders of magnitude past this, so the threshold does not need to be
// tight to be meaningful.
const tolerance = 64 << 10

type temporaryWatch struct {
	stop chan struct{}
	done chan struct{}

	mu       sync.Mutex
	peak     int64
	worst    []string
	remote   []string
	tmpDir   string
	remoteAt []string
}

type temporaryFindings struct {
	peakBytes int64
	local     []string
	remote    []string
}

func (f temporaryFindings) clean() bool {
	return f.peakBytes <= tolerance && len(f.local) == 0 && len(f.remote) == 0
}

func (f temporaryFindings) describe() string {
	if f.clean() {
		return "none (" + humanBytes(f.peakBytes) + " peak)"
	}
	var parts []string
	if f.peakBytes > tolerance {
		parts = append(parts, "peak "+humanBytes(f.peakBytes))
	}
	if len(f.local) > 0 {
		parts = append(parts, "koffr host: "+strings.Join(f.local, " "))
	}
	if len(f.remote) > 0 {
		parts = append(parts, "database host: "+strings.Join(f.remote, " "))
	}
	return strings.Join(parts, "; ")
}

// watchForTemporaries starts sampling both machines.
func watchForTemporaries(
	t *testing.T, ctx context.Context, pg *tcpostgres.PostgresContainer, staging string,
) *temporaryWatch {
	t.Helper()

	w := &temporaryWatch{
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		tmpDir: staging,
	}
	baselineRemote := remoteTemporaries(t, ctx, pg)

	go func() {
		defer close(w.done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-w.stop:
				w.sample(t, ctx, pg, baselineRemote)
				return
			case <-ticker.C:
				w.sample(t, ctx, pg, baselineRemote)
			}
		}
	}()
	return w
}

func (w *temporaryWatch) sample(t *testing.T, ctx context.Context, pg *tcpostgres.PostgresContainer, baseline map[string]bool) {
	total, unexpected := scanLocal(w.tmpDir)

	w.mu.Lock()
	defer w.mu.Unlock()
	if total > w.peak {
		w.peak = total
	}
	w.worst = mergeUnique(w.worst, unexpected)

	// The remote side is asked far less often: an exec into a container costs
	// more than a directory walk, and a staged backup does not disappear
	// between two samples a second apart.
	if len(w.remoteAt) == 0 || time.Now().Second()%2 == 0 {
		for name := range remoteTemporaries(t, ctx, pg) {
			if !baseline[name] {
				w.remote = mergeUnique(w.remote, []string{name})
			}
		}
		w.remoteAt = append(w.remoteAt, "sampled")
	}
}

func (w *temporaryWatch) read(t *testing.T) temporaryFindings {
	t.Helper()
	close(w.stop)
	<-w.done

	w.mu.Lock()
	defer w.mu.Unlock()
	return temporaryFindings{peakBytes: w.peak, local: w.worst, remote: w.remote}
}

// scanLocal measures TMPDIR, which the configuration points at a directory of
// its own. Everything Go stages goes through os.CreateTemp, and that follows
// TMPDIR, so an empty directory here is a real statement rather than a hopeful
// one.
func scanLocal(dir string) (total int64, unexpected []string) {
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // a file that vanished mid-walk is not a finding
		}
		total += info.Size()

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		if !strings.HasPrefix(rel, credentialsPrefix) {
			unexpected = append(unexpected, fmt.Sprintf("%s (%s)", rel, humanBytes(info.Size())))
		}
		return nil
	})
	return total, unexpected
}

// remoteTemporaries lists what is in the database host's temporary directories.
//
// PGDATA is deliberately not watched: WAL rotation and autovacuum write there
// during any normal minute, and calling that a backup artifact would make the
// check noisy enough to be turned off.
func remoteTemporaries(t *testing.T, ctx context.Context, pg *tcpostgres.PostgresContainer) map[string]bool {
	t.Helper()
	code, out, err := pg.Exec(ctx,
		[]string{"sh", "-c", "ls -A /tmp /var/tmp 2>/dev/null"}, tcexec.Multiplexed())
	require.NoError(t, err)
	require.Equal(t, 0, code)

	found := map[string]bool{}
	for _, line := range strings.Split(read(out), "\n") {
		if name := strings.TrimSpace(line); name != "" && !strings.HasSuffix(name, ":") {
			found[name] = true
		}
	}
	return found
}

func mergeUnique(into, add []string) []string {
	seen := map[string]bool{}
	for _, s := range into {
		seen[s] = true
	}
	for _, s := range add {
		if !seen[s] {
			into, seen[s] = append(into, s), true
		}
	}
	sort.Strings(into)
	return into
}
