package iagotest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"io"
	mrand "math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/relab/iago"
	"golang.org/x/crypto/ssh"
)

const tag = "iago-test"

var (
	//go:embed Dockerfile
	dockerfile []byte
	//go:embed entrypoint.sh
	entrypoint []byte

	rnd *mrand.Rand
)

func init() {
	rnd = mrand.New(mrand.NewSource(time.Now().Unix()))
}

// CreateSSHGroup starts n docker containers and connects to them with ssh.
// If skip is true, this function will call t.Skip() if docker is unavailable.
func CreateSSHGroup(t *testing.T, n int, skip bool) (g iago.Group) {
	signer, pub := generateKey(t)

	cli := createClient(t)

	// test connection
	if _, err := cli.Ping(context.Background()); err != nil {
		if skip && client.IsErrConnectionFailed(err) {
			t.Skip("could not connect to docker daemon")
		}
		t.Fatal(err)
	}

	buildImage(t, cli)

	containers := make([]string, n)
	network := createNetwork(t, cli)

	t.Cleanup(func() {
		if err := g.Close(); err != nil {
			t.Error(err)
		}
		timeout := 1 // seconds to wait before forcefully killing the container
		opts := container.StopOptions{Timeout: &timeout}
		for _, containerID := range containers {
			if err := cli.ContainerStop(context.Background(), containerID, opts); err != nil {
				t.Errorf("Failed to stop container '%s': %v", containerID, err)
			}
		}
		if err := cli.NetworkRemove(context.Background(), network); err != nil {
			t.Error(err)
		}
	})

	hosts := make([]iago.Host, n)
	for i := range n {
		id, addr := createContainer(t, cli, network, pub)
		t.Logf("Created container %s with ssh address %s", id, addr)
		containers[i] = id

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

	return iago.NewGroup(hosts)
}

func generateKey(t *testing.T) (ssh.Signer, string) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer, string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
}

func createClient(t *testing.T) *client.Client {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return cli
}

func buildImage(t *testing.T, cli *client.Client) {
	buildCtx, err := prepareBuildContext()
	if err != nil {
		t.Fatal(err)
	}
	res, err := cli.ImageBuild(context.Background(), buildCtx, types.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = res.Body.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	if _, err = io.Copy(os.Stdout, res.Body); err != nil {
		t.Error(err)
	}
}

func createContainer(t *testing.T, cli *client.Client, networkID, pubKey string) (name, addr string) {
	res, err := cli.ContainerCreate(context.Background(), &container.Config{
		Env:   []string{"AUTHORIZED_KEYS=" + pubKey},
		Image: tag,
		ExposedPorts: nat.PortSet{
			"22/tcp": struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: nat.PortMap{"22/tcp": {{}}}, // map ssh port to ephemeral port
		AutoRemove:   true,
	}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = cli.ContainerStart(context.Background(), res.ID, container.StartOptions{}); err != nil {
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

func sshPortBinding(details types.ContainerJSON) string {
	bindings, ok := details.NetworkSettings.Ports["22/tcp"]
	if !ok || len(bindings) == 0 {
		return ""
	}
	return "localhost:" + bindings[0].HostPort
}

func createNetwork(t *testing.T, cli *client.Client) string {
	res, err := cli.NetworkCreate(context.Background(), "iago-"+randString(8), network.CreateOptions{
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

func randString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	s := make([]rune, n)
	for i := range s {
		s[i] = letters[rnd.Intn(len(letters))]
	}
	return string(s)
}
