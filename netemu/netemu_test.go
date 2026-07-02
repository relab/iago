package netemu

import (
	"fmt"
	"strings"
	"testing"
)

// minimalConfig returns a small valid Config used as a base for tests.
func minimalConfig() *Config {
	return &Config{
		Interface: "eth0",
		IFB:       "ifb0",
		Profiles: map[string]Profile{
			"lan": {Rate: "1000mbit", Delay: "2ms"},
			"wan": {Rate: "100mbit", Delay: "80ms", Loss: 0.1},
		},
		Replicas: []Replica{
			{ID: "r1", Host: "bbchain1", Port: 7001, Group: "a"},
			{ID: "r2", Host: "bbchain2", Port: 7002, Group: "a"},
			{ID: "r3", Host: "bbchain2", Port: 7003, Group: "b"},
		},
		LinkProfiles: []GroupLinkProfile{
			{FromGroup: "a", ToGroup: "a", Profile: "lan"},
			{FromGroup: "b", ToGroup: "b", Profile: "lan"},
			{FromGroup: "a", ToGroup: "b", Profile: "wan"},
			{FromGroup: "b", ToGroup: "a", Profile: "wan"},
		},
	}
}

// --- Config validation ---

func TestValidateOK(t *testing.T) {
	if err := minimalConfig().validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMissingInterface(t *testing.T) {
	cfg := minimalConfig()
	cfg.Interface = ""
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for missing interface")
	}
}

func TestValidateDuplicateReplicaID(t *testing.T) {
	cfg := minimalConfig()
	cfg.Replicas = append(cfg.Replicas, Replica{ID: "r1", Port: 7099, Group: "a"})
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for duplicate replica id")
	}
}

func TestValidateMissingIP(t *testing.T) {
	cfg := minimalConfig()
	cfg.Replicas[0].Host = ""
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := minimalConfig()
	cfg.Replicas[0].Port = 0
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for port 0")
	}
}

func TestValidateUnknownLinkProfileProfile(t *testing.T) {
	cfg := minimalConfig()
	cfg.LinkProfiles = append(cfg.LinkProfiles, GroupLinkProfile{
		FromGroup: "a", ToGroup: "b", Profile: "nonexistent",
	})
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for unknown profile in link_profile")
	}
}

func TestValidateUnknownDefaultProfile(t *testing.T) {
	cfg := minimalConfig()
	cfg.DefaultProfile = "ghost"
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for unknown default_profile")
	}
}

func TestValidateLinkSelfLoop(t *testing.T) {
	cfg := minimalConfig()
	cfg.Links = []Link{{From: "r1", To: "r1", Rate: "100mbit", Delay: "10ms"}}
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for self-loop link")
	}
}

func TestValidateLinkUnknownReplica(t *testing.T) {
	cfg := minimalConfig()
	cfg.Links = []Link{{From: "r1", To: "r99", Rate: "100mbit", Delay: "10ms"}}
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for unknown replica in link")
	}
}

func TestValidateLinkUnknownProfile(t *testing.T) {
	cfg := minimalConfig()
	cfg.Links = []Link{{From: "r1", To: "r3", Profile: "ghost"}}
	if err := cfg.validate(); err == nil {
		t.Fatal("expected error for unknown profile in link")
	}
}

// --- Mark arithmetic ---

func TestFlowMarkUnique(t *testing.T) {
	n := 10
	seen := make(map[int]bool)
	for from := range n {
		for to := range n {
			if from == to {
				continue
			}
			m := flowMark(from, to, n)
			if seen[m] {
				t.Fatalf("flowMark(%d, %d, %d) = %d collides with earlier pair", from, to, n, m)
			}
			seen[m] = true
		}
	}
}

func TestFlowMarkNonZero(t *testing.T) {
	n := 5
	for from := range n {
		for to := range n {
			if from == to {
				continue
			}
			if m := flowMark(from, to, n); m == 0 {
				t.Fatalf("flowMark(%d, %d, %d) = 0, kernel treats 0 as unset", from, to, n)
			}
		}
	}
}

func TestDefaultClassMinorNoCollision(t *testing.T) {
	for n := 1; n <= 50; n++ {
		def := defaultClassMinor(n)
		for from := range n {
			for to := range n {
				if from == to {
					continue
				}
				if flowMark(from, to, n) == def {
					t.Fatalf("n=%d: flowMark(%d,%d) collides with defaultClassMinor", n, from, to)
				}
			}
		}
	}
}

// --- Profile resolution ---

func TestResolveGroupLinkProfile(t *testing.T) {
	cfg := minimalConfig()
	p, ok := cfg.resolve("r1", "r2") // both group a → lan
	if !ok {
		t.Fatal("expected resolution")
	}
	if p.Rate != "1000mbit" {
		t.Fatalf("expected lan rate 1000mbit, got %s", p.Rate)
	}
}

func TestResolveInterGroupProfile(t *testing.T) {
	cfg := minimalConfig()
	p, ok := cfg.resolve("r1", "r3") // a→b → wan
	if !ok {
		t.Fatal("expected resolution")
	}
	if p.Rate != "100mbit" {
		t.Fatalf("expected wan rate 100mbit, got %s", p.Rate)
	}
}

func TestResolveExplicitLinkWins(t *testing.T) {
	cfg := minimalConfig()
	cfg.Links = []Link{{From: "r1", To: "r2", Rate: "500mbit", Delay: "5ms"}}
	p, ok := cfg.resolve("r1", "r2")
	if !ok {
		t.Fatal("expected resolution")
	}
	if p.Rate != "500mbit" {
		t.Fatalf("explicit link should win: expected 500mbit, got %s", p.Rate)
	}
}

