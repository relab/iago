package iago

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	fs "github.com/relab/wrfs"
)

var (
	// ErrNotAbsolute is returned when a path is relative, but was expected to be absolute.
	ErrNotAbsolute = errors.New("not an absolute path")
	// ErrNotRelative is returned when a path is absolute, but was expected to be relative.
	ErrNotRelative = errors.New("not a relative path")
)

// Path is a path to a file or directory, relative to the prefix.
type Path struct {
	prefix string
	path   string
}

func (p Path) String() string {
	return filepath.Join(p.prefix, p.path)
}

func removeSlash(path string) string {
	return strings.TrimPrefix(path, "/")
}

// NewPath returns a new Path struct. prefix must be an absolute path,
// and path must be relative to the prefix.
func NewPath(prefix, path string) (p Path, err error) {
	if !filepath.IsAbs(prefix) {
		return Path{}, fmt.Errorf("'%s': %w", prefix, ErrNotAbsolute)
	}
	if filepath.IsAbs(path) {
		return Path{}, fmt.Errorf("'%s': %w", path, ErrNotRelative)
	}
	return Path{prefix: CleanPath(prefix), path: CleanPath(path)}, nil
}

// NewPathFromAbs returns a new Path struct from an absolute path.
func NewPathFromAbs(path string) (p Path, err error) {
	if !filepath.IsAbs(path) {
		return Path{}, ErrNotAbsolute
	}
	p.prefix = filepath.ToSlash(filepath.VolumeName(path)) + "/"
	p.path = strings.TrimPrefix(filepath.ToSlash(path), p.prefix)
	return p, nil
}

// CleanPath cleans the path and converts it to slashes.
func CleanPath(path string) string {
	// on windows, filepath.Clean will replace slashes with Separator, so we need to call ToSlash afterwards.
	return filepath.ToSlash(filepath.Clean(path))
}

// Perm describes the permissions that should be used when creating files or directories.
// Perm can use different permissions for files and directories.
// By default, it uses 644 for files and 755 for directories.
// If a file permission is specified by using NewPerm(), the WithDirPerm() method may be called
// to modify the directory permissions.
type Perm struct {
	perm         fs.FileMode
	haveFilePerm bool
	dirPerm      fs.FileMode
	haveDirPerm  bool
}

// NewPerm returns a Perm with the requested file permission.
// Note that this will also set the directory permission.
// If a different directory permission is desired,
// you must call WithDirPerm on the returned Perm also.
func NewPerm(perm fs.FileMode) Perm {
	return Perm{perm: perm, haveFilePerm: true}
}

// WithDirPerm sets the directory permission of the Perm.
// It both mutates the original perm and returns a copy of it.
func (p *Perm) WithDirPerm(dirPerm fs.FileMode) Perm {
	p.dirPerm = dirPerm
	p.haveDirPerm = true
	return *p
}

// GetFilePerm returns the current file permission, or 644 if no file permission was set.
func (p Perm) GetFilePerm() fs.FileMode {
	if p.haveFilePerm {
		return p.perm
	}
	return 0o644 // default
}

// GetDirPerm returns the current directory permission, or the current file permission,
// or 755 if no permissions were set.
func (p Perm) GetDirPerm() fs.FileMode {
	if p.haveDirPerm {
		return p.dirPerm
	}
	if p.haveFilePerm {
		return p.perm
	}
	return 0o755 // default
}

// Upload uploads a file or directory to a remote host.
type Upload struct {
	Src  Path
	Dest Path
	Perm Perm
}

// Apply performs the upload.
func (u Upload) Apply(ctx context.Context, host Host) error {
	return copyAction{src: u.Src, dest: u.Dest, perm: u.Perm, fetch: false}.Apply(ctx, host)
}

// Download downloads a file or directory from a remote host.
// For each host in a group, a subdirectory named after the host is created
// under Dest so that results from multiple hosts do not collide.
// Use DownloadDir when you are downloading from a single host and want the
// remote directory's contents placed directly into the local destination.
type Download struct {
	Src  Path
	Dest Path
	Perm Perm
}

// Apply performs the download.
func (d Download) Apply(ctx context.Context, host Host) error {
	return copyAction{src: d.Src, dest: d.Dest, perm: d.Perm, fetch: true}.Apply(ctx, host)
}

// ProgressFunc is called during a file transfer to report incremental progress.
// n is the number of bytes just transferred.
type ProgressFunc func(n int64)

