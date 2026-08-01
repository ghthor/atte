// Package attegittest provides helpers for tests that construct Git repositories.
package attegittest

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shoenig/test/must"
)

// GitRepo is a temporary Git repository for use in tests.
type GitRepo struct {
	t   *testing.T
	dir string
}

// NewGitRepo creates an initialized temporary Git repository with deterministic
// test settings.
func NewGitRepo(t *testing.T) *GitRepo {
	t.Helper()
	repo := &GitRepo{t: t, dir: t.TempDir()}
	repo.RunGitScript(`
set -x
git init
git config user.email test@example.com
git config user.name Test
git config commit.gpgSign false
git config tag.gpgSign false
`)
	return repo
}

// Dir returns the path to the repository.
func (r *GitRepo) Dir() string {
	r.t.Helper()
	return r.dir
}

// WriteFile writes contents to a repository-relative path, creating parent
// directories as needed.
func (r *GitRepo) WriteFile(path string, contents []byte, mode fs.FileMode) {
	r.t.Helper()
	filename := filepath.Join(r.dir, path)
	must.NoError(r.t, os.MkdirAll(filepath.Dir(filename), 0o755))
	must.NoError(r.t, os.WriteFile(filename, contents, mode))
}

// RunGitScript runs script from the repository root and fails the test when it
// reports an error.
func (r *GitRepo) RunGitScript(script string) {
	r.t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = r.dir
	output, err := cmd.CombinedOutput()
	must.NoError(r.t, err, must.Sprintf("git script: %s", output))
}
