package iago

import (
	"context"
	"errors"
	"io"
)

// CmdRunner defines an interface for running commands on remote hosts.
// This interface is based on the "exec.Cmd" struct.
type CmdRunner interface {
	Run(cmd string) error
	RunContext(ctx context.Context, cmd string) error
	Start(cmd string) error
	Wait() error

	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.ReadCloser, error)
	StderrPipe() (io.ReadCloser, error)
}

// Shell runs a shell command.
type Shell struct {
	Command string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Apply runs the shell command on the host.
func (sa Shell) Apply(ctx context.Context, host Host) (err error) {
	cmd, err := host.NewCommand()
	if err != nil {
		return err
	}

	goroutines := 0
	errChan := make(chan error)

	defer func() {
		for range goroutines {
			// Drain the error channel; nil errors are discarded by Join.
			err = errors.Join(err, <-errChan)
		}
	}()

	if sa.Stdin != nil {
		in, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		defer safeClose(in, &err, io.EOF)
		go pipe(in, sa.Stdin, errChan)
		goroutines++
	}

	if sa.Stdout != nil {
		out, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		defer safeClose(out, &err, io.EOF)
		go pipe(sa.Stdout, out, errChan)
		goroutines++
	}

	if sa.Stderr != nil {
		errOut, err := cmd.StderrPipe()
		if err != nil {
			return err
		}
		defer safeClose(errOut, &err, io.EOF)
		go pipe(sa.Stderr, errOut, errChan)
		goroutines++
	}

	err = cmd.RunContext(ctx, sa.Command)
	if err != nil && err != io.EOF {
		return err
	}

	return nil
}

func pipe(dst io.Writer, src io.Reader, errChan chan error) {
	_, err := io.Copy(dst, src)
	errChan <- err
}
