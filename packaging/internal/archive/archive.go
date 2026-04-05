package archive

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/solidarity-ai/toolbox/packaging/internal/manifest"
	"github.com/solidarity-ai/toolbox/packaging/internal/source"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const ArchiveExtension = ".toolbox.pkg"

// PackResult holds the paths to the produced archive and manifest files.
type PackResult struct {
	ArchivePath  string
	ManifestPath string
}

// Pack creates a .toolbox.pkg archive from a loaded source package.
// The archive is written to outDir as <name>.toolbox.pkg alongside
// an external toolbox.pkg.json with the sha256 of the archive.
func Pack(loaded source.LoadedPackage, outDir string) (PackResult, error) {
	// Compile the internal manifest (no sha256 — that goes in the external one)
	internalPkg := loaded.Package
	internalPkg.SHA256 = ""
	internalManifest, err := json.MarshalIndent(internalPkg, "", "  ")
	if err != nil {
		return PackResult{}, fmt.Errorf("marshal internal manifest: %w", err)
	}

	// Build tar+zstd archive in memory
	var archiveBuf bytes.Buffer
	zw, err := zstd.NewWriter(&archiveBuf)
	if err != nil {
		return PackResult{}, fmt.Errorf("create zstd writer: %w", err)
	}
	tw := tar.NewWriter(zw)

	// Add all source files from the filtered FS
	if err := addFSToTar(tw, loaded.Files); err != nil {
		tw.Close()
		zw.Close()
		return PackResult{}, fmt.Errorf("add files to archive: %w", err)
	}

	// Add internal manifest
	if err := addBytesToTar(tw, manifest.PkgManifestFilename, internalManifest); err != nil {
		tw.Close()
		zw.Close()
		return PackResult{}, fmt.Errorf("add internal manifest to archive: %w", err)
	}

	if err := tw.Close(); err != nil {
		zw.Close()
		return PackResult{}, fmt.Errorf("close tar writer: %w", err)
	}
	if err := zw.Close(); err != nil {
		return PackResult{}, fmt.Errorf("close zstd writer: %w", err)
	}

	archiveData := archiveBuf.Bytes()

	// Compute sha256
	h := sha256.Sum256(archiveData)
	hashStr := hex.EncodeToString(h[:])

	// Write archive file
	archivePath := filepath.Join(outDir, loaded.Package.Name+ArchiveExtension)
	if err := os.WriteFile(archivePath, archiveData, 0o644); err != nil {
		return PackResult{}, fmt.Errorf("write archive: %w", err)
	}

	// Write external manifest with sha256
	externalPkg := loaded.Package
	externalPkg.SHA256 = hashStr
	externalManifest, err := json.MarshalIndent(externalPkg, "", "  ")
	if err != nil {
		return PackResult{}, fmt.Errorf("marshal external manifest: %w", err)
	}
	externalManifest = append(externalManifest, '\n')

	manifestPath := filepath.Join(outDir, manifest.PkgManifestFilename)
	if err := os.WriteFile(manifestPath, externalManifest, 0o644); err != nil {
		return PackResult{}, fmt.Errorf("write external manifest: %w", err)
	}

	return PackResult{
		ArchivePath:  archivePath,
		ManifestPath: manifestPath,
	}, nil
}

