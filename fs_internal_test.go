package iago

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/relab/wrfs"
)

func TestUploadFile(t *testing.T) {
	srcDir, dstDir := t.TempDir(), t.TempDir()

	srcPath := filepath.Join(srcDir, "payload.bin")
	want := []byte("hello, host")
	if err := os.WriteFile(srcPath, want, 0o644); err != nil {
		t.Fatal(err)
	}

	host := fakeHost{name: "h", fsys: wrfs.DirFS(dstDir)}
	if err := UploadFile(context.Background(), host, srcPath, "/uploaded.bin", NewPerm(0o644)); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dstDir, "uploaded.bin"))
	if err != nil {
		t.Fatalf("reading uploaded file: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("uploaded content = %q, want %q", got, want)
	}
}
