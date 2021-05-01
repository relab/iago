package iago

import (
	"context"
	"log"
	"time"

	fs "github.com/Raytar/wrfs"
	"go.uber.org/multierr"
)

type Action interface {
	Apply(ctx context.Context, host Host) error
}

type RunOptions struct {
	ErrorHandler ErrorHandler
	Timeout      time.Duration
}

func NewRunOptions(opts []RunOption) RunOptions {
	o := RunOptions{}
	// applying the default options
	PanicOnError(&o)
	WithTimeout(time.Minute)(&o)

	// now applying the provided options
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

type RunOption func(opts *RunOptions)

func WithTimeout(timeout time.Duration) RunOption {
	return func(opts *RunOptions) {
		opts.Timeout = timeout
	}
}

type ErrorHandler func(error)

func PanicOnError(opts *RunOptions) {
	opts.ErrorHandler = func(e error) {
		log.Panicln(e)
	}
}

func IgnoreErrors(opts *RunOptions) {
	opts.ErrorHandler = func(e error) {
		log.Println(e, "(ignored)")
	}
}

type Group []Host

func (g Group) Run(action Action, opts ...RunOption) {
	o := NewRunOptions(opts)
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()

	errors := make(chan error)
	for _, h := range g {
		go func(h Host) {
			errors <- action.Apply(ctx, h)
		}(h)
	}
	for range g {
		err := <-errors
		if err != nil {
			o.ErrorHandler(err)
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
