package iago

import (
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestParseSSHConfigError(t *testing.T) {
	tests := []struct {
		name       string
		configFile string
	}{
		{name: "NoConfigFile", configFile: ""},
		{name: "MissingConfigFile", configFile: "testdata/config-missing"},
		{name: "BadConfigFile", configFile: "testdata/config-bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := ParseSSHConfig(tt.configFile)
			if err == nil {
				t.Errorf("ParseSSHConfig(%s): nil, want error", tt.configFile)
			}
			if config != nil {
				t.Errorf("ParseSSHConfig(%s): non-nil, want nil", tt.configFile)
			}
		})
	}
}

func TestConnectAddr(t *testing.T) {
	config, err := ParseSSHConfig("testdata/config")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		hostAlias string
		wantAddr  string
	}{
		{"localhost", "localhost", "127.0.0.1:22"},
		{"127.0.1.2_Matches_127.*", "127.0.1.2", "127.0.0.1:22"},
		{"Connect*PrefixMatch", "ConnectTest", "ConnectTest:1234"},
		{"ArbitraryHostMatchesWildcard", "asdf", "asdf:22"},
		{"HostnameExpandsPercH", "proxy-target", "proxy-target.example.com:2222"},
		{"JumpBoxHostname", "jumpbox", "jump.example.com:22"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAddr := config.ConnectAddr(tt.hostAlias)
			if gotAddr != tt.wantAddr {
				t.Errorf("Connect(%s) = %s, want %s", tt.hostAlias, gotAddr, tt.wantAddr)
			}
		})
	}
}

func TestClientConfig(t *testing.T) {
	config, err := ParseSSHConfig("testdata/config")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		hostAlias string
		wantUser  string
		wantDial  time.Duration
	}{
		{"localhost", "localhost", "testuser", ConnectTimeout},
		{"127.0.1.2_Matches_127.*", "127.0.1.2", "testuser", ConnectTimeout},
		{"Connect*PrefixMatches", "ConnectTest", "testuser2", ConnectTimeout},
		{"ArbitraryHostMatchesWildcard", "asdf", "testuser2", ConnectTimeout},
		{"ProxyTargetUser", "proxy-target", "proxyuser", 25 * time.Second},
		{"JumpBoxUser", "jumpbox", "jumpuser", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientConfig, err := config.ClientConfig(tt.hostAlias)
			if err != nil {
				t.Fatalf("ClientConfig(%s): unexpected error: %v", tt.hostAlias, err)
			}
			if clientConfig == nil {
				t.Fatalf("ClientConfig(%s): got nil, want non-nil", tt.hostAlias)
			}
			if clientConfig.User != tt.wantUser {
				t.Errorf("ClientConfig(%s).User = %s, want %s", tt.hostAlias, clientConfig.User, tt.wantUser)
			}
			if clientConfig.Timeout != tt.wantDial {
				t.Errorf("ClientConfig(%s).Timeout = %v, want %v", tt.hostAlias, clientConfig.Timeout, tt.wantDial)
			}
			if len(clientConfig.Auth) < 1 {
				t.Errorf("ClientConfig(%s): Auth has no methods", tt.hostAlias)
			}
		})
	}
}

func TestProxyJumpConfig(t *testing.T) {
	config, err := ParseSSHConfig("testdata/config")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		hostAlias string
		wantProxy string
	}{
		{"ProxyJumpSet", "proxy-target", "jumpbox"},
		{"ProxyJumpNotSet", "localhost", ""},
		{"ProxyJumpNotSetJumpbox", "jumpbox", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy, err := config.get(tt.hostAlias, "ProxyJump")
			if err != nil {
				t.Fatalf("get ProxyJump(%s): unexpected error: %v", tt.hostAlias, err)
			}
			if proxy != tt.wantProxy {
				t.Errorf("get ProxyJump(%s) = %q, want %q", tt.hostAlias, proxy, tt.wantProxy)
			}
		})
	}
}

