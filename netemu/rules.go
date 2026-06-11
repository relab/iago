package netemu

import "fmt"

// egressSetupCmds returns the ordered sequence of shell commands that install
// tc HTB + netem qdiscs and iptables MANGLE/OUTPUT marks for egress shaping.
//
// Structure on <iface>:
//
//	root qdisc: htb default <defaultClassMinor>
//	  class 1:<defaultClassMinor>  htb rate 100gbit  (unconstrained catch-all)
//	  class 1:<mark>               htb rate <rate>   (one per shaped link)
//	    qdisc <mark>:              netem delay <d> loss <l>%
//	  filter: fw handle <mark> -> classid 1:<mark>
//
// iptables stamps <mark> on packets in OUTPUT matching (sport, dport).
func egressSetupCmds(cfg *Config) []string {
	n := len(cfg.Replicas)
	iface := cfg.Interface
	defMinor := defaultClassMinor(n)
	idx := buildIndexMap(cfg.Replicas)

	cmds := []string{
		fmt.Sprintf("tc qdisc add dev %s root handle 1: htb default %d", iface, defMinor),
		fmt.Sprintf("tc class add dev %s parent 1: classid 1:%d htb rate 100gbit", iface, defMinor),
	}

	for _, from := range cfg.Replicas {
		for _, to := range cfg.Replicas {
			if from.ID == to.ID {
				continue
			}
			p, ok := cfg.resolve(from.ID, to.ID)
			if !ok {
				continue
			}
			mark := flowMark(idx[from.ID], idx[to.ID], n)
			cmds = append(cmds,
				fmt.Sprintf("tc class add dev %s parent 1: classid 1:%d htb rate %s",
					iface, mark, p.Rate),
				netemCmd(iface, mark, p),
				fmt.Sprintf("iptables -t mangle -A OUTPUT -p tcp --sport %d --dst %s --dport %d -j MARK --set-mark %d",
					from.Port, to.dst(), to.Port, mark),
				fmt.Sprintf("tc filter add dev %s parent 1: handle %d fw classid 1:%d",
					iface, mark, mark),
			)
		}
	}
	return cmds
}

// egressTeardownCmds returns commands that remove the egress tc qdisc and
// flush iptables MANGLE/OUTPUT rules installed by egressSetupCmds.
// Errors from these commands are expected when rules don't exist and should
// be ignored by the caller.
func egressTeardownCmds(cfg *Config) []string {
	return []string{
		fmt.Sprintf("tc qdisc del dev %s root", cfg.Interface),
		"iptables -t mangle -F OUTPUT",
	}
}

// ingressSetupCmds returns commands that redirect ingress traffic through an
// IFB (Intermediate Functional Block) device and apply the same HTB + netem
// hierarchy there, controlled by iptables MANGLE/PREROUTING marks.
//
// For a packet arriving at this host sent by replica <from> (sport=from.Port)
// destined to replica <to> (dport=to.Port), the mark applied in PREROUTING
// matches the same (from→to) profile used on the sender's egress. This lets
// each host independently enforce its own receive-side cap.
func ingressSetupCmds(cfg *Config) []string {
	n := len(cfg.Replicas)
	iface := cfg.Interface
	ifb := cfg.IFB
	defMinor := defaultClassMinor(n)
	idx := buildIndexMap(cfg.Replicas)

	cmds := []string{
		"modprobe ifb numifbs=1",
		fmt.Sprintf("ip link add %s type ifb", ifb),
		fmt.Sprintf("ip link set %s up", ifb),
		// Redirect all ingress to the IFB device.
		fmt.Sprintf("tc qdisc add dev %s ingress", iface),
		fmt.Sprintf(
			"tc filter add dev %s parent ffff: protocol ip u32 match u32 0 0 action mirred egress redirect dev %s",
			iface, ifb),
		// HTB root on IFB.
		fmt.Sprintf("tc qdisc add dev %s root handle 1: htb default %d", ifb, defMinor),
		fmt.Sprintf("tc class add dev %s parent 1: classid 1:%d htb rate 100gbit", ifb, defMinor),
	}

	for _, from := range cfg.Replicas {
		for _, to := range cfg.Replicas {
			if from.ID == to.ID {
				continue
			}
			p, ok := cfg.resolve(from.ID, to.ID)
			if !ok {
				continue
			}
			mark := flowMark(idx[from.ID], idx[to.ID], n)
			cmds = append(cmds,
				fmt.Sprintf("tc class add dev %s parent 1: classid 1:%d htb rate %s",
					ifb, mark, p.Rate),
				netemCmd(ifb, mark, p),
				// In PREROUTING the packet's source port belongs to the sender
				// and destination port belongs to the local replica.
				fmt.Sprintf("iptables -t mangle -A PREROUTING -p tcp --sport %d --dst %s --dport %d -j MARK --set-mark %d",
					from.Port, to.dst(), to.Port, mark),
				fmt.Sprintf("tc filter add dev %s parent 1: handle %d fw classid 1:%d",
					ifb, mark, mark),
			)
		}
	}
	return cmds
}

// ingressTeardownCmds returns commands that undo ingressSetupCmds.
func ingressTeardownCmds(cfg *Config) []string {
	return []string{
		"iptables -t mangle -F PREROUTING",
		fmt.Sprintf("tc qdisc del dev %s ingress", cfg.Interface),
		fmt.Sprintf("ip link set %s down", cfg.IFB),
		fmt.Sprintf("ip link del %s", cfg.IFB),
	}
}

// netemCmd builds the tc qdisc add command that attaches a netem discipline
// as a leaf under the HTB class identified by mark.
func netemCmd(dev string, mark int, p Profile) string {
	cmd := fmt.Sprintf("tc qdisc add dev %s parent 1:%d handle %d: netem delay %s",
		dev, mark, mark, p.Delay)
	if p.Loss > 0 {
		cmd += fmt.Sprintf(" loss %.4f%%", p.Loss)
	}
	return cmd
}
