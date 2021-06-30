package iago

import (
	"context"
	"fmt"
	"io"
	"log"
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
	Shell   string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Apply runs the shell command on the host.
func (sa Shell) Apply(ctx context.Context, host Host) error {
	buf := new(strings.Builder)
	cmd := host.NewCommand()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	err = cmd.RunContext(ctx, fmt.Sprintf("/bin/bash -c '%s'", sa.Command))
	if err != nil {
		return err
	}
	_, err = io.Copy(buf, out)
	if err != nil {
		return err
	}
	if buf.Len() > 0 {
		log.Println(buf.String())
	}
	return err
}
