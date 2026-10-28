// Package attegit detects the object and tree structure of a Git revision.
package attegit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ghthor/atte/detector/graph"
	"github.com/ghthor/atte/reference"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
)

// Kind identifies the Git object represented by an Obj.
type Kind uint8

const (
	// Blob identifies a Git blob object.
	Blob Kind = iota + 1
	// Tree identifies a Git tree object.
	Tree
)

// Source identifies where an object's contents are read from.
type Source uint8

const (
	// GitSource identifies content read from the selected Git revision.
	GitSource Source = iota + 1
	// WorkingTreeSource identifies content overlaid from the working tree.
	WorkingTreeSource
)

// Obj is an object discovered in a Git tree.
type Obj struct {
	Path   reference.Path
	Hash   plumbing.Hash
	Kind   Kind
	Source Source
}

// OpenOption configures Open.

type OpenOption func(*openConfig)

type openConfig struct {
	workingTree bool
}

// WithWorkingTree overlays the selected ref with the current working tree.
func WithWorkingTree() OpenOption {
	return func(cfg *openConfig) { cfg.workingTree = true }
}

type DirTree map[reference.Tree][]reference.Tree

// Repo contains the objects and direct-child index for one Git revision.
// Blob contents are cached and shared: see Show.
type Repo struct {
	Obj     map[reference.Path]Obj
	ObjKeys []reference.Path

	// index of Tree => all children (Tree & Blob)
	Tree map[reference.Tree][]Obj
	// index of Tree => all child Tree's
	DirTree DirTree

	TreeKeys []reference.Tree
	BlobKeys []reference.Blob

	// memoized graph values
	fullGraph *graph.Graph
	treeGraph *graph.Graph // tree => tree/sub => tree/sub/tree only

	blobs map[reference.Blob][]byte

	// gitDirs holds the repository's common Git directory followed by its
	// object alternates. gitMu guards it because Show replaces stale entries.
	gitDirs  []gitDir
	gitMu    sync.Mutex
	worktree billy.Filesystem
	mu       sync.RWMutex
}

// Open opens repositoryPath at ref, which may be a commit, branch, or tag. Options can overlay the working tree.
func Open(repositoryPath, ref string, options ...OpenOption) (*Repo, error) {
	if strings.TrimSpace(repositoryPath) == "" {
		return nil, errors.New("repository path is empty")
	}
	if strings.TrimSpace(ref) == "" {
		return nil, errors.New("git ref is empty")
	}
	cfg := openConfig{}
	for _, option := range options {
		option(&cfg)
	}

	dirs, err := openGitRepositories(repositoryPath)
	if err != nil {
		return nil, fmt.Errorf("open git repository: %w", err)
	}
	d := &Repo{
		gitDirs: dirs,
		Obj:     make(map[reference.Path]Obj),
		Tree:    make(map[reference.Tree][]Obj),
		DirTree: make(DirTree),
		blobs:   make(map[reference.Blob][]byte),
	}
	if err := d.readTree(repositoryPath, ref); err != nil {
		return nil, err
	}
	if cfg.workingTree {
		d.worktree = osfs.New(repositoryPath, osfs.WithBoundOS())
		if err := d.overlayWorkingTree(); err != nil {
			return nil, err
		}
	}
	d.rebuildIndexes()
	return d, nil
}

// gitDir is an opened Git directory: either the repository's own or one of its
// object alternates.
type gitDir struct {
	path string
	repo *git.Repository
}

// gitDirError records why one Git directory could not provide an object.
type gitDirError struct {
	index int // position in Repo.gitDirs
	path  string
	err   error
}

func (e gitDirError) Error() string { return fmt.Sprintf("git dir %q: %v", e.path, e.err) }

func (e gitDirError) Unwrap() error { return e.err }

