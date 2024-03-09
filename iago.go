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
type Group struct {
	Hosts        []Host
	ErrorHandler ErrorHandler
	Timeout      time.Duration
}

// NewGroup returns a new Group consisting of the given hosts.
func NewGroup(hosts []Host) Group {
	return Group{
		Hosts:        hosts,
		ErrorHandler: Panic,
		Timeout:      DefaultTimeout,
	}
}

// Run runs the task on all hosts in the group concurrently.
func (g Group) Run(name string, f func(ctx context.Context, host Host) error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.Timeout)
	defer cancel()

	errors := make(chan error)
	for _, h := range g.Hosts {
		go func(h Host) {
			errors <- wrapError(h.Name(), name, f(ctx, h))
		}(h)
	}

	for range g.Hosts {
		err := <-errors
		if err != nil {
			g.ErrorHandler(err)
		}
	}
}

// Close closes any connections to hosts.
func (g Group) Close() (err error) {
	for _, h := range g.Hosts {
		// Join close errors; nil errors are discarded by Join.
		err = errors.Join(err, h.Close())
	}
	return err
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
