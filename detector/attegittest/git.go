// Package attegittest provides helpers for tests that construct Git repositories.
package attegittest

import (
	"os/exec"
	"testing"

	"github.com/shoenig/test/must"
)

// InitGitRepo initializes a Git repository with deterministic test settings.
func InitGitRepo(t *testing.T, dir string) {
	t.Helper()
	RunGitScript(t, dir, `
set -x
git init
git config user.email test@example.com
git config user.name Test
git config commit.gpgSign false
git config tag.gpgSign false
`)
}

// RunGitScript runs script from dir and fails t when Git reports an error.
func RunGitScript(t *testing.T, dir, script string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git script: %s", output))
}