// gitCommonDir returns the absolute path of the Git directory that holds the
// shared state (objects, refs, config) of the repository containing
// repositoryPath.
//
// For a normal clone this is <root>/.git, for a bare repository it is the
// repository itself, and for a linked worktree (where <root>/.git is a file
// containing "gitdir: ...") it is the .git directory of the main worktree
// rather than the per-worktree .git/worktrees/<name> directory.
func gitCommonDir(repositoryPath string) (string, error) {
	output, err := gitCommand(repositoryPath, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
		}
		return "", err
	}
	return filepath.Clean(strings.TrimSuffix(string(output), "\n")), nil
}

// readAlternates returns the object directories listed in the
// objects/info/alternates file of gitDirPath. Relative paths are resolved
// against the objects directory, as Git does. A missing file is not an error.
func readAlternates(gitDirPath string) ([]string, error) {
	objectsDir := filepath.Join(gitDirPath, "objects")
	file, err := os.Open(filepath.Join(objectsDir, "info", "alternates"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var alternates []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !filepath.IsAbs(line) {
			line = filepath.Join(objectsDir, line)
		}
		alternates = append(alternates, filepath.Clean(line))
	}
	return alternates, scanner.Err()
}

func openGitDir(gitDirPath string) (*git.Repository, error) {
	return git.Open(filesystem.NewStorage(osfs.New(gitDirPath), cache.NewObjectLRUDefault()), nil)
}

// openGitRepositories opens the Git directory backing repositoryPath and every
// (possibly nested) object alternate configured for it.
//
// The Git directory is resolved by asking Git, so repositoryPath may be a
// normal clone, a bare repository, or a linked worktree. Alternates that do not
// exist are logged and skipped, as Git itself only warns about them.
func openGitRepositories(repositoryPath string) ([]gitDir, error) {
	common, err := gitCommonDir(repositoryPath)
	if err != nil {
		return nil, err
	}
	dirs := make([]gitDir, 0, 2)
	seen := make(map[string]bool)
	for queue := []string{common}; len(queue) > 0; queue = queue[1:] {
		dirPath := queue[0]
		if seen[dirPath] {
			continue
		}
		seen[dirPath] = true
		repo, err := openGitDir(dirPath)
		if err != nil {
			return nil, fmt.Errorf("open git dir %q: %w", dirPath, err)
		}
		dirs = append(dirs, gitDir{path: dirPath, repo: repo})
		alternates, err := readAlternates(dirPath)
		if err != nil {
			return nil, fmt.Errorf("read alternates of %q: %w", dirPath, err)
		}
		for _, objectsDir := range alternates {
			if info, err := os.Stat(objectsDir); err != nil || !info.IsDir() {
				slog.Warn("ignoring unusable git alternate", "path", objectsDir, "error", err)
				continue
			}
			// An alternate is an objects directory; go-git wants its parent.
			queue = append(queue, filepath.Dir(objectsDir))
		}
	}
	return dirs, nil
}

// hashWorkingTreeFile streams a working-tree file once and returns the object
// id Git would assign to it if committed, as `git hash-object` prints.
func hashWorkingTreeFile(fsys billy.Filesystem, name string, size int64) (plumbing.Hash, error) {
	file, err := fsys.Open(name)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer func() { _ = file.Close() }()
	hasher := plumbing.NewHasher(plumbing.BlobObject, size)
	n, err := io.Copy(hasher, file)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if n != size {
		return plumbing.ZeroHash, errors.New("file changed while being read")
	}
	return hasher.Sum(), nil
}

func (r *Repo) readTree(repositoryPath, ref string) error {
	cmd := gitCommand(repositoryPath, "ls-tree", "-r", "-t", "--full-tree", ref)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create ls-tree stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ls-tree: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		obj, err := parseLine(scanner.Text())
		if err != nil {
			_ = cmd.Wait()
			return fmt.Errorf("parse ls-tree output: %w", err)
		}
		if _, exists := r.Obj[obj.Path]; exists {
			_ = cmd.Wait()
			return fmt.Errorf("duplicate repository path %q", obj.Path)
		}
		obj.Source = GitSource
		r.Obj[obj.Path] = obj
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("read ls-tree output: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("ls-tree %q: %w: %s", ref, err, msg)
		}
		return fmt.Errorf("ls-tree %q: %w", ref, err)
	}
	return nil
}

