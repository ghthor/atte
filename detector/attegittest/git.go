// Package attegittest provides helpers for tests that construct Git repositories.
package attegittest

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/ghthor/atte/detector/attegitmock"
	"github.com/shoenig/test/must"
)

// GitRepo is a temporary Git repository for use in tests.
type GitRepo struct {
	repo *attegitmock.Repo
}

// NewGitRepo creates an initialized temporary Git repository with deterministic
// test settings.
func NewGitRepo(t *testing.T) *GitRepo {
	t.Helper()
	repo, err := attegitmock.New(t.Context())
	must.NoError(t, err)
	git := &GitRepo{repo: repo}
	t.Cleanup(func() { must.NoError(t, repo.Cleanup()) })
	return git
}

// Dir returns the path to the repository.
func (git *GitRepo) Dir() string {
	return git.repo.Dir()
}

// WriteFile writes contents to a repository-relative path, creating parent
// directories as needed.
func (git *GitRepo) WriteFile(t *testing.T, path string, contents []byte, mode fs.FileMode) {
	t.Helper()
	must.NoError(t, git.repo.WriteFile(path, contents, mode))
}

// RunGitScript runs a script from the repository root and fails the test when
// it reports an error.
func (git *GitRepo) RunGitScript(t *testing.T, script string) {
	t.Helper()
	output, err := git.repo.RunGitScript(t.Context(), script)
	must.NoError(t, err, must.Sprintf("git script: %s", output))
}

// writeFilesOptions contains the default behavior for WriteFiles and its
// functional options.
type writeFilesOptions struct {
	trim            bool
	trailingNewline bool
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
func (git *GitRepo) WriteFiles(t *testing.T, files map[string]string, options ...WriteFilesOption) {
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
		git.WriteFile(t, path, []byte(contents), 0o644)
	}
}

// CommitAll stages all repository files and creates a commit.
func (git *GitRepo) CommitAll(t *testing.T, message string) {
	t.Helper()
	must.NoError(t, git.repo.CommitAll(message))
}
