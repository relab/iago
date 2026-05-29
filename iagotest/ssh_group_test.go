package iagotest

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/relab/iago"
)

// countingProxy is a transparent TCP proxy that counts how many times a new
// connection is accepted. It is used in tests to verify that a single TCP
// connection to the jump host is reused across all targets rather than one
// connection being dialed per target.
type countingProxy struct {
	accepted atomic.Int64
	addr     string
	ln       net.Listener
}

func newCountingProxy(t *testing.T, target string) *countingProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &countingProxy{addr: ln.Addr().String(), ln: ln}
	var wg sync.WaitGroup
	wg.Go(func() {
		p.serve(target)
	})
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})
	return p
}

func (p *countingProxy) serve(target string) {
	for {
		src, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.accepted.Add(1)
		dst, err := net.Dial("tcp", target)
		if err != nil {
			src.Close()
			continue
		}
		go func() {
			defer src.Close()
			defer dst.Close()
			var wg sync.WaitGroup
			wg.Go(func() { io.Copy(dst, src) })
			wg.Go(func() { io.Copy(src, dst) })
			wg.Wait()
		}()
	}
}

func unusedLocalTCPPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if closeErr := ln.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestNewSSHGroup(t *testing.T) {
	tmpDir := t.TempDir()
	keyFiles := setupSSHKeys(t, tmpDir)

	cli, network := setupContainerEnvironment(t, true)
	// cleanup the network (will be called last due to LIFO)
	t.Cleanup(cleanupNetwork(t, cli, network))

	// Create multiple containers for the group test
	numContainers := 3
	containerInfos := make([]containerInfo, numContainers)

	// Create containers and set up individual cleanup for each
	for i := range numContainers {
		hostAlias := fmt.Sprintf("test-host-%d", i+1)
		containerInfo := createContainerWithInfo(t, cli, network, hostAlias, keyFiles.signer)
		containerInfos[i] = containerInfo
		t.Cleanup(cleanupContainer(t, cli, network, containerInfo.id))
	}

	// Build SSH config entries from containerInfos
	configEntries := make([]string, numContainers)
	for i, containerInfo := range containerInfos {
		configEntries[i] = sshConfigEntry(containerInfo.hostAlias, "127.0.0.1", "root", keyFiles.privateKeyPath, containerInfo.port)
	}

	// Create SSH config file
	configPath := filepath.Join(tmpDir, "config")
	createSSHConfigFile(t, configPath, configEntries)

	t.Logf("Created SSH config file at %s with %d host entries", configPath, numContainers)

	// Extract host aliases from containerInfos for NewSSHGroup
	hostAliases := make([]string, len(containerInfos))
	for i, containerInfo := range containerInfos {
		hostAliases[i] = containerInfo.hostAlias
	}

	// Test NewSSHGroup
	group, err := iago.NewSSHGroup(hostAliases, configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()

	// Verify the group was created successfully
	hosts := group.Hosts
	if len(hosts) != numContainers {
		t.Fatalf("Expected %d hosts in group, got %d", numContainers, len(hosts))
	}

	// Test each host in the group
	for i, host := range hosts {
		expectedName := containerInfos[i].hostAlias
		if host.Name() != expectedName {
			t.Errorf("Expected host name %s, got %s", expectedName, host.Name())
		}

		// Test basic connectivity by executing a simple command using the NewCommand interface
		cmd, err := host.NewCommand()
		if err != nil {
			t.Errorf("Failed to create command on host %s: %v", host.Name(), err)
			continue
		}

		err = cmd.Run("echo 'hello from host'")
		if err != nil {
			t.Errorf("Failed to execute command on host %s: %v", host.Name(), err)
		}

		t.Logf("Successfully tested host %s with address %s", host.Name(), host.Address())
	}

	group.Run("Echo SSH connection variable", func(ctx context.Context, host iago.Host) (err error) {
		var sb strings.Builder
		err = iago.Shell{
			Command: "echo SSH Connection details: $SSH_CONNECTION",
			Stdout:  &sb,
		}.Apply(ctx, host)
		if err != nil {
			return err
		}
		t.Log(strings.TrimSpace(sb.String()))
		return nil
	})

	// Test group-wide operation using Run method
	group.Run("Run hostname command", func(ctx context.Context, host iago.Host) error {
		cmd, err := host.NewCommand()
		if err != nil {
			return err
		}
		r, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		defer r.Close()
		err = cmd.Run("hostname")
		if err != nil {
			return fmt.Errorf("failed to run command on host %s: %w", host.Name(), err)
		}
		// read from r to verify output
		buf := make([]byte, 1024)
		n, err := r.Read(buf)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read from stdout pipe: %w", err)
		}
		t.Logf("Hostname on %s: %s", host.Name(), string(buf[:n]))
		return nil
	})
}