func (r *Repo) overlayWorkingTree() error {
	// The command must run in the repository root; the Billy filesystem's root
	// is not necessarily available as a portable OS path.
	root := r.worktree.Root()
	// Every path that may differ from the selected ref: unstaged and staged
	// changes (including deletions) and untracked files. --no-renames makes a
	// staged rename report the old path as well as the new one; without it Git
	// reports only the new path and the old path would stay visible.
	paths := make(map[string]struct{})
	for _, query := range [][]string{
		{"diff-files", "--name-only", "-z"},
		{"diff", "--cached", "--no-renames", "--name-only", "-z"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	} {
		names, err := runGitNul(root, query...)
		if err != nil {
			return fmt.Errorf("list working-tree changes (git %s): %w", strings.Join(query, " "), err)
		}
		for _, name := range names {
			paths[string(name)] = struct{}{}
		}
	}

	for _, name := range slices.Sorted(maps.Keys(paths)) {
		p, err := reference.ParseBlob(name)
		if err != nil {
			return err
		}
		info, err := r.worktree.Lstat(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Deleted in the working tree, staged or not: it must no longer
				// be visible even though it exists at the ref.
				r.removePath(p)
				continue
			}
			return fmt.Errorf("stat working-tree path %q: %w", p, err)
		}
		// FileMode's type bits occupy 0170000; 0120000 identifies a symlink.
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("working-tree symlink %q is not supported", p)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("working-tree path %q is not a regular file", p)
		}
		hash, err := hashWorkingTreeFile(r.worktree, name, info.Size())
		if err != nil {
			return fmt.Errorf("hash working-tree path %q: %w", p, err)
		}
		r.removePath(p)
		r.Obj[p] = Obj{Path: p, Hash: hash, Kind: Blob, Source: WorkingTreeSource}
	}
	r.pruneEmptyTrees()
	return nil
}

// pruneEmptyTrees drops trees that no longer contain any blob, such as those
// whose last file was deleted in the working tree. Git cannot represent empty
// trees, so none exist in the selected ref.
func (r *Repo) pruneEmptyTrees() {
	populated := make(map[reference.Path]bool)
	for p, obj := range r.Obj {
		if obj.Kind != Blob {
			continue
		}
		for dir := parentTree(p, obj.Kind); dir != ""; dir = dir.Parent() {
			populated[dir] = true
		}
	}
	for p, obj := range r.Obj {
		if obj.Kind == Tree && !populated[p] {
			delete(r.Obj, p)
		}
	}
}

