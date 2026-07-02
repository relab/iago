package iago

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/relab/iago/sftpfs"
	fs "github.com/relab/wrfs"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type sshHost struct {
	name          string
	env           map[string]string
	client        *ssh.Client
	sftpClient    *sftp.Client
	fsys          fs.FS
	vars          map[string]any
	forwardAgent  bool
	agentConn     net.Conn  // non-nil when agent forwarding is active; closed by Close
	stopKeepAlive func()    // non-nil when keepalives are running; stops them on Close
	agentFwdOnce  sync.Once // sends auth-agent-req on the first session only (see requestAgentForwarding)
}

// DialSSH connects to a remote host using ssh.
func DialSSH(name, addr string, cfg *ssh.ClientConfig) (Host, error) {
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return newHostFromClient(name, client, false, 0)
}

// dialViaProxy connects to a remote host by tunnelling through an already-established
// SSH connection to a jump host. The jump client is not owned by the returned Host;
// its lifetime is managed by the caller (typically [NewSSHGroup], which shares one
// jump client across all targets that route through the same ProxyJump spec).
func dialViaProxy(name, addr string, cfg *ssh.ClientConfig, jump *ssh.Client, forwardAgent bool, keepAlive time.Duration) (Host, error) {
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
	return newHostFromClient(name, ssh.NewClient(ncc, chans, reqs), forwardAgent, keepAlive)
}

// newHostFromClient wraps an established *ssh.Client as a Host, creating the SFTP
// sub-client and fetching the remote environment. When forwardAgent is true the
// local SSH agent is connected and registered with the client so that sessions
// opened via [sshHost.NewCommand] and [sshHost.Execute] can request forwarding.
// When keepAlive is positive, a background goroutine sends periodic SSH
// keepalives on the connection; it is stopped by [sshHost.Close].
func newHostFromClient(name string, client *ssh.Client, forwardAgent bool, keepAlive time.Duration) (Host, error) {
	var agentConn net.Conn
	if forwardAgent {
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, fmt.Errorf("iago: ForwardAgent requested for %s but SSH_AUTH_SOCK is not set", name)
		}
		var err error
		agentConn, err = net.Dial("unix", sock)
		if err != nil {
			return nil, fmt.Errorf("iago: ForwardAgent requested for %s but could not connect to SSH agent: %w", name, err)
		}
		if err := agent.ForwardToAgent(client, agent.NewClient(agentConn)); err != nil {
			_ = agentConn.Close()
			return nil, fmt.Errorf("iago: failed to set up agent forwarding for %s: %w", name, err)
		}
	}
	sftpClient, err := sftp.NewClient(client,
		sftp.UseConcurrentWrites(true),
		sftp.UseConcurrentReads(true),
	)
	if err != nil {
		if agentConn != nil {
			_ = agentConn.Close()
		}
		return nil, err
	}
	env, err := fetchEnv(client)
	if err != nil {
		if agentConn != nil {
			_ = agentConn.Close()
		}
		return nil, err
	}
	host := &sshHost{
		name:         name,
		env:          env,
		client:       client,
		sftpClient:   sftpClient,
		fsys:         sftpfs.New(sftpClient, "/"),
		vars:         make(map[string]any),
		forwardAgent: forwardAgent,
		agentConn:    agentConn,
	}
	if keepAlive > 0 {
		// On a dead connection the keepalive fails; close the client so any
		// session blocked on a read returns instead of hanging indefinitely.
		host.stopKeepAlive = startKeepAlive(client, keepAlive, func() { _ = client.Close() })
	}
	return host, nil
}

// keepAliveRequest is the OpenSSH-compatible global request name used to probe a
// connection's liveness; the server replies but takes no other action.
const keepAliveRequest = "keepalive@openssh.com"

// keepAlivePinger is the subset of [ssh.Client] the keepalive loop needs, so the
// loop can be exercised in tests without a live connection.
type keepAlivePinger interface {
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
}

// startKeepAlive launches a goroutine that pings client every interval and
// returns a function that stops it. When a ping fails, onDead is invoked once
// (typically to close the connection so blocked sessions return) and the loop
// exits. The returned stop function is idempotent and safe to call from Close.
func startKeepAlive(client keepAlivePinger, interval time.Duration, onDead func()) func() {
	done := make(chan struct{})
	ticker := time.NewTicker(interval)
	go keepAliveLoop(client, ticker.C, onDead, done)
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			ticker.Stop()
		})
	}
}

