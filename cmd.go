package iago

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
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

// Output runs cmd on host as a shell command and returns its captured
// standard output. It is a convenience wrapper around [Shell] for the common
// case of wanting a command's output as a string rather than streaming it to
// a caller-provided writer.
func Output(ctx context.Context, host Host, cmd string) (string, error) {
	var buf bytes.Buffer
	err := Shell{Command: cmd, Stdout: &buf}.Apply(ctx, host)
	return buf.String(), err
}

// Quote wraps s in single quotes so it is safe to embed as one argument in a
// [Shell] command run on a POSIX shell. An embedded single quote is escaped
// using the '\” idiom: end the quoted string, emit an escaped quote, and
// resume quoting.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ExitStatus is implemented by an error carrying a remote process's exit
// status, such as the golang.org/x/crypto/ssh package's *ssh.ExitError
// returned by [Shell.Apply] when the remote command runs to completion but
// exits non-zero. errors.AsType[iago.ExitStatus](err) extracts it from a
// wrapped error (for example a [TaskError] from [Group.Run]) without the
// caller importing golang.org/x/crypto/ssh, and distinguishes "the command
// ran and exited non-zero" from "the command never completed" (a dial
// failure, a dropped connection, and the like, none of which implement it).
type ExitStatus interface {
	error
	ExitStatus() int
}

// FileExists reports whether path exists on host and is not a directory,
// checked with a POSIX `test -f`. It returns false, nil when the remote
// command determines the path fails the test (exit status 1); any other
// failure (a transport error, a missing shell) is returned as an error.
func FileExists(ctx context.Context, host Host, path string) (bool, error) {
	return pathTest(ctx, host, "-f", path)
}

// DirExists reports whether path is a directory on host, checked with a
// POSIX `test -d`. See [FileExists] for the exit-status contract.
func DirExists(ctx context.Context, host Host, path string) (bool, error) {
	return pathTest(ctx, host, "-d", path)
}

func pathTest(ctx context.Context, host Host, flag, path string) (bool, error) {
	err := Shell{Command: "test " + flag + " " + Quote(path)}.Apply(ctx, host)
	if err == nil {
		return true, nil
	}
	if exitErr, ok := errors.AsType[ExitStatus](err); ok && exitErr.ExitStatus() == 1 {
		return false, nil
	}
	return false, err
}
