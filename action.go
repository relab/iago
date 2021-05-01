package iago

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"

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
	log.Println(out)
	return err
}

type Path struct {
	path       string
	relativeTo string
	perm       fs.FileMode
}

func (p Path) RelativeTo(path string) Path {
	p.relativeTo = path
	return p
}

func (p Path) WithPermissions(perm fs.FileMode) Path {
	p.perm = perm
	return p
}

func P(path string) Path {
	return Path{
		path:       filepath.Clean(path),
		relativeTo: ".",
		perm:       0664,
	}
}

type copyAction struct {
	src   Path
	dest  Path
	fetch bool
}

func Copy(src, dest Path) Action {
	return copyAction{src, dest, false}
}

func Fetch(src, dest Path) Action {
	return copyAction{src, dest, true}
}

func (ca copyAction) Apply(ctx context.Context, host Host) (err error) {
	var (
		from fs.FS
		to   fs.FS
	)
	if ca.fetch {
		from, err = fs.Sub(host, ca.src.relativeTo)
		if err != nil {
			return err
		}
		to = fs.DirFS("/" + ca.dest.relativeTo)
		// because we might be fetching from other hosts as well, we will append the host's name to the file
		ca.dest.path = ca.dest.path + "." + host.Name()
	} else {
		from = fs.DirFS("/" + ca.src.relativeTo)
		to, err = fs.Sub(host, ca.dest.relativeTo)
		if err != nil {
			return err
		}
		if err != nil {
			return err
		}
	}

	info, err := fs.Stat(from, ca.src.path)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return copy(ca.src.path, ca.dest.path, ca.dest.perm, from, to)
	}

	files, err := fs.ReadDir(from, ca.src.path)
	if err != nil {
		return err
	}

	err = fs.MkdirAll(to, ca.dest.path, ca.dest.perm)
	if err != nil {
		return err
	}

	for _, info := range files {
		err = copy(path.Join(ca.src.path, info.Name()), ca.dest.path, ca.dest.perm, from, to)
		if err != nil {
			return err
		}
	}
	return nil
}

func copy(src, dest string, perm fs.FileMode, from fs.FS, to fs.FS) error {
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
