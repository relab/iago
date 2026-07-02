// Example program that sets up and tears down network emulation across a
// cluster of machines using the netemu package.
//
// Usage:
//
//	go run ./netemu/example --config network.yaml [--teardown-only]
//
// Prerequisites:
//   - Passwordless SSH access from this machine to all nodes
//   - Passwordless sudo on all nodes
//   - Node aliases defined in ~/.ssh/config
package main

import (
	"flag"
	"log"

	"github.com/relab/iago"
	"github.com/relab/iago/netemu"
)

var (
	configFile   = flag.String("config", "network.yaml", "path to netemu YAML config")
	teardownOnly = flag.Bool("teardown-only", false, "only run teardown, skip setup")
)

// nodes lists the SSH aliases for all machines in the cluster.
// Each alias must be defined in ~/.ssh/config with the correct HostName.
var nodes = []string{
	"bbchain2",  "bbchain3",  "bbchain4",  "bbchain5",  "bbchain6",
	"bbchain7",  "bbchain8",  "bbchain9",  "bbchain10", "bbchain11",
	"bbchain12", "bbchain13", "bbchain14", "bbchain15", "bbchain16",
	"bbchain17", "bbchain18", "bbchain19", "bbchain20", "bbchain21",
	"bbchain22", "bbchain23", "bbchain24", "bbchain25", "bbchain26",
	"bbchain27", "bbchain28", "bbchain29", "bbchain30",
}

func main() {
	flag.Parse()

	cfg, err := netemu.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	group, err := iago.NewSSHGroup(nodes, "")
	if err != nil {
		log.Fatalf("ssh group: %v", err)
	}
	defer group.Close()

	setup := netemu.SetupEmulation{Config: cfg, Sudo: true}
	teardown := netemu.TeardownEmulation{Config: cfg, Sudo: true}

	// always start from a clean slate
	log.Println("tearing down any existing rules...")
	group.Run("netemu-teardown", teardown.Apply)

	if *teardownOnly {
		log.Println("teardown complete")
		return
	}

	log.Println("setting up network emulation...")
	group.Run("netemu-setup", setup.Apply)
	log.Println("setup complete")

	log.Println("tearing down rules...")
	group.Run("netemu-teardown", teardown.Apply)
	log.Println("teardown complete")
}
