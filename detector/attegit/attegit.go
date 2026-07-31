// Package attegit detects the object and tree structure of a Git revision.
package attegit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// Path is a slash-separated path relative to the repository root. The root
// tree is represented by the empty Path.
type Path string

// Kind identifies the Git object represented by an Obj.
type Kind uint8

const (
	Blob Kind = iota + 1
	Tree
)

// Source identifies where an object's contents are read from.
type Source uint8

const (
	GitSource Source = iota + 1
	WorkingTreeSource
)

// Obj is an object discovered in a Git tree.
type Obj struct {
	Path   Path
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

// Repo contains the objects and direct-child index for one Git revision.
// Show returns a new byte slice, so callers may safely modify its result.
type Repo struct {
	Obj      map[Path]Obj
	ObjKeys  []Path
	Tree     map[Path][]Obj
	TreeKeys []Path
	blobs    map[Path][]byte

	gitRepo  *git.Repository
	worktree billy.Filesystem
	mu       sync.RWMutex
}

// Open detects ref from repositoryPath. ref may be a commit, branch, or tag.
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

	gr, err := git.PlainOpen(repositoryPath)
	if err != nil {
		return nil, fmt.Errorf("open git repository: %w", err)
	}
	d := &Repo{
		Obj:     make(map[Path]Obj),
		Tree:    make(map[Path][]Obj),
		blobs:   make(map[Path][]byte),
		gitRepo: gr,
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
	tracked, err := runGitNul(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return fmt.Errorf("list working-tree files: %w", err)
	}
	modified, err := runGitNul(root, "diff-files", "--name-only", "-z")
	if err != nil {
		return fmt.Errorf("list modified working-tree files: %w", err)
	}
	workingTreePaths := make(map[string]bool, len(modified))
	for _, raw := range modified {
		workingTreePaths[string(raw)] = true
	}
	untracked, err := runGitNul(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return fmt.Errorf("list untracked working-tree files: %w", err)
	}
	for _, raw := range untracked {
		workingTreePaths[string(raw)] = true
	}

	for _, raw := range tracked {
		p, err := validatePath(string(raw))
		if err != nil {
			return err
		}
		info, err := r.worktree.Lstat(string(p))
		if err != nil {
			if os.IsNotExist(err) {
				r.removePath(p)
				continue
			}
			return fmt.Errorf("stat working-tree path %q: %w", p, err)
		}
		if !workingTreePaths[string(raw)] {
			continue
		}
		// FileMode's type bits occupy 0170000; 0120000 identifies a symlink.
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("working-tree symlink %q is not supported", p)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("working-tree path %q is not a regular file", p)
		}
		r.removePath(p)
		r.Obj[p] = Obj{Path: p, Kind: Blob, Source: WorkingTreeSource}
	}
	return nil
}

func runGitNul(root string, args ...string) ([][]byte, error) {
	cmd := gitCommand("", "")
	cmd.Args = append([]string{"git"}, args...)
	cmd.Dir = root
	output, err := cmd.Output()
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

func (r *Repo) removePath(p Path) {
	for candidate := range r.Obj {
		if candidate == p || strings.HasPrefix(string(candidate), string(p)+"/") {
			delete(r.Obj, candidate)
		}
	}
}

func (r *Repo) rebuildIndexes() {
	for p, obj := range r.Obj {
		if obj.Kind != Blob {
			continue
		}
		for dir := parentPath(p); dir != ""; dir = parentPath(dir) {
			if _, ok := r.Obj[dir]; !ok {
				r.Obj[dir] = Obj{Path: dir, Kind: Tree, Source: GitSource}
			}
		}
	}
	r.Tree = make(map[Path][]Obj)
	for p, obj := range r.Obj {
		r.Tree[parentPath(p)] = append(r.Tree[parentPath(p)], obj)
		if obj.Kind == Tree {
			if _, ok := r.Tree[p]; !ok {
				r.Tree[p] = nil
			}
		}
	}
	r.ObjKeys = slices.Sorted(maps.Keys(r.Obj))
	r.TreeKeys = slices.Sorted(maps.Keys(r.Tree))
}

func validatePath(raw string) (Path, error) {
	p := path.Clean(strings.ReplaceAll(raw, "\\", "/"))
	if p == "." || p == "" || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("invalid repository-relative path %q", raw)
	}
	return Path(p), nil
}

func parseLine(line string) (Obj, error) {
	var obj Obj
	i := strings.IndexByte(line, '\t')
	if i < 0 {
		return obj, errors.New("record has no tab separator")
	}
	fields := strings.Fields(line[:i])
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
	obj.Path = Path(line[i+1:])
	if _, err := validatePath(string(obj.Path)); err != nil {
		return Obj{}, err
	}
	if fields[1] == "blob" {
		obj.Kind = Blob
	} else {
		obj.Kind = Tree
	}
	return obj, nil
}

func parentPath(p Path) Path {
	s := string(p)
	i := strings.LastIndexByte(s, '/')
	if i < 0 {
		return ""
	}
	return Path(path.Dir(s))
}

// Show lazily reads a blob's contents from the repository or working tree.
func (r *Repo) Show(relativePath Path) ([]byte, error) {
	obj, ok := r.Obj[relativePath]
	if !ok {
		return nil, fmt.Errorf("path %q not found", relativePath)
	}
	if obj.Kind != Blob {
		return nil, fmt.Errorf("path %q is not a blob", relativePath)
	}
	r.mu.RLock()
	cached, ok := r.blobs[relativePath]
	if ok {
		result := append([]byte(nil), cached...)
		r.mu.RUnlock()
		return result, nil
	}
	r.mu.RUnlock()

	var data []byte
	var err error
	if obj.Source == WorkingTreeSource {
		file, openErr := r.worktree.Open(string(relativePath))
		if openErr == nil {
			data, err = io.ReadAll(file)
			_ = file.Close()
		} else {
			err = openErr
		}
	} else {
		blob, readErr := r.gitRepo.BlobObject(obj.Hash)
		if readErr == nil {
			reader, readerErr := blob.Reader()
			if readerErr == nil {
				data, err = io.ReadAll(reader)
				_ = reader.Close()
			} else {
				err = readerErr
			}
		} else {
			err = readErr
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read blob %q: %w", relativePath, err)
	}
	r.mu.Lock()
	if cached, ok := r.blobs[relativePath]; ok {
		data = cached
	} else {
		r.blobs[relativePath] = append([]byte(nil), data...)
	}
	result := append([]byte(nil), data...)
	r.mu.Unlock()
	return result, nil
}

// ShowContext is the context-aware form of Show.
func (r *Repo) ShowContext(ctx context.Context, relativePath Path) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return r.Show(relativePath)
}
