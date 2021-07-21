package iago

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	fs "github.com/relab/wrfs"
	"go.uber.org/multierr"
)

// DefaultTimeout is the default timeout for an action.
var DefaultTimeout = 30 * time.Second

// Host is a connection to a remote host.
type Host interface {
	// Name returns the name of this host.
	Name() string

	// Address returns the address of the host.
	Address() string

	// GetEnv retrieves the value of the environment variable named by the key.
	// It returns the value, which will be empty if the variable is not present.
	GetEnv(key string) string

	// GetFS returns the file system of the host.
	GetFS() fs.FS

	// NewCommand returns a new command runner.
	NewCommand() (CmdRunner, error)

	// Close closes the connection to the host.
	Close() error

	// SetVar sets a host variable with the given key and value
	SetVar(key string, val interface{})

	// GetVar gets the host variable with the given key.
	// Returns (val, true) if the variable exists, (nil, false) otherwise.
	GetVar(key string) (val interface{}, ok bool)
}

// Expand expands any environment variables in the string 's' using the environment of the host 'h'.
func Expand(h Host, s string) string {
	return os.Expand(s, h.GetEnv)
}

// GetStringVar gets a string variable from the host.
func GetStringVar(host Host, key string) string {
	val, ok := host.GetVar(key)
	if ok {
		s, _ := val.(string)
		return s
	}
	return ""
}

// GetIntVar gets an integer variable from the host.
func GetIntVar(host Host, key string) int {
	val, ok := host.GetVar(key)
	if ok {
		i, _ := val.(int)
		return i
	}
	return 0
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
			errors <- wrapError(h.Name(), task.Name, task.Action.Apply(ctx, h))
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

// Do returns an action that runs the function f.
func Do(f func(ctx context.Context, host Host) (err error)) Action {
	return funcAction{f}
}

type funcAction struct {
	f func(context.Context, Host) error
}

func (fa funcAction) Apply(ctx context.Context, host Host) error {
	return fa.f(ctx, host)
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

func wrapError(hostName string, taskName string, err error) error {
	if err == nil {
		return nil
	}
	return TaskError{
		TaskName: taskName,
		HostName: hostName,
		Err:      err,
	}
}

// TaskError is the error type returned when an error occurs while running a task.
type TaskError struct {
	TaskName string
	HostName string
	Err      error
}

func (err TaskError) Error() string {
	return fmt.Sprintf("(%s) %s: %s", err.HostName, err.TaskName, err.Err.Error())
}

// Unwrap returns the cause of the task error.
func (err TaskError) Unwrap() error {
	return err.Err
}
