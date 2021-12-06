package iagotest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
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
	_, err := cli.Ping(context.Background())
	if err != nil {
		if skip && client.IsErrConnectionFailed(err) {
			t.Skip("could not connect to docker daemon")
		}
		t.Fatal(err)
	}

	buildImage(t, cli)

	containers := make([]string, 0, n)
	ports := getFreePorts(t, n)

	network := createNetwork(t, cli)

	t.Cleanup(func() {
		err := g.Close()
		if err != nil {
			t.Error(err)
		}
		for _, container := range containers {
			err := cli.ContainerRemove(context.Background(), container, types.ContainerRemoveOptions{Force: true})
			if err != nil {
				t.Error(err)
			}
		}
		err = cli.NetworkRemove(context.Background(), network)
		if err != nil {
			t.Error(err)
		}
	})

	var hosts []iago.Host

	for i := 0; i < n; i++ {
		port := fmt.Sprintf("%d", ports.next())
		id := createContainer(t, cli, network, pub, port)
		containers = append(containers, id)
		var (
			host iago.Host
			err  error
		)

		for j := 0; j < 10; j++ {
			host, err = iago.DialSSH(id, "localhost:"+port, &ssh.ClientConfig{
				User:            "root",
				Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			})
			if err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		hosts = append(hosts, host)
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
	cli, err := client.NewClientWithOpts(client.FromEnv)
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
	_, err = io.Copy(os.Stdout, res.Body)
	if err != nil {
		t.Error(err)
	}
}

func createContainer(t *testing.T, cli *client.Client, networkID, pubKey, port string) string {
	res, err := cli.ContainerCreate(context.Background(), &container.Config{
		Env:   []string{"AUTHORIZED_KEYS=" + pubKey},
		Image: tag,
		ExposedPorts: nat.PortSet{
			"22/tcp": struct{}{},
		},
	}, &container.HostConfig{
		PortBindings: nat.PortMap{"22/tcp": {{HostPort: port}}},
		AutoRemove:   true,
	}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	details, err := cli.ContainerInspect(context.Background(), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(details.Name, "/")
	err = cli.NetworkConnect(context.Background(), networkID, res.ID, &network.EndpointSettings{
		Aliases: []string{name},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = cli.ContainerStart(context.Background(), res.ID, types.ContainerStartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func createNetwork(t *testing.T, cli *client.Client) string {
	res, err := cli.NetworkCreate(context.Background(), "iago-"+randString(8), types.NetworkCreate{
		Driver: "bridge",
	})
	if err != nil {
		t.Fatal("failed to create network: ", err)
	}
	return res.ID
}

type ports []int

func (p *ports) next() int {
	port := (*p)[0]
	*p = (*p)[1:]
	return port
}

// getFreePorts will get free ports from the kernel by opening a listener on 127.0.0.1:0 and then closing it.
func getFreePorts(t *testing.T, n int) ports {
	ports := make(ports, n)
	for i := 0; i < n; i++ {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			err := lis.Close()
			if err != nil {
				t.Fatal(err)
			}
		}()
		ports[i] = lis.Addr().(*net.TCPAddr).Port
	}
	return ports
}

func prepareBuildContext() (r io.ReadCloser, err error) {
	var buf bytes.Buffer
	tarw := tar.NewWriter(&buf)

	err = tarw.WriteHeader(&tar.Header{
		Name:   "Dockerfile",
		Size:   int64(len(dockerfile)),
		Mode:   0644,
		Format: tar.FormatUSTAR,
	})
	if err != nil {
		return nil, err
	}

	_, err = tarw.Write(dockerfile)
	if err != nil {
		return nil, err
	}

	err = tarw.WriteHeader(&tar.Header{
		Name:   "entrypoint.sh",
		Size:   int64(len(entrypoint)),
		Mode:   0755,
		Format: tar.FormatUSTAR,
	})
	if err != nil {
		return nil, err
	}

	_, err = tarw.Write(entrypoint)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(&buf), nil
}

func randString(n int) string {
	var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	s := make([]rune, n)
	for i := range s {
		s[i] = letters[rnd.Intn(len(letters))]
	}
	return string(s)
}
