package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/replica"
	"github.com/Gu1llaum-3/koffr/internal/cli"
	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/fs"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// run invokes the CLI the way a shell does and returns what a shell sees.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf strings.Builder
	code = cli.Run(t.Context(), args, cli.Streams{Out: &out, Err: &errBuf})
	return code, out.String(), errBuf.String()
}

// configFile writes a configuration whose password is the sentinel, pointing at
// a host that does not exist. Every command can be run against it: the ones
// that need a database fail, which is the interesting case for output.
func configFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	identity, recipient := testutil.AgeIdentity(t)
	_, recovery := testutil.AgeIdentity(t)
	t.Setenv("KOFFR_IDENTITY", identity)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	content := `version: 1
crypto:
  recipients:
    - ` + recipient + `
    - ` + recovery + `
  identity: env:KOFFR_IDENTITY
catalog:
  path: ` + filepath.Join(dir, "catalog.db") + `
destinations:
  main:
    type: fs
    path: ` + filepath.Join(dir, "repo") + `
sources:
  prod-pg-main:
    engine: postgresql
    host: 127.0.0.1
    port: 1
    user: koffr
    password: env:PGPASSWORD
    database: shop
    destinations: [main]
`
	path := filepath.Join(dir, "koffr.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// EF-113. The codes are a public interface: an operator's cron job branches on
// them, so they must be distinct, and each must mean one thing.
func TestExitCodes_AreDistinctAndDocumented(t *testing.T) {
	codes := map[string]int{
		"ok":      cli.ExitOK,
		"failure": cli.ExitFailure,
		"usage":   cli.ExitUsage,
		"config":  cli.ExitConfig,
		"backup":  cli.ExitBackup,
		"verify":  cli.ExitVerify,
		"restore": cli.ExitRestore,
	}
	seen := map[int]string{}
	for name, code := range codes {
		if other, dup := seen[code]; dup {
			t.Fatalf("%s and %s share exit code %d; a script cannot tell them apart", name, other, code)
		}
		seen[code] = name
	}

	_, help, _ := run(t, "--help")
	for name, code := range codes {
		assert.Contains(t, help, name,
			"exit code %d (%s) is not documented in the root help, so nobody can rely on it", code, name)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "version")
	assert.Equal(t, cli.ExitOK, code)
	assert.Contains(t, out, "koffr")
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	code, _, errOut := run(t, "backupp")
	assert.Equal(t, cli.ExitUsage, code)
	assert.Contains(t, errOut, "backupp")
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	code, _, errOut := run(t, "version", "--wat")
	assert.Equal(t, cli.ExitUsage, code)
	assert.Contains(t, errOut, "wat")
}

// A broken configuration is not a backup failure, and a cron job has to be able
// to tell the difference: one wakes someone up, the other waits for morning.
func TestConfigValidate_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "koffr.yml")
	require.NoError(t, os.WriteFile(path, []byte("version: 1\nsources:\n  prod:\n    engine: mysql\n"), 0o600))

	code, _, errOut := run(t, "--config", path, "config", "validate")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "sources.prod.engine", "the message has to name the key to fix")
	assert.Contains(t, errOut, path)
}

func TestConfigValidate_Valid(t *testing.T) {
	code, out, errOut := run(t, "--config", configFile(t), "config", "validate")
	assert.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "prod-pg-main", "a validation that says nothing about what it read is not reassuring")
}

func TestConfigValidate_MissingFileIsAConfigError(t *testing.T) {
	code, _, errOut := run(t, "--config", filepath.Join(t.TempDir(), "nope.yml"), "config", "validate")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "nope.yml")
}

