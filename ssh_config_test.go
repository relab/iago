package iago

import (
	"testing"
)

func TestClientConfigDefaults(t *testing.T) {
	config, connectString, err := ClientConfig("asdf", "")
	if err != nil {
		t.Error(err)
	}
	if config == nil || connectString == "" {
		t.Error("Nothing returned, expected default config and connection string")
	}
}

func TestClientConfigLocalhost(t *testing.T) {
	config, connectString, err := ClientConfig("127.0.1.2", "testdata/config")
	if err != nil {
		t.Error(err)
	}
	wantConnectString := "127.0.0.1:22"
	if connectString != wantConnectString {
		t.Errorf("ClientConfig(127.0.1.2): connectString %v, want %v", connectString, wantConnectString)
	}
	if len(config.Auth) < 1 {
		t.Errorf("ClientConfig(127.0.1.2): config.Auth has no methods")
	}
	if config.User != "testuser" {
		t.Errorf("ClientConfig(127.0.1.2): config.User %v, want %v", config.User, "testuser")
	}
}

func TestClientConfigMissing(t *testing.T) {
	_, _, err := ClientConfig("asdf", "testdata/config-missing")
	if err == nil {
		t.Errorf("ClientConfig(config-missing): nil, want error")
	}
}

func TestClientConfigBad(t *testing.T) {
	_, _, err := ClientConfig("asdf", "testdata/config-bad")
	if err == nil {
		t.Errorf("ClientConfig(config-bad): nil, want error")
	}
}
