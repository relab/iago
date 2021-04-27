package fs

import (
	"io"
	"io/fs"
	"os"
	"time"
)

type WriteFile interface {
	fs.File
	io.Writer
}

type WriteFS interface {
	fs.FS

	// Functions for creating files / directories

	Mkdir(name string, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Create(name string) (WriteFile, error)
	OpenFile(name string, flag int, perm fs.FileMode) (WriteFile, error)

	// Functions for modifying existing files

	Chmod(name string, mode fs.FileMode) error
	Chown(name string, uid, gid int) error
	Chtimes(name string, atime, mtime time.Time) error
	Remove(name string) error
	RemoveAll(path string) error
	Rename(oldpath, newpath string) error
	Symlink(oldname, newname string) error
	Truncate(name string, size int64) error
}

type LocalFS struct{}

// Open opens the named file.
//
// When Open returns an error, it should be of type *PathError
// with the Op field set to "open", the Path field set to name,
// and the Err field describing the problem.
//
// Open should reject attempts to open names that do not satisfy
// ValidPath(name), returning a *PathError with Err set to
// ErrInvalid or ErrNotExist.
func (LocalFS) Open(name string) (fs.File, error) {
	return os.Open(name)
}

// Stat returns a FileInfo describing the file.
// If there is an error, it should be of type *PathError.
func (LocalFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

// ReadDir reads the named directory
// and returns a list of directory entries sorted by filename.
func (LocalFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(name)
}

// Functions for creating files / directories
func (LocalFS) Mkdir(name string, perm fs.FileMode) error {
	return os.Mkdir(name, perm)
}

func (LocalFS) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (LocalFS) Create(name string) (WriteFile, error) {
	return os.Create(name)
}

func (LocalFS) OpenFile(name string, flag int, perm fs.FileMode) (WriteFile, error) {
	return os.OpenFile(name, flag, perm)
}

// Functions for modifying existing files
func (LocalFS) Chmod(name string, mode fs.FileMode) error {
	return os.Chmod(name, mode)
}

func (LocalFS) Chown(name string, uid int, gid int) error {
	return os.Chown(name, uid, gid)
}

func (LocalFS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	return os.Chtimes(name, atime, mtime)
}

func (LocalFS) Remove(name string) error {
	return os.Remove(name)
}

func (LocalFS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (LocalFS) Rename(oldpath string, newpath string) error {
	return os.Rename(oldpath, newpath)
}

func (LocalFS) Symlink(oldname string, newname string) error {
	return os.Symlink(oldname, newname)
}

func (LocalFS) Truncate(name string, size int64) error {
	return os.Truncate(name, size)
}
