package sftpfs

import (
	"errors"
	"os"
	"syscall"
	"time"

	fs "github.com/Raytar/wrfs"

	"github.com/pkg/sftp"
)

type sftpDirEntry struct {
	fi fs.FileInfo
}

// Name returns the name of the file (or subdirectory) described by the entry.
// This name is only the final element of the path (the base name), not the entire path.
// For example, Name would return "hello.go" not "/home/gopher/hello.go".
func (entry sftpDirEntry) Name() string {
	return entry.fi.Name()
}

// IsDir reports whether the entry describes a directory.
func (entry sftpDirEntry) IsDir() bool {
	return entry.fi.IsDir()
}

// Type returns the type bits for the entry.
// The type bits are a subset of the usual FileMode bits, those returned by the FileMode.Type method.
func (entry sftpDirEntry) Type() fs.FileMode {
	return entry.fi.Mode()
}

// Info returns the FileInfo for the file or subdirectory described by the entry.
// The returned FileInfo may be from the time of the original directory read
// or from the time of the call to Info. If the file has been removed or renamed
// since the directory read, Info may return an error satisfying errors.Is(err, ErrNotExist).
// If the entry denotes a symbolic link, Info reports the information about the link itself,
// not the link's target.
func (entry sftpDirEntry) Info() (fs.FileInfo, error) {
	return entry.fi, nil
}

type SFTPFS struct {
	client *sftp.Client
	root   string
}

// New returns a new SFTPFS from the given sftp client.
// All paths given in method calls on this FS will be relative to the given rootdir.
func New(client *sftp.Client, rootdir string) *SFTPFS {
	return &SFTPFS{client, rootdir}
}

// Open opens the named file.
//
// When Open returns an error, it should be of type *PathError
// with the Op field set to "open", the Path field set to name,
// and the Err field describing the problem.
//
// Open should reject attempts to open names that do not satisfy
// ValidPath(name), returning a *PathError with Err set to
// ErrInvalid or ErrNotExist.
func (wrapper *SFTPFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	file, err := wrapper.client.Open(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return file, nil
}

// Stat returns a FileInfo describing the file.
// If there is an error, it should be of type *PathError.
func (wrapper *SFTPFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	fi, err := wrapper.client.Stat(name)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	return fi, nil
}

// ReadDir reads the named directory
// and returns a list of directory entries sorted by filename.
func (wrapper *SFTPFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	files, err := wrapper.client.ReadDir(name)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	dirEntries := make([]fs.DirEntry, len(files))
	for i, fi := range files {
		dirEntries[i] = sftpDirEntry{fi}
	}
	return dirEntries, nil
}

// Functions for creating files / directories
func (wrapper *SFTPFS) Mkdir(name string, perm fs.FileMode) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "mkdir", Path: name, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Mkdir(name)
	if err != nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: err}
	}
	// sftp.Mkdir does not support setting the permissions, so we do it with sftp.Chmod instead.
	err = wrapper.client.Chmod(name, perm)
	if err != nil {
		return &fs.PathError{Op: "mkdir", Path: name, Err: err}
	}
	return nil
}

func (wrapper *SFTPFS) MkdirAll(p string, perm fs.FileMode) error {
	if !fs.ValidPath(p) {
		return &fs.PathError{Op: "mkdir", Path: p, Err: fs.ErrInvalid}
	}

	dir, err := wrapper.Stat(p)
	if err == nil {
		if dir.IsDir() {
			return nil
		}
		return &fs.PathError{Op: "mkdir", Path: p, Err: syscall.ENOTDIR}
	}

	i := len(p)
	for i > 0 && !os.IsPathSeparator(p[i-1]) {
		i--
	}

	if i > 1 {
		// create parent
		err = wrapper.MkdirAll(p[:i-1], perm)
		if err != nil {
			return err
		}
	}

	// now the parent has been created
	err = wrapper.Mkdir(p, perm)
	if err != nil {
		return err
	}
	return nil
}

func (wrapper *SFTPFS) OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	create := false
	fi, err := wrapper.Stat(name)
	if err == nil {
		if fi.IsDir() {
			return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EISDIR}
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		create = true
	}
	file, err := wrapper.client.OpenFile(name, flag)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if !create {
		return file, nil
	}
	// change the permissions of the created file since it is not done by sftp.OpenFile
	err = file.Chmod(perm)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Functions for modifying existing files
func (wrapper *SFTPFS) Chmod(name string, mode fs.FileMode) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "chmod", Path: name, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Chmod(name, mode)
	if err != nil {
		return &fs.PathError{Op: "chmod", Path: name, Err: err}
	}
	return nil
}

func (wrapper *SFTPFS) Chown(name string, uid int, gid int) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "chown", Path: name, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Chown(name, uid, gid)
	if err != nil {
		return &fs.PathError{Op: "chown", Path: name, Err: err}
	}
	return nil
}

func (wrapper *SFTPFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "chtimes", Path: name, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Chtimes(name, atime, mtime)
	if err != nil {
		return &fs.PathError{Op: "chtimes", Path: name, Err: err}
	}
	return nil
}

func (wrapper *SFTPFS) Remove(name string) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Remove(name)
	if err != nil {
		return &fs.PathError{Op: "remove", Path: name, Err: err}
	}
	return nil
}

func (wrapper *SFTPFS) Rename(oldpath string, newpath string) error {
	if !fs.ValidPath(oldpath) {
		return &fs.PathError{Op: "rename", Path: oldpath, Err: fs.ErrInvalid}
	}
	if !fs.ValidPath(newpath) {
		return &fs.PathError{Op: "rename", Path: newpath, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Rename(oldpath, newpath)
	if err != nil {
		return &fs.PathError{Op: "rename", Path: oldpath, Err: err}
	}
	return nil
}

func (wrapper *SFTPFS) Symlink(oldname string, newname string) error {
	if !fs.ValidPath(oldname) {
		return &fs.PathError{Op: "symlink", Path: oldname, Err: fs.ErrInvalid}
	}
	if !fs.ValidPath(newname) {
		return &fs.PathError{Op: "symlink", Path: newname, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Symlink(oldname, newname)
	if err != nil {
		return &fs.PathError{Op: "symlink", Path: oldname, Err: err}
	}
	return nil
}

func (wrapper *SFTPFS) Truncate(name string, size int64) error {
	if !fs.ValidPath(name) {
		return &fs.PathError{Op: "truncate", Path: name, Err: fs.ErrInvalid}
	}
	err := wrapper.client.Truncate(name, size)
	if err != nil {
		return &fs.PathError{Op: "truncate", Path: name, Err: fs.ErrInvalid}
	}
	return nil
}

var _ iagofs.WriteFS = (*SFTPFS)(nil)
