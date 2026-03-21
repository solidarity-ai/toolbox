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
// It verifies the sha256 from the external manifest and checks that
// the internal manifest matches the external one (ignoring sha256).
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
		fmt.Fprintf(os.Stderr, "warning: external manifest %s has no sha256 — archive integrity not verified\n", manifestPath)
	} else {
		h := sha256.Sum256(archiveData)
		actualHash := hex.EncodeToString(h[:])
		if externalPkg.SHA256 != actualHash {
			return source.LoadedPackage{}, fmt.Errorf("sha256 mismatch: manifest=%q, actual=%q", externalPkg.SHA256, actualHash)
		}
	}

	// Decompress and extract
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
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// archiveMemFS is an in-memory fs.FS built from a tar archive.
type archiveMemFS struct {
	files map[string][]byte
	dirs  map[string]struct{}
}

func (a *archiveMemFS) Open(name string) (fs.File, error) {
	name = strings.TrimPrefix(name, "./")
	if name == "" {
		name = "."
	}
	if _, ok := a.dirs[name]; ok {
		return &archiveDir{name: name, fs: a}, nil
	}
	data, ok := a.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &archiveFile{name: name, data: data, reader: bytes.NewReader(data)}, nil
}

func (a *archiveMemFS) ReadFile(name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "./")
	data, ok := a.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return append([]byte(nil), data...), nil
}

type archiveFile struct {
	name   string
	data   []byte
	reader *bytes.Reader
}

func (f *archiveFile) Stat() (fs.FileInfo, error) {
	return &archiveFileInfo{name: filepath.Base(f.name), size: int64(len(f.data))}, nil
}
func (f *archiveFile) Read(b []byte) (int, error) { return f.reader.Read(b) }
func (f *archiveFile) Close() error               { return nil }

type archiveDir struct {
	name string
	fs   *archiveMemFS
}

func (d *archiveDir) Stat() (fs.FileInfo, error) {
	return &archiveFileInfo{name: filepath.Base(d.name), isDir: true}, nil
}
func (d *archiveDir) Read([]byte) (int, error) { return 0, fmt.Errorf("is a directory") }
func (d *archiveDir) Close() error             { return nil }

type archiveFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *archiveFileInfo) Name() string       { return fi.name }
func (fi *archiveFileInfo) Size() int64        { return fi.size }
func (fi *archiveFileInfo) Mode() fs.FileMode  { return 0o644 }
func (fi *archiveFileInfo) IsDir() bool        { return fi.isDir }
func (fi *archiveFileInfo) Sys() any           { return nil }
func (fi *archiveFileInfo) ModTime() time.Time { return time.Time{} }

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

			// Track directories
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
