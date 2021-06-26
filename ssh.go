package iago

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Raytar/iago/sftpfs"
	fs "github.com/Raytar/wrfs"
	"github.com/alexhunt7/ssher"
	"github.com/pkg/sftp"
	"go.uber.org/multierr"
	"golang.org/x/crypto/ssh"
)

type sshHost struct {
	name       string
	env        map[string]string
	client     *ssh.Client
	sftpClient *sftp.Client
	fs.FS
}

// DialSSH connects to a remote host using ssh.
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

	env, err := fetchEnv(client)
	if err != nil {
		return nil, err
	}

	return &sshHost{name, env, client, sftpClient, sftpFS}, nil
}

// NewSSHGroup returns a new group from the given host aliases. sshConfigPath determines the ssh_config file to use.
// If sshConfigPath is empty, the default configuration files will be used.
func NewSSHGroup(hosts []string, sshConfigPath string) (g Group, err error) {
	for _, h := range hosts {
		clientCfg, addr, err := ssher.ClientConfig(h, sshConfigPath)
		if err != nil {
			return nil, err
		}
		host, err := DialSSH(h, fmt.Sprintf("%s:22", addr), clientCfg)
		if err != nil {
			return nil, err
		}
		g = append(g, host)
	}
	return g, nil
}

// fetchEnv returns a map containing the environment variables of the ssh server.
func fetchEnv(cli *ssh.Client) (env map[string]string, err error) {
	env = make(map[string]string)
	cmd, err := cli.NewSession()
	if err != nil {
		return nil, err
	}
	defer safeClose(cmd, &err, io.EOF)
	out, err := cmd.Output("env")
	if err != nil {
		return nil, err
	}
	s := bufio.NewScanner(bytes.NewReader(out))
	for s.Scan() {
		l := s.Text()
		i := strings.Index(l, "=")
		if i < 1 {
			continue
		}
		key := l[:i]
		value := l[i+1:]
		env[key] = value
	}
	return env, nil
}

// Name returns the name of this host.
func (h *sshHost) Name() string {
	return h.name
}

// Address returns the address of the client.
func (h *sshHost) Address() string {
	return h.client.RemoteAddr().String()
}

// GetEnv retrieves the value of the environment variable named by the key.
// It returns the value, which will be empty if the variable is not present.
func (h *sshHost) GetEnv(key string) string {
	return h.env[key]
}

// Execute executes the given command and returns the output.
func (h *sshHost) Execute(ctx context.Context, cmd string) (output string, err error) {
	var outb bytes.Buffer

	session, err := h.client.NewSession()
	if err != nil {
		return "", err
	}

	childCtx, cancel := context.WithCancel(ctx)
	// create a channel to wait for helper goroutine
	c := make(chan struct{})
	defer func() { <-c }()
	defer cancel()

	go func() {
		// closes the session when either the command completed, or the parent context was cancelled
		<-childCtx.Done()
		safeClose(session, &err, io.EOF)
		close(c)
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
