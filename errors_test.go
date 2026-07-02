package iago

import (
	"context"
	"errors"
	"io/fs"
	"testing"
)

// fakeHost is a no-op Host for exercising Group.Run without a real
// connection. Name is used to label task errors; cmd, if set, backs
// NewCommand for tests that exercise Shell/Output; fsys, if set, backs GetFS
// for tests that exercise Upload/UploadFile.
type fakeHost struct {
	name string
	cmd  CmdRunner
	fsys fs.FS
}

func (h fakeHost) Name() string                   { return h.name }
func (h fakeHost) Address() string                { return h.name }
func (h fakeHost) GetEnv(string) string           { return "" }
func (h fakeHost) GetFS() fs.FS                   { return h.fsys }
func (h fakeHost) NewCommand() (CmdRunner, error) { return h.cmd, nil }
func (h fakeHost) Close() error                   { return nil }
func (h fakeHost) SetVar(string, any)             {}
func (h fakeHost) GetVar(string) (any, bool)      { return nil, false }

func TestErrorsCollector(t *testing.T) {
	var errs Errors
	if err := errs.Err(); err != nil {
		t.Fatalf("empty collector: got %v, want nil", err)
	}

	e1 := errors.New("boom 1")
	e2 := errors.New("boom 2")
	errs.Handle(e1)
	errs.Handle(e2)

	got := errs.Err()
	if !errors.Is(got, e1) || !errors.Is(got, e2) {
		t.Fatalf("joined error %v does not wrap both %v and %v", got, e1, e2)
	}
}

func TestWithErrorHandlerOption(t *testing.T) {
	var errs Errors
	cfg := applyGroupOptions(WithErrorHandler(errs.Handle))
	if cfg.errorHandler == nil {
		t.Fatal("WithErrorHandler did not set errorHandler")
	}
	cfg.errorHandler(errors.New("boom"))
	if errs.Err() == nil {
		t.Fatal("handler set by the option did not reach the collector")
	}
}

func TestGroupRunCollectsErrors(t *testing.T) {
	var errs Errors
	g := NewGroup([]Host{fakeHost{name: "a"}, fakeHost{name: "b"}, fakeHost{name: "c"}})
	g.ErrorHandler = errs.Handle

	g.Run("task", func(ctx context.Context, host Host) error {
		if host.Name() == "b" {
			return errors.New("task failed")
		}
		return nil
	})

	err := errs.Err()
	if err == nil {
		t.Fatal("expected an error from host b, got nil")
	}
	// Only host b fails, so exactly one error should be collected.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		if n := len(joined.Unwrap()); n != 1 {
			t.Fatalf("collected %d errors, want 1", n)
		}
	}
	var te TaskError
	if !errors.As(err, &te) {
		t.Fatalf("error %v is not a TaskError", err)
	}
	if te.HostName != "b" {
		t.Fatalf("TaskError host = %q, want b", te.HostName)
	}
}

func TestCollect(t *testing.T) {
	g := NewGroup([]Host{fakeHost{name: "a"}, fakeHost{name: "b"}, fakeHost{name: "c"}})

	results, err := Collect(g, "task", func(ctx context.Context, host Host) (int, error) {
		if host.Name() == "b" {
			return 0, errors.New("task failed")
		}
		return len(host.Name()), nil
	})

	if err == nil {
		t.Fatal("expected an error from host b, got nil")
	}
	if len(results) != 2 {
		t.Fatalf("results = %v, want exactly hosts a and c", results)
	}
	if _, ok := results["b"]; ok {
		t.Fatal("host b returned an error but still has an entry")
	}
	for _, host := range []string{"a", "c"} {
		if got, want := results[host], 1; got != want {
			t.Fatalf("results[%q] = %d, want %d", host, got, want)
		}
	}
}
