package sftpfs

import (
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	fs "github.com/relab/wrfs"
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

type sftpFS struct {
	client *sftp.Client
	prefix string
}

// New returns a new sftpFS from the given sftp client.
// All paths given in method calls on this FS will be relative to the given rootdir.
func New(client *sftp.Client, rootdir string) fs.FS {
	return &sftpFS{client, rootdir}
}

func (wrapper *sftpFS) fullName(op, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	return wrapper.prefix + "/" + name, nil
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
func (wrapper *sftpFS) Open(name string) (fs.File, error) {
	full, err := wrapper.fullName("open", name)
	if err != nil {
		return nil, err
	}
	file, err := wrapper.client.Open(full)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return file, nil
}

// Stat returns a FileInfo describing the file.
// If there is an error, it should be of type *PathError.
func (wrapper *sftpFS) Stat(name string) (fs.FileInfo, error) {
	full, err := wrapper.fullName("stat", name)
	if err != nil {
		return nil, err
	}
	fi, err := wrapper.client.Stat(full)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	return fi, nil
}

// ReadDir reads the named directory
// and returns a list of directory entries sorted by filename.
func (wrapper *sftpFS) ReadDir(name string) ([]fs.DirEntry, error) {
	full, err := wrapper.fullName("readdir", name)
	if err != nil {
		return nil, err
	}
	files, err := wrapper.client.ReadDir(full)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	dirEntries := make([]fs.DirEntry, len(files))
	for i, fi := range files {
		dirEntries[i] = sftpDirEntry{fi}
	}
	return dirEntries, nil
}

func (wrapper *sftpFS) Mkdir(name string, perm fs.FileMode) error {
	full, err := wrapper.fullName("mkdir", name)
	if err != nil {
		return err
	}
	err = wrapper.client.Mkdir(full)
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

func (wrapper *sftpFS) OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error) {
	full, err := wrapper.fullName("open", name)
	if err != nil {
		return nil, err
	}
	create := false
	fi, err := wrapper.client.Stat(full)
	if err == nil {
		if fi.IsDir() {
			return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EISDIR}
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		create = true
	}
	file, err := wrapper.client.OpenFile(full, flag)
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

func (wrapper *sftpFS) Chmod(name string, mode fs.FileMode) error {
	full, err := wrapper.fullName("chmod", name)
	if err != nil {
		return err
	}
	err = wrapper.client.Chmod(full, mode)
	if err != nil {
		return &fs.PathError{Op: "chmod", Path: name, Err: err}
	}
	return nil
}

func (wrapper *sftpFS) Chown(name string, uid int, gid int) error {
	full, err := wrapper.fullName("chown", name)
	if err != nil {
		return err
	}
	err = wrapper.client.Chown(full, uid, gid)
	if err != nil {
		return &fs.PathError{Op: "chown", Path: name, Err: err}
	}
	return nil
}

func (wrapper *sftpFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	full, err := wrapper.fullName("chtimes", name)
	if err != nil {
		return err
	}
	err = wrapper.client.Chtimes(full, atime, mtime)
	if err != nil {
		return &fs.PathError{Op: "chtimes", Path: name, Err: err}
	}
	return nil
}

func (wrapper *sftpFS) Remove(name string) error {
	full, err := wrapper.fullName("remove", name)
	if err != nil {
		return err
	}
	err = wrapper.client.Remove(full)
	if err != nil {
		return &fs.PathError{Op: "remove", Path: name, Err: err}
	}
	return nil
}

func (wrapper *sftpFS) Rename(oldpath string, newpath string) error {
	oldfull, err := wrapper.fullName("rename", oldpath)
	if err != nil {
		return err
	}
	newfull, err := wrapper.fullName("rename", newpath)
	if err != nil {
		return err
	}
	err = wrapper.client.Rename(oldfull, newfull)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
	return nil
}

func (wrapper *sftpFS) Symlink(oldname string, newname string) error {
	oldfull, err := wrapper.fullName("symlink", oldname)
	if err != nil {
		return err
	}
	newfull, err := wrapper.fullName("symlink", newname)
	if err != nil {
		return err
	}
	err = wrapper.client.Symlink(oldfull, newfull)
	if err != nil {
		return &os.LinkError{Op: "symlink", Old: oldname, New: newname, Err: err}
	}
	return nil
}

func (wrapper *sftpFS) Truncate(name string, size int64) error {
	full, err := wrapper.fullName("truncate", name)
	if err != nil {
		return err
	}
	err = wrapper.client.Truncate(full, size)
	if err != nil {
		return &fs.PathError{Op: "truncate", Path: name, Err: err}
	}
	return nil
}
