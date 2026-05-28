package iago

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"github.com/relab/iago/sftpfs"
	fs "github.com/relab/wrfs"
	"golang.org/x/crypto/ssh"
)

type sshHost struct {
	name       string
	env        map[string]string
	client     *ssh.Client
	sftpClient *sftp.Client
	fsys       fs.FS
	vars       map[string]any
}

// DialSSH connects to a remote host using ssh.
func DialSSH(name, addr string, cfg *ssh.ClientConfig) (Host, error) {
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return newHostFromClient(name, client)
}

// dialViaProxy connects to a remote host by tunnelling through an already-established
// SSH connection to a jump host. The jump client is not owned by the returned Host;
// its lifetime is managed by the caller (typically [NewSSHGroup], which shares one
// jump client across all targets that route through the same ProxyJump spec).
func dialViaProxy(name, addr string, cfg *ssh.ClientConfig, jump *ssh.Client) (Host, error) {
	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	conn, err := jump.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	ncc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return nil, err
	}
	return newHostFromClient(name, ssh.NewClient(ncc, chans, reqs))
}

// newHostFromClient wraps an established *ssh.Client as a Host, creating the SFTP
// sub-client and fetching the remote environment.
func newHostFromClient(name string, client *ssh.Client) (Host, error) {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, err
	}
	env, err := fetchEnv(client)
	if err != nil {
		return nil, err
	}
	return &sshHost{
		name:       name,
		env:        env,
		client:     client,
		sftpClient: sftpClient,
		fsys:       sftpfs.New(sftpClient, "/"),
		vars:       make(map[string]any),
	}, nil
}

// NewSSHGroup returns a new ssh group from the given host aliases. The sshConfigFile
// argument specifies the ssh config file to use. If sshConfigFile is empty, the
// default configuration files will be used: ~/.ssh/config.
//
// The host aliases should be defined in the ssh config file, and the config file
// should contain the necessary information to connect to the hosts without a passphrase.
// This usually means setting up the ssh-agent with the necessary keys beforehand (and
// entering the passphrase), or specifying the passphrase-less key to use with the
// IdentityFile option. Moreover, the config file should specify whether or not to use
// strict host key checking using the StrictHostKeyChecking option. If strict host key
// checking is enabled, the ssh server's host keys should be present in the known_hosts
// files specified by UserKnownHostsFile (the default known_hosts files will be used if
// this option is not specified).
//
// The specified hosts must all contain a authorized_keys file containing the
// public key of the user running this program.
//
// When several aliases share the same ProxyJump spec, a single TCP/SSH connection
// to the jump host is dialed once and reused for every target tunnelled through it.
// This mirrors what OpenSSH's ControlMaster provides for the system ssh client and
// avoids opening one proxy connection per target alias. The shared jump clients are
// owned by the returned [Group] and closed by [Group.Close].
func NewSSHGroup(hostAliases []string, sshConfigFile string) (group Group, err error) {
	if sshConfigFile == "" {
		if err = initHomeDir(); err != nil {
			return group, err
		}
		sshConfigFile = filepath.Join(homeDir, ".ssh", "config")
	}
	config, err := ParseSSHConfig(sshConfigFile)
	if err != nil {
		return group, err
	}

	jumpClients := make(map[string]*ssh.Client)
	hosts := make([]Host, 0, len(hostAliases))
	defer func() {
		if err == nil {
			return
		}
		for _, h := range hosts {
			_ = h.Close()
		}
		for _, jc := range jumpClients {
			_ = jc.Close()
		}
	}()

	for _, alias := range hostAliases {
		var host Host
		host, err = dialHost(alias, config, jumpClients)
		if err != nil {
			return group, err
		}
		hosts = append(hosts, host)
	}

	group = NewGroup(hosts)
	for _, jc := range jumpClients {
		group.sharedClosers = append(group.sharedClosers, jc)
	}
	return group, nil
}

