package iago

import (
	"testing"
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
	}{
		{"localhost", "localhost", "testuser"},
		{"127.0.1.2_Matches_127.*", "127.0.1.2", "testuser"},
		{"Connect*PrefixMatches", "ConnectTest", "testuser2"},
		{"ArbitraryHostMatchesWildcard", "asdf", "testuser2"},
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
			if len(clientConfig.Auth) < 1 {
				t.Errorf("ClientConfig(%s): Auth has no methods", tt.hostAlias)
			}
		})
	}
}
