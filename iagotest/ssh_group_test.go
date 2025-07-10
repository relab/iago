package iagotest

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/relab/container"
	"github.com/relab/iago"
)

func TestNewSSHGroup(t *testing.T) {
	_, priv, pub := generateKey(t)

	tmpDir := t.TempDir()
	privKeyFile := filepath.Join(tmpDir, "id_ed25519")
	if err := os.WriteFile(privKeyFile, priv, 0o600); err != nil {
		t.Fatal(err)
	}
	pubKeyFile := filepath.Join(tmpDir, "id_ed25519.pub")
	if err := os.WriteFile(pubKeyFile, pub, 0o600); err != nil {
		t.Fatal(err)
	}

	cli := createClient(t)
	if err := cli.Ping(context.Background()); err != nil {
		t.Skip("could not connect to docker daemon")
	}
	buildImage(t, cli)

	network := createNetwork(t, cli)
	t.Logf("Created network %s", network)

	// Create multiple containers for the group test
	numContainers := 3
	containerIDs := make([]string, numContainers)
	hostAliases := make([]string, numContainers)
	configEntries := make([]string, numContainers)

	t.Cleanup(func() {
		timeout := 1 // seconds to wait before forcefully killing the container
		opts := container.StopOptions{Timeout: &timeout}
		for _, id := range containerIDs {
			if err := cli.ContainerStop(context.Background(), id, opts); err != nil {
				t.Errorf("Failed to stop container '%s': %v", id, err)
			}
			if err := cli.NetworkDisconnect(context.Background(), network, id, true); err != nil {
				t.Errorf("Failed to disconnect container %s from network '%s': %v", id, network, err)
			}
		}
		if err := cli.NetworkRemove(context.Background(), network); err != nil {
			t.Error(err)
		}
	})

	// Create containers and build SSH config entries
	for i := range numContainers {
		id, addr := createContainer(t, cli, network, string(pub))
		containerIDs[i] = id
		hostAlias := fmt.Sprintf("test-host-%d", i+1)
		hostAliases[i] = hostAlias

		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatal(err)
		}

		configEntry := sshConfigEntry(hostAlias, "127.0.0.1", "root", privKeyFile, port)
		configEntries[i] = configEntry

		t.Logf("Created container %s with ssh address %s for host alias %s", id, addr, hostAlias)
	}

	// Create SSH config file
	configPath := filepath.Join(tmpDir, "config")
	configContent := ""
	for _, entry := range configEntries {
		configContent += entry + "\n"
	}

	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Logf("Created SSH config file at %s with %d host entries", configPath, numContainers)

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
		expectedName := hostAliases[i]
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

	// Test group-wide operation using Run method
	group.Run("test hostname", func(ctx context.Context, host iago.Host) error {
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
		t.Logf("Hostname from host %s: %s", host.Name(), string(buf[:n]))
		return nil
	})
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

	_, err = iago.NewSSHGroup([]string{"test-host"}, invalidConfigPath)
	if err == nil {
		t.Error("Expected error for invalid config file, got nil")
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

	// Try to create group with unknown host
	_, err := iago.NewSSHGroup([]string{"unknown-host"}, configPath)
	if err == nil {
		t.Error("Expected error for unknown host, got nil")
	}
}