func runGitNul(root string, args ...string) ([][]byte, error) {
	output, err := gitCommand(root, args...).Output()
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(output, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts, nil
}

func gitCommand(repositoryPath string, args ...string) *exec.Cmd {
	if repositoryPath != "" {
		args = append([]string{"-C", repositoryPath}, args...)
	}
	return exec.Command("git", args...)
}

func (r *Repo) removePath(p reference.Path) {
	for candidate := range r.Obj {
		if candidate == p || strings.HasPrefix(candidate.String(), p.String()+"/") {
			delete(r.Obj, candidate)
		}
	}
}

func (r *Repo) rebuildIndexes() {
	// Working-tree overlays add blobs without Git tree objects; synthesize every
	// missing ancestor so nested untracked files remain reachable through the indexes.
	for p, obj := range r.Obj {
		if obj.Kind != Blob {
			continue
		}
		for dir := parentTree(p, obj.Kind); dir != ""; dir = dir.Parent() {
			if _, ok := r.Obj[dir]; !ok {
				r.Obj[dir] = Obj{Path: dir, Kind: Tree, Source: obj.Source}
			}
		}
	}

	clear(r.Tree)
	r.BlobKeys = make([]reference.Blob, 0, len(r.Obj))
	r.ObjKeys = make([]reference.Path, 0, len(r.Obj))
	for p, obj := range r.Obj {
		r.ObjKeys = append(r.ObjKeys, p)
		if obj.Kind == Blob {
			r.BlobKeys = append(r.BlobKeys, reference.Blob(p.String()))
		}
		tree := parentTree(p, obj.Kind)
		r.Tree[tree] = append(r.Tree[tree], obj)
		// TODO: r.Tree is not sorted
	}
	slices.SortFunc(r.ObjKeys, func(a, b reference.Path) int { return strings.Compare(a.String(), b.String()) })
	slices.Sort(r.BlobKeys)

	clear(r.DirTree)
	r.TreeKeys = make([]reference.Tree, 0, len(r.Tree))
	for tree := range r.Tree {
		r.TreeKeys = append(r.TreeKeys, tree)
		if tree == reference.Root {
			// don't root as a reference to itself
			continue
		}
		p := tree.Parent()
		r.DirTree[p] = append(r.DirTree[p], tree)
		slices.Sort(r.DirTree[p])
	}
	slices.Sort(r.TreeKeys)
}

func parseLine(line string) (Obj, error) {
	var obj Obj
	header, rawPath, ok := strings.Cut(line, "\t")
	if !ok {
		return obj, errors.New("record has no tab separator")
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return obj, fmt.Errorf("record header has %d fields", len(fields))
	}
	if fields[1] != "blob" && fields[1] != "tree" {
		return obj, fmt.Errorf("unsupported object type %q", fields[1])
	}
	if len(fields[2]) != 40 {
		return obj, fmt.Errorf("invalid object id %q", fields[2])
	}
	obj.Hash = plumbing.NewHash(fields[2])
	if obj.Hash.IsZero() {
		return Obj{}, fmt.Errorf("invalid object id %q", fields[2])
	}
	if fields[1] == "blob" {
		obj.Kind = Blob
		blob, err := reference.ParseBlob(rawPath)
		if err != nil {
			return Obj{}, err
		}
		obj.Path = blob
	} else {
		obj.Kind = Tree
		tree, err := reference.ParseTree(rawPath)
		if err != nil {
			return Obj{}, err
		}
		obj.Path = tree
	}
	return obj, nil
}

func parentTree(p reference.Path, kind Kind) reference.Tree {
	if kind == Tree {
		return p.(reference.Tree).Parent()
	}
	return p.Tree()
}

// readGitBlob reads a blob from the first Git directory that has it.
//
// go-git lists a directory's pack .idx files once and caches that list for the
// life of its storage, but the directories read here are modified concurrently
// by other processes: a shared object alternate (such as a CI git mirror) can
// be repacked at any time, which deletes packs go-git still has indexed. Two
// errors come out of that stale cache:
//
//   - dotgit.ErrPackfileNotFound: a cached .idx points at a .pack that no
//     longer exists.
//   - plumbing.ErrObjectNotFound: the object was loose when the pack list was
//     cached and has since been packed, so go-git never looks in the new pack.
//
// When a lookup fails that way, the failing directories are re-opened, which
// re-reads their pack directories as Git does after a failed object read, and
// the lookup is retried once. An object that is truly missing costs one extra
// re-open and lookup before the error is reported.
func (r *Repo) readGitBlob(hash plumbing.Hash) ([]byte, error) {
	r.gitMu.Lock()
	defer r.gitMu.Unlock()

	data, failures, found := r.readGitBlobLocked(hash)
	if !found && r.reopenStaleGitDirsLocked(failures) {
		data, failures, found = r.readGitBlobLocked(hash)
	}
	if found {
		return data, nil
	}
	if len(failures) == 0 {
		return nil, errors.New("no git repositories to search")
	}
	errs := make([]error, 0, len(failures))
	for _, failure := range failures {
		errs = append(errs, failure)
	}
	return nil, errors.Join(errs...)
}

// readGitBlobLocked returns the blob from the first Git directory that has it.
// When none does it returns each directory's failure instead. Callers must hold
// r.gitMu.
func (r *Repo) readGitBlobLocked(hash plumbing.Hash) (data []byte, failures []gitDirError, found bool) {
	failures = make([]gitDirError, 0, len(r.gitDirs))
	for i, dir := range r.gitDirs {
		data, err := readBlobFrom(dir.repo, hash)
		if err == nil {
			return data, nil, true
		}
		failures = append(failures, gitDirError{index: i, path: dir.path, err: err})
	}
	return nil, failures, false
}

// reopenStaleGitDirsLocked re-opens every Git directory whose lookup failed
// in a way that suggests go-git's cached pack list is stale, and reports whether
// any was re-opened. Callers must hold r.gitMu.
func (r *Repo) reopenStaleGitDirsLocked(failures []gitDirError) bool {
	reopened := false
	for _, failure := range failures {
		if !errors.Is(failure.err, dotgit.ErrPackfileNotFound) && !errors.Is(failure.err, plumbing.ErrObjectNotFound) {
			continue
		}
		repo, err := openGitDir(failure.path)
		if err != nil {
			slog.Warn("object lookup failed and re-opening the git dir failed", "path", failure.path, "error", err)
			continue
		}
		slog.Warn("object lookup failed (concurrent repack?); re-opened git dir and retrying", "path", failure.path, "error", failure.err)
		r.gitDirs[failure.index].repo = repo
		reopened = true
	}
	return reopened
}

func readBlobFrom(repo *git.Repository, hash plumbing.Hash) ([]byte, error) {
	blob, err := repo.BlobObject(hash)
	if err != nil {
		return nil, err
	}
	reader, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}

// Show lazily reads a blob's contents from the repository or working tree.
//
// The returned slice is the cached copy shared by every caller. Callers must
// not modify it; copy it first when mutation is needed.
func (r *Repo) Show(relativePath reference.Path) ([]byte, error) {
	obj, ok := r.Obj[relativePath]
	blob, _ := relativePath.(reference.Blob)
	if !ok {
		return nil, fmt.Errorf("path %q not found", relativePath)
	}
	if obj.Kind != Blob {
		return nil, fmt.Errorf("path %q is not a blob", relativePath)
	}
	r.mu.RLock()
	cached, ok := r.blobs[blob]
	r.mu.RUnlock()
	if ok {
		return cached, nil
	}

	var data []byte
	var err error
	if obj.Source == WorkingTreeSource {
		file, openErr := r.worktree.Open(blob.String())
		if openErr == nil {
			data, err = io.ReadAll(file)
			_ = file.Close()
		} else {
			err = openErr
		}
	} else {
		data, err = r.readGitBlob(obj.Hash)
	}
	if err != nil {
		return nil, fmt.Errorf("read blob %q: %w", relativePath, err)
	}
	// Clip the capacity so an append by a caller reallocates rather than writing
	// into the shared backing array.
	data = slices.Clip(data)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.blobs[blob]; ok {
		// Another goroutine cached the blob first; share its copy.
		return cached, nil
	}
	r.blobs[blob] = data
	return data, nil
}

// ShowContext is the context-aware form of Show.
func (r *Repo) ShowContext(ctx context.Context, relativePath reference.Path) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return r.Show(relativePath)
}

// BlobsNamed returns repository blobs whose basename matches name in repository order.
func (r *Repo) BlobsNamed(name string) []reference.Blob {
	matches := make([]reference.Blob, 0)
	for _, tree := range r.TreeKeys {
		candidate := reference.Blob(path.Join(string(tree), name))
		if obj, ok := r.Obj[candidate]; ok && obj.Kind == Blob {
			matches = append(matches, candidate)
		}
	}
	slices.Sort(matches)
	return matches
}
