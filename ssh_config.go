package iago

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	once                      sync.Once
	username, homeDir, sshDir string = initUserAndPaths()
)

func initUserAndPaths() (username, homeDir, sshDir string) {
	once.Do(func() {
		currentUser, err := user.Current()
		if err != nil {
			panic("failed to get current user: " + err.Error())
		}
		username = currentUser.Username
		homeDir, err = os.UserHomeDir()
		if err != nil {
			panic("failed to get user home directory: " + err.Error())
		}
		sshDir = filepath.Join(homeDir, ".ssh")
		if _, err := os.Stat(sshDir); errors.Is(err, fs.ErrNotExist) {
			panic("ssh directory does not exist: " + sshDir)
		}
	})
	return username, homeDir, sshDir
}

// ClientConfig returns a [ssh.ClientConfig] and a connection string (for dialing)
// for the given host alias. If no configFile is provided, the default ssh config
// file paths are used: ~/.ssh/config and /etc/ssh/ssh_config.
func ClientConfig(hostAlias string, configFile string) (*ssh.ClientConfig, string, error) {
	configFile = cmp.Or(configFile, filepath.Join(sshDir, "config"), filepath.Join("/", "etc", "ssh", "ssh_config"))
	userConfig, err := decodeSSHConfig(configFile)
	if err != nil {
		return nil, "", err
	}

	userKnownHostsFile := userConfig.get(hostAlias, "UserKnownHostsFile", "")
	hostKeyCallback, err := getHostKeyCallback(strings.Split(userKnownHostsFile, " "))
	if err != nil {
		return nil, "", fmt.Errorf("failed to create host key callback: %w", err)
	}

	signers := agentSigners()
	identityFile := userConfig.get(hostAlias, "IdentityFile", "")
	pubkey := fileSigner(identityFile)
	if pubkey != nil {
		signers = append(signers, pubkey)
	}

	clientConfig := &ssh.ClientConfig{
		Config:          ssh.Config{},
		User:            userConfig.get(hostAlias, "User", username),
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: hostKeyCallback,
	}
	return clientConfig, userConfig.connect(hostAlias), nil
}

func decodeSSHConfig(configFile string) (*configWrapper, error) {
	fd, err := os.Open(expand(configFile))
	if err != nil {
		return nil, fmt.Errorf("failed to open ssh config file: %w", err)
	}
	defer fd.Close()

	decodedConfig, err := ssh_config.Decode(fd)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ssh config file: %w", err)
	}
	return &configWrapper{decodedConfig}, nil
}

type configWrapper struct {
	config *ssh_config.Config
}

func (cw *configWrapper) get(alias, key, defaultValue string) string {
	val, err := cw.config.Get(alias, key)
	if err != nil {
		if defaultValue == "" {
			return ssh_config.Default(key)
		}
		return defaultValue
	}
	return val
}

func (cw *configWrapper) connect(hostAlias string) string {
	hostname := cw.get(hostAlias, "Hostname", hostAlias)
	port := cw.get(hostAlias, "Port", "")
	return net.JoinHostPort(hostname, port)
}

// fileSigner returns a SSH signer based on the private key in the specified IdentityFile.
// If the file cannot be read or parsed, it returns nil.
func fileSigner(file string) ssh.Signer {
	buffer, err := os.ReadFile(expand(file))
	if err != nil {
		return nil
	}
	key, err := ssh.ParsePrivateKey(buffer)
	if err != nil {
		return nil
	}
	return key
}

// agentSigners returns a list of SSH signers obtained from the SSH agent.
// It returns nil if there are no signers available or if there is an error connecting to the agent.
func agentSigners() []ssh.Signer {
	if sshAgent, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		signers, err := agent.NewClient(sshAgent).Signers()
		if err != nil {
			return nil
		}
		return signers
	}
	return nil
}

func getHostKeyCallback(userKnownHostsFilesPaths []string) (ssh.HostKeyCallback, error) {
	var userKnownHostsFiles []string
	for _, file := range userKnownHostsFilesPaths {
		file = expand(file)
		if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		userKnownHostsFiles = append(userKnownHostsFiles, file)
	}
	return knownhosts.New(userKnownHostsFiles...)
}

func expand(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		return filepath.Join(homeDir, path[2:])
	}
	return path
}