// TestParseHostsRange covers numeric range expansion without consulting the
// SSH config. Passing an empty configFile is safe here because no glob token
// appears in any of the specs.
func TestParseHostsRange(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		want    []string
		wantErr bool
	}{
		{
			name: "SingleRange",
			spec: "bb[1-3]",
			want: []string{"bb1", "bb2", "bb3"},
		},
		{
			name: "SingleElementRange",
			spec: "bb[5-5]",
			want: []string{"bb5"},
		},
		{
			name: "PrefixAndSuffix",
			spec: "rack[1-2]-node",
			want: []string{"rack1-node", "rack2-node"},
		},
		{
			name: "RangePlusLiteral",
			spec: "bb[1-3],nebula",
			want: []string{"bb1", "bb2", "bb3", "nebula"},
		},
		{
			name: "MultipleRangesAcrossTokens",
			spec: "bb[1-2],cc[3-4]",
			want: []string{"bb1", "bb2", "cc3", "cc4"},
		},
		{
			name: "WhitespaceTrimmed",
			spec: " bb[1-2] , , nebula ",
			want: []string{"bb1", "bb2", "nebula"},
		},
		{
			name: "LargeRange",
			spec: "bb[1-30]",
			want: func() []string {
				out := make([]string, 30)
				for i := range 30 {
					out[i] = "bb" + strconv.Itoa(i+1)
				}
				return out
			}(),
		},
		{
			name:    "ReversedRange",
			spec:    "bb[5-1]",
			wantErr: true,
		},
		{
			name:    "MultipleRangesInOneToken",
			spec:    "bb[1-2]x[3-4]",
			wantErr: true,
		},
		{
			name:    "ReversedRangeInSecondToken",
			spec:    "nebula,bb[7-1]",
			wantErr: true,
		},
		{
			name: "ZeroPaddedBothBounds",
			spec: "node[01-03]",
			want: []string{"node01", "node02", "node03"},
		},
		{
			name: "ZeroPaddedLoBoundOnly",
			spec: "node[01-10]",
			want: []string{"node01", "node02", "node03", "node04", "node05", "node06", "node07", "node08", "node09", "node10"},
		},
		{
			name: "ZeroPaddedWidthThree",
			spec: "node[001-003]",
			want: []string{"node001", "node002", "node003"},
		},
		{
			name: "ZeroPaddedMixedWidth",
			spec: "node[001-100]",
			want: func() []string {
				out := make([]string, 100)
				for i := range 100 {
					out[i] = fmt.Sprintf("node%03d", i+1)
				}
				return out
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHosts(tt.spec, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseHosts(%q) error = %v, wantErr = %v", tt.spec, err, tt.wantErr)
			}
			if !tt.wantErr && !slices.Equal(got, tt.want) {
				t.Errorf("ParseHosts(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

// TestParseHostsGlob covers glob-based host enumeration from the SSH config.
// The testdata/config-hosts fixture has explicit entries bb1, bb2, bb3 plus a
// wildcard "Host bb*" stanza (which must be skipped) and a "Host nebula" entry.
func TestParseHostsGlob(t *testing.T) {
	const cfg = "testdata/config-hosts"
	tests := []struct {
		name    string
		spec    string
		want    []string
		wantErr bool
	}{
		{
			name: "WildcardMatchesExplicitEntries",
			spec: "bb*",
			want: []string{"bb1", "bb2", "bb3"},
		},
		{
			name: "GlobBracketExpr",
			// Numeric [lo-hi] is parsed via range expansion (not filepath.Match).
			spec: "bb[1-3]",
			want: []string{"bb1", "bb2", "bb3"},
		},
		{
			name: "GlobBracketExprAllDigits",
			// filepath.Match bracket expression: matches bb1, bb2, bb3
			spec: "bb[123]",
			want: []string{"bb1", "bb2", "bb3"},
		},
		{
			name: "GlobBracketExprPartialMatch",
			// Numeric [lo-hi] is parsed via range expansion (not filepath.Match).
			spec: "bb[1-2]",
			want: []string{"bb1", "bb2"},
		},
		{
			name: "GlobNoMatch",
			spec: "cc*",
			want: nil,
		},
		{
			name: "GlobPlusLiteral",
			spec: "bb*,nebula",
			want: []string{"bb1", "bb2", "bb3", "nebula"},
		},
		{
			name:    "InvalidGlobPattern",
			spec:    "bb[",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHosts(tt.spec, cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseHosts(%q, %q) error = %v, wantErr = %v", tt.spec, cfg, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ParseHosts(%q, %q) = %v, want %v", tt.spec, cfg, got, tt.want)
			}
		})
	}
}
