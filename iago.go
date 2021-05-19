package iago

import (
	"context"
	"log"
	"os"
	"time"

	fs "github.com/Raytar/wrfs"
	"go.uber.org/multierr"
)

const DefaultTimeout = 30 * time.Second

type Task struct {
	Name    string
	Action  Action
	OnError ErrorHandler
	Timeout time.Duration
}

func (t *Task) SetDefaults() {
	if t.OnError == nil {
		t.OnError = Panic
	}
	if t.Timeout == 0 {
		t.Timeout = DefaultTimeout
	}
}

type Action interface {
	Apply(ctx context.Context, host Host) error
}

type ErrorHandler func(error)

func Panic(e error) {
	log.Panicln(e)
}

func Ignore(e error) {
	log.Println(e, "(ignored)")
}

type Group []Host

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

func (g Group) Close() (err error) {
	for _, h := range g {
		err = multierr.Append(err, h.Close())
	}
	return err
}

type Host interface {
	fs.FS

	// Name returns the name of this host.
	Name() string

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
