package iago_test

import (
	"context"
	"os"
	"path/filepath"
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

	g := iagotest.CreateSSHGroup(t, 4, false)

	g.ErrorHandler = func(e error) {
		t.Fatal(e)
	}

	g.Run("Custom Shell Command", func(ctx context.Context, host Host) (err error) {
		var sb strings.Builder
		err = Shell{
			Command: "lsb_release -a",
			Stdout:  &sb,
		}.Apply(ctx, host)
		if err != nil {
			return err
		}
		t.Log(sb.String())
		return nil
	})

	g.Run("Read distribution name", Shell{Command: "grep '^ID=' /etc/os-release > $HOME/os"}.Apply)

	g.Run("Upload a file", func(ctx context.Context, host Host) error {
		src, err := NewPath(wd, "LICENSE")
		if err != nil {
			return err
		}
		dest, err := NewPath(Expand(host, "$HOME"), "LICENSE")
		if err != nil {
			return err
		}
		return Upload{
			Src:  src,
			Dest: dest,
		}.Apply(ctx, host)
	})

	g.Run("Download files", func(ctx context.Context, host Host) error {
		src, err := NewPath(Expand(host, "$HOME"), "os")
		if err != nil {
			return err
		}
		dest, err := NewPath(dir, "os")
		if err != nil {
			return err
		}
		return Download{
			Src:  src,
			Dest: dest,
		}.Apply(ctx, host)
	})

	for _, h := range g.Hosts {
		f, err := os.ReadFile(filepath.Join(dir, "os."+h.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(f))
	}
}
