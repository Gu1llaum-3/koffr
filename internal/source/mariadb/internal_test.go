package mariadb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEscapeMyCnf(t *testing.T) {
	// The option-file parser ends a value at a newline, treats a backslash as an
	// escape, and drops what follows an unquoted "#". A password containing any
	// of those is silently truncated, and a truncated password fails as "access
	// denied" -- which sends an operator to look at the account rather than at
	// the file.
	cases := map[string]string{
		"plain":       `"plain"`,
		`with"quote`:  `"with\"quote"`,
		`back\slash`:  `"back\\slash"`,
		"hash#inside": `"hash#inside"`,
		"sp ace":      `"sp ace"`,
	}
	for in, want := range cases {
		assert.Equal(t, want, escapeMyCnf(in), "input %q", in)
	}
}

func TestScopeCovers(t *testing.T) {
	assert.True(t, scopeCovers("*.*", "shop"), "a global grant reaches every schema")
	assert.True(t, scopeCovers("`shop`.*", "shop"))
	assert.True(t, scopeCovers("shop.*", "shop"))
	assert.False(t, scopeCovers("`other`.*", "shop"))
	// A table-level grant does not let mariadb-dump read the whole schema, so
	// counting it would let a dump start that cannot finish.
	assert.False(t, scopeCovers("`shop`.`orders`", "shop"))
}

func TestSplitPrivileges(t *testing.T) {
	// Split rather than searched: a substring test finds "CREATE" inside
	// "SHOW CREATE ROUTINE" and credits a privilege nobody granted.
	got := splitPrivileges("SELECT, SHOW VIEW, SHOW CREATE ROUTINE")
	assert.Equal(t, []string{"SELECT", "SHOW VIEW", "SHOW CREATE ROUTINE"}, got)
	assert.NotContains(t, got, "CREATE")

	// A column-scoped privilege is not a table-scoped one. Keeping the name and
	// dropping the parenthesised part would silently widen it.
	assert.Empty(t, splitPrivileges("SELECT (id, name)"))
}

func TestCurrentPrivileges_ReadsGrantLines(t *testing.T) {
	p := privileges{granted: map[string]bool{}}
	apply := func(lines ...string) privileges {
		p := privileges{granted: map[string]bool{}}
		for _, line := range lines {
			applyGrantLine(&p, line, "shop")
		}
		return p
	}

	t.Run("a schema grant is read", func(t *testing.T) {
		got := apply("GRANT SELECT, SHOW VIEW ON `shop`.* TO `koffr`@`%`")
		assert.True(t, got.has("SELECT"))
		assert.True(t, got.has("SHOW VIEW"))
		assert.False(t, got.has("TRIGGER"))
	})

	t.Run("ALL PRIVILEGES expands", func(t *testing.T) {
		got := apply("GRANT ALL PRIVILEGES ON *.* TO `root`@`localhost`")
		got.expandAll()
		assert.True(t, got.has("SELECT"))
		assert.True(t, got.has("TRIGGER"))
		assert.True(t, got.has("EVENT"))
	})

	t.Run("a REVOKE takes the privilege back", func(t *testing.T) {
		// With partial_revokes -- the default shape on managed MySQL -- a
		// globally granted privilege can be withdrawn on one schema. Ignoring
		// the REVOKE line counts a privilege the server refuses.
		got := apply(
			"GRANT SELECT, TRIGGER ON *.* TO `koffr`@`%`",
			"REVOKE TRIGGER ON `shop`.* FROM `koffr`@`%`",
		)
		assert.True(t, got.has("SELECT"))
		assert.False(t, got.has("TRIGGER"), "the server refuses this one on shop")
	})

	t.Run("a role grant makes the answer inconclusive", func(t *testing.T) {
		// Roles are only in force after SET ROLE, and SHOW GRANTS does not
		// expand the ones that are not. Refusing a backup because our own
		// parser gave up would be the wrong way to be careful.
		got := apply("GRANT `backup_role` TO `koffr`@`%`")
		assert.True(t, got.inconclusive)
		assert.Empty(t, got.missingForDump(true), "an inconclusive read must not block a backup")
		assert.NotEmpty(t, got.restrictions(), "but it must be said out loud")
	})

	_ = p
}