// TestNewSSHGroupProxyJump verifies that NewSSHGroup can dial multiple target
// hosts via a shared ProxyJump connection. The jump container is reached on
// its host-mapped port; targets are reached through the jump on the internal
// docker network using their container alias on port 22.
//
// NewSSHGroup reuses one *ssh.Client per unique ProxyJump spec, so a single
// TCP connection to the jump host serves all N targets that share it.
func TestNewSSHGroupProxyJump(t *testing.T) {
	tmpDir := t.TempDir()
	keyFiles := setupSSHKeys(t, tmpDir)

	cli, network := setupContainerEnvironment(t, true)
	t.Cleanup(cleanupNetwork(t, cli, network))

	// Jump host: reached from this test on a host-mapped ephemeral port.
	jump := createContainerWithInfo(t, cli, network, "jump", keyFiles.signer)
	t.Cleanup(cleanupContainer(t, cli, network, jump.id))

	// Targets: reached from inside the jump container on the docker network,
	// addressed by the container's network alias on the internal port 22.
	numTargets := 3
	targets := make([]containerInfo, numTargets)
	for i := range numTargets {
		alias := fmt.Sprintf("target-%d", i+1)
		targets[i] = createContainerWithInfo(t, cli, network, alias, keyFiles.signer)
		t.Cleanup(cleanupContainer(t, cli, network, targets[i].id))
	}

	// Route the jump host through a counting proxy so we can assert that
	// NewSSHGroup dials the jump exactly once, regardless of how many targets
	// share the same ProxyJump spec.
	proxy := newCountingProxy(t, jump.address)
	_, proxyPort, err := net.SplitHostPort(proxy.addr)
	if err != nil {
		t.Fatal(err)
	}

	configEntries := []string{sshConfigEntry(jump.hostAlias, "127.0.0.1", "root", keyFiles.privateKeyPath, proxyPort)}
	for _, tgt := range targets {
		// Hostname must be the Docker-assigned container name, which is the
		// DNS alias registered on the Docker network. The host-mapped ephemeral
		// port is only reachable from outside Docker; the jump container reaches
		// the target on the internal port 22.
		configEntries = append(configEntries, fmt.Sprintf(`Host %s
	Hostname %s
	User root
	IdentityFile %s
	Port 22
	ProxyJump %s
	StrictHostKeyChecking no
	UserKnownHostsFile /dev/null
`, tgt.hostAlias, tgt.id, keyFiles.privateKeyPath, jump.hostAlias))
	}

	configPath := filepath.Join(tmpDir, "config")
	createSSHConfigFile(t, configPath, configEntries)

	hostAliases := make([]string, numTargets)
	for i, tgt := range targets {
		hostAliases[i] = tgt.hostAlias
	}

	group, err := iago.NewSSHGroup(hostAliases, configPath, iago.DialConcurrency(numTargets))
	if err != nil {
		t.Fatalf("NewSSHGroup: %v", err)
	}
	t.Cleanup(func() {
		if err := group.Close(); err != nil {
			t.Errorf("group.Close: %v", err)
		}
	})

	if len(group.Hosts) != numTargets {
		t.Fatalf("group has %d hosts, want %d", len(group.Hosts), numTargets)
	}

	// Assert that the jump host was dialed exactly once. If sharing is broken
	// and each target opens its own jump connection, this will equal numTargets.
	if n := proxy.accepted.Load(); n != 1 {
		t.Errorf("jump host dialed %d times, want 1 (shared connection not reused)", n)
	}

	group.ErrorHandler = func(e error) { t.Error(e) }
	group.Run("hostname via proxy", func(ctx context.Context, host iago.Host) error {
		var sb strings.Builder
		if err := (iago.Shell{Command: "hostname", Stdout: &sb}).Apply(ctx, host); err != nil {
			return err
		}
		t.Logf("hostname on %s: %s", host.Name(), strings.TrimSpace(sb.String()))
		return nil
	})
}

