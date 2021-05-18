package iago

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	fs "github.com/Raytar/wrfs"
)

type shellAction struct {
	cmd string
}

func Shell(cmd string) Action {
	return shellAction{cmd}
}

func (sa shellAction) Apply(ctx context.Context, host Host) error {
	out, err := host.Execute(ctx, fmt.Sprintf("/bin/bash -c '%s'", sa.cmd))
	if len(out) > 0 {
		log.Println(out)
	}
	return err
}

type Path struct {
	Path   string
	Prefix string
}

func (p Path) RelativeTo(path string) Path {
	p.Prefix = path
	return p
}

func P(path string) Path {
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, "/")
	return Path{
		Path:   path,
		Prefix: "",
	}
}

type Upload struct {
	Src  Path
	Dest Path
	Mode fs.FileMode
}

func (u Upload) Apply(ctx context.Context, host Host) error {
	return copyAction{src: u.Src, dest: u.Dest, mode: u.Mode, fetch: false}.Apply(ctx, host)
}

type Download struct {
	Src  Path
	Dest Path
	Mode fs.FileMode
}

func (d Download) Apply(ctx context.Context, host Host) error {
	return copyAction{src: d.Src, dest: d.Dest, mode: d.Mode, fetch: true}.Apply(ctx, host)
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
		from, err = fs.Sub(host, ca.src.Prefix)
		if err != nil {
			return err
		}
		to = fs.DirFS("/" + ca.dest.Prefix)
		// because we might be fetching from other hosts as well, we will append the host's name to the file
		ca.dest.Path = ca.dest.Path + "." + host.Name()
	} else {
		from = fs.DirFS("/" + ca.src.Prefix)
		to, err = fs.Sub(host, ca.dest.Prefix)
		if err != nil {
			return err
		}
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
