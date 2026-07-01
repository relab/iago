package iagotest

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relab/iago"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// startTestAgent serves an in-process SSH agent holding the test key on a
// unix socket and points SSH_AUTH_SOCK at it, so NewSSHGroup both
// authenticates with the key (agent signers) and forwards this agent to the
// dialed hosts when ForwardAgent is set.
func startTestAgent(t *testing.T, privateKeyPath string) {
	t.Helper()
	pemBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	rawKey, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: rawKey}); err != nil {
		t.Fatal(err)
	}
	// A dedicated short-path dir: unix socket paths are length-limited
	// (~104 bytes on macOS) and t.TempDir can exceed that under long test names.
	sockDir, err := os.MkdirTemp("", "iago-agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, conn) }()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
}

// TestForwardAgentConnectionWideGrant verifies iago's once-per-connection
// agent forwarding model against a real sshd: with ForwardAgent set, only the
// first session sends auth-agent-req (OpenSSH grants it at most once per
// connection), and a later session — which sends no request of its own — must
// still see the connection-wide SSH_AUTH_SOCK and reach the forwarded agent
// through it.
func TestForwardAgentConnectionWideGrant(t *testing.T) {
	tmpDir := t.TempDir()
	keyFiles := setupSSHKeys(t, tmpDir)
	startTestAgent(t, keyFiles.privateKeyPath)

	cli, network := setupContainerEnvironment(t, true)
	t.Cleanup(cleanupNetwork(t, cli, network))
	info := createContainerWithInfo(t, cli, network, "agent-fwd-host", keyFiles.signer)
	t.Cleanup(cleanupContainer(t, cli, network, info.id))

	configPath := filepath.Join(tmpDir, "config")
	createSSHConfigFile(t, configPath, []string{
		sshConfigEntry(info.hostAlias, "127.0.0.1", "root", keyFiles.privateKeyPath, info.port),
	})

	group, err := iago.NewSSHGroup([]string{info.hostAlias}, configPath, iago.ForwardAgent())
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	host := group.Hosts[0]

	// Two sequential sessions on the same connection: the first is granted
	// forwarding; the second inherits the connection-wide grant.
	for _, session := range []string{"first", "second"} {
		var sb strings.Builder
		err := iago.Shell{
			Command: `echo "SOCK=${SSH_AUTH_SOCK:-MISSING}"; ssh-add -l`,
			Stdout:  &sb,
			Stderr:  &sb,
		}.Apply(context.Background(), host)
		out := sb.String()
		if err != nil {
			t.Fatalf("%s session: %v\noutput:\n%s", session, err, out)
		}
		if strings.Contains(out, "SOCK=MISSING") {
			t.Errorf("%s session: SSH_AUTH_SOCK not set\noutput:\n%s", session, out)
		}
		if !strings.Contains(out, "ED25519") {
			t.Errorf("%s session: forwarded agent did not list the test key\noutput:\n%s", session, out)
		}
	}
}
