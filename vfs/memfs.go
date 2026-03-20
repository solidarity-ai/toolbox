package vfs

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemFS is a simple in-memory writable filesystem.
type MemFS struct {
	mu    sync.RWMutex
	nodes map[string]*memNode
}

type memNode struct {
	isDir   bool
	data    []byte
	modTime time.Time
}

// NewMemFS creates a new empty in-memory filesystem with a root directory.
func NewMemFS() *MemFS {
	fs := &MemFS{
		nodes: make(map[string]*memNode),
	}
	fs.nodes["/"] = &memNode{isDir: true, modTime: time.Now()}
	return fs
}

func (fs *MemFS) cleanPath(p string) string {
	p = path.Clean("/" + p)
	return p
}

func (fs *MemFS) Metadata(p string) (MetadataWire, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	p = fs.cleanPath(p)
	node, ok := fs.nodes[p]
	if !ok {
		return MetadataWire{}, fmt.Errorf("not found: %s", p)
	}
	return nodeMetadata(node), nil
}

func nodeMetadata(node *memNode) MetadataWire {
	t := uint64(node.modTime.UnixNano())
	return MetadataWire{
		IsDir:    node.isDir,
		IsFile:   !node.isDir,
		Len:      uint64(len(node.data)),
		Accessed: t,
		Created:  t,
		Modified: t,
	}
}

