package restore

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The header mariadb-dump --databases actually produces, measured against
// MariaDB 10.11 and 11.4.
const dumpHeader = `-- MariaDB dump 10.19
--
-- Current Database: ` + "`shop`" + `
--

CREATE DATABASE /*!32312 IF NOT EXISTS*/ ` + "`shop`" +
	` /*!40100 DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci */;

USE ` + "`shop`" + `;

--
-- Table structure for table ` + "`orders`" + `
--

CREATE TABLE ` + "`orders`" + ` (
  ` + "`id`" + ` int(11) NOT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`orders`" + ` VALUES (1),(2);
`

func rewritten(t *testing.T, in, database string) string {
	t.Helper()
	body, err := io.ReadAll(retarget(strings.NewReader(in), database))
	require.NoError(t, err)
	return string(body)
}

// The defect this exists for: --into named a database, the empty database was
// created, and every row went back into the one the backup came from -- with
// the command reporting success.
func TestRetarget_SendsTheDataWhereItWasAsked(t *testing.T) {
	got := rewritten(t, dumpHeader, "shop_restored")

	assert.Contains(t, got, "USE `shop_restored`;")
	assert.NotContains(t, got, "USE `shop`;")
	assert.Contains(t, got, "CREATE DATABASE /*!32312 IF NOT EXISTS*/ `shop_restored`")

	// The character set travels with the rename. Stripping the statement
	// instead would land the data in a database created with whatever the
	// target server's defaults happen to be, which is a quieter kind of wrong.
	assert.Contains(t, got, "DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci")
}

func TestRetarget_LeavesTheBodyAlone(t *testing.T) {
	got := rewritten(t, dumpHeader, "elsewhere")
	assert.Contains(t, got, "CREATE TABLE `orders`")
	assert.Contains(t, got, "INSERT INTO `orders` VALUES (1),(2);")
}

// Rewriting stops at the first statement that carries schema or data, so
// nothing in the body can be touched however it is spelled. A row holding the
// text of a USE statement is the case that would otherwise corrupt silently.
func TestRetarget_NeverRewritesRowData(t *testing.T) {
	body := dumpHeader + "INSERT INTO `notes` VALUES ('USE `shop`;'),('CREATE DATABASE `shop`;');\n"
	got := rewritten(t, body, "elsewhere")

	assert.Contains(t, got, "VALUES ('USE `shop`;'),('CREATE DATABASE `shop`;');",
		"a row that merely looks like a statement is still a row")
}

// Restoring under the backup's own name is the ordinary case and must come
// through unchanged.
func TestRetarget_SameNameChangesNothing(t *testing.T) {
	assert.Equal(t, dumpHeader, rewritten(t, dumpHeader, "shop"))
}

func TestRetarget_QuotesABacktickInTheName(t *testing.T) {
	// Not a name anyone should choose, but one the client would otherwise read
	// as the end of the identifier.
	got := rewritten(t, dumpHeader, "od`d")
	assert.Contains(t, got, "USE `od``d`;")
}

func TestRetarget_HandlesADumpWithNoDatabaseStatements(t *testing.T) {
	// A dump taken without --databases has neither statement. It must pass
	// through untouched rather than gain one.
	plain := "CREATE TABLE `t` (`id` int);\nINSERT INTO `t` VALUES (1);\n"
	assert.Equal(t, plain, rewritten(t, plain, "anywhere"))
}

// A dump of several databases carries a USE per database. Rewriting the ones
// after the first would pour every database into the target and merge them --
// silently, with a success at the end. Koffr backs up one database at a time
// today, but the rewriter is pointed at a file an operator can also have made
// themselves, and this is the shape that punishes a rewriter with no end.
func TestRetarget_StopsAtTheFirstBodyAndLeavesLaterUseStatementsAlone(t *testing.T) {
	multi := dumpHeader +
		"\n--\n-- Current Database: `other`\n--\n\n" +
		"CREATE DATABASE /*!32312 IF NOT EXISTS*/ `other`;\n" +
		"USE `other`;\n" +
		"CREATE TABLE `t2` (`id` int);\n"

	got := rewritten(t, multi, "elsewhere")

	assert.Contains(t, got, "USE `elsewhere`;", "the first database is the one being restored")
	assert.Contains(t, got, "USE `other`;",
		"a later database keeps its own name; merging two databases into one is not a restore")
	assert.Contains(t, got, "CREATE DATABASE /*!32312 IF NOT EXISTS*/ `other`;")
}
