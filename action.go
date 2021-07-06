package iago

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	fs "github.com/relab/wrfs"
)

// Func returns an action that runs the function f.
func Func(f func(ctx context.Context, host Host) (err error)) Action {
	return funcAction{f}
}

type funcAction struct {
	f func(context.Context, Host) error
}

func (fa funcAction) Apply(ctx context.Context, host Host) error {
	return fa.f(ctx, host)
}

func cleanPath(path string) string {
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
	p.Prefix = cleanPath(path)
	return p
}

// Expand expands environment variables in the path and prefix strings using the environment of the given host.
func (p Path) Expand(h Host) Path {
	return Path{
		Path:   cleanPath(Expand(h, p.Path)),
		Prefix: cleanPath(Expand(h, p.Prefix)),
	}
}

// P returns a path relative to the root directory.
func P(path string) Path {
	return Path{
		Path:   cleanPath(path),
		Prefix: "",
	}
}

// Upload uploads a file or directory to a remote host.
type Upload struct {
	Src  Path
	Dest Path
	Mode fs.FileMode
}

// Apply performs the upload.
func (u Upload) Apply(ctx context.Context, host Host) error {
	return copyAction{src: u.Src.Expand(host), dest: u.Dest.Expand(host), mode: u.Mode, fetch: false}.Apply(ctx, host)
}

// Download downloads a file or directory from a remote host.
type Download struct {
	Src  Path
	Dest Path
	Mode fs.FileMode
}

// Apply performs the download.
func (d Download) Apply(ctx context.Context, host Host) error {
	return copyAction{src: d.Src.Expand(host), dest: d.Dest.Expand(host), mode: d.Mode, fetch: true}.Apply(ctx, host)
}

type copyAction struct {
	src   Path
	dest  Path
	fetch bool
	mode  fs.FileMode
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
		// because we might be fetching from other hosts as well, we will append the host's name to the file
		ca.dest.Path = ca.dest.Path + "." + host.Name()
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
		return ca.copyDir(from, to)
	}
	return copyFile(ca.src.Path, ca.dest.Path, ca.mode, from, to)
}

func (ca copyAction) copyDir(from, to fs.FS) error {
	files, err := fs.ReadDir(from, ca.src.Path)
	if err != nil {
		return err
	}

	err = fs.MkdirAll(to, ca.dest.Path, ca.mode)
	if err != nil {
		return err
	}

	for _, info := range files {
		if info.IsDir() {
			err = ca.copyDir(from, to)
		} else {
			err = copyFile(path.Join(ca.src.Path, info.Name()), ca.dest.Path, ca.mode, from, to)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dest string, perm fs.FileMode, from fs.FS, to fs.FS) error {
	fromF, err := from.Open(src)
	if err != nil {
		return err
	}
	toF, err := fs.OpenFile(to, dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(toF.(io.Writer), fromF)
	return err
}
