package fsoverlay

import (
	"errors"
	"io/fs"
	"sort"
)

// NOTE: This package is intentionally tiny. If we need to grow beyond a small
// layered fs.FS with Open-based reads, prefer adopting an existing merged/union
// fs.FS implementation rather than reimplementing directory, stat, glob, or
// writable overlay semantics here.
//
// Examples to review first:
// - github.com/yalue/merged_fs
// - github.com/laher/mergefs
// - github.com/absfs/unionfs
//
// Also do a fresh search before expanding this package, in case there is now a
// better-maintained io/fs overlay implementation available.

// FS overlays multiple filesystems. Earlier layers win.
type FS struct {
	layers []fs.FS
}

// New constructs an overlay filesystem from highest to lowest precedence.
func New(layers ...fs.FS) FS {
	out := make([]fs.FS, 0, len(layers))
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		out = append(out, layer)
	}
	return FS{layers: out}
}

func (o FS) Open(name string) (fs.File, error) {
	var errs []error
	for _, layer := range o.layers {
		file, err := layer.Open(name)
		if err == nil {
			return file, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, fs.ErrNotExist
}

func (o FS) Stat(name string) (fs.FileInfo, error) {
	var errs []error
	for _, layer := range o.layers {
		info, err := fs.Stat(layer, name)
		if err == nil {
			return info, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, fs.ErrNotExist
}

func (o FS) ReadDir(name string) ([]fs.DirEntry, error) {
	byName := map[string]fs.DirEntry{}
	var found bool
	var errs []error

	for _, layer := range o.layers {
		entries, err := fs.ReadDir(layer, name)
		if err == nil {
			found = true
			for _, entry := range entries {
				if _, ok := byName[entry.Name()]; ok {
					continue
				}
				byName[entry.Name()] = entry
			}
			continue
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		errs = append(errs, err)
	}

	if !found {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, fs.ErrNotExist
	}

	out := make([]fs.DirEntry, 0, len(byName))
	for _, entry := range byName {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out, nil
}
