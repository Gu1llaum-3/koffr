package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// s3Config is `valid` with an object store instead of a directory, since these
// two settings only mean anything there.
func s3Config(t *testing.T, extra string) string {
	t.Helper()
	return strings.Replace(valid, `    type: fs
    path: /var/backups/koffr`, `    type: s3
    bucket: backups
    region: us-east-1
`+extra, 1)
}

func TestTransfer_Defaults(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	cfg, err := config.Load(write(t, s3Config(t, "")))
	require.NoError(t, err)
	d := cfg.Destinations["main"]

	// Measured, not chosen for roundness: with the SDK's own retry settings a
	// five-second outage ended a backup outright, and there is no resuming one
	// -- the stream comes from pg_dump, and running pg_dump again produces
	// different bytes.
	require.NotNil(t, d.RetryWindow)
	assert.Equal(t, 2*time.Minute, *d.RetryWindow)
	assert.Equal(t, 16, d.PartSizeMiB)
}

func TestTransfer_RetryWindowMustFitInsideTheStallBudget(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	// A retry is silence on the storage branch, and silence is what the stall
	// watcher kills a job for. A window too close to that budget means the
	// watcher ends the job the retry was about to rescue -- and blames the
	// wrong actor while doing it.
	_, err := config.Load(write(t, s3Config(t, "    retry_window: 10m")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry_window")
	assert.Contains(t, err.Error(), "5m", "the message must name the budget it has to fit inside")
}

// Giving up costs the window twice: the cleanup that abandons the half-written
// upload is retried on the same terms. Measured, a 30s window took 1m24s to
// fail. So a window of three minutes fits inside a five-minute budget on paper
// and blows through it in practice.
func TestTransfer_RetryWindowIsBoundedByWhatGivingUpCosts(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	_, err := config.Load(write(t, s3Config(t, "    retry_window: 3m")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "twice the window")

	// And the default sits under that bound rather than at it.
	cfg, err := config.Load(write(t, s3Config(t, "")))
	require.NoError(t, err)
	assert.Less(t, 2*(*cfg.Destinations["main"].RetryWindow), 5*time.Minute)
}

func TestTransfer_RejectsANegativeRetryWindow(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	_, err := config.Load(write(t, s3Config(t, "    retry_window: -1s")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retry_window")
}

// Zero is how an operator says "do not retry", and it has to be sayable: an
// endpoint that is failing for a reason retrying cannot fix is better failed
// fast.
func TestTransfer_ZeroRetryWindowIsAllowed(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	cfg, err := config.Load(write(t, s3Config(t, "    retry_window: 0s")))
	require.NoError(t, err)
	w := cfg.Destinations["main"].RetryWindow
	require.NotNil(t, w, "an explicit zero must survive as a zero, not become the default")
	assert.Zero(t, *w)
}

func TestTransfer_PartSizeBounds(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	t.Run("below the S3 minimum", func(t *testing.T) {
		_, err := config.Load(write(t, s3Config(t, "    part_size_mib: 4")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "part_size_mib")
	})

	t.Run("above what the memory budget allows", func(t *testing.T) {
		// The upload manager holds several parts at once, so part size is a
		// memory setting as much as a transfer one. ENF-001 caps the process at
		// 512 MiB whatever the database size, and that cap decides the ceiling.
		_, err := config.Load(write(t, s3Config(t, "    part_size_mib: 64")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "part_size_mib")
	})

	t.Run("at the top of the range", func(t *testing.T) {
		cfg, err := config.Load(write(t, s3Config(t, "    part_size_mib: 32")))
		require.NoError(t, err)
		assert.Equal(t, 32, cfg.Destinations["main"].PartSizeMiB)
	})
}

// Neither setting means anything for a directory, and accepting them there
// would tell an operator their transfers are tuned when nothing reads the
// value (PD-006).
func TestTransfer_RefusedOnAFilesystemDestination(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	_, err := config.Load(write(t, strings.Replace(valid,
		"    path: /var/backups/koffr",
		"    path: /var/backups/koffr\n    part_size_mib: 16", 1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "part_size_mib")
}

// How many parts a multipart upload may have is set by the provider, and there
// is no way to ask. AWS allows ten thousand; Scaleway and several other
// S3-compatible services allow one thousand. With 16 MiB parts that is the
// difference between a 156 GiB artifact and a 15.6 GiB one, and the refusal
// arrives at the last part either way.
//
// The only signal available is the one wire.go already trusts to decide on
// path-style addressing: an endpoint is set when the destination is not AWS.
func TestTransfer_MaxPartsDefaultsToWhatTheProviderLikelyAllows(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	t.Run("no endpoint means AWS", func(t *testing.T) {
		cfg, err := config.Load(write(t, s3Config(t, "")))
		require.NoError(t, err)
		assert.Equal(t, 10000, cfg.Destinations["main"].MaxParts)
	})

	t.Run("an endpoint means a service whose limit we cannot know", func(t *testing.T) {
		cfg, err := config.Load(write(t, s3Config(t, "    endpoint: https://s3.fr-par.scw.cloud")))
		require.NoError(t, err)
		assert.Equal(t, 1000, cfg.Destinations["main"].MaxParts,
			"guessing high fails at the provider's limit with the provider's own error")
	})

	t.Run("and it can be raised when the service allows it", func(t *testing.T) {
		cfg, err := config.Load(write(t,
			s3Config(t, "    endpoint: http://minio.internal:9000\n    max_parts: 10000")))
		require.NoError(t, err)
		assert.Equal(t, 10000, cfg.Destinations["main"].MaxParts)
	})

	t.Run("beyond what S3 itself allows", func(t *testing.T) {
		_, err := config.Load(write(t, s3Config(t, "    max_parts: 20000")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_parts")
	})

	t.Run("zero parts is not a destination", func(t *testing.T) {
		_, err := config.Load(write(t, s3Config(t, "    max_parts: -1")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_parts")
	})
}

// A second engine is only supported once a configuration naming it loads and a
// path from cmd/koffr reaches it. This is the first half.
func TestSource_MariaDBIsAccepted(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	body := strings.Replace(valid, "engine: postgresql", "engine: mariadb", 1)
	cfg, err := config.Load(write(t, body))
	require.NoError(t, err)
	assert.Equal(t, "mariadb", cfg.Sources["prod-pg-main"].Engine)
	assert.False(t, cfg.Sources["prod-pg-main"].AllowInconsistentSnapshot,
		"a dump that is not a snapshot is never the default")
}

func TestSource_AllowInconsistentSnapshotIsExplicit(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	body := strings.Replace(valid, "engine: postgresql",
		"engine: mariadb\n    allow_inconsistent_snapshot: true", 1)
	cfg, err := config.Load(write(t, body))
	require.NoError(t, err)
	assert.True(t, cfg.Sources["prod-pg-main"].AllowInconsistentSnapshot)
}

func TestSource_AnUnknownEngineIsStillRefused(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	body := strings.Replace(valid, "engine: postgresql", "engine: mysql", 1)
	_, err := config.Load(write(t, body))
	require.Error(t, err, "naming an engine that is not implemented accepts a job that cannot run")
	assert.Contains(t, err.Error(), "mysql")
}
