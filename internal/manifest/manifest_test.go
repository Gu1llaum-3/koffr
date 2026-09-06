package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/manifest"
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
		Objects: []manifest.Object{{
			Key:        "dump.pgdump.zst.age",
			SizeBytes:  8123456789,
			SHA256:     "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			Codec:      "zstd",
			Encryption: "age",
			Recipients: []string{"age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"},
		}},
		Tool: manifest.Tool{
			Name:       "pg_dump",
			Version:    "17.11",
			ArgsDigest: "sha256:3a7bd3e2360a3d29eea436fcfb7e44c735d117c42d1c1835420b6b9942dd4f1b",
		},
		KoffrVersion: "0.1.0",
	}
}

// The manifest is the one artifact a human reads when restoring by hand, and
// every RESTORE.md already written refers to it. Pinning the exact bytes is the
// point, not an implementation detail.
func TestEncode_Golden(t *testing.T) {
	var b strings.Builder
	require.NoError(t, manifest.Encode(&b, fixture()))

	golden := filepath.Join("testdata", "manifest.golden.json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(b.String()), 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create it")
	assert.Equal(t, string(want), b.String())
}

func TestRoundTrip(t *testing.T) {
	var b strings.Builder
	require.NoError(t, manifest.Encode(&b, fixture()))

	got, err := manifest.Decode(strings.NewReader(b.String()))
	require.NoError(t, err)
	assert.Equal(t, fixture(), got)
}

// A newer Koffr may add fields. An older one reading such a manifest should
// still be able to list and restore, so unknown keys are ignored rather than
// rejected.
func TestDecode_TolerantOfUnknownFields(t *testing.T) {
	var b strings.Builder
	require.NoError(t, manifest.Encode(&b, fixture()))

	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(b.String()), &raw))
	raw["something_from_the_future"] = []string{"a", "b"}
	extended, err := json.Marshal(raw)
	require.NoError(t, err)

	got, err := manifest.Decode(strings.NewReader(string(extended)))
	require.NoError(t, err)
	assert.Equal(t, fixture().BackupID, got.BackupID)
}

// A format version we do not understand must say so in terms an operator can
// act on: which version was found, which is supported.
func TestDecode_RejectsFutureFormatVersion(t *testing.T) {
	m := fixture()
	m.FormatVersion = manifest.FormatVersion + 1
	var b strings.Builder
	require.NoError(t, manifest.Encode(&b, m))

	_, err := manifest.Decode(strings.NewReader(b.String()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2")
	assert.Contains(t, err.Error(), "1")
}

func TestDecode_RejectsMissingRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		strip string
	}{
		{"backup id", "backup_id"},
		{"source id", "source_id"},
		{"engine", "engine"},
		{"kind", "kind"},
		{"started at", "started_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			require.NoError(t, manifest.Encode(&b, fixture()))
			var raw map[string]any
			require.NoError(t, json.Unmarshal([]byte(b.String()), &raw))
			delete(raw, tc.strip)
			stripped, err := json.Marshal(raw)
			require.NoError(t, err)

			_, err = manifest.Decode(strings.NewReader(string(stripped)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.strip)
		})
	}
}

// The digest exists precisely so the arguments themselves never reach the
// repository: a command line can carry a path, a host, or a credential someone
// passed the wrong way.
func TestToolFrom_DigestsArgsInsteadOfStoringThem(t *testing.T) {
	tool := manifest.ToolFrom("pg_dump", "17.11", []string{
		"--dbname=postgres://user:" + testutil.SecretSentinel + "@host/db",
		"--format=c",
	})

	assert.NotEmpty(t, tool.ArgsDigest)
	assert.True(t, strings.HasPrefix(tool.ArgsDigest, "sha256:"))

	m := fixture()
	m.Tool = tool
	var b strings.Builder
	require.NoError(t, manifest.Encode(&b, m))
	testutil.AssertNoSecretLeak(t, b.String())
}

func TestToolFrom_DigestIsStable(t *testing.T) {
	args := []string{"--format=c", "--no-password"}
	assert.Equal(t,
		manifest.ToolFrom("pg_dump", "17.11", args).ArgsDigest,
		manifest.ToolFrom("pg_dump", "17.11", args).ArgsDigest)
	assert.NotEqual(t,
		manifest.ToolFrom("pg_dump", "17.11", args).ArgsDigest,
		manifest.ToolFrom("pg_dump", "17.11", []string{"--format=d"}).ArgsDigest)
}

// EF-055 splits metadata: manifest.json stays plaintext so a write-only node can
// list and prune without any key, and anything describing the contents goes into
// details.json.zst.age.
//
// This test fails whenever a field is added to the manifest. That is the
// intent: adding a plaintext field should be a decision, not an accident.
func TestManifestJSON_TopLevelKeysAreDeliberate(t *testing.T) {
	var b strings.Builder
	require.NoError(t, manifest.Encode(&b, fixture()))
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(b.String()), &raw))

	got := make([]string, 0, len(raw))
	for k := range raw {
		got = append(got, k)
	}
	assert.ElementsMatch(t, []string{
		"format_version", "backup_id", "source_id", "engine", "server_version",
		"kind", "parent_id", "started_at", "finished_at", "status",
		"objects", "tool", "koffr_version",
	}, got, "a new plaintext manifest field must be reviewed against EF-055")

	// The physical shape is checked too, or a field added behind omitempty
	// would slip past the guard entirely.
	physical := fixture()
	physical.Kind = "physical"
	physical.PostgreSQL = &manifest.PostgreSQLDetails{
		StartLSN: "0/3000060", EndLSN: "0/3000220", Timeline: 1, WALMethod: "fetch",
	}
	var pb strings.Builder
	require.NoError(t, manifest.Encode(&pb, physical))
	var praw map[string]any
	require.NoError(t, json.Unmarshal([]byte(pb.String()), &praw))

	pgot := make([]string, 0, len(praw))
	for k := range praw {
		pgot = append(pgot, k)
	}
	assert.ElementsMatch(t, append(got, "postgresql"), pgot)
}

func TestDetails_CarriesContentNames(t *testing.T) {
	d := manifest.Details{
		Databases: []string{"probe"},
		Relations: []manifest.Relation{
			{Schema: "public", Name: "customers", SizeBytes: 4096},
		},
	}
	var b strings.Builder
	require.NoError(t, manifest.EncodeDetails(&b, d))

	got, err := manifest.DecodeDetails(strings.NewReader(b.String()))
	require.NoError(t, err)
	assert.Equal(t, d, got)
	assert.Contains(t, b.String(), "customers")
}

// Timestamps are stored in UTC. A manifest carrying a local offset would sort
// wrongly against its siblings and confuse a PITR target.
func TestEncode_NormalisesTimestampsToUTC(t *testing.T) {
	m := fixture()
	m.StartedAt = time.Date(2026, 9, 5, 4, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	var b strings.Builder
	require.NoError(t, manifest.Encode(&b, m))
	assert.Contains(t, b.String(), `"started_at": "2026-09-05T02:00:00Z"`)
}

func TestValidate(t *testing.T) {
	require.NoError(t, fixture().Validate())

	bad := fixture()
	bad.Objects[0].SHA256 = "not-a-digest"
	assert.Error(t, bad.Validate(), "a malformed digest makes integrity checking impossible")

	bad = fixture()
	bad.Objects[0].Recipients = nil
	assert.Error(t, bad.Validate(), "an encrypted object with no recipients cannot be opened")
}
