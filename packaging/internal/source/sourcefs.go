package source

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

type sourceFS struct {
	base  fs.FS
	files map[string]struct{}
	dirs  map[string]struct{}
}

func (f sourceFS) Open(name string) (fs.File, error) {
	clean := cleanFSPath(name)
	if _, ok := f.files[clean]; ok {
		return f.base.Open(clean)
	}
	if _, ok := f.dirs[clean]; ok {
		return f.base.Open(clean)
	}
	return nil, fs.ErrNotExist
}

func (f sourceFS) ReadDir(name string) ([]fs.DirEntry, error) {
	clean := cleanFSPath(name)
	if _, ok := f.dirs[clean]; !ok {
		return nil, fs.ErrNotExist
	}

	entries, err := fs.ReadDir(f.base, clean)
	if err != nil {
		return nil, err
	}

	filtered := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		child := cleanFSPath(path.Join(clean, entry.Name()))
		if _, ok := f.files[child]; ok {
			filtered = append(filtered, entry)
			continue
		}
		if _, ok := f.dirs[child]; ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

func (f sourceFS) Stat(name string) (fs.FileInfo, error) {
	clean := cleanFSPath(name)
	if _, ok := f.files[clean]; ok {
		return fs.Stat(f.base, clean)
	}
	if _, ok := f.dirs[clean]; ok {
		return fs.Stat(f.base, clean)
	}
	return nil, fs.ErrNotExist
}

func allowedTypeScriptFiles(dir string, pkg tooldef.Package) map[string]struct{} {
	out := make(map[string]struct{})
	for _, tool := range pkg.Tools {
		out[cleanFSPath(tool.EntryTS)] = struct{}{}
	}
	for _, execPath := range pkg.Executables {
		out[cleanFSPath(execPath)] = struct{}{}
	}
	for _, glob := range pkg.AdditionalTypeScriptGlobs {
		matches, err := doublestar.FilepathGlob(filepath.Join(dir, filepath.FromSlash(glob)))
		if err != nil {
			continue
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(dir, match)
			if err != nil {
				continue
			}
			out[cleanFSPath(filepath.ToSlash(rel))] = struct{}{}
		}
	}
	return out
}

func allowedDirectories(files map[string]struct{}) map[string]struct{} {
	dirs := map[string]struct{}{
		".": {},
	}
	for file := range files {
		dir := path.Dir(file)
		for {
			dirs[dir] = struct{}{}
			if dir == "." {
				break
			}
			dir = path.Dir(dir)
		}
	}
	return dirs
}

func cleanFSPath(name string) string {
	clean := path.Clean(name)
	if clean == "/" {
		return "."
	}
	return clean
}
