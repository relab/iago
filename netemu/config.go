package netemu

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config describes the bandwidth emulation to apply across a set of replicas.
type Config struct {
	// Interface is the NIC to apply egress shaping rules on (e.g. "eth0").
	Interface string `yaml:"interface"`
	// IFB is the name of the Intermediate Functional Block device used for
	// ingress shaping. It is created by SetupEmulation when ShapeIngress is true.
	IFB string `yaml:"ifb"`

	// Profiles are named bandwidth templates that can be referenced by
	// LinkProfiles or Links.
	Profiles map[string]Profile `yaml:"profiles"`

	// DefaultProfile names the Profile to apply to any replica pair not
	// covered by LinkProfiles or Links. Leave empty to leave such pairs
	// unconstrained (default tc catch-all class).
	DefaultProfile string `yaml:"default_profile"`

	Replicas     []Replica          `yaml:"replicas"`
	LinkProfiles []GroupLinkProfile `yaml:"link_profiles"`

	// Links are per-pair overrides. They take precedence over LinkProfiles
	// and DefaultProfile. Either set Profile to name a predefined profile,
	// or set Rate/Delay/Loss directly (direct fields win over Profile).
	Links []Link `yaml:"links"`
}

// Profile is a reusable bandwidth template.
type Profile struct {
	// Rate is passed directly to tc (e.g. "100mbit", "1gbit").
	Rate string `yaml:"rate"`
	// Delay is passed directly to netem (e.g. "50ms").
	Delay string `yaml:"delay"`
	// Loss is the packet loss percentage passed to netem (e.g. 0.1 for 0.1%).
	Loss float64 `yaml:"loss"`
}

// Replica is a single process endpoint identified by its host and listening port.
// Host may be an IP address or a hostname resolvable from the controller.
// IP, if set, is used instead of Host in iptables rules (required when the
// node running iptables cannot resolve Host via DNS).
// Multiple replicas may share the same host with distinct ports.
type Replica struct {
	ID    string `yaml:"id"`
	Host  string `yaml:"host"`
	IP    string `yaml:"ip,omitempty"`
	Port  int    `yaml:"port"`
	Group string `yaml:"group"`
}

// dst returns the IP address to use in iptables --dst rules: the explicit IP
// field if set, otherwise Host.
func (r Replica) dst() string {
	if r.IP != "" {
		return r.IP
	}
	return r.Host
}

// GroupLinkProfile assigns a named Profile to all (from, to) pairs whose
// replicas belong to the specified groups.
type GroupLinkProfile struct {
	FromGroup string `yaml:"from_group"`
	ToGroup   string `yaml:"to_group"`
	Profile   string `yaml:"profile"`
}

// Link is an explicit per-pair bandwidth override. It takes precedence over
// any matching GroupLinkProfile or DefaultProfile.
// Set Profile to reference a named profile, or set Rate/Delay/Loss directly.
// Direct fields win over Profile when both are present.
type Link struct {
	From    string  `yaml:"from"`
	To      string  `yaml:"to"`
	Profile string  `yaml:"profile"`
	Rate    string  `yaml:"rate"`
	Delay   string  `yaml:"delay"`
	Loss    float64 `yaml:"loss"`
}

// LoadConfig reads and validates a YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// resolve returns the effective Profile for a given (fromID, toID) pair,
// following the precedence: explicit Link > GroupLinkProfile > DefaultProfile.
// Returns (profile, false) if no rule matches and DefaultProfile is unset.
func (c *Config) resolve(fromID, toID string) (Profile, bool) {
	fromGroup := c.groupOf(fromID)
	toGroup := c.groupOf(toID)

	// 1. Explicit per-pair link.
	for _, l := range c.Links {
		if l.From == fromID && l.To == toID {
			return c.mergeLink(l), true
		}
	}

	// 2. Group-to-group profile.
	for _, glp := range c.LinkProfiles {
		if glp.FromGroup == fromGroup && glp.ToGroup == toGroup {
			p, ok := c.Profiles[glp.Profile]
			return p, ok
		}
	}

	// 3. Default profile.
	if c.DefaultProfile != "" {
		p, ok := c.Profiles[c.DefaultProfile]
		return p, ok
	}

	return Profile{}, false
}

// mergeLink builds a Profile from a Link entry. Direct fields (Rate, Delay,
// Loss) take precedence over any named Profile the Link references.
func (c *Config) mergeLink(l Link) Profile {
	base := Profile{}
	if l.Profile != "" {
		base = c.Profiles[l.Profile]
	}
	if l.Rate != "" {
		base.Rate = l.Rate
	}
	if l.Delay != "" {
		base.Delay = l.Delay
	}
	if l.Loss != 0 {
		base.Loss = l.Loss
	}
	return base
}

func (c *Config) groupOf(id string) string {
	for _, r := range c.Replicas {
		if r.ID == id {
			return r.Group
		}
	}
	return ""
}

func (c *Config) validate() error {
	if c.Interface == "" {
		return fmt.Errorf("netemu: interface is required")
	}

	ids := make(map[string]bool, len(c.Replicas))
	for _, r := range c.Replicas {
		if r.ID == "" {
			return fmt.Errorf("netemu: replica has empty id")
		}
		if ids[r.ID] {
			return fmt.Errorf("netemu: duplicate replica id %q", r.ID)
		}
		if r.Host == "" {
			return fmt.Errorf("netemu: replica %q has empty host", r.ID)
		}
		if r.Port <= 0 || r.Port > 65535 {
			return fmt.Errorf("netemu: replica %q has invalid port %d", r.ID, r.Port)
		}
		ids[r.ID] = true
	}

	for name, p := range c.Profiles {
		if p.Rate == "" {
			return fmt.Errorf("netemu: profile %q is missing rate", name)
		}
		if p.Delay == "" {
			return fmt.Errorf("netemu: profile %q is missing delay", name)
		}
	}

	for _, glp := range c.LinkProfiles {
		if _, ok := c.Profiles[glp.Profile]; !ok {
			return fmt.Errorf("netemu: link_profile references unknown profile %q", glp.Profile)
		}
	}

	if c.DefaultProfile != "" {
		if _, ok := c.Profiles[c.DefaultProfile]; !ok {
			return fmt.Errorf("netemu: default_profile references unknown profile %q", c.DefaultProfile)
		}
	}

	for _, l := range c.Links {
		if !ids[l.From] {
			return fmt.Errorf("netemu: link references unknown replica %q", l.From)
		}
		if !ids[l.To] {
			return fmt.Errorf("netemu: link references unknown replica %q", l.To)
		}
		if l.From == l.To {
			return fmt.Errorf("netemu: link from replica to itself: %q", l.From)
		}
		if l.Profile != "" {
			if _, ok := c.Profiles[l.Profile]; !ok {
				return fmt.Errorf("netemu: link references unknown profile %q", l.Profile)
			}
		}
	}

	return nil
}