func (fs *MemFS) ReadDir(p string) ([]DirEntryWire, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	p = fs.cleanPath(p)
	node, ok := fs.nodes[p]
	if !ok {
		return nil, fmt.Errorf("not found: %s", p)
	}
	if !node.isDir {
		return nil, fmt.Errorf("not a directory: %s", p)
	}

	prefix := p
	if prefix != "/" {
		prefix += "/"
	}

	var entries []DirEntryWire
	seen := map[string]bool{}
	for name, child := range fs.nodes {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := name[len(prefix):]
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		if seen[rest] {
			continue
		}
		seen[rest] = true
		entries = append(entries, DirEntryWire{
			Name: rest,
			Meta: nodeMetadata(child),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func (fs *MemFS) CreateDir(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	p = fs.cleanPath(p)
	if _, ok := fs.nodes[p]; ok {
		return fmt.Errorf("already exists: %s", p)
	}
	parent := path.Dir(p)
	pNode, ok := fs.nodes[parent]
	if !ok || !pNode.isDir {
		return fmt.Errorf("parent not found: %s", parent)
	}
	fs.nodes[p] = &memNode{isDir: true, modTime: time.Now()}
	return nil
}

func (fs *MemFS) RemoveDir(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	p = fs.cleanPath(p)
	node, ok := fs.nodes[p]
	if !ok {
		return fmt.Errorf("not found: %s", p)
	}
	if !node.isDir {
		return fmt.Errorf("not a directory: %s", p)
	}
	prefix := p + "/"
	for name := range fs.nodes {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("directory not empty: %s", p)
		}
	}
	delete(fs.nodes, p)
	return nil
}

func (fs *MemFS) Rename(from, to string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	from = fs.cleanPath(from)
	to = fs.cleanPath(to)
	node, ok := fs.nodes[from]
	if !ok {
		return fmt.Errorf("not found: %s", from)
	}
	toParent := path.Dir(to)
	if pNode, ok := fs.nodes[toParent]; !ok || !pNode.isDir {
		return fmt.Errorf("destination parent not found: %s", toParent)
	}
	// Move node and all children for directories.
	if node.isDir {
		prefix := from + "/"
		var toMove []struct{ old, new string }
		for name := range fs.nodes {
			if strings.HasPrefix(name, prefix) {
				toMove = append(toMove, struct{ old, new string }{name, to + name[len(from):]})
			}
		}
		for _, m := range toMove {
			fs.nodes[m.new] = fs.nodes[m.old]
			delete(fs.nodes, m.old)
		}
	}
	fs.nodes[to] = node
	delete(fs.nodes, from)
	return nil
}

func (fs *MemFS) RemoveFile(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	p = fs.cleanPath(p)
	node, ok := fs.nodes[p]
	if !ok {
		return fmt.Errorf("not found: %s", p)
	}
	if node.isDir {
		return fmt.Errorf("is a directory: %s", p)
	}
	delete(fs.nodes, p)
	return nil
}

func (fs *MemFS) Readlink(p string) (string, error) {
	return "", fmt.Errorf("symlinks not supported")
}

// FileHandle operations

type memFileHandle struct {
	fs     *MemFS
	path   string
	pos    int64
	read   bool
	write  bool
	append bool
}

func (fs *MemFS) Open(p string, opts OpenOpts) (*memFileHandle, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	p = fs.cleanPath(p)

	node, exists := fs.nodes[p]

	if opts.CreateNew {
		if exists {
			return nil, fmt.Errorf("already exists: %s", p)
		}
		parent := path.Dir(p)
		if pNode, ok := fs.nodes[parent]; !ok || !pNode.isDir {
			return nil, fmt.Errorf("parent not found: %s", parent)
		}
		fs.nodes[p] = &memNode{modTime: time.Now()}
		return &memFileHandle{fs: fs, path: p, read: opts.Read, write: opts.Write, append: opts.Append}, nil
	}

	if opts.Create && !exists {
		parent := path.Dir(p)
		if pNode, ok := fs.nodes[parent]; !ok || !pNode.isDir {
			return nil, fmt.Errorf("parent not found: %s", parent)
		}
		fs.nodes[p] = &memNode{modTime: time.Now()}
		return &memFileHandle{fs: fs, path: p, read: opts.Read, write: opts.Write, append: opts.Append}, nil
	}

	if !exists {
		return nil, fmt.Errorf("not found: %s", p)
	}
	if node.isDir {
		return nil, fmt.Errorf("is a directory: %s", p)
	}

	if opts.Truncate && opts.Write {
		node.data = nil
		node.modTime = time.Now()
	}

	return &memFileHandle{fs: fs, path: p, read: opts.Read, write: opts.Write, append: opts.Append}, nil
}

func (h *memFileHandle) Read(length int) ([]byte, int, error) {
	h.fs.mu.RLock()
	defer h.fs.mu.RUnlock()
	node, ok := h.fs.nodes[h.path]
	if !ok {
		return nil, 0, fmt.Errorf("file deleted")
	}
	if h.pos >= int64(len(node.data)) {
		return nil, 0, io.EOF
	}
	end := h.pos + int64(length)
	if end > int64(len(node.data)) {
		end = int64(len(node.data))
	}
	buf := make([]byte, end-h.pos)
	copy(buf, node.data[h.pos:end])
	n := int(end - h.pos)
	h.pos = end
	return buf, n, nil
}

func (h *memFileHandle) Write(data []byte) (int, error) {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	node, ok := h.fs.nodes[h.path]
	if !ok {
		return 0, fmt.Errorf("file deleted")
	}
	if h.append {
		h.pos = int64(len(node.data))
	}
	end := h.pos + int64(len(data))
	if end > int64(len(node.data)) {
		grown := make([]byte, end)
		copy(grown, node.data)
		node.data = grown
	}
	copy(node.data[h.pos:end], data)
	h.pos = end
	node.modTime = time.Now()
	return len(data), nil
}

func (h *memFileHandle) Seek(from int, offset int64) (int64, error) {
	h.fs.mu.RLock()
	defer h.fs.mu.RUnlock()
	node, ok := h.fs.nodes[h.path]
	if !ok {
		return 0, fmt.Errorf("file deleted")
	}
	var newPos int64
	switch from {
	case 0: // Start
		newPos = offset
	case 1: // Current
		newPos = h.pos + offset
	case 2: // End
		newPos = int64(len(node.data)) + offset
	default:
		return 0, fmt.Errorf("invalid seek origin: %d", from)
	}
	if newPos < 0 {
		return 0, fmt.Errorf("negative seek position")
	}
	h.pos = newPos
	return newPos, nil
}

func (h *memFileHandle) SetLen(newSize int64) error {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	node, ok := h.fs.nodes[h.path]
	if !ok {
		return fmt.Errorf("file deleted")
	}
	if int64(len(node.data)) < newSize {
		grown := make([]byte, newSize)
		copy(grown, node.data)
		node.data = grown
	} else {
		node.data = node.data[:newSize]
	}
	node.modTime = time.Now()
	return nil
}

// ReadAll returns the full contents of a file. Useful for inspecting the
// filesystem after WASM execution completes.
func (fs *MemFS) ReadAll(p string) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	p = fs.cleanPath(p)
	node, ok := fs.nodes[p]
	if !ok {
		return nil, fmt.Errorf("not found: %s", p)
	}
	if node.isDir {
		return nil, fmt.Errorf("is a directory: %s", p)
	}
	out := make([]byte, len(node.data))
	copy(out, node.data)
	return out, nil
}

// WriteFile is a convenience for pre-populating the filesystem before serving.
func (fs *MemFS) WriteFile(p string, data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	p = fs.cleanPath(p)
	parent := path.Dir(p)
	if pNode, ok := fs.nodes[parent]; !ok || !pNode.isDir {
		return fmt.Errorf("parent not found: %s", parent)
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	fs.nodes[p] = &memNode{data: buf, modTime: time.Now()}
	return nil
}
