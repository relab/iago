package iago

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"time"

	fs "github.com/Raytar/wrfs"
	"go.uber.org/multierr"
)

// DefaultTimeout is the default timeout for an action.
var DefaultTimeout = 30 * time.Second

// Host is a connection to a remote host.
type Host interface {
	fs.FS

	// Name returns the name of this host.
	Name() string

	// Address returns the address of the host.
	Address() string

	// GetEnv retrieves the value of the environment variable named by the key.
	// It returns the value, which will be empty if the variable is not present.
	GetEnv(key string) string

	// Execute executes the given command and returns the output.
	Execute(ctx context.Context, cmd string) (output string, err error)

	// Close closes the connection to the host.
	Close() error
}

// Expand expands any environment variables in the string 's' using the environment of the host 'h'.
func Expand(h Host, s string) string {
	return os.Expand(s, h.GetEnv)
}

// Group is a group of hosts.
type Group []Host

// Run runs the task on all hosts in the group concurrently.
func (g Group) Run(task Task) {
	task.SetDefaults()

	ctx, cancel := context.WithTimeout(context.Background(), task.Timeout)
	defer cancel()

	errors := make(chan error)
	for _, h := range g {
		go func(h Host) {
			errors <- task.Action.Apply(ctx, h)
		}(h)
	}
	for range g {
		err := <-errors
		if err != nil {
			task.OnError(err)
		}
	}
}

// Close closes any connections to hosts.
func (g Group) Close() (err error) {
	for _, h := range g {
		err = multierr.Append(err, h.Close())
	}
	return err
}

// Task describes an action to be performed on a host.
type Task struct {
	Name    string
	Action  Action
	OnError ErrorHandler
	Timeout time.Duration
}

// SetDefaults sets the default values for the task.
func (t *Task) SetDefaults() {
	if t.OnError == nil {
		t.OnError = Panic
	}
	if t.Timeout == 0 {
		t.Timeout = DefaultTimeout
	}
}

// Action is an interface for applying changes to a host.
// Tasks execute actions by running the Apply function.
type Action interface {
	Apply(ctx context.Context, host Host) error
}

// ErrorHandler is a function that handles errors from actions.
type ErrorHandler func(error)

// Panic handles errors by panicking.
func Panic(e error) {
	log.Panicln(e)
}

// Ignore ignores errors.
func Ignore(e error) {
	log.Println(e, "(ignored)")
}

func safeClose(closer io.Closer, errPtr *error, ignoredErrs ...error) {
	err := closer.Close()
	if *errPtr != nil {
		return
	}
	for _, ignored := range ignoredErrs {
		if errors.Is(err, ignored) {
			return
		}
	}
	*errPtr = err
}