// LoadArchive loads a package from a .toolbox.pkg archive file.
// It verifies the sha256 from the external manifest, checks that
// the internal manifest matches the external one (ignoring sha256),
// and validates the loaded package against the dist schema.
func LoadArchive(archivePath, manifestPath string) (source.LoadedPackage, error) {
	// Read external manifest
	externalRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return source.LoadedPackage{}, fmt.Errorf("read external manifest: %w", err)
	}
	externalPkg, err := manifest.ParsePkg(externalRaw)
	if err != nil {
		return source.LoadedPackage{}, fmt.Errorf("parse external manifest: %w", err)
	}

	// Read archive
	archiveData, err := os.ReadFile(archivePath)
	if err != nil {
		return source.LoadedPackage{}, fmt.Errorf("read archive: %w", err)
	}

	// Verify sha256
	if externalPkg.SHA256 == "" {
		return source.LoadedPackage{}, fmt.Errorf("external manifest %s missing sha256", manifestPath)
	}
	h := sha256.Sum256(archiveData)
	actualHash := hex.EncodeToString(h[:])
	if externalPkg.SHA256 != actualHash {
		return source.LoadedPackage{}, fmt.Errorf("sha256 mismatch: manifest=%q, actual=%q", externalPkg.SHA256, actualHash)
	}

	// Decompress and extract to in-memory FS
	archiveFS, internalPkg, err := extractArchive(archiveData)
	if err != nil {
		return source.LoadedPackage{}, fmt.Errorf("extract archive: %w", err)
	}

	// Verify internal manifest matches external (ignoring sha256)
	externalForCompare := externalPkg
	externalForCompare.SHA256 = ""
	internalForCompare := internalPkg
	internalForCompare.SHA256 = ""

	externalJSON, _ := json.Marshal(externalForCompare)
	internalJSON, _ := json.Marshal(internalForCompare)
	if string(externalJSON) != string(internalJSON) {
		return source.LoadedPackage{}, fmt.Errorf("internal/external manifest mismatch: external name=%q, internal name=%q", externalPkg.Name, internalPkg.Name)
	}
	if _, err := manifest.ValidateCompiled(externalPkg, manifest.ValidationModeDist); err != nil {
		return source.LoadedPackage{}, fmt.Errorf("validate archive manifest for distribution: %w", err)
	}

	if err := source.EnrichToolMetadata(archiveFS, &externalPkg); err != nil {
		return source.LoadedPackage{}, err
	}

	return source.LoadedPackage{
		Package: externalPkg,
		Files:   archiveFS,
	}, nil
}

func addFSToTar(tw *tar.Writer, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = path
		normalizeTarHeader(header)

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		f, err := fsys.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return nil
	})
}

func addBytesToTar(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(data)),
	}
	normalizeTarHeader(header)
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func normalizeTarHeader(header *tar.Header) {
	header.ModTime = time.Unix(0, 0)
	header.AccessTime = time.Unix(0, 0)
	header.ChangeTime = time.Unix(0, 0)
	header.Uid = 0
	header.Gid = 0
	header.Uname = ""
	header.Gname = ""
}

// archiveMemFS is an in-memory fs.FS built from a tar archive.
// It implements fs.FS, fs.ReadFileFS, fs.StatFS, and fs.ReadDirFS.
type archiveMemFS struct {
	files map[string][]byte
	dirs  map[string]struct{}
}

func (a *archiveMemFS) Open(name string) (fs.File, error) {
	name = cleanName(name)
	if _, ok := a.dirs[name]; ok {
		return &memDir{name: name, entries: a.dirEntries(name)}, nil
	}
	data, ok := a.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &memFile{name: filepath.Base(name), data: data, reader: bytes.NewReader(data)}, nil
}

func (a *archiveMemFS) ReadFile(name string) ([]byte, error) {
	name = cleanName(name)
	data, ok := a.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return append([]byte(nil), data...), nil
}

func (a *archiveMemFS) Stat(name string) (fs.FileInfo, error) {
	name = cleanName(name)
	if _, ok := a.dirs[name]; ok {
		return &memFileInfo{name: filepath.Base(name), isDir: true}, nil
	}
	data, ok := a.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return &memFileInfo{name: filepath.Base(name), size: int64(len(data))}, nil
}

func (a *archiveMemFS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = cleanName(name)
	if _, ok := a.dirs[name]; !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	return a.dirEntries(name), nil
}

