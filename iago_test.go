package iago_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/Raytar/iago"
	"golang.org/x/crypto/ssh"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/client"
)

func TestIago(t *testing.T) {
	dir := t.TempDir()

	g := setup(t, 4)

	errFunc := func(e error) {
		t.Fatal(e)
	}

	g.Run(Task{
		Name:    "Read distribution name",
		Action:  Shell("grep '^ID=' /etc/os-release > $HOME/os"),
		OnError: errFunc,
	})

	g.Run(Task{
		Name: "Download files",
		Action: Download{
			Src:  P("os").RelativeTo("$HOME"),
			Dest: P("os").RelativeTo(dir),
			Mode: 0644,
		},
		OnError: errFunc,
	})

	for i := range g {
		f, err := os.ReadFile(filepath.Join(dir, "os."+strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(f))
	}
}

const tag = "iago-test"

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

func setup(t *testing.T, n int) (g Group) {
	signer, pub := generateKey(t)

	cli := createClient(t)
	images, err := cli.ImageList(context.Background(), types.ImageListOptions{})
	if err != nil {
		t.Fatal(err)
	}

	haveImage := false
	for _, image := range images {
		for _, repoTag := range image.RepoTags {
			if strings.Contains(repoTag, tag) {
				haveImage = true
			}
		}
	}

	if !haveImage {
		buildImage(t, cli)
	}

	containers := make([]string, 0, n)
	ports := getFreePorts(t, n)

	t.Cleanup(func() {
		g.Close()
		for _, container := range containers {
			err := cli.ContainerRemove(context.Background(), container, types.ContainerRemoveOptions{Force: true})
			if err != nil {
				t.Log(err)
			}
		}
	})

	for i := 0; i < n; i++ {
		port := fmt.Sprintf("%d", ports.next())
		containers = append(containers, createContainer(t, cli, pub, port))
		var host Host

		for j := 0; j < 10; j++ {
			host, err = DialSSH(strconv.Itoa(i), "localhost:"+port, &ssh.ClientConfig{
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
		g = append(g, host)
	}

	return g
}

func createClient(t *testing.T) *client.Client {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	return cli
}

func buildImage(t *testing.T, cli *client.Client) {
	dockerfile, err := archive.TarWithOptions("./scripts", &archive.TarOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := cli.ImageBuild(context.Background(), dockerfile, types.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{tag},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	io.Copy(os.Stdout, res.Body)
}

func createContainer(t *testing.T, cli *client.Client, pubKey, port string) string {
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
	err = cli.ContainerStart(context.Background(), res.ID, types.ContainerStartOptions{})
	if err != nil {
		t.Fatal(err)
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
