package restore_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

func fixture() manifest.Manifest {
	return manifest.Manifest{
		FormatVersion: manifest.FormatVersion,
		BackupID:      "01JQ8Z3K5M7P9R2T4V6X8Y0A2B",
		SourceID:      "prod-pg-main",
		Engine:        "postgresql",
		ServerVersion: "17.11",
		Kind:          "logical",
		StartedAt:     time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 9, 5, 2, 14, 37, 0, time.UTC),
		Status:        "completed",
		Objects: []manifest.Object{
			{
				Key:        "dump.pgdump.zst.age",
				SizeBytes:  8123456789,
				SHA256:     "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
				Codec:      "zstd",
				Encryption: "age",
				Recipients: []string{"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"},
			},
			{
				Key:        "globals.sql.zst.age",
				SizeBytes:  4096,
				SHA256:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				Codec:      "zstd",
				Encryption: "age",
				Recipients: []string{"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"},
			},
		},
		Tool:         manifest.ToolFrom("pg_dump", "17.11", []string{"--format=custom"}),
		KoffrVersion: "0.1.0",
	}
}

func render(t *testing.T, m manifest.Manifest) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, restore.WriteDoc(&b, restore.DocInput{
		Manifest:   m,
		Repository: "s3://backups/koffr",
		Prefix:     "sources/prod-pg-main/logical/01JQ8Z3K5M7P9R2T4V6X8Y0A2B/",
	}))
	return b.String()
}

// The document is the whole of PD-001. It is what someone reads at 3 AM when
// Koffr is unavailable, so its exact wording is the deliverable, not an
// implementation detail.
func TestWriteDoc_Golden(t *testing.T) {
	got := render(t, fixture())

	golden := filepath.Join("testdata", "RESTORE.logical.golden.md")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create it")
	assert.Equal(t, string(want), got)
}

// P-006, as a regression test.
//
// pg_restore stops reading at the archive's end marker, so the decompressor
// feeding it exits 141 on SIGPIPE during a perfectly successful restore. A
// document that told the operator to use pipefail would report every good
// restore as a failure, and the natural response to that is to distrust the
// backup.
func TestWriteDoc_DoesNotUsePipefail(t *testing.T) {
	got := render(t, fixture())

	// The document must not *use* pipefail, and must *explain* why. Forbidding
	// the word outright would forbid the warning too, which is the part that
	// stops someone adding it back.
	assert.NotContains(t, got, "set -o pipefail")
	assert.NotContains(t, got, "set -eo pipefail")
	assert.Contains(t, got, "pipefail",
		"the document has to say not to use it, or someone will")
	assert.Contains(t, strings.ToLower(got), "sigpipe",
		"the document should explain the exit code rather than leave it to be discovered")
	assert.Contains(t, got, "141",
		"naming the exit code is what makes it recognisable when it appears")
}

// Every command the document gives must be runnable as written. A placeholder
// left in is a command that fails at 3 AM.
func TestWriteDoc_CommandsAreConcrete(t *testing.T) {
	got := render(t, fixture())

	for _, want := range []string{
		"age -d",
		"zstd -d",
		"pg_restore",
		"dump.pgdump.zst.age",
		"globals.sql.zst.age",
		"01JQ8Z3K5M7P9R2T4V6X8Y0A2B",
		"s3://backups/koffr",
	} {
		assert.Contains(t, got, want)
	}
	// A placeholder, not any angle bracket: shell redirection uses one, and the
	// document is full of legitimate `age -d ... < file`.
	placeholder := regexp.MustCompile(`<[a-zA-Z][a-zA-Z0-9 _-]*>`)
	assert.NotRegexp(t, placeholder, got,
		"a placeholder in a restore procedure is a command that fails when it is needed")
	for _, unwanted := range []string{"TODO", "FIXME", "example.com"} {
		assert.NotContains(t, got, unwanted)
	}
}

// The digests are the one check that needs no key at all (EF-053), so the
// document has to carry them and say how to use them.
func TestWriteDoc_CarriesDigests(t *testing.T) {
	got := render(t, fixture())

	assert.Contains(t, got, "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08")
	assert.Contains(t, got, "sha256")
	assert.Contains(t, got, "without any key",
		"the point of a ciphertext digest is that it works before decryption")
}

