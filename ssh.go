package iago

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/Raytar/iago/sftpfs"
	fs "github.com/Raytar/wrfs"
	"github.com/kevinburke/ssh_config"
	"github.com/pkg/sftp"
	"go.uber.org/multierr"
	"golang.org/x/crypto/ssh"
)

type sshHost struct {
	name       string
	client     *ssh.Client
	sftpClient *sftp.Client
	fs.FS
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

	return &sshHost{name, client, sftpClient, sftpFS}, nil
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
	return multierr.Combine(h.sftpClient.Close(), h.client.Close())
}

func NewSSHGroup(hosts []string, sshConfigPath string, clientCfg ssh.ClientConfig) (g Group, err error) {
	f, err := os.Open(sshConfigPath)
	if err != nil {
		return nil, err
	}

	sshCfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, err
	}

	for _, h := range hosts {
		addr, err := sshCfg.Get(h, "HostName")
		if err != nil {
			return nil, err
		}
		if addr == "" {
			continue
		}
		host, err := DialSSH(h, fmt.Sprintf("%s:22", addr), &clientCfg)
		if err != nil {
			return nil, err
		}
		g = append(g, host)
	}

	return g, nil
}