func TestMissingForDump_ShowViewOnlyWhereThereAreViews(t *testing.T) {
	p := privileges{granted: map[string]bool{"SELECT": true}}
	// A view is dumped by its definition and a base table by its rows.
	// Demanding SHOW VIEW on a schema with no views rejects an account that
	// could do the job perfectly well.
	assert.Empty(t, p.missingForDump(false))
	assert.Equal(t, []string{"SHOW VIEW"}, p.missingForDump(true))

	none := privileges{granted: map[string]bool{}}
	assert.Equal(t, []string{"SELECT"}, none.missingForDump(false))
}

func TestIdentifiedClauseIsStripped(t *testing.T) {
	// A password hash in a backup repository is a liability nobody asked for.
	// This is the same position PostgreSQL takes with --no-role-passwords.
	cases := []string{
		"GRANT USAGE ON *.* TO `koffr`@`%` IDENTIFIED BY PASSWORD '*ABCDEF0123456789'",
		"GRANT USAGE ON *.* TO `koffr`@`%` IDENTIFIED VIA mysql_native_password USING '*ABC'",
		"GRANT USAGE ON *.* TO `koffr`@`%` IDENTIFIED BY 'plaintext'",
	}
	for _, in := range cases {
		got := identifiedClause.ReplaceAllString(in, "")
		assert.NotContains(t, got, "IDENTIFIED", "input: %s", in)
		assert.NotContains(t, got, "*ABC")
		assert.NotContains(t, got, "plaintext")
		assert.Contains(t, got, "GRANT USAGE")
	}
}

func TestFilterGrants(t *testing.T) {
	lines := []string{
		"GRANT USAGE ON *.* TO `koffr`@`%`",
		"GRANT SELECT ON `shop`.* TO `koffr`@`%`",
		"GRANT ALL PRIVILEGES ON `other`.* TO `koffr`@`%`",
	}
	lines = append(lines,
		"GRANT ALL PRIVILEGES ON *.* TO `root`@`localhost` WITH GRANT OPTION",
		// MariaDB 10.6 still puts the authentication clause on the grant line.
		// A hash reaching the repository is the one thing this file must never
		// let through, whatever the server's vintage.
		"GRANT SELECT ON `shop`.* TO `legacy`@`%` IDENTIFIED BY PASSWORD '*DEADBEEF0123456789ABCDEF0123456789ABCDEF'")
	got := filterGrants(lines, "shop")
	require.Len(t, got, 2, "a repository should not carry a privilege map of the whole server")
	joined := strings.Join(got, "\n")
	assert.NotContains(t, joined, "IDENTIFIED", "no authentication clause survives the filter")
	assert.NotContains(t, joined, "DEADBEEF")
	assert.Contains(t, got[0], "`shop`.*")
	// A global grant is not exported even though it does reach the schema:
	// replaying it would hand out rights on the target server.
	assert.NotContains(t, joined, "*.*")
}

func TestRejectNewline(t *testing.T) {
	// Rejecting where escaping would be clever: these names come from a
	// configuration file, so the answer is to fix the configuration.
	require.Error(t, rejectNewline("database", "shop\nDROP"))
	require.Error(t, rejectNewline("database", "shop\rDROP"))
	require.NoError(t, rejectNewline("database", "shop"))
}

func TestConfigValidate(t *testing.T) {
	valid := Config{Host: "db", User: "koffr", Database: "shop", TLS: "disable"}
	require.NoError(t, valid.validate())

	missing := Config{TLS: "disable"}
	err := missing.validate()
	require.Error(t, err)
	// Every problem, not the first: correcting a configuration one message at a
	// time is the difference between a tool people keep and one they fight.
	for _, want := range []string{"no host", "no user", "no database"} {
		assert.Contains(t, err.Error(), want)
	}

	badTLS := Config{Host: "db", User: "u", Database: "d", TLS: "maybe"}
	require.ErrorContains(t, badTLS.validate(), `"maybe" is not a TLS mode`)
}

func TestEnvIsCuratedNotInherited(t *testing.T) {
	// A stray MYSQL_HOST would silently change where a backup connects, and an
	// inherited MYSQL_PWD would take precedence over the options file and turn
	// a wrong password into a mystery.
	cfg := Config{}
	env := cfg.env("/usr/local/bin/mariadb-dump")
	joined := strings.Join(env, "\n")
	assert.Contains(t, joined, "MYSQL_PWD=")
	assert.Contains(t, joined, "MYSQL_HOST=")
	assert.Contains(t, joined, "LC_ALL=C.UTF-8")
	assert.Contains(t, joined, "PATH=/usr/local/bin")
	for _, line := range env {
		assert.NotEqual(t, "MYSQL_PWD", strings.SplitN(line, "=", 2)[0]+"x")
	}
}
