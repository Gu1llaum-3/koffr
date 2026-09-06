package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// valid is the smallest configuration that describes a real backup.
const valid = `
version: 1

crypto:
  recipients:
    - age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p
    - age1lggyhqrw2nlhcxprm67z43rta597azn8gknawjehu9d9dl0jq3yqqvfafg
  identity: env:KOFFR_IDENTITY

catalog:
  path: /var/lib/koffr/catalog.db

destinations:
  main:
    type: fs
    path: /var/backups/koffr

sources:
  prod-pg-main:
    engine: postgresql
    host: db.internal
    user: koffr
    password: env:PGPASSWORD
    database: shop
    destinations: [main]
`

// setIdentity puts a real, freshly generated age identity in the environment.
func setIdentity(t *testing.T) {
	t.Helper()
	identity, _ := testutil.AgeIdentity(t)
	t.Setenv("KOFFR_IDENTITY", identity)
}

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "koffr.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoad_Valid(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	cfg, err := config.Load(write(t, valid))
	require.NoError(t, err)

	assert.Equal(t, 1, cfg.Version)
	assert.Len(t, cfg.Crypto.Recipients, 2)
	assert.Equal(t, "/var/lib/koffr/catalog.db", cfg.Catalog.Path)

	src, ok := cfg.Source("prod-pg-main")
	require.True(t, ok, "the CLI looks sources up by id; that has to work")
	assert.Equal(t, "postgresql", src.Engine)
	assert.Equal(t, "db.internal", src.Host)
	assert.Equal(t, 5432, src.Port, "a default the operator should not have to write")
	assert.Equal(t, "verify-full", src.SSLMode,
		"libpq defaults to prefer, which silently accepts plaintext")

	_, ok = cfg.Source("nope")
	assert.False(t, ok)
}

// EF-103: secrets are references so the file itself can live in Git. A literal
// is refused rather than warned about, because a warning in a file that is
// already committed is advice arriving too late.
func TestLoad_ResolvesSecretReferences(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	cfg, err := config.Load(write(t, valid))
	require.NoError(t, err)

	src, _ := cfg.Source("prod-pg-main")
	assert.Equal(t, testutil.SecretSentinel, src.Password.Value())
}

func TestLoad_SecretFromFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "pgpass")
	require.NoError(t, os.WriteFile(secretPath, []byte(testutil.SecretSentinel+"\n"), 0o600))
	setIdentity(t)

	cfg, err := config.Load(write(t, strings.Replace(valid,
		"password: env:PGPASSWORD", "password: file:"+secretPath, 1)))
	require.NoError(t, err)

	src, _ := cfg.Source("prod-pg-main")
	assert.Equal(t, testutil.SecretSentinel, src.Password.Value(),
		"a trailing newline is what an editor adds, not part of the password")
}

// A secret file anyone can read is not a secret. Refusing is the only way the
// check means anything.
func TestLoad_RejectsAWorldReadableSecretFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "pgpass")
	require.NoError(t, os.WriteFile(secretPath, []byte("hunter2"), 0o644))
	setIdentity(t)

	_, err := config.Load(write(t, strings.Replace(valid,
		"password: env:PGPASSWORD", "password: file:"+secretPath, 1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "0600")
}

// The whole point of "report every error": correcting a configuration one
// message at a time is the difference between a tool people keep and one they
// fight.
func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	broken := `
version: 1
crypto:
  recipients: [age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p]
catalog:
  path: /var/lib/koffr/catalog.db
destinations:
  main:
    type: fs
sources:
  prod:
    engine: mysql
    user: koffr
    password: hunter2
    database: shop
    destinations: [nowhere]
`
	_, err := config.Load(write(t, broken))
	require.Error(t, err)

	var problems *config.ValidationError
	require.ErrorAs(t, err, &problems)

	paths := make([]string, 0, len(problems.Problems))
	for _, p := range problems.Problems {
		paths = append(paths, p.Path)
	}
	assert.ElementsMatch(t, []string{
		"crypto.recipients",         // only one, and EF-051 wants an offline key
		"crypto.identity",           // missing
		"destinations.main.path",    // fs without a path
		"sources.prod.engine",       // mysql is not supported
		"sources.prod.host",         // missing
		"sources.prod.password",     // a literal, not a reference
		"sources.prod.destinations", // names a destination that does not exist
	}, paths)
}

