package iago

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"

	iagofs "github.com/Raytar/iago/fs"
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

type copyAction struct {
	src   string
	dest  string
	perm  fs.FileMode
	fetch bool
}

func Copy(src, dest string, perm fs.FileMode) Action {
	return copyAction{src, dest, perm, false}
}

func Fetch(src, dest string, perm fs.FileMode) Action {
	return copyAction{src, dest, perm, true}
}

func (ca copyAction) Apply(ctx context.Context, host Host) (err error) {
	var (
		src  string
		dest string
		from fs.FS
		to   iagofs.WriteFS
	)
	if ca.fetch {
		from = host
		to = iagofs.LocalFS{}
		src = filepath.Clean(ca.src)
		dest, err = filepath.Abs(ca.dest)
		// because we might be fetching from other hosts as well, we will append the host's name to the file
		dest = dest + "." + host.Name()
		if err != nil {
			return err
		}
	} else {
		from = iagofs.LocalFS{}
		to = host
		src, err = filepath.Abs(ca.src)
		if err != nil {
			return err
		}
		dest = filepath.Clean(ca.dest)
	}

	info, err := fs.Stat(from, src)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return copy(src, dest, ca.perm, from, to)
	}

	files, err := fs.ReadDir(from, src)
	if err != nil {
		return err
	}

	err = to.MkdirAll(dest, ca.perm)
	if err != nil {
		return err
	}

	for _, info := range files {
		err = copy(path.Join(src, info.Name()), dest, ca.perm, from, to)
		if err != nil {
			return err
		}
	}
	return nil
}

func copy(src, dest string, perm fs.FileMode, from fs.FS, to iagofs.WriteFS) error {
	fromF, err := from.Open(src)
	if err != nil {
		return err
	}
	toF, err := to.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(toF, fromF)
	return err
}
