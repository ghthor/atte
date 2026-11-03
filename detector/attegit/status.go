package attegit

import (
	"bytes"
	"fmt"
)

// HasUncommittedChanges reports whether Git sees uncommitted changes in the
// repository at repositoryPath: unstaged, staged, or untracked and not
// ignored. These are the sources WithWorkingTree reads.
//
// Git decides what is dirty, so a file that was only touched is not reported,
// and a change the graph does not model, such as a mode change, is.
func HasUncommittedChanges(repositoryPath string) (bool, error) {
	// --no-optional-locks keeps a read-only question from taking the index
	// lock that a concurrent git command may need.
	output, err := gitCommand(
		repositoryPath,
		"--no-optional-locks",
		"status",
		"--porcelain",
		"-z",
		"--untracked-files=normal",
	).Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(bytes.TrimSpace(output)) > 0, nil
}
