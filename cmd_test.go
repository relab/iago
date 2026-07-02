package iago

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

// fakeCmdRunner is a minimal CmdRunner backing fakeHost.NewCommand in tests
// that exercise Shell/Output without a real SSH connection. StdoutPipe
// returns output in full immediately, so it is unsuitable for testing
// streaming behavior, only the captured-result path Output relies on.
type fakeCmdRunner struct {
	output string
	err    error
}

func (r *fakeCmdRunner) Run(string) error                         { return r.err }
func (r *fakeCmdRunner) RunContext(context.Context, string) error { return r.err }
func (r *fakeCmdRunner) Start(string) error                       { return r.err }
func (r *fakeCmdRunner) Wait() error                              { return r.err }
func (r *fakeCmdRunner) StdinPipe() (io.WriteCloser, error)       { return nil, errors.New("not supported") }
func (r *fakeCmdRunner) StdoutPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.output)), nil
}
func (r *fakeCmdRunner) StderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func TestOutputCapturesStdout(t *testing.T) {
	host := fakeHost{name: "h", cmd: &fakeCmdRunner{output: "hello\n"}}
	out, err := Output(context.Background(), host, "echo hello")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if out != "hello\n" {
		t.Fatalf("Output = %q, want %q", out, "hello\n")
	}
}

func TestOutputPropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	host := fakeHost{name: "h", cmd: &fakeCmdRunner{err: wantErr}}
	_, err := Output(context.Background(), host, "false")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Output error = %v, want %v", err, wantErr)
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "hello", want: "'hello'"},
		{name: "spaces", in: "hello world", want: "'hello world'"},
		{name: "single quote", in: "it's", want: `'it'\''s'`},
		{name: "empty", in: "", want: "''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Quote(tt.in); got != tt.want {
				t.Errorf("Quote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// fakeExitError is a minimal error implementing ExitStatus, standing in for
// golang.org/x/crypto/ssh's *ssh.ExitError in tests.
type fakeExitError struct{ status int }

func (e fakeExitError) Error() string   { return "exit status " + strconv.Itoa(e.status) }
func (e fakeExitError) ExitStatus() int { return e.status }

func TestFileExists(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    bool
		wantErr bool
	}{
		{name: "exists", err: nil, want: true},
		{name: "does not exist", err: fakeExitError{status: 1}, want: false},
		{name: "other exit status", err: fakeExitError{status: 2}, wantErr: true},
		{name: "transport error", err: errors.New("connection reset"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := fakeHost{name: "h", cmd: &fakeCmdRunner{err: tt.err}}
			got, err := FileExists(context.Background(), host, "/some/path")
			if (err != nil) != tt.wantErr {
				t.Fatalf("FileExists error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("FileExists = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDirExists(t *testing.T) {
	host := fakeHost{name: "h", cmd: &fakeCmdRunner{err: fakeExitError{status: 1}}}
	got, err := DirExists(context.Background(), host, "/some/dir")
	if err != nil {
		t.Fatalf("DirExists: %v", err)
	}
	if got {
		t.Fatal("DirExists = true, want false")
	}
}