// It records which recipients can open the backup, so the reader knows which
// identity to fetch. Recipients are public keys; identities never appear.
func TestWriteDoc_NamesRecipientsNotIdentities(t *testing.T) {
	got := render(t, fixture())

	assert.Contains(t, got, "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p")
	testutil.AssertNoSecretLeak(t, got)
	assert.NotContains(t, got, "AGE-SECRET-KEY")
}

func TestWriteDoc_PhysicalBackup(t *testing.T) {
	m := fixture()
	m.Kind = "physical"
	m.Objects = []manifest.Object{{
		Key:        "base.tar.zst.age",
		SizeBytes:  1 << 30,
		SHA256:     "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Codec:      "zstd",
		Encryption: "age",
		Recipients: []string{"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"},
	}}
	m.PostgreSQL = &manifest.PostgreSQLDetails{
		StartLSN: "0/3000060", EndLSN: "0/3000220", Timeline: 1, WALMethod: "fetch",
	}

	got := render(t, m)
	assert.Contains(t, got, "tar -x")
	assert.Contains(t, got, "0/3000060")
	assert.NotContains(t, got, "pg_restore",
		"a base backup is extracted, not restored with pg_restore")
}

func TestWriteDoc_MariaDBPhysicalMentionsPrepare(t *testing.T) {
	m := fixture()
	m.Engine = "mariadb"
	m.Kind = "physical"
	m.Objects = []manifest.Object{{
		Key: "base.xb.zst.age", SizeBytes: 1 << 30,
		SHA256:     "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Codec:      "zstd",
		Encryption: "age",
		Recipients: []string{"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"},
	}}

	got := render(t, m)
	assert.Contains(t, got, "mbstream")
	assert.Contains(t, got, "--prepare",
		"CT-005: a MariaDB physical backup is unusable until it has been prepared")
	assert.Contains(t, strings.ToLower(got), "disk space",
		"prepare needs room for the whole uncompressed backup, and finding that out midway is too late")
}

// The document is only worth anything if every command runs as written.
//
// It had a real bug: step 3 produced dump.pgdump.zst and the restore step named
// dump.pgdump, a file no step created. Reading the prose did not catch it; this
// does.
func TestWriteDoc_CommandsNameFilesThatExist(t *testing.T) {
	for name, m := range map[string]manifest.Manifest{
		"postgres logical": fixture(),
		"postgres physical": func() manifest.Manifest {
			m := fixture()
			m.Kind = "physical"
			m.Objects = []manifest.Object{objectNamed("base.tar.zst.age")}
			return m
		}(),
		"mariadb physical": func() manifest.Manifest {
			m := fixture()
			m.Engine, m.Kind = "mariadb", "physical"
			m.Objects = []manifest.Object{objectNamed("base.xb.zst.age")}
			return m
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			got := render(t, m)

			// Whatever step 3 writes with a > redirection is what later steps
			// may name; anything else in a .zst or .age position is a file the
			// reader will not find.
			produced := map[string]bool{}
			for _, line := range strings.Split(got, "\n") {
				if _, after, found := strings.Cut(line, " > "); found {
					produced[strings.TrimSpace(after)] = true
				}
			}
			require.NotEmpty(t, produced, "no step produces anything")

			referenced := regexp.MustCompile(`[\w./-]+\.(zst|age|xb|tar|pgdump|sql)\b`)
			for _, ref := range referenced.FindAllString(got, -1) {
				if strings.HasSuffix(ref, ".age") || strings.Contains(ref, "/") {
					continue // an object being downloaded, not a local file
				}
				assert.True(t, produced[ref],
					"a command names %q, which no earlier step creates", ref)
			}
		})
	}
}

func objectNamed(key string) manifest.Object {
	return manifest.Object{
		Key: key, SizeBytes: 1 << 20,
		SHA256:     "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Codec:      "zstd",
		Encryption: "age",
		Recipients: []string{"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"},
	}
}

func TestWriteDoc_RejectsAnEmptyManifest(t *testing.T) {
	var b strings.Builder
	err := restore.WriteDoc(&b, restore.DocInput{Manifest: manifest.Manifest{}})
	require.Error(t, err)
}
