package iago_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	. "github.com/Raytar/iago"
	"github.com/Raytar/iago/iagotest"
)

func TestIago(t *testing.T) {
	dir := t.TempDir()

	g := iagotest.CreateSSHGroup(t, 4)

	errFunc := func(e error) {
		t.Fatal(e)
	}

	g.Run(Task{
		Name: "Custom Shell Command",
		Action: Func(func(ctx context.Context, host Host) (err error) {
			var sb strings.Builder
			err = Shell{
				Command: "whoami",
				Stdout:  &sb,
			}.Apply(ctx, host)
			if err != nil {
				return err
			}
			t.Log(sb.String())
			return nil
		}),
		OnError: errFunc,
	})

	g.Run(Task{
		Name:    "Read distribution name",
		Action:  Shell{Command: "grep '^ID=' /etc/os-release > $HOME/os"},
		OnError: errFunc,
	})

	g.Run(Task{
		Name: "Download files",
		Action: Download{
			Src:  P("os").RelativeTo("$HOME"),
			Dest: P("os").RelativeTo(dir),
			Mode: 0644,
		},
		OnError: errFunc,
	})

	g.Run(Task{
		Name: "Custom Func",
		Action: Func(func(ctx context.Context, host Host) (err error) {
			t.Log(host.GetEnv("HOME"))
			return nil
		}),
		OnError: errFunc,
	})

	g.Run(Task{
		Name:    "Should Error",
		Action:  Func(func(ctx context.Context, host Host) (err error) { return errors.New("something happened") }),
		OnError: func(e error) { t.Log(e) },
	})

	for i := range g {
		f, err := os.ReadFile(filepath.Join(dir, "os."+strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(f))
	}
}
