package iago_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	. "github.com/relab/iago"
	"github.com/relab/iago/iagotest"
)

func TestIago(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()

	if err != nil {
		t.Fatal(err)
	}

	g := iagotest.CreateSSHGroup(t, 4)

	errFunc := func(e error) {
		t.Fatal(e)
	}

	g.Run(Task{
		Name: "Custom Shell Command",
		Action: Do(func(ctx context.Context, host Host) (err error) {
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
		Name: "Upload a file",
		Action: Upload{
			Src:  P("LICENSE").RelativeTo(wd),
			Dest: P("LICENSE").RelativeTo("$HOME"),
			Mode: 0644,
		},
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
		Action: Do(func(ctx context.Context, host Host) (err error) {
			t.Log(host.GetEnv("HOME"))
			return nil
		}),
		OnError: errFunc,
	})

	g.Run(Task{
		Name:    "Should Error",
		Action:  Do(func(ctx context.Context, host Host) (err error) { return errors.New("something happened") }),
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
