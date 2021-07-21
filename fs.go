package iago

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	fs "github.com/relab/wrfs"
)

// CleanPath cleans a path and removes leading forward slashes.
// io/fs only support relative paths, and by default,
// all paths in iago are assumed to be relative to the root directory.
func CleanPath(path string) string {
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, "/")
	return path
}

// Path is a path to a file or directory, relative to the prefix.
type Path struct {
	Path   string
	Prefix string
}

// RelativeTo sets the prefix for this path.
func (p Path) RelativeTo(path string) Path {
	p.Prefix = CleanPath(path)
	return p
}

// Expand expands environment variables in the path and prefix strings using the environment of the given host.
func (p Path) Expand(h Host) Path {
	return Path{
		Path:   CleanPath(Expand(h, p.Path)),
		Prefix: CleanPath(Expand(h, p.Prefix)),
	}
}

// P returns a path relative to the root directory.
func P(path string) Path {
	return Path{
		Path:   CleanPath(path),
		Prefix: "",
	}
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

// GetFilePerm returs the current file permission, or 644 if no file permission was set.
func (p Perm) GetFilePerm() fs.FileMode {
	if p.haveFilePerm {
		return p.perm
	}
	return 0644 // default
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
	return 0755 // default
}

// Upload uploads a file or directory to a remote host.
type Upload struct {
	Src  Path
	Dest Path
	Perm Perm
}

// Apply performs the upload.
func (u Upload) Apply(ctx context.Context, host Host) error {
	return copyAction{src: u.Src.Expand(host), dest: u.Dest.Expand(host), perm: u.Perm, fetch: false}.Apply(ctx, host)
}

// Download downloads a file or directory from a remote host.
type Download struct {
	Src  Path
	Dest Path
	Perm Perm
}

// Apply performs the download.
func (d Download) Apply(ctx context.Context, host Host) error {
	return copyAction{src: d.Src.Expand(host), dest: d.Dest.Expand(host), perm: d.Perm, fetch: true}.Apply(ctx, host)
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
		from, err = fs.Sub(host.GetFS(), ca.src.Prefix)
		if err != nil {
			return err
		}
		to = fs.DirFS("/" + ca.dest.Prefix)
	} else {
		from = fs.DirFS("/" + ca.src.Prefix)
		to, err = fs.Sub(host.GetFS(), ca.dest.Prefix)
		if err != nil {
			return err
		}
	}

	info, err := fs.Stat(from, ca.src.Path)
	if err != nil {
		return err
	}

	if info.IsDir() {
		dest := ca.dest.Path
		if ca.fetch {
			// since we might be copying from multiple hosts, we will create a subdirectory in the destination folder
			dest += "/" + host.Name()
		}
		return copyDir(ca.src.Path, dest, ca.perm, from, to)
	}
	dest := ca.dest.Path
	if ca.fetch {
		// since we might be copying from multiple hosts, we will prefix the filename with the host's name.
		dest += "." + host.Name()
	}
	return copyFile(ca.src.Path, dest, ca.perm, from, to)
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
