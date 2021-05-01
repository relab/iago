package iago

import (
	"bytes"
	"context"

	"github.com/Raytar/iago/sftpfs"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sshHost struct {
	name   string
	client *ssh.Client
	*sftpfs.SFTPFS
}

func DialSSH(name, addr string, cfg *ssh.ClientConfig) (Host, error) {
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, err
	}

	sftpFS := sftpfs.New(sftpClient, "/")
	if err != nil {
		return nil, err
	}

	return &sshHost{name, client, sftpFS}, nil
}

// Name returns the name of this host.
func (h *sshHost) Name() string {
	return h.name
}

// Execute executes the given command and returns the output.
func (h *sshHost) Execute(ctx context.Context, cmd string) (output string, err error) {
	var outb bytes.Buffer

	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	session, err := h.client.NewSession()
	if err != nil {
		return "", err
	}

	go func() {
		// closes the session when either the command completed, or the parent context was cancelled
		<-childCtx.Done()
		session.Close()
	}()

	session.Stdout = &outb
	if err := session.Run(cmd); err != nil {
		return "", nil
	}

	return outb.String(), nil
}

// Close closes the connection to the host.
func (h *sshHost) Close() error {
	return h.client.Close()
}
