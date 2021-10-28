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

func getPrefix(path string) string {
	var sb strings.Builder
	sb.WriteString(filepath.VolumeName(path))
	sb.WriteRune(os.PathSeparator)
	return sb.String()
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
	Src  string
	Dest string
	Perm Perm
}

// Apply performs the upload.
func (u Upload) Apply(ctx context.Context, host Host) error {
	return copyAction{src: u.Src, dest: Expand(host, u.Dest), perm: u.Perm, fetch: false}.Apply(ctx, host)
}

// Download downloads a file or directory from a remote host.
type Download struct {
	Src  string
	Dest string
	Perm Perm
}

// Apply performs the download.
func (d Download) Apply(ctx context.Context, host Host) error {
	return copyAction{src: Expand(host, d.Src), dest: d.Dest, perm: d.Perm, fetch: true}.Apply(ctx, host)
}

type copyAction struct {
	src   string
	dest  string
	fetch bool
	perm  Perm
}

func (ca copyAction) Apply(ctx context.Context, host Host) (err error) {
	var (
		from fs.FS
		to   fs.FS
	)

	srcPrefix := getPrefix(ca.src)
	destPrefix := getPrefix(ca.dest)

	src, err := filepath.Rel(srcPrefix, ca.src)
	if err != nil {
		return err
	}

	dest, err := filepath.Rel(destPrefix, ca.dest)
	if err != nil {
		return err
	}

	if ca.fetch {
		from = host.GetFS()
		to = fs.DirFS(destPrefix)
		// ensure the correct type of slashes
		src = filepath.ToSlash(src)
	} else {
		from = fs.DirFS(srcPrefix)
		to = host.GetFS()
		// ensure the correct type of slashes
		dest = filepath.ToSlash(dest)
	}

	info, err := fs.Stat(from, src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		if ca.fetch {
			// since we might be copying from multiple hosts, we will create a subdirectory in the destination folder
			dest += "/" + host.Name()
		}
		return copyDir(src, dest, ca.perm, from, to)
	}
	if ca.fetch {
		// since we might be copying from multiple hosts, we will prefix the filename with the host's name.
		dest += "." + host.Name()
	}
	return copyFile(src, dest, ca.perm, from, to)
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
