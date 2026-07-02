// Package netemu provides Iago actions for per-flow bandwidth emulation using
// Linux tc (traffic control) and iptables. It shapes traffic between replicas
// identified by TCP port, supporting asymmetric bandwidth and latency via a
// declarative YAML configuration.
package netemu

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/relab/iago"
)

// SetupEmulation is an Iago action that installs tc HTB qdiscs, netem leaf
// disciplines, and iptables MANGLE rules on a host to enforce the per-flow
// bandwidth and latency declared in Config.
//
// It requires CAP_NET_ADMIN on the target host (run as root or with sudo).
// Use TeardownEmulation to remove the rules when the experiment ends.
type SetupEmulation struct {
	Config *Config
	// ShapeIngress installs a matching rule set on an IFB device so that
	// incoming traffic is also shaped. When false only egress is shaped.
	ShapeIngress bool
	// Sudo prepends "sudo" to every privileged command. Set to true when the
	// SSH user is not root but has passwordless sudo.
	Sudo bool
}

// Apply implements the iago action interface.
func (s SetupEmulation) Apply(ctx context.Context, host iago.Host) error {
	cmds := egressSetupCmds(s.Config)
	if s.ShapeIngress {
		if s.Config.IFB == "" {
			return fmt.Errorf("netemu: ShapeIngress is true but config.ifb is empty")
		}
		cmds = append(cmds, ingressSetupCmds(s.Config)...)
	}
	if err := runScript(ctx, host, s.Sudo, true, cmds); err != nil {
		return fmt.Errorf("netemu setup on %s: %w", host.Name(), err)
	}
	return nil
}

// TeardownEmulation is an Iago action that removes the tc qdiscs and iptables
// rules installed by SetupEmulation.
//
// Teardown commands that fail (e.g. because rules were never installed) are
// silently ignored so that TeardownEmulation is safe to call as a deferred
// cleanup regardless of whether SetupEmulation succeeded.
type TeardownEmulation struct {
	Config       *Config
	ShapeIngress bool
	Sudo         bool
}

// Apply implements the iago action interface.
func (t TeardownEmulation) Apply(ctx context.Context, host iago.Host) error {
	cmds := egressTeardownCmds(t.Config)
	if t.ShapeIngress {
		cmds = append(cmds, ingressTeardownCmds(t.Config)...)
	}
	// Intentionally ignore errors: del/flush on non-existent rules exits non-zero.
	runScript(ctx, host, t.Sudo, false, cmds) //nolint:errcheck
	return nil
}

// runScript pipes cmds to a single remote sh session, reducing SSH round-trips
// from O(n²) to 1 for n replicas. When failFast is true the shell runs with
// set -e so any failing command aborts the script.
func runScript(ctx context.Context, host iago.Host, sudo, failFast bool, cmds []string) error {
	runner, err := host.NewCommand()
	if err != nil {
		return err
	}
	stderrPipe, err := runner.StderrPipe()
	if err != nil {
		return err
	}
	stdin, err := runner.StdinPipe()
	if err != nil {
		return err
	}
	shell := "sh"
	if failFast {
		shell = "sh -e"
	}
	if sudo {
		shell = "sudo " + shell
	}
	if err := runner.Start(shell); err != nil {
		return err
	}
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		io.Copy(&stderrBuf, stderrPipe) //nolint:errcheck
		close(stderrDone)
	}()
	for _, cmd := range cmds {
		if _, err := fmt.Fprintln(stdin, cmd); err != nil {
			stdin.Close()
			<-stderrDone
			return err
		}
	}
	if err := stdin.Close(); err != nil {
		<-stderrDone
		return err
	}
	<-stderrDone
	if err := runner.Wait(); err != nil {
		if stderrBuf.Len() > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderrBuf.String()))
		}
		return err
	}
	return nil
}
