package attegit

import (
	"testing"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// dirtyAfter reports what HasUncommittedChanges says about a committed
// repository after script ran in it.
func dirtyAfter(t *testing.T, script string) bool {
	t.Helper()
	git := committedRepo(t, map[string]string{
		"tracked.txt": "one",
		"other.txt":   "two",
		".gitignore":  "ignored.txt\n",
	})
	git.RunGitScript(t, script)
	got, err := HasUncommittedChanges(git.Dir())
	must.NoError(t, err)
	return got
}

func TestHasUncommittedChanges(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   bool
	}{
		{name: "a clean tree", script: "true", want: false},
		{name: "an ignored file", script: "echo x > ignored.txt", want: false},
		{name: "a file that was only touched", script: "touch tracked.txt", want: false},
		{name: "an untracked file", script: "echo x > new.txt", want: true},
		{name: "an unstaged edit", script: "echo edited > tracked.txt", want: true},
		{name: "a staged edit", script: "echo staged > tracked.txt && git add tracked.txt", want: true},
		{name: "a deleted file", script: "rm other.txt", want: true},
		{
			name:   "a staged edit reverted on disk",
			script: "echo staged > tracked.txt && git add tracked.txt && echo one > tracked.txt",
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			test.EqOp(t, tt.want, dirtyAfter(t, tt.script))
		})
	}
}

func TestHasUncommittedChangesRejectsANonRepository(t *testing.T) {
	_, err := HasUncommittedChanges(t.TempDir())

	test.Error(t, err)
}
