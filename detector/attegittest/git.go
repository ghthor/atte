// Package attegittest provides helpers for tests that construct Git repositories.
package attegittest

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shoenig/test/must"
)

// GitRepo is a temporary Git repository for use in tests.
type GitRepo struct {
	dir string
}

// NewGitRepo creates an initialized temporary Git repository with deterministic
// test settings.
func NewGitRepo(t *testing.T) *GitRepo {
	t.Helper()
	repo := &GitRepo{dir: t.TempDir()}
	repo.RunGitScript(t, `
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
	return r.dir
}

// WriteFile writes contents to a repository-relative path, creating parent
// directories as needed.
func (r *GitRepo) WriteFile(t *testing.T, path string, contents []byte, mode fs.FileMode) {
	t.Helper()
	filename := filepath.Join(r.dir, path)
	must.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	must.NoError(t, os.WriteFile(filename, contents, mode))
}

// RunGitScript runs script from the repository root and fails the test when it
// reports an error.
func (r *GitRepo) RunGitScript(t *testing.T, script string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = r.dir
	output, err := cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git script: %s", output))
}


// writeFilesOptions contains the default behavior for WriteFiles and its
// functional options.
type writeFilesOptions struct {
	trim             bool
	trailingNewline  bool
}

// WriteFilesOption configures WriteFiles.
type WriteFilesOption func(*writeFilesOptions)

// WithTrimContent controls whether file contents are trimmed before writing.
func WithTrimContent(enabled bool) WriteFilesOption {
	return func(options *writeFilesOptions) {
		options.trim = enabled
	}
}

// WithTrailingNewline controls whether a newline is appended to file contents.
func WithTrailingNewline(enabled bool) WriteFilesOption {
	return func(options *writeFilesOptions) {
		options.trailingNewline = enabled
	}
}

// WriteFiles writes multiple text files to the repository, creating parent
// directories as needed. By default, surrounding whitespace is trimmed from
// each file's contents and a trailing newline is appended. Use WriteFilesOption
// values to customize those defaults.
func (r *GitRepo) WriteFiles(t *testing.T, files map[string]string, options ...WriteFilesOption) {
	t.Helper()
	config := writeFilesOptions{trim: true, trailingNewline: true}
	for _, option := range options {
		option(&config)
	}
	for path, contents := range files {
		if config.trim {
			contents = strings.TrimSpace(contents)
		}
		if config.trailingNewline {
			contents += "\n"
		}
		r.WriteFile(t, path, []byte(contents), 0o644)
	}
}

// CommitAll stages all repository files and creates a commit.
func (r *GitRepo) CommitAll(t *testing.T, message string) {
	t.Helper()
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = r.dir
	output, err := cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git add: %s", output))

	cmd = exec.Command("git", "commit", "-qm", message)
	cmd.Dir = r.dir
	output, err = cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git commit: %s", output))
}
