package cmd

import (
	"path/filepath"
	"testing"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/shoenig/test"
)

func TestExecutionContextForOptions(t *testing.T) {
	root := t.TempDir()
	options := ExecuteOptions{
		Repository:       &attegit.Repo{},
		RepositoryRoot:   root,
		WorkingDirectory: "nested/working",
	}

	execution, err := executionContextForOptions(options)
	test.NoError(t, err)
	test.EqOp(t, root, execution.root)
	test.EqOp(t, filepath.Join(root, "nested", "working"), execution.workingDirectory)
	test.EqOp(t, "nested/working", execution.relative)
}

func TestExecutionContextForOptionsRejectsInvalidWorkingDirectory(t *testing.T) {
	options := ExecuteOptions{
		Repository:       &attegit.Repo{},
		RepositoryRoot:   "/repository",
		WorkingDirectory: "../outside",
	}

	_, err := executionContextForOptions(options)
	test.ErrorContains(t, err, "repository-relative")
}

func TestExecutionContextForOptionsRequiresRepositoryRoot(t *testing.T) {
	_, err := executionContextForOptions(ExecuteOptions{Repository: &attegit.Repo{}})
	test.ErrorContains(t, err, "repository root is required")
}
