package main

import (
	"log"
	"os"

	"github.com/Raytar/iago"
	. "github.com/Raytar/iago"
	"golang.org/x/crypto/ssh"
)

func readKey() ssh.AuthMethod {
	b, err := os.ReadFile("id_ed25519")
	if err != nil {
		log.Fatalln(err)
	}
	auth, err := ssh.ParsePrivateKey(b)
	if err != nil {
		log.Fatalln(err)
	}
	return ssh.PublicKeys(auth)
}

func main() {
	clientCfg := ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			readKey(),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	var hosts = []string{"node1", "node2"}

	g, err := iago.NewSSHGroup(hosts, "ssh_config", clientCfg)
	if err != nil {
		log.Fatal(err)
	}

	run(g)
}

func run(g Group) {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	g.Run(Shell("uname -a > /tmp/test"))
	g.Run(Fetch(
		P("tmp/test"),
		P("test").RelativeTo(wd),
	))
}
