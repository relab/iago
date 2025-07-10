package iagotest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relab/iago"
)

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
		containerInfo := createContainerWithInfo(t, cli, network, string(keyFiles.publicKeyData), hostAlias)
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