// A path and a message are not enough: the operator has to know what to type
// instead.
func TestLoad_ProblemsCarryAHint(t *testing.T) {
	broken := strings.Replace(valid, "password: env:PGPASSWORD", "password: hunter2", 1)
	setIdentity(t)

	_, err := config.Load(write(t, broken))
	require.Error(t, err)

	var problems *config.ValidationError
	require.ErrorAs(t, err, &problems)
	require.Len(t, problems.Problems, 1)
	assert.Contains(t, problems.Problems[0].Hint, "env:")
	assert.Contains(t, problems.Problems[0].Hint, "file:")
}

// EF-051 in the one place an operator can still fix it.
func TestLoad_RequiresAnOfflineRecoveryRecipient(t *testing.T) {
	setIdentity(t)
	one := strings.Replace(valid,
		"    - age1lggyhqrw2nlhcxprm67z43rta597azn8gknawjehu9d9dl0jq3yqqvfafg\n", "", 1)

	_, err := config.Load(write(t, one))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recovery")
}

// A typo in a key is a setting that silently does not apply, which for a backup
// tool means a policy nobody is enforcing.
func TestLoad_RejectsUnknownKeys(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", "x")

	typo := strings.Replace(valid, "    database: shop", "    databse: shop", 1)
	_, err := config.Load(write(t, typo))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "databse")
}

func TestLoad_RejectsAnUnknownVersion(t *testing.T) {
	_, err := config.Load(write(t, strings.Replace(valid, "version: 1", "version: 2", 1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2")
}

func TestLoad_MissingFileSaysWhereItLooked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yml")
	_, err := config.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), path)
}

// Every message the CLI can print goes through here, so none of them may carry
// a resolved secret (ENF-021).
func TestLoad_NoSecretInAnyErrorMessage(t *testing.T) {
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)
	t.Setenv("KOFFR_IDENTITY", testutil.SecretSentinel)

	broken := strings.Replace(valid, "  path: /var/lib/koffr/catalog.db", "  path: \"\"", 1)
	_, err := config.Load(write(t, broken))
	require.Error(t, err)
	testutil.AssertNoSecretLeak(t, err.Error())
}

// koffr config show has to be safe to paste into a ticket.
func TestRedacted_HidesResolvedSecrets(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	cfg, err := config.Load(write(t, valid))
	require.NoError(t, err)

	rendered, err := cfg.Redacted()
	require.NoError(t, err)
	testutil.AssertNoSecretLeak(t, rendered)
	assert.Contains(t, rendered, "env:PGPASSWORD",
		"the reference is what an operator needs to see; the value is not")
	assert.Contains(t, rendered, "prod-pg-main")
}

// ENF-033. The classification is tested rather than the syscall: mounting an
// NFS share inside a unit test is not possible, and a check nobody can exercise
// is a check nobody can trust.
func TestIsNetworkFilesystem(t *testing.T) {
	for _, name := range []string{"nfs", "NFS4", "cifs", "smbfs", " smb2 ", "fuse.sshfs", "9p", "ceph"} {
		assert.True(t, config.IsNetworkFSName(name), "%q should be refused for the catalog", name)
	}
	for _, name := range []string{"ext4", "xfs", "btrfs", "apfs", "zfs", "overlay", "tmpfs", "", "fuse"} {
		// A bare "fuse" is a transport, not a network filesystem. Calling every
		// FUSE mount unsafe would make the check noisy enough to be turned off.
		assert.False(t, config.IsNetworkFSName(name), "%q should be accepted", name)
	}
}

