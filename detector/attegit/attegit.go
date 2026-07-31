// Package attegit detects the object and tree structure of a Git revision.
package attegit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"
	"sync"

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

// Obj is an object discovered in a Git tree.
type Obj struct {
	Path Path
	Hash plumbing.Hash
	Kind Kind
}

// Repo contains the objects and direct-child index for one Git revision.
// Show returns a new byte slice, so callers may safely modify its result.
type Repo struct {
	Obj   map[Path]Obj
	Tree  map[Path][]Obj
	blobs map[Path][]byte

	gitRepo *git.Repository
	mu      sync.RWMutex
}

// Open detects ref from repositoryPath. ref may be a commit, branch, or tag.
func Open(repositoryPath, ref string) (*Repo, error) {
	if strings.TrimSpace(repositoryPath) == "" {
		return nil, errors.New("repository path is empty")
	}
	if strings.TrimSpace(ref) == "" {
		return nil, errors.New("git ref is empty")
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
	return d, nil
}

func (r *Repo) readTree(repositoryPath, ref string) error {
	cmd := exec.Command("git", "-C", repositoryPath, "ls-tree", "-r", "-t", "--full-tree", ref)
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
		r.Obj[obj.Path] = obj
		parent := parentPath(obj.Path)
		r.Tree[parent] = append(r.Tree[parent], obj)
		if obj.Kind == Tree {
			if _, exists := r.Tree[obj.Path]; !exists {
				r.Tree[obj.Path] = nil
			}
		}
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
	if obj.Path == "" || strings.HasPrefix(string(obj.Path), "/") {
		return Obj{}, errors.New("invalid repository-relative path")
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

// Show lazily reads a blob's contents from the repository object database.
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

	blob, err := r.gitRepo.BlobObject(obj.Hash)
	if err != nil {
		return nil, fmt.Errorf("read blob %q: %w", relativePath, err)
	}
	reader, err := blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("read blob %q: %w", relativePath, err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return nil, fmt.Errorf("read blob %q contents: %w", relativePath, err)
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

// ShowContext is the context-aware form of Show. Git object reads currently
// use go-git's synchronous API; cancellation is checked before the read.
func (r *Repo) ShowContext(ctx context.Context, relativePath Path) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return r.Show(relativePath)
}
