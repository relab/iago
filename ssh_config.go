package iago

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

var homeDir string

func initHomeDir() (err error) {
	if homeDir != "" {
		return nil
	}
	homeDir, err = os.UserHomeDir()
	return err
}

// ParseSSHConfig returns a ssh configuration object that can be used to create
// a [ssh.ClientConfig] for a given host alias.
func ParseSSHConfig(configFile string) (*sshConfig, error) {
	if configFile == "" {
		return nil, fmt.Errorf("iago: no ssh config file provided")
	}
	if err := initHomeDir(); err != nil {
		return nil, fmt.Errorf("iago: failed to initialize home directory: %w", err)
	}
	fd, err := os.Open(expand(configFile))
	if err != nil {
		return nil, fmt.Errorf("iago: failed to open ssh config file: %w", err)
	}
	defer fd.Close()

	decodedConfig, err := ssh_config.Decode(fd)
	if err != nil {
		return nil, fmt.Errorf("iago: failed to decode ssh config file: %w", err)
	}
	return &sshConfig{decodedConfig}, nil
}

type sshConfig struct {
	config *ssh_config.Config
}

// ClientConfig returns a [ssh.ClientConfig] for the given host alias.
func (cw *sshConfig) ClientConfig(hostAlias string) (*ssh.ClientConfig, error) {
	hostKeyCallback, err := cw.getHostKeyCallback(hostAlias)
	if err != nil {
		return nil, err
	}

	signers := agentSigners()
	identityFile, err := cw.get(hostAlias, "IdentityFile")
	if err != nil {
		return nil, err
	}
	pubkey := fileSigner(identityFile)
	if pubkey != nil {
		signers = append(signers, pubkey)
	}
	if len(signers) == 0 {
		// Cannot authenticate without any signers in ssh agent or the provided identity file.
		// If the identity file contains a passphrase protected private key, this will fail
		// as the passphrase cannot be provided here.
		return nil, fmt.Errorf("iago: no valid authentication methods found for %s", hostAlias)
	}

	usr, err := cw.get(hostAlias, "User")
	if err != nil {
		return nil, err
	}
	if usr == "" {
		// default to the current user if User not specified in the config file
		currentUser, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("iago: failed to get current user: %w", err)
		}
		usr = currentUser.Username
	}

	clientConfig := &ssh.ClientConfig{
		Config:          ssh.Config{},
		User:            usr,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback: hostKeyCallback,
	}
	return clientConfig, nil
}

// ConnectAddr returns the connection address for the given host alias.
// If no hostname is specified in the SSH config, it defaults to the provide host alias.
// An empty string is returned if there was an error retrieving the hostname or port
// for the host alias.
func (cw *sshConfig) ConnectAddr(hostAlias string) string {
	hostname, err := cw.get(hostAlias, "Hostname")
	if err != nil {
		return ""
	}
	// if no hostname is specified, use the host alias (SSH default behavior)
	if hostname == "" {
		hostname = hostAlias
	}
	port, err := cw.get(hostAlias, "Port")
	if err != nil {
		return ""
	}
	return net.JoinHostPort(hostname, port)
}

// get retrieves the value for the specified key for the given host alias.
// If the value is not set in the config file, it returns the default value for that key.
func (cw *sshConfig) get(alias, key string) (string, error) {
	val, err := cw.config.Get(alias, key)
	if err != nil {
		return "", fmt.Errorf("iago: failed to get %s for %s: %w", key, alias, err)
	}
	if val == "" {
		val = ssh_config.Default(key)
	}
	return val, nil
}

// fileSigner returns a SSH signer based on the private key in the specified IdentityFile.
// If the file cannot be read, parsed, or if the private key is passphrase protected, it returns nil.
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

// getHostKeyCallback returns a [ssh.HostKeyCallback] for use with [ssh.ClientConfig].
// If StrictHostKeyChecking is set to "no", host key checking is disabled, ignoring
// any host keys. Otherwise, it creates a host key callback using the known hosts files
// specified by UserKnownHostsFile.
func (cw *sshConfig) getHostKeyCallback(hostAlias string) (hostKeyCallback ssh.HostKeyCallback, err error) {
	strictHostKeyChecking, err := cw.get(hostAlias, "StrictHostKeyChecking")
	if err != nil {
		return nil, err
	}
	if strictHostKeyChecking == "no" {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	userKnownHostsFile, err := cw.get(hostAlias, "UserKnownHostsFile")
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err = createHostKeyCallback(strings.Split(userKnownHostsFile, " "))
	if err != nil {
		return nil, fmt.Errorf("iago: failed to create host key callback for %s: %w", hostAlias, err)
	}
	return hostKeyCallback, nil
}

// createHostKeyCallback returns a HostKeyCallback that checks the host keys against the known hosts files.
// It skips files that do not exist and returns an error if no valid known hosts files are provided.
func createHostKeyCallback(userKnownHostsFilesPaths []string) (ssh.HostKeyCallback, error) {
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
