// Package iagotest provides utilities for external libraries to test using the iago package.
package iagotest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relab/container"
	"github.com/relab/container/build"
	"github.com/relab/container/network"
	"github.com/relab/iago"
	"golang.org/x/crypto/ssh"
)

const tag = "iago-test"

var (
	//go:embed Dockerfile
	dockerfile []byte
	//go:embed entrypoint.sh
	entrypoint []byte
)

// CreateSSHGroup starts n docker containers and connects to them with ssh.
// If skip is true, this function will call t.Skip() if docker is unavailable.
func CreateSSHGroup(t testing.TB, n int, skip bool) (g iago.Group) {
	signer, _, pub := generateKey(t)

	cli, network := setupContainerEnvironment(t, skip)
	// cleanup the network (will be called last due to LIFO)
	t.Cleanup(cleanupNetwork(t, cli, network))

	hosts := make([]iago.Host, n)
	for i := range n {
		id, addr := createContainer(t, cli, network, string(pub))
		t.Cleanup(cleanupContainer(t, cli, network, id))
		t.Logf("Created container %s with ssh address %s", id, addr)

		var err error
		hosts[i], err = iago.DialSSH(id, addr, &ssh.ClientConfig{
			User:            "root",
			Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
	})
	return iago.NewGroup(hosts)
}

func generateKey(t testing.TB) (ssh.Signer, []byte, []byte) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "Test Key")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer, pem.EncodeToMemory(pemBlock), ssh.MarshalAuthorizedKey(signer.PublicKey())
}

func createClient(t testing.TB) *container.Container {
	cli, err := container.NewContainer()
	if err != nil {
		t.Fatal(err)
	}
	return cli
}

