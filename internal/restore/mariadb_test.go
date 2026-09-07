package restore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// fakeMariaTool stands in for the mariadb client and records how it was called.
// A real program run by the real executor, for the same reason the PostgreSQL
// fakes are: what is under test is the command Koffr builds and what it pipes
// into it.
func fakeMariaTool(t *testing.T, exitCode int, stderr string) (binDir, recordDir string) {
	t.Helper()
	binDir, recordDir = t.TempDir(), t.TempDir()
	// Appended to, not overwritten: the driver runs the client more than once
	// per restore, and the run that matters is the one carrying the dump.
	script := fmt.Sprintf(`#!/bin/sh
rec=%q/mariadb
printf '%%s\n' "$@" >> "$rec.args"
/bin/cat >> "$rec.stdin"
printf '%%s' %q >&2
exit %d
`, recordDir, stderr, exitCode)
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "mariadb"), []byte(script), 0o700))
	return binDir, recordDir
}

func mariaTargetConfig(t *testing.T, binDir string) mariadb.Config {
	t.Helper()
	return mariadb.Config{
		Host: "db.invalid", Port: 3306,
		User: "koffr", Password: testutil.SecretSentinel,
		Database: "placeholder", TLS: "disable",
		BinDir: binDir, ToolRunner: local.New(),
	}
}

const mariaDumpBody = "-- MariaDB dump 10.19\n" +
	"CREATE DATABASE /*!32312 IF NOT EXISTS*/ `shop` /*!40100 DEFAULT CHARACTER SET utf8mb4 */;\n" +
	"USE `shop`;\n" +
	"CREATE TABLE `orders` (`id` int);\n" +
	"INSERT INTO `orders` VALUES (1);\n"

// The defect a real deployment found: --into created the named database, put
// every row back into the one the backup came from, and reported success. On a
// source that had moved on since the backup, that is a silent revert of live
// data by the command whose whole point was to leave it alone.
func TestMariaDB_RestoresIntoTheRequestedDatabaseAndNotTheDumpsOwn(t *testing.T) {
	binDir, recordDir := fakeMariaTool(t, 0, "")
	driver := restore.MariaDB{Config: mariaTargetConfig(t, binDir)}

	_, err := driver.Restore(context.Background(), local.New(), restore.MariaDBRequest{
		Database: "shop_restored",
		Dump:     strings.NewReader(mariaDumpBody),
	})
	require.NoError(t, err)

	piped := recorded(t, recordDir, "mariadb", "stdin")
	assert.Contains(t, piped, "USE `shop_restored`;")
	assert.NotContains(t, piped, "USE `shop`;",
		"the USE inside the dump beats --database, so leaving it is how data lands on the wrong database")
	assert.Contains(t, piped, "CREATE DATABASE /*!32312 IF NOT EXISTS*/ `shop_restored`")
	assert.Contains(t, piped, "DEFAULT CHARACTER SET utf8mb4",
		"the character set travels with the rename")

	// And the rows are untouched.
	assert.Contains(t, piped, "INSERT INTO `orders` VALUES (1);")
}

func TestMariaDB_NoCredentialInArgumentsOrEnvironment(t *testing.T) {
	binDir, recordDir := fakeMariaTool(t, 0, "")
	driver := restore.MariaDB{Config: mariaTargetConfig(t, binDir)}

	_, err := driver.Restore(context.Background(), local.New(), restore.MariaDBRequest{
		Database: "shop", Dump: strings.NewReader(mariaDumpBody),
	})
	require.NoError(t, err)

	// ENF-021: argv is world-readable for as long as the restore runs.
	testutil.AssertNoSecretLeak(t, recorded(t, recordDir, "mariadb", "args"))
}

func TestMariaDB_RefusesAnEmptyTarget(t *testing.T) {
	binDir, _ := fakeMariaTool(t, 0, "")
	driver := restore.MariaDB{Config: mariaTargetConfig(t, binDir)}

	_, err := driver.Restore(context.Background(), local.New(), restore.MariaDBRequest{
		Dump: strings.NewReader(mariaDumpBody),
	})
	require.ErrorContains(t, err, "no target database",
		"the database to restore into is never taken from the backup")
}

func TestMariaDB_FailureCarriesTheToolsOwnMessage(t *testing.T) {
	binDir, _ := fakeMariaTool(t, 1, "ERROR 1045 (28000): Access denied")
	driver := restore.MariaDB{Config: mariaTargetConfig(t, binDir)}

	_, err := driver.Restore(context.Background(), local.New(), restore.MariaDBRequest{
		Database: "shop", Dump: strings.NewReader(mariaDumpBody),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Access denied",
		"the client's own words are the difference between a diagnosis and a shrug")
}
