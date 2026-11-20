// Package workspace is the broker-side copy of the repository the agent works
// on. The agent never holds the files; every read and write goes through here,
// confined to the workspace directory with os.Root.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// MaxFileSize bounds a single read or write.
const MaxFileSize = 256 << 10

// Workspace is one named repository under a base directory.
type Workspace struct {
	Name string // e.g. "web-app"
	base string // directory that contains Name/
	seed string // pristine copy used by Reset

	mu sync.Mutex
}

// New returns a workspace rooted at base/name, reset from seedDir.
func New(base, name, seedDir string) *Workspace {
	return &Workspace{Name: name, base: base, seed: seedDir}
}

// Dir is the workspace directory on disk.
func (w *Workspace) Dir() string { return filepath.Join(w.base, w.Name) }

// CleanPath normalises an agent-supplied path and rejects anything that could
// leave the workspace or smuggle a second resource segment.
func CleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." || p == "/" {
		return "", nil
	}
	if strings.ContainsAny(p, ":\x00\\") {
		return "", errors.New("path must not contain ':', '\\' or NUL")
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("path must be relative to the repository root")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", errors.New("path must not contain '..'")
		}
	}
	clean := path.Clean(p)
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

func (w *Workspace) root() (*os.Root, error) { return os.OpenRoot(w.Dir()) }

// Entry is one directory entry.
type Entry struct {
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size,omitempty"`
}

// List returns the entries directly under rel.
func (w *Workspace) List(rel string) ([]Entry, error) {
	r, err := w.root()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	name := rel
	if name == "" {
		name = "."
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, simplify(err)
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, simplify(err)
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		e := Entry{Path: path.Join(rel, de.Name()), Dir: de.IsDir()}
		if info, err := de.Info(); err == nil && !de.IsDir() {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Read returns the content of a file.
func (w *Workspace) Read(rel string) ([]byte, error) {
	if rel == "" {
		return nil, errors.New("path is a directory")
	}
	r, err := w.root()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(rel)
	if err != nil {
		return nil, simplify(err)
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.IsDir() {
		return nil, errors.New("path is a directory")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, simplify(err)
	}
	if len(b) > MaxFileSize {
		return nil, fmt.Errorf("file is larger than %d bytes", MaxFileSize)
	}
	return b, nil
}

// Write replaces the content of a file, creating parent directories.
func (w *Workspace) Write(rel string, content []byte) error {
	if rel == "" {
		return errors.New("path is required")
	}
	if len(content) > MaxFileSize {
		return fmt.Errorf("content is larger than %d bytes", MaxFileSize)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	r, err := w.root()
	if err != nil {
		return err
	}
	defer r.Close()
	if dir := path.Dir(rel); dir != "." {
		if err := r.MkdirAll(dir, 0o755); err != nil {
			return simplify(err)
		}
	}
	f, err := r.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return simplify(err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return simplify(err)
	}
	return f.Close()
}

// FileHash returns the SHA-256 of a file, or "" if it does not exist.
func (w *Workspace) FileHash(rel string) string {
	b, err := w.Read(rel)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TreeHash is a content hash over every file; it is the version that gets deployed.
func (w *Workspace) TreeHash() (string, error) {
	return hashTree(w.Dir())
}

// SeedFileHash returns the SHA-256 of a file in the pristine seed.
func (w *Workspace) SeedFileHash(rel string) string {
	b, err := os.ReadFile(filepath.Join(w.seed, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashTree(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(b)
		fmt.Fprintf(h, "%s\x00%s\n", filepath.ToSlash(rel), hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Reset restores the workspace from the seed, then writes overlay files on top.
func (w *Workspace) Reset(overlay map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	dir := w.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Empty the directory rather than removing it: it may be a mount point.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	if err := copyTree(w.seed, dir); err != nil {
		return fmt.Errorf("workspace: copying seed: %w", err)
	}
	for rel, content := range overlay {
		clean, err := CleanPath(rel)
		if err != nil || clean == "" {
			return fmt.Errorf("workspace: bad overlay path %q", rel)
		}
		dst := filepath.Join(dir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Ready reports whether the workspace has been populated.
func (w *Workspace) Ready() bool {
	entries, err := os.ReadDir(w.Dir())
	return err == nil && len(entries) > 0
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// simplify strips host paths from filesystem errors before they reach the agent.
func simplify(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		switch {
		case errors.Is(pe.Err, fs.ErrNotExist):
			return errors.New("no such file or directory")
		case errors.Is(pe.Err, fs.ErrPermission):
			return errors.New("permission denied")
		}
		return fmt.Errorf("%s failed: %v", pe.Op, pe.Err)
	}
	if strings.Contains(err.Error(), "escapes") {
		return errors.New("path escapes the workspace")
	}
	return err
}