func buildImage(t testing.TB, cli *container.Container) {
	buildCtx, err := prepareBuildContext()
	if err != nil {
		t.Fatal(err)
	}
	res, err := cli.ImageBuild(context.Background(), buildCtx, build.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = res.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	if _, err = io.Copy(os.Stdout, res); err != nil {
		t.Error(err)
	}
}

func createContainer(t testing.TB, cli *container.Container, networkID, pubKey string) (name, addr string) {
	res, err := cli.ContainerCreate(context.Background(), &container.Config{
		Env:   []string{"AUTHORIZED_KEYS=" + pubKey},
		Image: tag,
		ExposedPorts: container.PortSet{
			"22/tcp": struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: container.PortMap{"22/tcp": {{}}}, // map ssh port to ephemeral port
	}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = cli.ContainerStart(context.Background(), res.ID); err != nil {
		t.Fatal(err)
	}
	details, err := cli.ContainerInspect(context.Background(), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	name = strings.TrimPrefix(details.Name, "/")
	err = cli.NetworkConnect(context.Background(), networkID, res.ID, &network.EndpointSettings{
		Aliases: []string{name},
	})
	if err != nil {
		t.Fatal(err)
	}
	// retry until the port is assigned, or give up after 10ms
	for i := range 10 {
		details, err = cli.ContainerInspect(context.Background(), res.ID)
		if err != nil {
			t.Fatal(err)
		}
		addr = sshPortBinding(details)
		if addr != "" {
			break
		}
		t.Logf("retry %d: no port bindings found for container %s", i, name)
		time.Sleep(1 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("no port bindings found after 10ms")
	}
	return name, addr
}

func sshPortBinding(details container.InspectResponse) string {
	bindings, ok := details.NetworkSettings.Ports["22/tcp"]
	if !ok || len(bindings) == 0 {
		return ""
	}
	return "localhost:" + bindings[0].HostPort
}

func createNetwork(t testing.TB, cli *container.Container) string {
	res, err := cli.NetworkCreate(context.Background(), network.CreateOptions{
		Name:   "iago-" + rand.Text()[:8],
		Driver: "bridge",
	})
	if err != nil {
		t.Fatal("failed to create network: ", err)
	}
	return res.ID
}

func prepareBuildContext() (r io.ReadCloser, err error) {
	var buf bytes.Buffer
	tarWriter := tar.NewWriter(&buf)

	err = tarWriter.WriteHeader(&tar.Header{
		Name:   "Dockerfile",
		Size:   int64(len(dockerfile)),
		Mode:   0o644,
		Format: tar.FormatUSTAR,
	})
	if err != nil {
		return nil, err
	}

	_, err = tarWriter.Write(dockerfile)
	if err != nil {
		return nil, err
	}

	err = tarWriter.WriteHeader(&tar.Header{
		Name:   "entrypoint.sh",
		Size:   int64(len(entrypoint)),
		Mode:   0o755,
		Format: tar.FormatUSTAR,
	})
	if err != nil {
		return nil, err
	}

	_, err = tarWriter.Write(entrypoint)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(&buf), nil
}

// sshKeyFiles represents the paths to SSH key files created for testing
type sshKeyFiles struct {
	privateKeyPath string
	publicKeyPath  string
	publicKeyData  []byte
}

// setupSSHKeys generates SSH keys and writes them to temporary files in the given directory.
// Returns the file paths and public key data for use in tests.
func setupSSHKeys(t testing.TB, tmpDir string) sshKeyFiles {
	t.Helper()
	_, priv, pub := generateKey(t)

	privKeyFile := filepath.Join(tmpDir, "id_ed25519")
	if err := os.WriteFile(privKeyFile, priv, 0o600); err != nil {
		t.Fatal(err)
	}
	pubKeyFile := filepath.Join(tmpDir, "id_ed25519.pub")
	if err := os.WriteFile(pubKeyFile, pub, 0o600); err != nil {
		t.Fatal(err)
	}

	return sshKeyFiles{
		privateKeyPath: privKeyFile,
		publicKeyPath:  pubKeyFile,
		publicKeyData:  pub,
	}
}

// setupContainerEnvironment creates a Docker client, pings it, builds the image, and creates a network.
// Returns the client and network ID for use in tests.
// If skip is true, it will skip the test if the Docker daemon is not reachable.
func setupContainerEnvironment(t testing.TB, skip bool) (*container.Container, string) {
	t.Helper()
	cli := createClient(t)
	if err := cli.Ping(context.Background()); err != nil {
		if skip {
			t.Skip("could not connect to docker daemon")
		} else {
			t.Fatal(err)
		}
	}
	buildImage(t, cli)

	network := createNetwork(t, cli)
	t.Logf("Created network %s", network)

	return cli, network
}

// containerInfo represents information about a created container
type containerInfo struct {
	id        string
	address   string
	hostAlias string
	port      string
}

// createContainerWithInfo creates a container and returns structured information about it
func createContainerWithInfo(t testing.TB, cli *container.Container, network, pubKey, hostAlias string) containerInfo {
	t.Helper()
	id, addr := createContainer(t, cli, network, pubKey)

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Created container %s with ssh address %s for host alias %s", id, addr, hostAlias)

	return containerInfo{
		id:        id,
		address:   addr,
		hostAlias: hostAlias,
		port:      port,
	}
}

// cleanupContainer provides a cleanup function for stopping a single container
func cleanupContainer(t testing.TB, cli *container.Container, network, containerID string) func() {
	t.Helper()
	return func() {
		timeout := 1 // seconds to wait before forcefully killing the container
		opts := container.StopOptions{Timeout: &timeout}
		ctx := context.Background()
		if err := cli.ContainerStop(ctx, containerID, opts); err != nil {
			t.Errorf("Failed to stop container '%s': %v", containerID, err)
		}
		if err := cli.NetworkDisconnect(ctx, network, containerID, true); err != nil {
			t.Errorf("Failed to disconnect container %s from network '%s': %v", containerID, network, err)
		}
		removeOpts := container.RemoveOptions{Force: true, RemoveVolumes: true}
		if err := cli.ContainerRemove(ctx, containerID, removeOpts); err != nil {
			t.Errorf("Failed to remove container '%s': %v", containerID, err)
		}
	}
}

// cleanupNetwork provides a cleanup function for removing a network
func cleanupNetwork(t testing.TB, cli *container.Container, network string) func() {
	t.Helper()
	return func() {
		if err := cli.NetworkRemove(context.Background(), network); err != nil {
			t.Error(err)
		}
	}
}

// sshConfigEntry generates an SSH config entry for the given parameters
func sshConfigEntry(hostAlias, hostname, user, identityFile, port string) string {
	return fmt.Sprintf(`Host %s
	Hostname %s
	User %s
	IdentityFile %s
	Port %s
	StrictHostKeyChecking no
	UserKnownHostsFile /dev/null
`, hostAlias, hostname, user, identityFile, port)
}

// createSSHConfigFile creates an SSH config file with the given entries
func createSSHConfigFile(t testing.TB, configPath string, entries []string) {
	t.Helper()
	configContent := ""
	for _, entry := range entries {
		configContent += entry + "\n"
	}

	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}
}
