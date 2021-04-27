package main

import (
	"log"
	"os"

	. "github.com/Raytar/iago"
	"github.com/kevinburke/ssh_config"
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
	f, err := os.Open("ssh_config")
	if err != nil {
		log.Fatalln(err)
	}

	sshCfg, err := ssh_config.Decode(f)
	if err != nil {
		log.Fatalln(err)
	}

	clientCfg := ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			readKey(),
		},
	}

	var hosts = []string{"node1", "node2", "node3"}
	var g Group
	for _, h := range hosts {
		addr, err := sshCfg.Get(h, "HostName")
		if err != nil {
			log.Println(err)
			continue
		}
		host, err := DialSSH(h, addr, &clientCfg)
		if err != nil {
			log.Println(err)
			continue
		}
		g = append(g, host)
	}

	if len(g) == 0 {
		log.Fatalln("No hosts in group.")
	}

	run(g)
}

func run(g Group) {
	g.Run(Shell("uname -a > /tmp/test"))
	g.Run(Fetch("/tmp/test", "./test", 0644))
}