// A temporary directory is local on every machine this runs on, so a
// configuration pointing at one must load without complaint.
func TestLoad_AcceptsACatalogOnLocalStorage(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", "x")

	local := filepath.Join(t.TempDir(), "catalog.db")
	cfg, err := config.Load(write(t, strings.Replace(valid,
		"  path: /var/lib/koffr/catalog.db", "  path: "+local, 1)))
	require.NoError(t, err)
	assert.Equal(t, local, cfg.Catalog.Path)
}

// The CLI resolves the file the same way every time, and says which one it
// used: an operator editing the wrong file is a long afternoon.
func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.yml")
	require.NoError(t, os.WriteFile(explicit, []byte(valid), 0o600))

	local := filepath.Join(dir, "koffr.yml")
	require.NoError(t, os.WriteFile(local, []byte(valid), 0o600))

	t.Run("flag wins", func(t *testing.T) {
		t.Setenv("KOFFR_CONFIG", local)
		got, err := config.ResolvePath(explicit, dir)
		require.NoError(t, err)
		assert.Equal(t, explicit, got)
	})

	t.Run("environment beats the working directory", func(t *testing.T) {
		t.Setenv("KOFFR_CONFIG", explicit)
		got, err := config.ResolvePath("", dir)
		require.NoError(t, err)
		assert.Equal(t, explicit, got)
	})

	t.Run("working directory last", func(t *testing.T) {
		t.Setenv("KOFFR_CONFIG", "")
		got, err := config.ResolvePath("", dir)
		require.NoError(t, err)
		assert.Equal(t, local, got)
	})

	t.Run("nothing found lists where it looked", func(t *testing.T) {
		t.Setenv("KOFFR_CONFIG", "")
		_, err := config.ResolvePath("", t.TempDir())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "koffr.yml")
	})
}

// A destination's credentials are secrets like any other, and they were the one
// place nothing resolved them: `access_key_id: env:NAME` stayed unresolved, so
// the S3 backend received an empty key and failed with "static credentials are
// empty". Every S3 configuration using explicit keys was broken, and no test
// saw it because the storage tests build their client directly.
func TestLoad_ResolvesDestinationCredentials(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", "x")
	t.Setenv("KOFFR_S3_KEY", "AKIAEXAMPLE")
	t.Setenv("KOFFR_S3_SECRET", testutil.SecretSentinel)

	cfg, err := config.Load(write(t, strings.Replace(valid, `  main:
    type: fs
    path: /var/backups/koffr`, `  main:
    type: s3
    bucket: koffr-backups
    region: eu-west-3
    access_key_id: env:KOFFR_S3_KEY
    secret_access_key: env:KOFFR_S3_SECRET`, 1)))
	require.NoError(t, err)

	dest := cfg.Destinations["main"]
	assert.Equal(t, "AKIAEXAMPLE", dest.AccessKeyID.Value())
	assert.Equal(t, testutil.SecretSentinel, dest.SecretAccessKey.Value(),
		"an unresolved key reaches the SDK as an empty string, which fails at the first upload")
}

// A destination with no keys is the normal case in EKS or on EC2, where the SDK
// finds instance credentials. Requiring them would break the deployment that
// needs them least.
func TestLoad_S3WithoutExplicitCredentials(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", "x")

	cfg, err := config.Load(write(t, strings.Replace(valid, `  main:
    type: fs
    path: /var/backups/koffr`, `  main:
    type: s3
    bucket: koffr-backups
    region: eu-west-3`, 1)))
	require.NoError(t, err)
	assert.True(t, cfg.Destinations["main"].AccessKeyID.IsZero())
}

// An environment variable set to nothing is not a password. LookupEnv reports
// it as present, so `export PGPASSWORD=` produced an empty credential and a
// connection refused at 3 AM instead of a message at load time.
func TestLoad_RejectsAnEmptyEnvironmentVariable(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", "")

	_, err := config.Load(write(t, valid))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PGPASSWORD")
	assert.Contains(t, err.Error(), "empty")
}
