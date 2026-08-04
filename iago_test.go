package iago_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relab/iago"
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
		t.Error(e)
	}

	g.Run("Custom Shell Command", func(ctx context.Context, host iago.Host) (err error) {
		var sb strings.Builder
		err = iago.Shell{
			Command: "lsb_release -a",
			Stdout:  &sb,
		}.Apply(ctx, host)
		if err != nil {
			return err
		}
		t.Log(sb.String())
		return nil
	})

	g.Run("Read distribution name", iago.Shell{Command: "grep '^ID=' /etc/os-release > $HOME/os"}.Apply)

	g.Run("Upload a file", func(ctx context.Context, host iago.Host) error {
		src, err := iago.NewPath(wd, "LICENSE")
		if err != nil {
			return err
		}
		dest, err := iago.NewPath(iago.Expand(host, "$HOME"), "LICENSE")
		if err != nil {
			return err
		}
		return iago.Upload{
			Src:  src,
			Dest: dest,
		}.Apply(ctx, host)
	})

	g.Run("Download files", func(ctx context.Context, host iago.Host) error {
		src, err := iago.NewPath(iago.Expand(host, "$HOME"), "os")
		if err != nil {
			return err
		}
		dest, err := iago.NewPath(dir, "os")
		if err != nil {
			return err
		}
		return iago.Download{
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

// TestStartHonorsContextTimeout verifies that a task using the "start now,
// wait later" pattern via RunContext respects the group's timeout: a
// long-running remote command must be aborted promptly when the context
// deadline expires, instead of blocking until the remote process exits.
func TestStartHonorsContextTimeout(t *testing.T) {
	g := iagotest.CreateSSHGroup(t, 1, false)
	g.Timeout = 500 * time.Millisecond

	start := time.Now()
	g.ErrorHandler = iago.Ignore
	g.Run("Sleep beyond timeout", func(ctx context.Context, host iago.Host) error {
		return iago.Shell{Command: "sleep 60"}.Apply(ctx, host)
	})
	elapsed := time.Since(start)

	if elapsed >= 60*time.Second {
		t.Fatalf("Wait did not honor context timeout: took %s", elapsed)
	}
}

func TestIagoDownloadExample(t *testing.T) {
	dir := t.TempDir()

	// The iagotest package provides a helper function that automatically
	// builds and starts docker containers with an exposed SSH port for testing.
	g := iagotest.CreateSSHGroup(t, 2, false)

	g.Run("Download files", func(ctx context.Context, host iago.Host) error {
		src, err := iago.NewPath("/etc", "os-release")
		if err != nil {
			return err
		}
		t.Logf("Downloading %s from %s", src, host.Name())
		dest, err := iago.NewPath(dir, "os")
		if err != nil {
			return err
		}
		t.Logf("Saving to %s", dest)
		return iago.Download{
			Src:  src,
			Dest: dest,
			Perm: iago.NewPerm(0o644),
		}.Apply(ctx, host)
	})

	filesInDir, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, len(filesInDir))
	for i, file := range filesInDir {
		files[i] = file.Name()
	}
	t.Logf("Copied files: %s", strings.Join(files, ", "))

	for _, h := range g.Hosts {
		t.Logf("Reading file %s for host %s", filepath.Join(dir, "os."+h.Name()), h.Name())
		f, err := os.ReadFile(filepath.Join(dir, "os."+h.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(f))
	}
}
