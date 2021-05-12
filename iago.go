package iago

import (
	"context"
	"log"
	"time"

	fs "github.com/Raytar/wrfs"
	"go.uber.org/multierr"
)

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
		t.Timeout = time.Minute
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

	// Execute executes the given command and returns the output.
	Execute(ctx context.Context, cmd string) (output string, err error)

	// Close closes the connection to the host.
	Close() error
}
