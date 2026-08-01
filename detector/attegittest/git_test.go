package attegittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shoenig/test/must"
)

func TestNewGitRepoCreatesUsableRepo(t *testing.T) {
	git := NewGitRepo(t)
	must.NotEq(t, "", git.Dir())
	git.RunGitScript("git rev-parse --is-inside-work-tree")
}

func TestWriteFileCreatesNestedDirectories(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile("nested/deeper/file.txt", []byte("contents"), 0o644)

	got, err := os.ReadFile(filepath.Join(git.Dir(), "nested", "deeper", "file.txt"))
	must.NoError(t, err)
	must.EqOp(t, "contents", string(got))
}

func TestWriteFileOverwritesExistingFile(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile("file.txt", []byte("first"), 0o644)
	git.WriteFile("file.txt", []byte("second"), 0o644)

	got, err := os.ReadFile(filepath.Join(git.Dir(), "file.txt"))
	must.NoError(t, err)
	must.EqOp(t, "second", string(got))
}

func TestRunGitScriptCanLeaveFilesStaged(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile("staged.txt", []byte("staged"), 0o644)
	git.RunGitScript("git add staged.txt")

	cmd := "git diff --cached --name-only"
	got := runGitOutput(t, git.Dir(), cmd)
	must.EqOp(t, "staged.txt\n", got)
}

func TestRunGitScriptRunsFromRepoRoot(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile("marker.txt", []byte("here"), 0o644)
	git.RunGitScript("test -f ./marker.txt")
}

func runGitOutput(t *testing.T, dir, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git script: %s", output))
	return string(output)
}
