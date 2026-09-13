// Package attegitmock provides temporary Git repositories for examples and tests.
package attegitmock

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a temporary Git repository.
type Repo struct {
	root string
	dir  string
}

// New creates and initializes a temporary Git repository with deterministic
// commit settings. ctx controls the initialization commands.
func New(ctx context.Context) (*Repo, error) {
	root, err := os.MkdirTemp("", "attegitmock-")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	repo := &Repo{root: root, dir: dir}
	output, err := repo.RunGitScript(ctx, `
set -eu
git init --quiet
git config user.email test@example.com
git config user.name Test
git config commit.gpgSign false
git config tag.gpgSign false
`)
	if err != nil {
		_ = repo.Cleanup()
		return nil, fmt.Errorf("initialize repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return repo, nil
}

// Dir returns the repository directory.
func (repo *Repo) Dir() string {
	return repo.dir
}

// Cleanup removes the temporary repository.
func (repo *Repo) Cleanup() error {
	return os.RemoveAll(repo.root)
}

// WriteFile writes contents to a repository-relative path, creating parent
// directories as needed.
func (repo *Repo) WriteFile(path string, contents []byte, mode fs.FileMode) error {
	filename := filepath.Join(repo.dir, path)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filename, contents, mode)
}

// RunGitScript runs a shell script from the repository root. ctx controls the
// script process.
func (repo *Repo) RunGitScript(ctx context.Context, script string) ([]byte, error) {
	command := exec.CommandContext(ctx, "bash", "-c", script)
	command.Dir = repo.dir
	return command.CombinedOutput()
}

// CommitAll stages all repository files and creates a commit.
func (repo *Repo) CommitAll(message string) error {
	if err := repo.runGit("add", "."); err != nil {
		return err
	}
	return repo.runGit("commit", "-qm", message)
}

func (repo *Repo) runGit(args ...string) error {
	command := exec.Command("git", args...)
	command.Dir = repo.dir
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
