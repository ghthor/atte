package cone_test

import (
	"testing"

	"github.com/ghthor/atte/cone"
	"github.com/ghthor/atte/detector"
	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/detector/attegittest"
	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/detector/scanner"
	"github.com/ghthor/atte/reference"
	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func defaultScanner(t *testing.T) detector.Scanner {
	t.Helper()
	builder, err := scanner.NewDefault()
	must.NoError(t, err)
	scan, err := builder.Compile()
	must.NoError(t, err)
	return scan
}

func openFiles(t *testing.T, files map[string]string) *attegit.Repo {
	t.Helper()
	git := attegittest.NewGitRepo(t)
	git.WriteFiles(t, files)
	git.CommitAll(t, "init")
	repo, err := attegit.Open(git.Dir(), "HEAD")
	must.NoError(t, err)
	return repo
}

func fileID(t *testing.T, path string) graph.EntityID {
	t.Helper()
	blob, err := reference.ParseBlob(path)
	must.NoError(t, err)
	return attegit.EntityID(blob)
}

func TestCaptureBuildsTheCompleteGraph(t *testing.T) {
	repo := openFiles(t, map[string]string{
		"go.mod":  "module example.com/root\n\ngo 1.20\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})

	snapshot, err := cone.Capture(t.Context(), defaultScanner(t), repo)

	must.NoError(t, err)
	test.NotNil(t, snapshot.Graph)
	test.Nil(t, snapshot.Hashes)
	test.True(t, snapshot.Graph.Has(fileID(t, "go.mod")), test.Sprint("graph should contain go.mod"))
	test.True(t, snapshot.Graph.Has(fileID(t, "main.go")), test.Sprint("graph should contain main.go"))
	test.True(t, len(snapshot.Graph.Entities) > 3, test.Sprint("detector entities such as the Go package should be merged into the Git tree graph"))
}

func TestCaptureComputesContentHashesOnRequest(t *testing.T) {
	repo := openFiles(t, map[string]string{
		"go.mod":  "module example.com/root\n\ngo 1.20\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})

	snapshot, err := cone.Capture(t.Context(), defaultScanner(t), repo, cone.WithContentHashes())

	must.NoError(t, err)
	must.NotNil(t, snapshot.Hashes)
	for id := range snapshot.Graph.Entities {
		_, err := snapshot.Hashes.Sum(id)
		must.NoError(t, err)
	}
}

func TestCaptureHashesDifferWhenContentDiffers(t *testing.T) {
	sum := func(body string) string {
		repo := openFiles(t, map[string]string{
			"go.mod":  "module example.com/root\n\ngo 1.20\n",
			"main.go": body,
		})
		snapshot, err := cone.Capture(t.Context(), defaultScanner(t), repo, cone.WithContentHashes())
		must.NoError(t, err)
		hash, err := snapshot.Hashes.Sum(fileID(t, "main.go"))
		must.NoError(t, err)
		return hash.String()
	}

	test.EqOp(t, sum("package main\n"), sum("package main\n"))
	test.NotEq(t, sum("package main\n"), sum("package main\n\nfunc main() {}\n"))
}

func TestCaptureWithoutHashesToleratesUnresolvedExternalImports(t *testing.T) {
	repo := openFiles(t, map[string]string{
		"go.mod":  "module example.com/root\n\ngo 1.20\n",
		"main.go": "package main\n\nimport _ \"github.com/shoenig/test/must\"\n",
	})

	_, err := cone.Capture(t.Context(), defaultScanner(t), repo)
	test.NoError(t, err)

	_, err = cone.Capture(t.Context(), defaultScanner(t), repo, cone.WithContentHashes())
	test.ErrorContains(t, err, "matches no require directive")
}
