// Package hooks_test exercises the git hook scripts.
//
// These scripts are safety controls, and a safety control that fails open is
// worse than no control: it produces confidence instead of protection. The
// secret guard did exactly that once -- BSD grep read a pattern starting with
// "-" as an option, so the private key armour it exists to catch passed
// silently. Hence these tests, which run with everything else.
package hooks_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scriptPath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "scripts", name))
	require.NoError(t, err)
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("%s not present: %v", name, err)
	}
	return abs
}

// newRepo gives each case its own repository, so staging in one cannot affect
// another and none of them can touch the real working tree.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "probe@example.invalid"},
		{"config", "user.name", "probe"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	return dir
}

func stage(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))

	cmd := exec.CommandContext(t.Context(), "git", "add", name)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git add: %s", out)
}

// runScript binds every child to the test's context, so a hung git or a script
// waiting on stdin fails the test instead of hanging the suite.
func runScript(t *testing.T, dir, script string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), script, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		require.ErrorAs(t, err, &exitErr, "running %s: %v", script, err)
		code = exitErr.ExitCode()
	}
	return string(out), code
}

func TestCheckSecrets_RejectsKeyMaterial(t *testing.T) {
	script := scriptPath(t, "check-secrets.sh")

	for _, tc := range []struct {
		name    string
		file    string
		content string
	}{
		{
			// The case that regressed: the pattern begins with "-".
			name: "openssh private key",
			file: "deploy_key",
			content: "-----BEGIN OPENSSH PRIVATE KEY-----\n" +
				"b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAA\n" +
				"-----END OPENSSH PRIVATE KEY-----\n",
		},
		{
			name: "rsa private key",
			file: "tls/server.key",
			content: "-----BEGIN RSA PRIVATE KEY-----\nMIIEow==\n" +
				"-----END RSA PRIVATE KEY-----\n",
		},
		{
			// Content, not filename: a key pasted into configuration has no
			// telltale name, which is the harder case to catch.
			name:    "age identity inside a config file",
			file:    "koffr.yml",
			content: "crypto:\n  identity: AGE-SECRET-KEY-1QQPQZRFR7ZZ2WCVXWCVXWCVXWCVXWCVXWCVXWCVXWCV\n",
		},
		{
			name:    "putty key",
			file:    "notes.txt",
			content: "PuTTY-User-Key-File-3: ssh-ed25519\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			stage(t, dir, tc.file, tc.content)

			out, code := runScript(t, dir, script)
			assert.Equal(t, 1, code, "script should refuse the commit\n%s", out)
			assert.Contains(t, out, tc.file)
		})
	}
}

// The guard must not fire on prose about keys, or nobody will keep it enabled.
func TestCheckSecrets_AllowsInnocentContent(t *testing.T) {
	script := scriptPath(t, "check-secrets.sh")

	for _, tc := range []struct{ name, file, content string }{
		{"prose about keys", "docs/security.md",
			"The manifest records age recipients, never the private identity.\n"},
		{"a public age recipient", "koffr.yml",
			"crypto:\n  recipients:\n    - age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p\n"},
		{"a certificate, which is public", "tls/server.crt",
			"-----BEGIN CERTIFICATE-----\nMIIDazCCAlOgAwIBAgI=\n-----END CERTIFICATE-----\n"},
		{"an ssh public key", "authorized_keys",
			"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIH probe@host\n"},
		{"go source", "main.go", "package main\n\nfunc main() {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			stage(t, dir, tc.file, tc.content)

			out, code := runScript(t, dir, script)
			assert.Equal(t, 0, code, "false positive on %s\n%s", tc.file, out)
		})
	}
}

// The allowlist exists so that this file, and the script itself, can carry the
// patterns they are about. It must stay narrow: an allowlisted path that later
// gains a real key would be invisible.
func TestCheckSecrets_AllowlistIsNarrow(t *testing.T) {
	script := scriptPath(t, "check-secrets.sh")
	source, err := os.ReadFile(script)
	require.NoError(t, err)

	inList := false
	for line := range strings.SplitSeq(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "allowlist_files=("):
			inList = true
		case inList && trimmed == ")":
			inList = false
		case inList && trimmed != "":
			assert.True(t,
				strings.Contains(trimmed, "check-secrets.sh") ||
					strings.Contains(trimmed, "hooks_test.go"),
				"unexpected allowlist entry %s: only files that describe the patterns belong here", trimmed)
		}
	}
}

func TestCheckSecrets_EmptyIndex(t *testing.T) {
	script := scriptPath(t, "check-secrets.sh")
	out, code := runScript(t, newRepo(t), script)
	assert.Equal(t, 0, code, out)
}

func TestCheckCommitMsg(t *testing.T) {
	script := scriptPath(t, "check-commit-msg.sh")

	for _, tc := range []struct {
		name     string
		msg      string
		wantCode int
	}{
		{"subject only", "Add the storage contract suite\n", 0},
		{"subject and body", "Add the storage contract suite\n\nThe suite is written before the first implementation.\n", 0},
		{
			name:     "subject too long",
			msg:      strings.Repeat("x", 73) + "\n",
			wantCode: 1,
		},
		{
			name:     "subject ends with a period",
			msg:      "Add the storage contract suite.\n",
			wantCode: 1,
		},
		{
			name:     "body glued to subject",
			msg:      "Add the storage contract suite\nThe suite comes first.\n",
			wantCode: 1,
		},
		// Messages git wrote itself are not the author's to reformat.
		{"merge commit", "Merge branch 'main' into feature-branch-with-a-very-long-name-here\n", 0},
		{"revert commit", "Revert \"Add the storage contract suite\"\n", 0},
		{"fixup commit", "fixup! Add the storage contract suite\n", 0},
		{"empty message", "\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "COMMIT_EDITMSG")
			require.NoError(t, os.WriteFile(path, []byte(tc.msg), 0o600))

			out, code := runScript(t, dir, script, path)
			assert.Equal(t, tc.wantCode, code, out)
		})
	}
}

// git appends comments and a diff below the scissors line. Neither is part of
// the message, and counting them would reject perfectly good commits.
func TestCheckCommitMsg_IgnoresGitBoilerplate(t *testing.T) {
	script := scriptPath(t, "check-commit-msg.sh")
	dir := t.TempDir()
	path := filepath.Join(dir, "COMMIT_EDITMSG")

	msg := "Add the storage contract suite\n" +
		"\n" +
		"Written before the first implementation.\n" +
		"# Please enter the commit message for your changes.\n" +
		"# ------------------------ >8 ------------------------\n" +
		"diff --git a/x b/x\n" +
		strings.Repeat("+", 100) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(msg), 0o600))

	out, code := runScript(t, dir, script, path)
	assert.Equal(t, 0, code, out)
}