// dialHost dials a single host using the given SSH config. If the host has a
// ProxyJump, jumpClients is consulted first: if a client for that proxy spec
// already exists it is reused, otherwise a new connection to the proxy is
// dialed and stored in the map. The map is the caller's responsibility to
// close (typically via [Group.sharedClosers]).
func dialHost(alias string, config *sshConfig, jumpClients map[string]*ssh.Client) (Host, error) {
	clientCfg, err := config.ClientConfig(alias)
	if err != nil {
		return nil, err
	}
	proxySpec, err := config.get(alias, "ProxyJump")
	if err != nil {
		return nil, err
	}
	targetAddr := config.ConnectAddr(alias)
	if proxySpec == "" || proxySpec == "none" {
		return DialSSH(alias, targetAddr, clientCfg)
	}
	jumpClient, ok := jumpClients[proxySpec]
	if !ok {
		jumpCfg, err := config.ClientConfig(proxySpec)
		if err != nil {
			return nil, fmt.Errorf("proxy jump %q for %q: %w", proxySpec, alias, err)
		}
		jumpClient, err = ssh.Dial("tcp", config.ConnectAddr(proxySpec), jumpCfg)
		if err != nil {
			return nil, fmt.Errorf("dial proxy jump %q: %w", proxySpec, err)
		}
		jumpClients[proxySpec] = jumpClient
	}
	return dialViaProxy(alias, targetAddr, clientCfg, jumpClient)
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
	for line := range strings.Lines(string(out)) {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		env[key] = strings.TrimSpace(value)
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

// GetFS returns the file system of the host.
func (h *sshHost) GetFS() fs.FS {
	return h.fsys
}

// Execute executes the given command and returns the output.
func (h *sshHost) Execute(ctx context.Context, cmd string) (output string, err error) {
	var buf bytes.Buffer

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

	session.Stdout = &buf
	if err := session.Run(cmd); err != nil {
		return "", nil
	}

	return buf.String(), nil
}

func (h *sshHost) NewCommand() (CmdRunner, error) {
	session, err := h.client.NewSession()
	if err != nil {
		return nil, err
	}
	return sshCmd{
		session: session,
	}, nil
}

// Close closes the connection to the host.
//
// Note: when the host was dialed via ProxyJump by [NewSSHGroup], the underlying
// jump connection is shared with other hosts and not closed here; it is closed
// by [Group.Close].
func (h *sshHost) Close() error {
	return errors.Join(h.sftpClient.Close(), h.client.Close())
}

func (h *sshHost) SetVar(key string, val any) {
	h.vars[key] = val
}

func (h *sshHost) GetVar(key string) (val any, ok bool) {
	val, ok = h.vars[key]
	return
}

type sshCmd struct {
	session *ssh.Session
}

func (c sshCmd) Run(cmd string) (err error) {
	defer safeClose(c.session, &err, io.EOF)
	return c.session.Run(cmd)
}

func (c sshCmd) RunContext(ctx context.Context, cmd string) (err error) {
	if err = c.session.Start(cmd); err != nil {
		return err
	}

	errChan := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		if err == nil {
			err = <-errChan
		}
	}()

	go func() {
		<-ctx.Done()
		errChan <- c.session.Close()
	}()

	return c.session.Wait()
}

func (c sshCmd) Start(cmd string) error {
	return c.session.Start(cmd)
}

func (c sshCmd) Wait() (err error) {
	defer safeClose(c.session, &err, io.EOF)
	return c.session.Wait()
}

func (c sshCmd) StdinPipe() (io.WriteCloser, error) {
	return c.session.StdinPipe()
}

func (c sshCmd) StdoutPipe() (io.ReadCloser, error) {
	rdr, err := c.session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	return io.NopCloser(rdr), nil
}

func (c sshCmd) StderrPipe() (io.ReadCloser, error) {
	rdr, err := c.session.StderrPipe()
	if err != nil {
		return nil, err
	}
	return io.NopCloser(rdr), nil
}
