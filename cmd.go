package iago

import (
	"context"
	"fmt"
	"io"

	"go.uber.org/multierr"
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
	Shell   string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

func (sa *Shell) defaultValues() {
	if sa.Shell == "" {
		sa.Shell = "/bin/bash"
	}
}

// Apply runs the shell command on the host.
func (sa Shell) Apply(ctx context.Context, host Host) (err error) {
	sa.defaultValues()

	cmd, err := host.NewCommand()
	if err != nil {
		return err
	}

	goroutines := 0
	errChan := make(chan error)

	defer func() {
		for i := 0; i < goroutines; i++ {
			cerr := <-errChan
			if cerr != nil {
				err = multierr.Append(err, cerr)
			}
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

	err = cmd.RunContext(ctx, fmt.Sprintf("/bin/bash -c '%s'", sa.Command))
	if err != nil && err != io.EOF {
		return err
	}

	return nil
}

func pipe(dst io.Writer, src io.Reader, errChan chan error) {
	_, err := io.Copy(dst, src)
	errChan <- err
}