func TestResolveLinkProfileOverridesGroup(t *testing.T) {
	cfg := minimalConfig()
	cfg.Links = []Link{{From: "r1", To: "r2", Profile: "wan"}}
	p, ok := cfg.resolve("r1", "r2")
	if !ok {
		t.Fatal("expected resolution")
	}
	if p.Rate != "100mbit" {
		t.Fatalf("link profile 'wan' should override group 'lan': got %s", p.Rate)
	}
}

func TestResolveLinkDirectFieldsOverrideProfile(t *testing.T) {
	cfg := minimalConfig()
	cfg.Links = []Link{{From: "r1", To: "r3", Profile: "wan", Rate: "50mbit", Delay: "150ms"}}
	p, ok := cfg.resolve("r1", "r3")
	if !ok {
		t.Fatal("expected resolution")
	}
	if p.Rate != "50mbit" {
		t.Fatalf("direct Rate should override profile rate: got %s", p.Rate)
	}
	if p.Delay != "150ms" {
		t.Fatalf("direct Delay should override profile delay: got %s", p.Delay)
	}
}

func TestResolveDefaultProfile(t *testing.T) {
	cfg := minimalConfig()
	cfg.LinkProfiles = nil
	cfg.DefaultProfile = "wan"
	p, ok := cfg.resolve("r1", "r3")
	if !ok {
		t.Fatal("expected resolution via default_profile")
	}
	if p.Rate != "100mbit" {
		t.Fatalf("expected wan rate, got %s", p.Rate)
	}
}

func TestResolveNoRuleReturnsNotFound(t *testing.T) {
	cfg := minimalConfig()
	cfg.LinkProfiles = nil
	_, ok := cfg.resolve("r1", "r3")
	if ok {
		t.Fatal("expected no resolution when no rules match and no default")
	}
}

// --- Command generation ---

func TestEgressSetupContainsTCAndIPTables(t *testing.T) {
	cmds := egressSetupCmds(minimalConfig())

	assertContainsSubstr(t, cmds, "tc qdisc add dev eth0 root handle 1: htb")
	// r1→r2 (both group a, lan profile)
	assertContainsSubstr(t, cmds, "iptables -t mangle -A OUTPUT -p tcp --sport 7001 --dst bbchain2 --dport 7002")
	assertContainsSubstr(t, cmds, "htb rate 1000mbit")
	// r1→r3 (a→b, wan profile)
	assertContainsSubstr(t, cmds, "iptables -t mangle -A OUTPUT -p tcp --sport 7001 --dst bbchain2 --dport 7003")
	assertContainsSubstr(t, cmds, "htb rate 100mbit")
}

func TestEgressSetupNetemIncludesLoss(t *testing.T) {
	cmds := egressSetupCmds(minimalConfig())
	// wan profile has Loss: 0.1 — a→b and b→a flows should have loss in netem.
	assertContainsSubstr(t, cmds, "loss")
}

func TestEgressSetupNoNetemLossForLan(t *testing.T) {
	cfg := minimalConfig()
	cmds := egressSetupCmds(cfg)
	n := len(cfg.Replicas)
	idx := buildIndexMap(cfg.Replicas)
	mark := flowMark(idx["r1"], idx["r2"], n)
	target := fmt.Sprintf("parent 1:%d", mark)
	for _, cmd := range cmds {
		if strings.Contains(cmd, "netem") && strings.Contains(cmd, target) {
			if strings.Contains(cmd, "loss") {
				t.Fatalf("lan netem cmd should not contain loss: %q", cmd)
			}
			return
		}
	}
	t.Fatal("no netem command found for r1→r2")
}

func TestEgressTeardownCommands(t *testing.T) {
	cmds := egressTeardownCmds(minimalConfig())
	assertContainsSubstr(t, cmds, "tc qdisc del dev eth0 root")
	assertContainsSubstr(t, cmds, "iptables -t mangle -F OUTPUT")
}

func TestIngressSetupContainsIFBRedirect(t *testing.T) {
	cmds := ingressSetupCmds(minimalConfig())
	assertContainsSubstr(t, cmds, "modprobe ifb")
	assertContainsSubstr(t, cmds, "ip link add ifb0 type ifb")
	assertContainsSubstr(t, cmds, "mirred egress redirect dev ifb0")
	assertContainsSubstr(t, cmds, "iptables -t mangle -A PREROUTING")
}

func TestIngressTeardownCommands(t *testing.T) {
	cmds := ingressTeardownCmds(minimalConfig())
	assertContainsSubstr(t, cmds, "iptables -t mangle -F PREROUTING")
	assertContainsSubstr(t, cmds, "ip link del ifb0")
}

func TestNoSelfFlowCommands(t *testing.T) {
	cmds := egressSetupCmds(minimalConfig())
	for _, cmd := range cmds {
		if strings.Contains(cmd, "--sport 7001 --dport 7001") ||
			strings.Contains(cmd, "--sport 7002 --dport 7002") ||
			strings.Contains(cmd, "--sport 7003 --dport 7003") {
			t.Fatalf("self-flow rule found: %q", cmd)
		}
	}
}

// --- helpers ---

func assertContainsSubstr(t *testing.T, cmds []string, sub string) {
	t.Helper()
	for _, c := range cmds {
		if strings.Contains(c, sub) {
			return
		}
	}
	t.Fatalf("no command contains substring %q\n  in: %v", sub, cmds)
}