// The JSON envelope is what scripts parse. Its shape is fixed here so that
// adding a field stays possible and renaming one does not happen by accident.
func TestJSONOutput_HasAStableEnvelope(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		code, out, _ := run(t, "--config", configFile(t), "--output", "json", "config", "validate")
		require.Equal(t, cli.ExitOK, code)

		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &got), "output was not JSON: %s", out)
		assert.Equal(t, true, got["ok"])
		assert.Equal(t, "config validate", got["command"])
		assert.NotEmpty(t, got["koffr"])
		assert.Contains(t, got, "result")
		assert.NotContains(t, got, "error")
	})

	t.Run("failure carries every problem", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "koffr.yml")
		require.NoError(t, os.WriteFile(path,
			[]byte("version: 1\nsources:\n  prod:\n    engine: mysql\n    password: hunter2\n"), 0o600))

		code, out, _ := run(t, "--config", path, "--output", "json", "config", "validate")
		require.Equal(t, cli.ExitConfig, code)

		var got struct {
			OK    bool `json:"ok"`
			Error struct {
				Code     string `json:"code"`
				Message  string `json:"message"`
				Problems []struct {
					Path    string `json:"path"`
					Message string `json:"message"`
					Hint    string `json:"hint"`
				} `json:"problems"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got), "output was not JSON: %s", out)
		assert.False(t, got.OK)
		assert.Equal(t, "config", got.Error.Code)
		require.NotEmpty(t, got.Error.Problems)

		paths := make([]string, 0, len(got.Error.Problems))
		for _, p := range got.Error.Problems {
			paths = append(paths, p.Path)
		}
		assert.Contains(t, paths, "sources.prod.engine")
		assert.Contains(t, paths, "sources.prod.password",
			"reporting one problem at a time is how a config takes an afternoon")
	})
}

// A script that parses JSON must never have to parse around a human sentence,
// so nothing else may reach stdout in JSON mode.
func TestJSONOutput_IsTheOnlyThingOnStdout(t *testing.T) {
	for _, args := range [][]string{
		{"config", "validate"},
		{"ls"},
		{"check"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			full := append([]string{"--config", configFile(t), "--output", "json"}, args...)
			_, out, _ := run(t, full...)
			var any any
			assert.NoError(t, json.Unmarshal([]byte(out), &any),
				"stdout must be exactly one JSON document, got: %s", out)
		})
	}
}

// ENF-021, across the whole surface. Walking the command tree rather than
// listing commands is deliberate: a command added next year is covered the day
// it is added, not the day someone remembers this test.
func TestNoSecretInAnyCommandOutput(t *testing.T) {
	// Arguments for the commands that need them. The map is asserted to cover
	// every leaf below, so adding a command without adding it here fails.
	args := map[string][]string{
		"koffr version":         {},
		"koffr config validate": {},
		"koffr config show":     {},
		"koffr catalog sync":    {},
		"koffr check":           {},
		"koffr ls":              {},
		"koffr show":            {"01JQ0000000000000000000000"},
		"koffr backup":          {"prod-pg-main"},
		"koffr fetch":           {"01JQ0000000000000000000000"},
		"koffr restore":         {"01JQ0000000000000000000000", "--into", "shop_restored", "--yes"},
	}

	leaves := leafCommands(cli.New(cli.Streams{}))
	require.NotEmpty(t, leaves)

	for _, path := range leaves {
		extra, ok := args[path]
		require.True(t, ok,
			"%q is a new command with no entry in this test; add one so its output is checked for secrets", path)

		t.Run(path, func(t *testing.T) {
			cfg := configFile(t)
			for _, format := range []string{"text", "json"} {
				words := append(strings.Fields(strings.TrimPrefix(path, "koffr ")), extra...)
				full := append([]string{"--config", cfg, "--output", format}, words...)
				_, out, errOut := run(t, full...)
				testutil.AssertNoSecretLeak(t, out, errOut)
			}
		})
	}
}

// Help is the documentation people actually read.
func TestHelp_RendersForEveryCommand(t *testing.T) {
	for _, path := range leafCommands(cli.New(cli.Streams{})) {
		t.Run(path, func(t *testing.T) {
			words := strings.Fields(strings.TrimPrefix(path, "koffr "))
			code, out, errOut := run(t, append(words, "--help")...)
			assert.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
			assert.Contains(t, out, words[len(words)-1])
			assert.NotContains(t, out, "%!", "a formatting verb leaked into help text")
		})
	}
}

// A backup that cannot reach its database is a backup failure, not a usage
// error: that is the code that has to page someone.
func TestBackup_UnreachableDatabaseExitsBackup(t *testing.T) {
	code, _, errOut := run(t, "--config", configFile(t), "backup", "prod-pg-main")
	assert.Equal(t, cli.ExitBackup, code)
	assert.NotEmpty(t, errOut)
}

// Naming a source that is not in the configuration is the operator's typo, and
// the message should list what is there.
func TestBackup_UnknownSourceIsAUsageError(t *testing.T) {
	code, _, errOut := run(t, "--config", configFile(t), "backup", "prod-pg-mian")
	assert.Equal(t, cli.ExitUsage, code)
	assert.Contains(t, errOut, "prod-pg-main", "list what does exist, so the fix is visible")
}

// leafCommands returns the runnable commands, as the words a user types.
func leafCommands(root *cobra.Command) []string {
	var out []string
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if !c.HasSubCommands() && c.Runnable() {
			out = append(out, c.CommandPath())
			return
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" || sub.Hidden {
				continue
			}
			walk(sub)
		}
	}
	walk(root)
	return out
}

// A digest mismatch is not a generic failure. "The backup exists and does not
// match its manifest" is what exit code 5 is for, and a script that treats it
// like an unreachable repository will retry forever against bit rot.
func TestFetch_CorruptedObjectExitsVerify(t *testing.T) {
	cfgPath := configFile(t)

	// A repository with one object, whose manifest promises a digest the object
	// does not have. That is what a damaged repository looks like from outside.
	dir := filepath.Dir(cfgPath)
	prefix := filepath.Join(dir, "repo", "sources", "prod-pg-main", "logical", "01JQ0000000000000000000000")
	require.NoError(t, os.MkdirAll(prefix, 0o700))

	m := `{"format_version":1,"backup_id":"01JQ0000000000000000000000","source_id":"prod-pg-main",` +
		`"engine":"postgresql","server_version":"17.0","kind":"logical","parent_id":null,` +
		`"started_at":"2026-01-01T00:00:00Z","finished_at":"2026-01-01T00:00:01Z","status":"completed",` +
		`"objects":[{"key":"dump.pgdump.age","size_bytes":9,` +
		`"sha256":"` + strings.Repeat("0", 64) + `","codec":"none","encryption":"age",` +
		`"recipients":["age1x"]}],"tool":{"name":"postgresql","version":"17.0","args_digest":""},` +
		`"koffr_version":"test"}`
	require.NoError(t, os.WriteFile(filepath.Join(prefix, "manifest.json"), []byte(m), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(prefix, "dump.pgdump.age"), []byte("not a dump"), 0o600))

	code, _, errOut := run(t, "--config", cfgPath, "fetch", "01JQ0000000000000000000000",
		"--into", filepath.Join(dir, "out"))
	assert.Equal(t, cli.ExitVerify, code, "stderr: %s", errOut)
	assert.Contains(t, errOut, "digest")
}

// The whole point of EF-141 and EF-142, exercised end to end through the CLI:
// a catalog that has been lost comes back from the repository, including the
// record of jobs that failed -- which produce no manifest and exist nowhere
// else.
func TestCatalogSync_RebuildsFromTheReplica(t *testing.T) {
	cfgPath := configFile(t)
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	repo := filepath.Join(filepath.Dir(cfgPath), "repo")
	st, err := fs.New(repo)
	require.NoError(t, err)
	sealer, err := crypto.NewSealer(cfg.Crypto.Recipients)
	require.NoError(t, err)

	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, replica.Write(t.Context(), st, sealer, catalog.Snapshot{
		FormatVersion: catalog.SnapshotFormatVersion,
		ExportedAt:    at,
		Backups: []catalog.Backup{{
			ID: "01BACKUP0000000000000000AA", SourceID: "prod-pg-main", Kind: "logical",
			Destination: "main", Status: catalog.StatusCompleted,
			StartedAt: at, FinishedAt: at.Add(time.Minute), SizeBytes: 4096,
		}},
		Jobs: []catalog.Job{{
			ID: "01JOBFAILED000000000000000", SourceID: "prod-pg-main", Kind: "logical",
			Trigger: catalog.TriggerSchedule, Status: catalog.StatusFailed,
			ErrorClass: catalog.ErrClassSource, ErrorDetail: "pg_dump exited with status 1",
			StartedAt: at, FinishedAt: at.Add(time.Second),
		}},
	}))

	// The catalog file has never existed on this machine, which is what a new
	// pod on a new node looks like.
	code, out, errOut := run(t, "--config", cfgPath, "catalog", "sync")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "replicated catalog")

	code, out, _ = run(t, "--config", cfgPath, "ls")
	require.Equal(t, cli.ExitOK, code)
	assert.Contains(t, out, "01BACKUP0000000000000000AA")

	// Twice changes nothing: a sync is what an operator runs when unsure, which
	// means running it again when still unsure.
	code, _, _ = run(t, "--config", cfgPath, "catalog", "sync")
	require.Equal(t, cli.ExitOK, code)

	code, out, _ = run(t, "--config", cfgPath, "--output", "json", "catalog", "sync")
	require.Equal(t, cli.ExitOK, code)
	var got struct {
		Result struct {
			Backups int `json:"backups"`
			Jobs    int `json:"jobs"`
			Added   int `json:"added"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, 1, got.Result.Backups)
	assert.Equal(t, 1, got.Result.Jobs, "the failed job is the part that exists nowhere else")
	assert.Equal(t, 0, got.Result.Added)

	// --from-manifests is not the same as "the replica happened to be
	// unreadable". With a good replica and a working identity right there, the
	// flag must still take the other road, or an operator who suspects the
	// replica is wrong has no way to bypass it.
	code, out, _ = run(t, "--config", cfgPath, "--output", "json", "catalog", "sync", "--from-manifests")
	require.Equal(t, cli.ExitOK, code)
	var forced struct {
		Result struct {
			Source string `json:"source"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &forced))
	assert.Equal(t, "manifests", forced.Result.Source)
}

// The level that has to work when everything is gone: no replica, no key, just
// the plaintext manifests. This is why manifests are not encrypted (ADR-0004).
func TestCatalogSync_RebuildsFromManifestsWithoutAKey(t *testing.T) {
	cfgPath := configFile(t)
	repo := filepath.Join(filepath.Dir(cfgPath), "repo")
	st, err := fs.New(repo)
	require.NoError(t, err)

	src, err := storage.ForSource("prod-pg-main")
	require.NoError(t, err)
	b, err := src.Backup(storage.DirLogical, "01BACKUP0000000000000000BB")
	require.NoError(t, err)

	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	require.NoError(t, manifest.Encode(&buf, manifest.Manifest{
		FormatVersion: manifest.FormatVersion,
		BackupID:      "01BACKUP0000000000000000BB",
		SourceID:      "prod-pg-main", Engine: "postgresql", ServerVersion: "17.0",
		Kind: "logical", StartedAt: at, FinishedAt: at.Add(time.Minute),
		Status: string(catalog.StatusCompleted),
		Objects: []manifest.Object{{
			Key: "dump.pgdump.zst.age", SizeBytes: 8192,
			SHA256: strings.Repeat("0", 64), Codec: "zstd",
			Encryption: "age", Recipients: []string{"age1x"},
		}},
		Tool: manifest.Tool{Name: "postgresql", Version: "17.0"}, KoffrVersion: "test",
	}))
	_, err = st.Put(t.Context(), b.ManifestKey(), bytes.NewReader(buf.Bytes()), storage.PutOptions{})
	require.NoError(t, err)

	// No identity at all: the fallback must not need one.
	t.Setenv("KOFFR_IDENTITY", "")

	code, out, errOut := run(t, "--config", cfgPath, "catalog", "sync", "--from-manifests")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "manifests")

	code, out, _ = run(t, "--config", cfgPath, "ls")
	require.Equal(t, cli.ExitOK, code)
	assert.Contains(t, out, "01BACKUP0000000000000000BB")
	assert.Contains(t, out, "main", "the destination name comes from the file, not the repository")
}

// EF-085. A restore is the one command that destroys data, and a scheduled job
// has no terminal to be asked on. Refusing is the only safe reading of silence:
// a job that meant to restore says so with a flag, and one that did not must
// not have a prompt answered for it by an empty pipe.
func TestRestore_WithoutATerminalAndWithoutYesIsRefused(t *testing.T) {
	cfgPath := configFile(t)
	putBackup(t, cfgPath, "01JQ0000000000000000000000")

	var out, errBuf strings.Builder
	code := cli.Run(t.Context(),
		[]string{"--config", cfgPath, "restore", "01JQ0000000000000000000000", "--into", "shop_x"},
		cli.Streams{Out: &out, Err: &errBuf})

	assert.Equal(t, cli.ExitUsage, code)
	assert.Contains(t, errBuf.String(), "--yes")
}

// An answer that is not yes is a no, and a no leaves the database alone.
func TestRestore_AnsweringNoCancels(t *testing.T) {
	cfgPath := configFile(t)
	putBackup(t, cfgPath, "01JQ0000000000000000000000")

	var out, errBuf strings.Builder
	code := cli.Run(t.Context(),
		[]string{"--config", cfgPath, "restore", "01JQ0000000000000000000000", "--into", "shop_x"},
		cli.Streams{In: strings.NewReader("n\n"), Out: &out, Err: &errBuf})

	assert.Equal(t, cli.ExitUsage, code)
	assert.Contains(t, errBuf.String(), "cancelled")
	assert.Contains(t, errBuf.String(), "Restore backup 01JQ0000000000000000000000",
		"the prompt has to name what it is about to do, or confirming it means nothing")
}

// EF-080: the server to restore into is named by a configured source, not by
// connection flags. A password on a command line is visible in ps (ENF-021),
// and the configuration stays the thing that says what exists (PD-005).
func TestRestore_UnknownTargetListsWhatExists(t *testing.T) {
	cfgPath := configFile(t)
	putBackup(t, cfgPath, "01JQ0000000000000000000000")

	code, _, errOut := run(t, "--config", cfgPath, "restore", "01JQ0000000000000000000000",
		"--into", "shop_x", "--target", "nowhere", "--yes")
	assert.Equal(t, cli.ExitUsage, code)
	assert.Contains(t, errOut, "prod-pg-main")
}

// EF-083: --into - puts the artifact in a pipe, so nothing else may share it.
func TestFetch_ToStdoutRefusesJSON(t *testing.T) {
	cfgPath := configFile(t)
	putBackup(t, cfgPath, "01JQ0000000000000000000000")

	code, out, _ := run(t, "--config", cfgPath, "--output", "json",
		"fetch", "01JQ0000000000000000000000", "--into", "-")
	assert.Equal(t, cli.ExitUsage, code)
	// In JSON mode the refusal is itself the JSON document, on stdout.
	assert.Contains(t, out, "--output json")
	assert.Contains(t, out, `"code": "usage"`)
}

// putBackup writes a manifest and one object, enough for a command to find a
// backup and get as far as the guard being tested.
func putBackup(t *testing.T, cfgPath, backupID string) {
	t.Helper()
	prefix := filepath.Join(filepath.Dir(cfgPath), "repo",
		"sources", "prod-pg-main", "logical", backupID)
	require.NoError(t, os.MkdirAll(prefix, 0o700))

	m := `{"format_version":1,"backup_id":"` + backupID + `","source_id":"prod-pg-main",` +
		`"engine":"postgresql","server_version":"17.0","kind":"logical","parent_id":null,` +
		`"started_at":"2026-01-01T00:00:00Z","finished_at":"2026-01-01T00:00:01Z","status":"completed",` +
		`"objects":[{"key":"dump.pgdump.zst.age","size_bytes":9,"sha256":"` + strings.Repeat("0", 64) +
		`","codec":"zstd","encryption":"age","recipients":["age1x"]}],` +
		`"tool":{"name":"postgresql","version":"17.0","args_digest":""},"koffr_version":"test"}`
	require.NoError(t, os.WriteFile(filepath.Join(prefix, "manifest.json"), []byte(m), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(prefix, "dump.pgdump.zst.age"), []byte("not a dump"), 0o600))
}