func TestNewSSHGroupPartialDialErrors(t *testing.T) {
	tmpDir := t.TempDir()
	keyFiles := setupSSHKeys(t, tmpDir)

	cli, network := setupContainerEnvironment(t, true)
	t.Cleanup(cleanupNetwork(t, cli, network))

	reachable := createContainerWithInfo(t, cli, network, "reachable-host", keyFiles.signer)
	t.Cleanup(cleanupContainer(t, cli, network, reachable.id))

	configEntries := []string{
		sshConfigEntry(reachable.hostAlias, "127.0.0.1", "root", keyFiles.privateKeyPath, reachable.port),
		sshConfigEntry("unreachable-host", "127.0.0.1", "root", keyFiles.privateKeyPath, unusedLocalTCPPort(t)),
	}
	configPath := filepath.Join(tmpDir, "config")
	createSSHConfigFile(t, configPath, configEntries)

	group, err := iago.NewSSHGroup([]string{reachable.hostAlias, "unreachable-host"}, configPath)
	if err != nil {
		t.Fatalf("NewSSHGroup returned error despite one reachable host: %v", err)
	}
	defer group.Close()

	if len(group.Hosts) != 1 {
		t.Fatalf("group has %d hosts, want 1", len(group.Hosts))
	}
	if group.Hosts[0].Name() != reachable.hostAlias {
		t.Errorf("host name = %q, want %q", group.Hosts[0].Name(), reachable.hostAlias)
	}
	if group.DialErrors["unreachable-host"] == nil {
		t.Error("Expected dial error for unreachable-host in group.DialErrors, got nil")
	}
}

func TestNewSSHGroupInvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with non-existent config file
	nonExistentConfig := filepath.Join(tmpDir, "non-existent-config")
	_, err := iago.NewSSHGroup([]string{"test-host"}, nonExistentConfig)
	if err == nil {
		t.Error("Expected error for non-existent config file, got nil")
	}

	// Test with invalid config file
	invalidConfigPath := filepath.Join(tmpDir, "invalid-config")
	invalidConfig := "Invalid SSH Config Content\nNot a valid format"
	if err := os.WriteFile(invalidConfigPath, []byte(invalidConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	group, err := iago.NewSSHGroup([]string{"test-host"}, invalidConfigPath, iago.FailFast())
	if err == nil {
		group.Close()
		t.Error("Expected error for unreachable host with FailFast, got nil")
	}
}

func TestNewSSHGroupEmptyHostList(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty-config")

	// Create an empty but valid config file
	if err := os.WriteFile(configPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	group, err := iago.NewSSHGroup([]string{}, configPath)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()

	hosts := group.Hosts
	if len(hosts) != 0 {
		t.Errorf("Expected 0 hosts for empty host list, got %d", len(hosts))
	}
}

func TestNewSSHGroupUnknownHost(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	// Create a config with one host
	configContent := sshConfigEntry("known-host", "127.0.0.1", "root", "/tmp/key", "22")
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// By default, dial failures are stored in group.DialErrors when at least
	// one host connects. If every requested host fails, NewSSHGroup returns an
	// error instead of an empty group.
	_, err := iago.NewSSHGroup([]string{"unknown-host"}, configPath)
	if err == nil {
		t.Error("Expected error when all requested hosts fail to dial, got nil")
	}

	// With FailFast, the same failure is returned directly from NewSSHGroup.
	_, err = iago.NewSSHGroup([]string{"unknown-host"}, configPath, iago.FailFast())
	if err == nil {
		t.Error("Expected error from NewSSHGroup with FailFast for unknown host, got nil")
	}
}
