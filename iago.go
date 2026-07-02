// Package iago provides a framework for running tasks on remote hosts.
package iago

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	fs "github.com/relab/wrfs"
)

// DefaultTimeout is the default timeout for an action.
var DefaultTimeout = 30 * time.Second

// Host is a connection to a remote host.
type Host interface {
	// Name returns the name of this host.
	Name() string

	// Address returns the address of the host.
	Address() string

	// GetEnv retrieves the value of the environment variable named by the key.
	// It returns the value, which will be empty if the variable is not present.
	GetEnv(key string) string

	// GetFS returns the file system of the host.
	GetFS() fs.FS

	// NewCommand returns a new command runner.
	NewCommand() (CmdRunner, error)

	// Close closes the connection to the host.
	Close() error

	// SetVar sets a host variable with the given key and value
	SetVar(key string, val any)

	// GetVar gets the host variable with the given key.
	// Returns (val, true) if the variable exists, (nil, false) otherwise.
	GetVar(key string) (val any, ok bool)
}

// Expand expands any environment variables in the string 's' using the environment of the host 'h'.
func Expand(h Host, s string) string {
	return os.Expand(s, h.GetEnv)
}

// GetStringVar gets a string variable from the host.
func GetStringVar(host Host, key string) string {
	val, ok := host.GetVar(key)
	if ok {
		s, _ := val.(string)
		return s
	}
	return ""
}

// GetIntVar gets an integer variable from the host.
func GetIntVar(host Host, key string) int {
	val, ok := host.GetVar(key)
	if ok {
		i, _ := val.(int)
		return i
	}
	return 0
}

// GroupOption configures how [NewSSHGroup] dials hosts.
type GroupOption func(*groupConfig)

type groupConfig struct {
	failFast          bool
	dialConcurrency   int
	forwardAgent      bool
	keepAliveInterval time.Duration
	errorHandler      ErrorHandler
}

