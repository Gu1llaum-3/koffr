package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/replica"
	"github.com/Gu1llaum-3/koffr/internal/catalog/sqlite"
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
		"koffr schedule":        {"--dry-run"},
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

	// An identity that cannot open anything in this repository. That is what
	// "no key" looks like from a valid configuration -- crypto.identity is
	// required, so an operator without the real key has a wrong one, not an
	// absent one. The manifest path must not care either way.
	wrong, _ := testutil.AgeIdentity(t)
	t.Setenv("KOFFR_IDENTITY", wrong)

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

// EF-090 and EF-091 in one place: the timetable is readable before it runs, and
// a source with no schedule is not scheduled -- delegating to cron or systemd
// stays a supported choice rather than something to work around.
func TestSchedule_DryRunShowsTheTimetable(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(body), "    database: shop",
			"    database: shop\n    schedule: \"0 2 * * *\"", 1)), 0o600))

	code, out, errOut := run(t, "--config", cfgPath, "--output", "json", "schedule", "--dry-run")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)

	var got struct {
		Result struct {
			Timezone string `json:"timezone"`
			Jobs     []struct {
				Source   string `json:"source"`
				Schedule string `json:"schedule"`
				Next     string `json:"next_run"`
			} `json:"jobs"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "UTC", got.Result.Timezone,
		"the default has to be the one that does not move twice a year")
	require.Len(t, got.Result.Jobs, 1)
	assert.Equal(t, "prod-pg-main", got.Result.Jobs[0].Source)

	next, err := time.Parse(time.RFC3339, got.Result.Jobs[0].Next)
	require.NoError(t, err)
	assert.Equal(t, 2, next.Hour(), "0 2 * * * is two in the morning, and an operator has to be able to check that")
	assert.True(t, next.After(time.Now()), "the next run is in the future")
}

// A configuration where nothing is scheduled is a mistake worth naming: the
// operator ran `koffr schedule` expecting something to happen.
func TestSchedule_NothingScheduled(t *testing.T) {
	code, _, errOut := run(t, "--config", configFile(t), "schedule", "--dry-run")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "schedule")
}

// PD-006 again: a schedule that does not parse is a source that silently never
// runs, so it is refused when the file loads rather than at two in the morning.
func TestConfig_RefusesABadSchedule(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(body), "    database: shop",
			"    database: shop\n    schedule: \"every night\"", 1)), 0o600))

	code, _, errOut := run(t, "--config", cfgPath, "config", "validate")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "sources.prod-pg-main.schedule")
	assert.Contains(t, errOut, "@daily", "the message has to say what would have worked")
}

// A job left running by a process that died is the one state an operator cannot
// act on: neither "it worked" nor "it failed". Start-up is the only moment it
// is knowable, because a single-writer catalog means anything still marked
// running belongs to a previous life. Databasus does this; Koffr did not.
func TestSchedule_ClosesOutJobsLeftRunningByADeadProcess(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(body), "    database: shop",
			"    database: shop\n    schedule: \"@daily\"", 1)), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	cat, err := sqlite.Open(t.Context(), cfg.Catalog.Path)
	require.NoError(t, err)
	require.NoError(t, cat.RecordJob(t.Context(), catalog.Job{
		ID: "01JOBSTUCK0000000000000000", SourceID: "prod-pg-main", Kind: "logical",
		Trigger: catalog.TriggerSchedule, Status: catalog.StatusRunning,
		StartedAt: time.Now().Add(-time.Hour).UTC(),
	}))
	require.NoError(t, cat.Close())

	// Not --dry-run: printing a timetable must not change the catalog, so the
	// repair only happens on a real start. The scheduler is given a moment and
	// then told to stop.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	var out, errBuf strings.Builder
	code := cli.Run(ctx, []string{"--config", cfgPath, "schedule"},
		cli.Streams{Out: &out, Err: &errBuf})
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errBuf.String())
	assert.Contains(t, errBuf.String(), "marked failed")

	cat, err = sqlite.Open(t.Context(), cfg.Catalog.Path)
	require.NoError(t, err)
	defer func() { _ = cat.Close() }()

	snap, err := cat.Export(t.Context())
	require.NoError(t, err)

	// Not a count: the scheduler also catches up a source that has never been
	// backed up, so it records a job of its own. The stuck one is what this
	// test is about.
	var stuck *catalog.Job
	for i, j := range snap.Jobs {
		if j.ID == "01JOBSTUCK0000000000000000" {
			stuck = &snap.Jobs[i]
		}
	}
	require.NotNil(t, stuck, "the interrupted job vanished from the catalog")
	assert.Equal(t, catalog.StatusFailed, stuck.Status,
		"a job nobody is doing must not still read as in progress")
	assert.Equal(t, catalog.ErrClassCanceled, stuck.ErrorClass)
	assert.False(t, stuck.FinishedAt.IsZero())
}

// EF-093 through the configuration, because a window that only exists in the
// scheduler's struct is a window nobody can set.
func TestSchedule_WindowComesFromTheConfiguration(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	updated := strings.Replace(string(body), "catalog:", `scheduler:
  window:
    start: "22:00"
    end: "06:00"
catalog:`, 1)
	updated = strings.Replace(updated, "    database: shop",
		"    database: shop\n    schedule: \"@daily\"", 1)
	require.NoError(t, os.WriteFile(cfgPath, []byte(updated), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "22:00-06:00", cfg.Scheduler.ExecutionWindow().String())

	code, _, errOut := run(t, "--config", cfgPath, "schedule", "--dry-run")
	assert.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
}

// A window nobody can agree on is worse than none, so it is refused at load.
func TestConfig_RefusesAnAmbiguousWindow(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(body), "catalog:",
		`scheduler:
  window:
    start: "22:00"
    end: "22:00"
catalog:`, 1)), 0o600))

	code, _, errOut := run(t, "--config", cfgPath, "config", "validate")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "scheduler.window")
}

// The event the user asked for: a backup running because last night's did not
// is a fact worth telling someone, and the only signal a window was ever
// missed. End to end, through the configuration, to a real HTTP receiver.
func TestSchedule_NotifiesOnCatchUpAndFailure(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev map[string]any
		if json.Unmarshal(body, &ev) == nil {
			mu.Lock()
			received = append(received, ev)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfgPath := configFile(t)
	t.Setenv("KOFFR_WEBHOOK", srv.URL)

	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	updated := strings.Replace(string(body), "catalog:", `notify:
  webhooks:
    - url: env:KOFFR_WEBHOOK
      min_severity: warning
catalog:`, 1)
	updated = strings.Replace(updated, "    database: shop",
		"    database: shop\n    schedule: \"@every 1s\"", 1)
	require.NoError(t, os.WriteFile(cfgPath, []byte(updated), 0o600))

	// The source points at a port nothing listens on, so the catch-up fires and
	// then fails -- both events in one run.
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()

	var out, errBuf strings.Builder
	code := cli.Run(ctx, []string{"--config", cfgPath, "schedule"},
		cli.Streams{Out: &out, Err: &errBuf})
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errBuf.String())

	mu.Lock()
	defer mu.Unlock()
	kinds := map[string]bool{}
	for _, ev := range received {
		kinds[ev["kind"].(string)] = true
		testutil.AssertNoSecretLeak(t, ev["message"].(string))
	}
	assert.True(t, kinds["backup.caught_up"],
		"a missed window being made good has to be reported; got %v", kinds)
	assert.True(t, kinds["backup.failed"] || kinds["backup.retrying"],
		"a failing backup has to be reported; got %v", kinds)
	assert.False(t, kinds["backup.completed"],
		"a channel asking for warnings and above must not receive routine successes")
}

// A broken alerting channel is worse than none, because absent is obvious and
// broken looks like quiet. It is refused when the configuration loads.
func TestConfig_RefusesABrokenWebhook(t *testing.T) {
	cfgPath := configFile(t)
	t.Setenv("KOFFR_WEBHOOK", "not-a-url")

	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(body), "catalog:",
		"notify:\n  webhooks:\n    - url: env:KOFFR_WEBHOOK\ncatalog:", 1)), 0o600))

	code, _, errOut := run(t, "--config", cfgPath, "config", "validate")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "notify.webhooks[0]")
}

// A monitor watching a source that does not exist will alarm tonight, for a
// reason nobody will be able to find.
func TestConfig_RefusesADeadMansSwitchForAnUnknownSource(t *testing.T) {
	cfgPath := configFile(t)
	t.Setenv("KOFFR_DMS", "https://hc-ping.example/abc")

	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(body), "catalog:",
		"notify:\n  dead_mans_switch:\n    typo-in-the-name: env:KOFFR_DMS\ncatalog:", 1)), 0o600))

	code, _, errOut := run(t, "--config", cfgPath, "config", "validate")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "typo-in-the-name")
}

// EF-114 and EF-136 together: prose when a person is watching, JSON when a
// machine is. Nothing here is configured -- the daemon decides from whether
// there is a terminal on the other end, so a container and a systemd unit both
// get structured logs without anyone having asked.
func TestSchedule_LogsJSONWhenNotOnATerminal(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(body), "    database: shop",
			"    database: shop\n    schedule: \"@every 1s\"", 1)), 0o600))

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	var out, errBuf strings.Builder
	code := cli.Run(ctx, []string{"--config", cfgPath, "schedule"},
		cli.Streams{Out: &out, Err: &errBuf})
	require.Equal(t, cli.ExitOK, code)

	var structured int
	for _, line := range strings.Split(strings.TrimSpace(errBuf.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		structured++
		assert.NotEmpty(t, rec["time"], "a log line nobody can correlate")
		assert.NotEmpty(t, rec["level"])
		testutil.AssertNoSecretLeak(t, line)
	}
	assert.Positive(t, structured, "no structured line reached stderr:\n%s", errBuf.String())

	// stdout stays the command's answer, whatever the log does to stderr.
	assert.Empty(t, strings.TrimSpace(out.String()),
		"a log line on stdout would corrupt `koffr ls --output json | jq`")
}

// The failure of a backup is the line an operator greps for, so it carries the
// fields they would filter on rather than a sentence they would have to parse.
func TestSchedule_FailureLineCarriesFields(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(strings.Replace(string(body), "    database: shop",
			"    database: shop\n    schedule: \"@every 1s\"", 1)), 0o600))

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	var out, errBuf strings.Builder
	_ = cli.Run(ctx, []string{"--config", cfgPath, "schedule"},
		cli.Streams{Out: &out, Err: &errBuf})

	var found map[string]any
	for _, line := range strings.Split(errBuf.String(), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "backup attempt failed" {
			found = rec
		}
	}
	require.NotNil(t, found, "the failure was never logged:\n%s", errBuf.String())
	assert.Equal(t, "prod-pg-main", found["source"])
	// source, not config: an unreachable database could be a server that is
	// down or a port that is wrong, and Koffr cannot tell. Retrying an
	// unclassifiable failure is safer than declaring it permanent, and the
	// class is what carries that decision into the line an operator greps.
	assert.Equal(t, "source", found["class"])
	assert.Contains(t, found, "will_retry")
}

// A level the machine does not know is refused while someone is looking at the
// file, not when the daemon starts (PD-006).
func TestConfig_RefusesAnUnknownLogLevel(t *testing.T) {
	cfgPath := configFile(t)
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(body), "catalog:",
		"log:\n  level: chatty\ncatalog:", 1)), 0o600))

	code, _, errOut := run(t, "--config", cfgPath, "config", "validate")
	assert.Equal(t, cli.ExitConfig, code)
	assert.Contains(t, errOut, "log")
	assert.Contains(t, errOut, "chatty")
}

// `koffr check` fails exactly when it has the most to say. Discarding the
// findings on the failure path made the command that exists to report what is
// wrong report nothing as soon as something was.
func TestCheck_ReportsItsFindingsWhenItFails(t *testing.T) {
	cfgPath := configFile(t)

	t.Run("text", func(t *testing.T) {
		code, out, errOut := run(t, "--config", cfgPath, "check")
		require.NotEqual(t, cli.ExitOK, code)
		assert.Contains(t, out, "prod-pg-main", "the table has to be printed, not swallowed")
		assert.Contains(t, out, "FAIL")
		assert.Contains(t, errOut, "checks failed")
	})

	t.Run("json", func(t *testing.T) {
		code, out, _ := run(t, "--config", cfgPath, "--output", "json", "check")
		require.NotEqual(t, cli.ExitOK, code)

		var got struct {
			OK     bool `json:"ok"`
			Result struct {
				Checks []struct {
					What    string `json:"what"`
					Target  string `json:"target"`
					OK      bool   `json:"ok"`
					Problem string `json:"problem"`
				} `json:"checks"`
				Failed int `json:"failed"`
			} `json:"result"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got), "output was not JSON: %s", out)
		assert.False(t, got.OK)
		assert.Positive(t, got.Result.Failed)
		require.NotEmpty(t, got.Result.Checks, "a script needs to know which check failed, not just that one did")

		var named bool
		for _, c := range got.Result.Checks {
			if !c.OK {
				named = named || c.Problem != ""
			}
		}
		assert.True(t, named, "a failed check has to say why")
	})
}
