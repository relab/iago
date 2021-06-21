package iago_test

import (
	"os"
	"path/filepath"
	"strconv"
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
		Name:    "Read distribution name",
		Action:  Shell("grep '^ID=' /etc/os-release > $HOME/os"),
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

	for i := range g {
		f, err := os.ReadFile(filepath.Join(dir, "os."+strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(f))
	}
}
