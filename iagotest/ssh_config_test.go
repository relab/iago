package iagotest

import (
	"path/filepath"
	"testing"

	"github.com/relab/iago"
	"golang.org/x/crypto/ssh"
)

func TestClientConfigActuallyConnecting(t *testing.T) {
	tmpDir := t.TempDir()
	keyFiles := setupSSHKeys(t, tmpDir)

	cli, network := setupContainerEnvironment(t, true)
	// cleanup the network (will be called last due to LIFO)
	t.Cleanup(cleanupNetwork(t, cli, network))

	containerInfo := createContainerWithInfo(t, cli, network, string(keyFiles.publicKeyData), "yummy")
	t.Cleanup(cleanupContainer(t, cli, network, containerInfo.id))

	configEntry := sshConfigEntry("yummy", "127.0.0.1", "root", keyFiles.privateKeyPath, containerInfo.port)

	configPath := filepath.Join(tmpDir, "config")
	createSSHConfigFile(t, configPath, []string{configEntry})

	sshConfig, err := iago.ParseSSHConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	sshClientConfig, err := sshConfig.ClientConfig("yummy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ssh.Dial("tcp", sshConfig.ConnectAddr("yummy"), sshClientConfig)
	if err != nil {
		t.Fatal(err)
	}
}