// DownloadDir downloads the contents of a remote directory directly into a
// local destination via SFTP, without adding a per-host subdirectory. Use
// this instead of Download when fetching from a single host and direct
// placement is desired. Progress, if non-nil, is called with each chunk of
// bytes transferred.
type DownloadDir struct {
	Src      Path
	Dest     Path
	Progress ProgressFunc
}

// Apply downloads the contents of d.Src on host into d.Dest.
func (d DownloadDir) Apply(_ context.Context, host Host) error {
	from, err := fs.Sub(host.GetFS(), removeSlash(d.Src.prefix))
	if err != nil {
		return err
	}
	to := fs.DirFS(d.Dest.prefix)
	return copyDir(d.Src.path, d.Dest.path, Perm{}, from, to, d.Progress)
}

// Size returns the total byte count of all files under d.Src on host.
// Directory metadata is not counted. Call this before Apply to obtain the
// total for progress display.
func (d DownloadDir) Size(_ context.Context, host Host) (int64, error) {
	from, err := fs.Sub(host.GetFS(), removeSlash(d.Src.prefix))
	if err != nil {
		return 0, err
	}
	return totalSize(from, d.Src.path)
}

func totalSize(fsys fs.FS, dir string) (int64, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		p := path.Join(dir, e.Name())
		if e.IsDir() {
			n, err := totalSize(fsys, p)
			if err != nil {
				return total, err
			}
			total += n
		} else {
			info, err := e.Info()
			if err != nil {
				return total, err
			}
			total += info.Size()
		}
	}
	return total, nil
}

type copyAction struct {
	src   Path
	dest  Path
	fetch bool
	perm  Perm
}

func (ca copyAction) Apply(_ context.Context, host Host) (err error) {
	var (
		from fs.FS
		to   fs.FS
	)
	if ca.fetch {
		from, err = fs.Sub(host.GetFS(), removeSlash(ca.src.prefix))
		if err != nil {
			return err
		}
		to = fs.DirFS(ca.dest.prefix)
	} else {
		from = fs.DirFS(ca.src.prefix)
		to, err = fs.Sub(host.GetFS(), removeSlash(ca.dest.prefix))
		if err != nil {
			return err
		}
	}

	info, err := fs.Stat(from, ca.src.path)
	if err != nil {
		return err
	}

	if info.IsDir() {
		dest := ca.dest.path
		if ca.fetch {
			// since we might be copying from multiple hosts, we will create a subdirectory in the destination folder
			dest = filepath.Join(dest, host.Name())
		}
		return copyDir(ca.src.path, dest, ca.perm, from, to, nil)
	}
	dest := ca.dest.path
	if ca.fetch {
		// since we might be copying from multiple hosts, we add the host's name to the destination file
		dest += "." + host.Name()
	}
	return copyFile(ca.src.path, dest, ca.perm, from, to, nil)
}

func copyDir(src, dest string, perm Perm, from, to fs.FS, progress ProgressFunc) error {
	files, err := fs.ReadDir(from, src)
	if err != nil {
		return err
	}

	err = fs.MkdirAll(to, dest, perm.GetDirPerm())
	if err != nil {
		return err
	}

	for _, info := range files {
		if info.IsDir() {
			err = copyDir(path.Join(src, info.Name()), path.Join(dest, info.Name()), perm, from, to, progress)
		} else {
			err = copyFile(path.Join(src, info.Name()), path.Join(dest, info.Name()), perm, from, to, progress)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dest string, perm Perm, from fs.FS, to fs.FS, progress ProgressFunc) (err error) {
	fromF, err := from.Open(src)
	if err != nil {
		return err
	}
	defer safeClose(fromF, &err, io.EOF)

	toF, err := fs.OpenFile(to, dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm.GetFilePerm())
	if err != nil {
		return err
	}
	defer safeClose(toF, &err, io.EOF)

	writer, ok := toF.(io.Writer)
	if !ok {
		return fmt.Errorf("cannot write to %s: %w", dest, fs.ErrUnsupported)
	}

	var r io.Reader = fromF
	if progress != nil {
		r = &progressReader{r: fromF, fn: progress}
	}
	_, err = io.Copy(writer, r)
	return err
}

type progressReader struct {
	r  io.Reader
	fn ProgressFunc
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.fn(int64(n))
	}
	return n, err
}
