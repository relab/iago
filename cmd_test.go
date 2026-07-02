package iago

import (
	"context"
	"errors"
	"io"
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