func (a *archiveMemFS) dirEntries(dir string) []fs.DirEntry {
	var entries []fs.DirEntry
	seen := map[string]struct{}{}

	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}

	for name, data := range a.files {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if strings.Contains(rest, "/") {
			continue
		}
		if _, ok := seen[rest]; ok {
			continue
		}
		seen[rest] = struct{}{}
		entries = append(entries, &memDirEntry{name: rest, size: int64(len(data))})
	}
	for name := range a.dirs {
		if name == dir {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if strings.Contains(rest, "/") {
			continue
		}
		if _, ok := seen[rest]; ok {
			continue
		}
		seen[rest] = struct{}{}
		entries = append(entries, &memDirEntry{name: rest, isDir: true})
	}
	return entries
}

func cleanName(name string) string {
	name = strings.TrimPrefix(name, "./")
	if name == "" {
		return "."
	}
	return name
}

type memFile struct {
	name   string
	data   []byte
	reader *bytes.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) {
	return &memFileInfo{name: f.name, size: int64(len(f.data))}, nil
}
func (f *memFile) Read(b []byte) (int, error) { return f.reader.Read(b) }
func (f *memFile) Close() error               { return nil }

type memDir struct {
	name    string
	entries []fs.DirEntry
	offset  int
}

func (d *memDir) Stat() (fs.FileInfo, error) {
	return &memFileInfo{name: filepath.Base(d.name), isDir: true}, nil
}
func (d *memDir) Read([]byte) (int, error) { return 0, fmt.Errorf("is a directory") }
func (d *memDir) Close() error             { return nil }
func (d *memDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		entries := d.entries[d.offset:]
		d.offset = len(d.entries)
		return entries, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := d.offset + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	entries := d.entries[d.offset:end]
	d.offset = end
	if d.offset >= len(d.entries) {
		return entries, io.EOF
	}
	return entries, nil
}

type memFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *memFileInfo) Name() string { return fi.name }
func (fi *memFileInfo) Size() int64  { return fi.size }
func (fi *memFileInfo) Mode() fs.FileMode {
	if fi.isDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (fi *memFileInfo) IsDir() bool        { return fi.isDir }
func (fi *memFileInfo) Sys() any           { return nil }
func (fi *memFileInfo) ModTime() time.Time { return time.Time{} }

type memDirEntry struct {
	name  string
	size  int64
	isDir bool
}

func (de *memDirEntry) Name() string { return de.name }
func (de *memDirEntry) IsDir() bool  { return de.isDir }
func (de *memDirEntry) Type() fs.FileMode {
	if de.isDir {
		return fs.ModeDir
	}
	return 0
}
func (de *memDirEntry) Info() (fs.FileInfo, error) {
	return &memFileInfo{name: de.name, size: de.size, isDir: de.isDir}, nil
}

func extractArchive(data []byte) (*archiveMemFS, tooldef.Package, error) {
	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, tooldef.Package{}, fmt.Errorf("create zstd reader: %w", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	memFS := &archiveMemFS{
		files: make(map[string][]byte),
		dirs:  map[string]struct{}{".": {}},
	}

	var internalPkg tooldef.Package
	foundManifest := false

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, tooldef.Package{}, fmt.Errorf("read tar entry: %w", err)
		}

		name := strings.TrimPrefix(header.Name, "./")

		switch header.Typeflag {
		case tar.TypeDir:
			name = strings.TrimSuffix(name, "/")
			memFS.dirs[name] = struct{}{}
		case tar.TypeReg:
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, tooldef.Package{}, fmt.Errorf("read tar file %s: %w", name, err)
			}
			memFS.files[name] = content

			dir := filepath.Dir(name)
			for dir != "." {
				memFS.dirs[dir] = struct{}{}
				dir = filepath.Dir(dir)
			}

			if name == manifest.PkgManifestFilename {
				pkg, err := manifest.ParsePkg(content)
				if err != nil {
					return nil, tooldef.Package{}, fmt.Errorf("parse internal manifest: %w", err)
				}
				internalPkg = pkg
				foundManifest = true
			}
		}
	}

	if !foundManifest {
		return nil, tooldef.Package{}, fmt.Errorf("archive missing internal %s", manifest.PkgManifestFilename)
	}

	return memFS, internalPkg, nil
}