func applyGroupOptions(opts ...GroupOption) groupConfig {
	var cfg groupConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// FailFast returns a [GroupOption] that makes [NewSSHGroup] stop and return
// an error if any dial fails. Targets that have not yet been dialed when the
// first failure is observed are skipped; combined with [DialConcurrency] this
// is best-effort, as dials already in flight still run to completion. Without
// this option, [NewSSHGroup] collects all dial errors in [Group.DialErrors]
// and returns only the successfully connected hosts.
func FailFast() GroupOption {
	return func(cfg *groupConfig) {
		cfg.failFast = true
	}
}

// DialConcurrency returns a [GroupOption] that sets the maximum number of
// target hosts dialed concurrently inside [NewSSHGroup]. Values less than 2
// leave dialing sequential (the default). Jump connections are always
// established sequentially before concurrent target dialing begins, so
// there is at most one TCP connection to each jump host regardless of n.
func DialConcurrency(n int) GroupOption {
	return func(cfg *groupConfig) {
		cfg.dialConcurrency = n
	}
}

// KeepAlive returns a [GroupOption] that sends an SSH keepalive request on every
// dialed connection every interval. golang.org/x/crypto/ssh does not honor the
// OpenSSH ServerAliveInterval config directive, so without this a connection
// left idle (for example, a control channel streaming a long, quiet remote run)
// can be silently dropped by a NAT or firewall idle timeout. The periodic
// traffic keeps the mapping alive; if a keepalive fails, the connection is
// closed so any in-flight session unblocks promptly instead of hanging. An
// interval of zero or less disables keepalives (the default).
func KeepAlive(interval time.Duration) GroupOption {
	return func(cfg *groupConfig) {
		cfg.keepAliveInterval = interval
	}
}

// ForwardAgent returns a [GroupOption] that forces SSH agent forwarding for
// every host in the group, regardless of the ForwardAgent setting in the SSH
// config. This is equivalent to passing -A to the ssh command-line tool:
// each session opened on a dialed host will request agent forwarding so that
// the remote process can authenticate onward to other hosts using the
// local agent.
func ForwardAgent() GroupOption {
	return func(cfg *groupConfig) {
		cfg.forwardAgent = true
	}
}

// WithErrorHandler returns a [GroupOption] that sets the [ErrorHandler] of the
// group returned by [NewSSHGroup]. Without it the group defaults to [Panic],
// matching [NewGroup]. Pass a shared [Errors] collector's Handle method to
// gather task errors instead of panicking:
//
//	var errs iago.Errors
//	g, err := iago.NewSSHGroup(aliases, cfg, iago.WithErrorHandler(errs.Handle))
//	// ...
//	g.Run("task", task)
//	return errs.Err()
func WithErrorHandler(h ErrorHandler) GroupOption {
	return func(cfg *groupConfig) {
		cfg.errorHandler = h
	}
}

// Group is a group of hosts.
type Group struct {
	Hosts        []Host
	ErrorHandler ErrorHandler
	Timeout      time.Duration

	// DialErrors holds per-alias dial failures from [NewSSHGroup] when
	// [FailFast] is not set. A nil map means all aliases connected successfully.
	DialErrors map[string]error

	// sharedClosers are resources owned by the group rather than by any
	// individual host (for example, a single SSH connection to a ProxyJump
	// host shared by all targets that route through it). They are closed
	// after all hosts on [Group.Close].
	sharedClosers []io.Closer
}

// NewGroup returns a new Group consisting of the given hosts.
func NewGroup(hosts []Host) Group {
	return Group{
		Hosts:        hosts,
		ErrorHandler: Panic,
		Timeout:      DefaultTimeout,
	}
}

// Run runs the task on all hosts in the group concurrently.
func (g Group) Run(name string, f func(context.Context, Host) error) {
	ctx, cancel := context.WithTimeout(context.Background(), g.Timeout)
	defer cancel()

	errors := make(chan error)
	for _, h := range g.Hosts {
		go func(h Host) {
			errors <- wrapError(h.Name(), name, f(ctx, h))
		}(h)
	}

	for range g.Hosts {
		err := <-errors
		if err != nil {
			g.ErrorHandler(err)
		}
	}
}

// Collect runs fn concurrently on every host in g and returns each host's
// result keyed by host name, the value-returning counterpart to [Group.Run].
// A host whose fn returns a non-nil error contributes no entry to the map;
// that error is still delivered to g.ErrorHandler exactly as in Run, and the
// joined result is returned as Collect's second value (nil when every host
// succeeded). Writes into the returned map are synchronized, so callers need
// no mutex of their own.
//
// A legitimate "nothing to report" for one host — an empty result, not a
// failure — should be represented as a zero-value T with a nil error, so it
// still gets an entry; reserve the error return for genuine command
// failures and filter zero-value entries out afterward if desired.
func Collect[T any](g Group, name string, fn func(context.Context, Host) (T, error)) (map[string]T, error) {
	results := make(map[string]T, len(g.Hosts))
	var mu sync.Mutex
	var errs Errors
	g.ErrorHandler = errs.Handle
	g.Run(name, func(ctx context.Context, host Host) error {
		v, err := fn(ctx, host)
		if err != nil {
			return err
		}
		mu.Lock()
		results[host.Name()] = v
		mu.Unlock()
		return nil
	})
	return results, errs.Err()
}

// Close closes any connections to hosts and any group-owned shared resources
// (such as ProxyJump connections shared across hosts in this group).
// Hosts are closed concurrently; shared resources (e.g. ProxyJump clients)
// are closed sequentially after all hosts have been closed.
func (g Group) Close() error {
	errs := make([]error, len(g.Hosts))
	var wg sync.WaitGroup
	for i, h := range g.Hosts {
		wg.Go(func() {
			errs[i] = h.Close()
		})
	}
	wg.Wait()
	err := errors.Join(errs...)
	for _, c := range g.sharedClosers {
		err = errors.Join(err, c.Close())
	}
	return err
}

// ErrorHandler is a function that handles errors from actions.
type ErrorHandler func(error)

// Panic handles errors by panicking.
func Panic(e error) {
	log.Panicln(e)
}

// Ignore ignores errors.
func Ignore(e error) {
	log.Println(e, "(ignored)")
}

// Errors is an [ErrorHandler] that accumulates the errors passed to it for
// later retrieval instead of panicking like [Panic]. It is the collecting
// counterpart to [Panic] and [Ignore], for a caller that runs a task on every
// host and then acts on the joined result:
//
//	var errs iago.Errors
//	g.ErrorHandler = errs.Handle
//	g.Run("task", task)
//	return errs.Err()
//
// [Group.Run] invokes the handler sequentially, so within a single Run the
// collector is uncontended; the mutex makes it safe to share one Errors across
// concurrent Runs as well. The zero value is ready to use.
type Errors struct {
	mu   sync.Mutex
	errs []error
}

// Handle records err. It satisfies the [ErrorHandler] signature, so it can be
// assigned to [Group.ErrorHandler] or passed to [WithErrorHandler].
func (e *Errors) Handle(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, err)
}

// Err returns the recorded errors joined with [errors.Join], or nil when none
// were recorded.
func (e *Errors) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return errors.Join(e.errs...)
}

func safeClose(closer io.Closer, errPtr *error, ignoredErrs ...error) {
	err := closer.Close()
	if *errPtr != nil {
		return
	}
	for _, ignored := range ignoredErrs {
		if errors.Is(err, ignored) {
			return
		}
	}
	*errPtr = err
}

func wrapError(hostName string, taskName string, err error) error {
	if err == nil {
		return nil
	}
	return TaskError{
		TaskName: taskName,
		HostName: hostName,
		Err:      err,
	}
}

// TaskError is the error type returned when an error occurs while running a task.
type TaskError struct {
	TaskName string
	HostName string
	Err      error
}

func (err TaskError) Error() string {
	return fmt.Sprintf("(%s) %s: %s", err.HostName, err.TaskName, err.Err.Error())
}

// Unwrap returns the cause of the task error.
func (err TaskError) Unwrap() error {
	return err.Err
}
