package iagotest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/relab/container"
	"github.com/relab/iago"
	"golang.org/x/crypto/ssh"
)

func TestClientConfigActuallyConnecting(t *testing.T) {
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
		t.Fatal(err)
	}
	buildImage(t, cli)

	network := createNetwork(t, cli)
	t.Logf("Created network %s", network)

	id, addr := createContainer(t, cli, network, string(pub))
	t.Logf("Created container %s with ssh address %s", id, addr)

	t.Cleanup(func() {
		timeout := 1 // seconds to wait before forcefully killing the container
		opts := container.StopOptions{Timeout: &timeout}
		if err := cli.ContainerStop(context.Background(), id, opts); err != nil {
			t.Errorf("Failed to stop container '%s': %v", id, err)
		}
		if err := cli.NetworkDisconnect(context.Background(), network, id, true); err != nil {
			t.Errorf("Failed to disconnect container %s from network '%s': %v", id, network, err)
		}
		if err := cli.NetworkRemove(context.Background(), network); err != nil {
			t.Error(err)
		}
	})

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	config := sshConfigEntry("yummy", "127.0.0.1", "root", privKeyFile, port)

	configPath := filepath.Join(tmpDir, "config")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	sshConfig, hostPort, err := iago.ClientConfig("yummy", configPath)
	if err != nil {
		t.Fatal(err)
	}
	// skip host key checking for this test
	sshConfig.HostKeyCallback = ssh.InsecureIgnoreHostKey()

	_, err = ssh.Dial("tcp", hostPort, sshConfig)
	if err != nil {
		t.Fatal(err)
	}
}

func sshConfigEntry(hostAlias, hostname, user, identityFile, port string) string {
	return fmt.Sprintf(`Host %s
	Hostname %s
	User %s
	IdentityFile %s
	Port %s
`, hostAlias, hostname, user, identityFile, port)
}
