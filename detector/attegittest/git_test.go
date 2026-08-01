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
	git.RunGitScript(t, "git rev-parse --is-inside-work-tree")
}

func TestWriteFileCreatesNestedDirectories(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile(t, "nested/deeper/file.txt", []byte("contents"), 0o644)

	got, err := os.ReadFile(filepath.Join(git.Dir(), "nested", "deeper", "file.txt"))
	must.NoError(t, err)
	must.EqOp(t, "contents", string(got))
}

func TestWriteFileOverwritesExistingFile(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile(t, "file.txt", []byte("first"), 0o644)
	git.WriteFile(t, "file.txt", []byte("second"), 0o644)

	got, err := os.ReadFile(filepath.Join(git.Dir(), "file.txt"))
	must.NoError(t, err)
	must.EqOp(t, "second", string(got))
}

func TestWriteFilesCreatesAllFiles(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFiles(t, map[string]string{
		"root.txt":         "root",
		"nested/child.txt": "child",
	})

	root, err := os.ReadFile(filepath.Join(git.Dir(), "root.txt"))
	must.NoError(t, err)
	must.EqOp(t, "root\n", string(root))
	child, err := os.ReadFile(filepath.Join(git.Dir(), "nested", "child.txt"))
	must.NoError(t, err)
	must.EqOp(t, "child\n", string(child))
}

func TestWriteFilesOptions(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFiles(t, map[string]string{
		"default.txt": "  default  ",
		"raw.txt":     "  raw  ",
	}, WithTrimContent(false), WithTrailingNewline(false))

	got, err := os.ReadFile(filepath.Join(git.Dir(), "default.txt"))
	must.NoError(t, err)
	must.EqOp(t, "  default  ", string(got))
	got, err = os.ReadFile(filepath.Join(git.Dir(), "raw.txt"))
	must.NoError(t, err)
	must.EqOp(t, "  raw  ", string(got))

	git.WriteFiles(t, map[string]string{"normalized.txt": "  normalized  "})
	got, err = os.ReadFile(filepath.Join(git.Dir(), "normalized.txt"))
	must.NoError(t, err)
	must.EqOp(t, "normalized\n", string(got))
}

func TestRunGitScriptCanLeaveFilesStaged(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile(t, "staged.txt", []byte("staged"), 0o644)
	git.RunGitScript(t, "git add staged.txt")

	cmd := "git diff --cached --name-only"
	got := runGitOutput(t, git.Dir(), cmd)
	must.EqOp(t, "staged.txt\n", got)
}

func TestRunGitScriptRunsFromRepoRoot(t *testing.T) {
	git := NewGitRepo(t)
	git.WriteFile(t, "marker.txt", []byte("here"), 0o644)
	git.RunGitScript(t, "test -f ./marker.txt")
}

func runGitOutput(t *testing.T, dir, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	must.NoError(t, err, must.Sprintf("git script: %s", output))
	return string(output)
}