// keepAliveLoop pings client on every tick until either a ping fails (then it
// calls onDead, if set, and returns) or done is closed (then it returns
// silently). It is split from [startKeepAlive] so tests can drive the tick
// channel deterministically.
func keepAliveLoop(client keepAlivePinger, tick <-chan time.Time, onDead func(), done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-tick:
			if _, _, err := client.SendRequest(keepAliveRequest, true, nil); err != nil {
				if onDead != nil {
					onDead()
				}
				return
			}
		}
	}
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
// The specified hosts must all contain an authorized_keys file containing the
// public key of the user running this program.
//
// When several aliases share the same ProxyJump spec, a single TCP/SSH connection
// to the jump host is dialed once and reused for every target tunnelled through it.
// This mirrors what OpenSSH's ControlMaster provides for the system ssh client and
// avoids opening one proxy connection per target alias. The shared jump clients are
// owned by the returned [Group] and closed by [Group.Close].
//
// By default, dial failures are collected in [Group.DialErrors] instead of
// aborting the call. If no hosts connect successfully, an error is returned.
// Pass [FailFast] to return an error if any target fails. Pass [DialConcurrency]
// to dial target hosts concurrently; jump connections are always established
// sequentially first so at most one TCP connection is made to each jump host.
func NewSSHGroup(hostAliases []string, sshConfigFile string, opts ...GroupOption) (group Group, err error) {
	cfg := applyGroupOptions(opts...)
	sshConfigFile, err = resolveSSHConfigFile(sshConfigFile)
	if err != nil {
		return group, err
	}
	config, err := ParseSSHConfig(sshConfigFile)
	if err != nil {
		return group, err
	}

	dialer := newGroupDialer(config, hostAliases, cfg)
	defer dialer.closeOnError(&err)

	if err = dialer.prepareJumps(cfg); err != nil {
		return group, err
	}
	if err = dialer.dialAll(cfg); err != nil {
		return group, err
	}
	return dialer.group(), nil
}

// groupDialer assembles a [Group] by dialing a set of host aliases under a single
// SSH config, sharing one connection per distinct ProxyJump spec.
//
// Its lifecycle has two phases separated by a deliberate concurrency boundary:
//
//  1. prepareJumps establishes the shared jump connections. This is the only
//     phase that writes jumpClients and jumpErrs.
//  2. dialAll dials the target hosts, optionally in parallel. Its workers only
//     read the jump maps (through jumpFor), so the read-only boundary holds by
//     construction and no locking is required.
//
// The dialer owns every connection it opens until [groupDialer.group] transfers
// ownership to the returned Group; on any earlier error, [groupDialer.closeOnError]
// closes them all.
type groupDialer struct {
	config      *sshConfig
	aliases     []string
	cfg         groupConfig
	jumpClients map[string]*ssh.Client // proxy spec -> shared jump connection
	jumpErrs    map[string]error       // proxy spec -> error establishing it
	hosts       []Host                 // successfully dialed targets
	dialErrs    map[string]error       // alias -> dial error; nil until first failure
}

func newGroupDialer(config *sshConfig, aliases []string, cfg groupConfig) *groupDialer {
	return &groupDialer{
		config:      config,
		aliases:     aliases,
		cfg:         cfg,
		jumpClients: make(map[string]*ssh.Client),
		jumpErrs:    make(map[string]error),
	}
}

// prepareJumps establishes one shared connection per distinct ProxyJump spec
// referenced by the aliases. With [FailFast] set, the first failure is returned
// immediately; otherwise failures are recorded per spec and later surfaced as
// the dial error of every alias routing through that jump (see [groupDialer.jumpFor]).
func (d *groupDialer) prepareJumps(cfg groupConfig) error {
	for _, alias := range d.aliases {
		proxySpec, err := d.config.get(alias, "ProxyJump")
		if err != nil || proxySpec == "" || proxySpec == "none" {
			continue
		}
		if _, ok := d.jumpClients[proxySpec]; ok {
			continue
		}
		if _, ok := d.jumpErrs[proxySpec]; ok {
			continue
		}
		client, err := dialJump(proxySpec, d.config)
		if err != nil {
			if cfg.failFast {
				return err
			}
			d.jumpErrs[proxySpec] = err
			continue
		}
		d.jumpClients[proxySpec] = client
	}
	return nil
}

