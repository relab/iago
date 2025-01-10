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
	return p.prefix + p.path
}

func isAbs(path string) bool {
	path = filepath.ToSlash(path)
	if strings.HasPrefix(path, "/") {
		return true
	}
	l := len(filepath.VolumeName(path))
	if l == 0 {
		return false
	}
	path = path[l:]
	if path == "" {
		return false
	}
	return path[0] == '/'
}

func removeSlash(path string) string {
	return strings.TrimPrefix(path, "/")
}

// NewPath returns a new Path struct. prefix must be an absolute path,
// and path must be relative to the prefix.
func NewPath(prefix, path string) (p Path, err error) {
	if !isAbs(prefix) {
		return Path{}, fmt.Errorf("'%s': %w", prefix, ErrNotAbsolute)
	}
	if isAbs(path) {
		return Path{}, fmt.Errorf("'%s': %w", path, ErrNotRelative)
	}
	return Path{prefix: CleanPath(prefix), path: CleanPath(path)}, nil
}

// NewPathFromAbs returns a new Path struct from an absolute path.
func NewPathFromAbs(path string) (p Path, err error) {
	if !isAbs(path) {
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
type Download struct {
	Src  Path
	Dest Path
	Perm Perm
}

// Apply performs the download.
func (d Download) Apply(ctx context.Context, host Host) error {
	return copyAction{src: d.Src, dest: d.Dest, perm: d.Perm, fetch: true}.Apply(ctx, host)
}

type copyAction struct {
	src   Path
	dest  Path
	fetch bool
	perm  Perm
}

func (ca copyAction) Apply(ctx context.Context, host Host) (err error) {
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
			dest += "/" + host.Name()
		}
		return copyDir(ca.src.path, dest, ca.perm, from, to)
	}
	dest := ca.dest.path
	if ca.fetch {
		// since we might be copying from multiple hosts, we will prefix the filename with the host's name.
		dest += "." + host.Name()
	}
	return copyFile(ca.src.path, dest, ca.perm, from, to)
}

func copyDir(src, dest string, perm Perm, from, to fs.FS) error {
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
			err = copyDir(path.Join(src, info.Name()), path.Join(dest, info.Name()), perm, from, to)
		} else {
			err = copyFile(path.Join(src, info.Name()), path.Join(dest, info.Name()), perm, from, to)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dest string, perm Perm, from fs.FS, to fs.FS) (err error) {
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
		return fmt.Errorf("cannot write to %s: %v", dest, fs.ErrUnsupported)
	}

	_, err = io.Copy(writer, fromF)
	return err
}