// jumpFor resolves the shared jump connection for alias. It returns (nil, nil)
// when alias connects directly, (client, nil) when it routes through an
// established jump, or (nil, err) when its jump could not be established. It only
// reads state populated by prepareJumps, so it is safe for concurrent callers.
func (d *groupDialer) jumpFor(alias string) (*ssh.Client, error) {
	proxySpec, err := d.config.get(alias, "ProxyJump")
	if err != nil {
		return nil, err
	}
	if proxySpec == "" || proxySpec == "none" {
		return nil, nil
	}
	if err := d.jumpErrs[proxySpec]; err != nil {
		return nil, err
	}
	client := d.jumpClients[proxySpec]
	if client == nil {
		// prepareJumps establishes every referenced spec, so a missing client
		// signals a programming error rather than a connection failure.
		return nil, fmt.Errorf("iago: proxy jump %q was not prepared", proxySpec)
	}
	return client, nil
}

// dialAll dials every alias, reusing shared jump connections, records the
// outcomes in hosts and dialErrs, and reports the first fatal error: with
// [FailFast] the earliest failing alias's dial error, otherwise a combined
// error only when no alias connected at all. Up to cfg.dialConcurrency aliases
// are dialed in parallel; a value below 2 dials sequentially.
//
// With [FailFast] set, the first failed dial stops further targets from being
// queued: sequentially this means no later alias is contacted at all, while
// concurrently it is best-effort because dials already in flight still run to
// completion (a direct [DialSSH] cannot be cancelled mid-handshake).
func (d *groupDialer) dialAll(cfg groupConfig) error {
	if len(d.aliases) == 0 {
		return nil
	}
	results := make([]sshDialResult, len(d.aliases))
	jobs := make(chan int)
	abort := make(chan struct{})
	var abortOnce sync.Once
	var wg sync.WaitGroup
	for range dialConcurrency(cfg.dialConcurrency, len(d.aliases)) {
		wg.Go(func() {
			for i := range jobs {
				results[i] = d.dialOne(d.aliases[i])
				if cfg.failFast && results[i].err != nil {
					// Signal the scheduler to stop queuing further targets and
					// stop this worker from picking up any already-queued one.
					abortOnce.Do(func() { close(abort) })
					return
				}
			}
		})
	}
schedule:
	for i := range d.aliases {
		select {
		case <-abort:
			break schedule
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	d.collect(results)

	if cfg.failFast {
		if err := d.firstDialError(); err != nil {
			return err
		}
	}
	return d.allFailedError()
}

// dialOne dials a single alias through its shared jump connection, if any.
func (d *groupDialer) dialOne(alias string) sshDialResult {
	jump, err := d.jumpFor(alias)
	if err != nil {
		return sshDialResult{err: err}
	}
	host, err := dialTarget(alias, d.config, jump, d.cfg.forwardAgent, d.cfg.keepAliveInterval)
	return sshDialResult{host: host, err: err}
}

// collect records per-alias results in aliases order, lazily allocating dialErrs
// so it stays nil when every alias connects.
func (d *groupDialer) collect(results []sshDialResult) {
	d.hosts = make([]Host, 0, len(results))
	for i, alias := range d.aliases {
		if results[i].host != nil {
			d.hosts = append(d.hosts, results[i].host)
		}
		if results[i].err != nil {
			if d.dialErrs == nil {
				d.dialErrs = make(map[string]error)
			}
			d.dialErrs[alias] = results[i].err
		}
	}
}

// firstDialError returns the dial error of the earliest alias that failed, or nil
// if all connected. It is used to honor [FailFast].
func (d *groupDialer) firstDialError() error {
	for _, alias := range d.aliases {
		if err := d.dialErrs[alias]; err != nil {
			return err
		}
	}
	return nil
}

// allFailedError returns a combined error when no alias connected, or nil if at
// least one did (or none were requested).
func (d *groupDialer) allFailedError() error {
	if len(d.hosts) > 0 || len(d.dialErrs) == 0 {
		return nil
	}
	errs := make([]error, 0, len(d.dialErrs))
	for _, alias := range d.aliases {
		if err := d.dialErrs[alias]; err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", alias, err))
		}
	}
	return fmt.Errorf("iago: failed to dial any hosts: %w", errors.Join(errs...))
}

// group transfers ownership of the dialed hosts and shared jump connections to a
// new [Group]. After this call the dialer no longer owns those connections, so it
// must only run on the success path where closeOnError is a no-op.
func (d *groupDialer) group() Group {
	group := NewGroup(d.hosts)
	group.DialErrors = d.dialErrs
	for _, jc := range d.jumpClients {
		group.sharedClosers = append(group.sharedClosers, jc)
	}
	return group
}

// closeOnError closes every connection the dialer opened when *errPtr is non-nil.
// It is a no-op on success, where ownership has passed to the returned Group.
func (d *groupDialer) closeOnError(errPtr *error) {
	if *errPtr == nil {
		return
	}
	for _, h := range d.hosts {
		_ = h.Close()
	}
	for _, jc := range d.jumpClients {
		_ = jc.Close()
	}
}

type sshDialResult struct {
	host Host
	err  error
}

func dialConcurrency(concurrency, aliases int) int {
	if concurrency < 2 {
		return 1
	}
	return min(concurrency, aliases)
}

// dialJump establishes a new SSH connection to the given ProxyJump spec. The
// returned client is a bare [ssh.Client] used only as a tunnel; unlike a target
// host it has no SFTP sub-client or fetched environment.
func dialJump(proxySpec string, config *sshConfig) (*ssh.Client, error) {
	jumpCfg, err := config.ClientConfig(proxySpec)
	if err != nil {
		return nil, fmt.Errorf("proxy jump %q: %w", proxySpec, err)
	}
	client, err := ssh.Dial("tcp", config.ConnectAddr(proxySpec), jumpCfg)
	if err != nil {
		return nil, fmt.Errorf("dial proxy jump %q: %w", proxySpec, err)
	}
	return client, nil
}

// dialTarget dials a single target alias. When jump is non-nil the connection is
// tunnelled through that shared jump client; otherwise it is dialed directly. The
// jump client's lifetime is managed by the caller, not by the returned Host.
// forceForwardAgent forces agent forwarding regardless of the SSH config value;
// it is set when [ForwardAgent] was passed as a [GroupOption] to [NewSSHGroup].
// keepAlive, when positive, starts periodic SSH keepalives on the connection.
func dialTarget(alias string, config *sshConfig, jump *ssh.Client, forceForwardAgent bool, keepAlive time.Duration) (Host, error) {
	clientCfg, err := config.ClientConfig(alias)
	if err != nil {
		return nil, err
	}
	configForwardAgent, err := config.forwardAgent(alias)
	if err != nil {
		return nil, err
	}
	forwardAgent := forceForwardAgent || configForwardAgent
	addr := config.ConnectAddr(alias)
	if jump == nil {
		client, err := ssh.Dial("tcp", addr, clientCfg)
		if err != nil {
			return nil, err
		}
		return newHostFromClient(alias, client, forwardAgent, keepAlive)
	}
	return dialViaProxy(alias, addr, clientCfg, jump, forwardAgent, keepAlive)
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

	h.requestAgentForwarding(session)

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
	err = session.Run(cmd)
	return buf.String(), err
}

func (h *sshHost) NewCommand() (CmdRunner, error) {
	session, err := h.client.NewSession()
	if err != nil {
		return nil, err
	}
	h.requestAgentForwarding(session)
	return sshCmd{
		session: session,
	}, nil
}

// requestAgentForwarding asks the server to forward the local SSH agent when
// this host was dialed with agent forwarding enabled. The request is sent on
// the first session only: OpenSSH's sshd honors auth-agent-req at most once
// per connection and the grant is connection-wide — the agent socket persists
// until the connection closes and is injected as SSH_AUTH_SOCK into every
// later session. Requesting on every session therefore yields a spurious
// "forwarding request denied" on each session after the first, so later
// sessions skip the request and ride on the connection-wide grant. A denial
// of the one real request is non-fatal (matching OpenSSH's -A behavior) and
// is logged; a session that genuinely needs the forwarded agent surfaces its
// own failure downstream.
//
// Note: this relies on OpenSSH's connection-wide forwarding model; a server
// that scopes forwarding strictly per session would forward only to the first
// session. OpenSSH is the deployment target.
func (h *sshHost) requestAgentForwarding(session *ssh.Session) {
	if !h.forwardAgent {
		return
	}
	h.agentFwdOnce.Do(func() {
		if err := agent.RequestAgentForwarding(session); err != nil {
			log.Printf("iago: agent forwarding denied for %s: %v", h.name, err)
		}
	})
}

// Close closes the connection to the host.
//
// Note: when the host was dialed via ProxyJump by [NewSSHGroup], the underlying
// jump connection is shared with other hosts and not closed here; it is closed
// by [Group.Close].
func (h *sshHost) Close() error {
	if h.stopKeepAlive != nil {
		h.stopKeepAlive()
	}
	var agentErr error
	if h.agentConn != nil {
		agentErr = h.agentConn.Close()
	}
	return errors.Join(h.sftpClient.Close(), h.client.Close(), agentErr)
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
