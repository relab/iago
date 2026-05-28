package iago

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ConnectTimeout is the default timeout for establishing an SSH connection.
// It is used when a host does not set ConnectTimeout in the SSH config.
// The zero value means no timeout, which preserves the prior behavior of ssh.ClientConfig.Timeout.
// Callers can set this to a non-zero value to apply a default dial timeout across all hosts.
var ConnectTimeout time.Duration

var (
	homeDir     string
	homeDirOnce = sync.OnceValues(func() (string, error) {
		return os.UserHomeDir()
	})
)

func initHomeDir() (err error) {
	homeDir, err = homeDirOnce()
	if err != nil {
		return fmt.Errorf("iago: failed to initialize home directory: %w", err)
	}
	return nil
}

// ParseSSHConfig returns a ssh configuration object that can be used to create
// a [ssh.ClientConfig] for a given host alias.
func ParseSSHConfig(configFile string) (*sshConfig, error) {
	if configFile == "" {
		return nil, fmt.Errorf("iago: no ssh config file provided")
	}
	if err := initHomeDir(); err != nil {
		return nil, err
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

	timeout, err := cw.connectTimeout(hostAlias)
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

	username, err := cw.get(hostAlias, "User")
	if err != nil {
		return nil, err
	}
	if username == "" {
		// default to the current user if User not specified in the config file
		currentUser, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("iago: failed to get current user: %w", err)
		}
		username = currentUser.Username
	}

	clientConfig := &ssh.ClientConfig{
		Config:            ssh.Config{},
		User:              username,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signers...)},
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: cw.knownHostAlgorithms(hostAlias),
		Timeout:           timeout,
	}
	return clientConfig, nil
}

// connectTimeout returns the dial timeout for the given host alias.
//
// The timeout is taken from the SSH config's ConnectTimeout option when set.
// Otherwise, the package-level ConnectTimeout default is used.
func (cw *sshConfig) connectTimeout(hostAlias string) (time.Duration, error) {
	connectTimeout, err := cw.get(hostAlias, "ConnectTimeout")
	if err != nil {
		return 0, err
	}
	connectTimeout = strings.TrimSpace(strings.ToLower(connectTimeout))
	if connectTimeout == "" || connectTimeout == "none" {
		return ConnectTimeout, nil
	}
	seconds, err := strconv.Atoi(connectTimeout)
	if err != nil {
		return 0, fmt.Errorf("iago: invalid ConnectTimeout for %s: %q", hostAlias, connectTimeout)
	}
	if seconds < 0 {
		return 0, fmt.Errorf("iago: invalid ConnectTimeout for %s: %q", hostAlias, connectTimeout)
	}
	return time.Duration(seconds) * time.Second, nil
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
	// Expand the %h token that OpenSSH substitutes with the original hostname.
	hostname = strings.ReplaceAll(hostname, "%h", hostAlias)
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

// knownHostAlgorithms returns the host key algorithms present in the known_hosts
// files for the resolved address of hostAlias. The returned slice is used as
// [ssh.ClientConfig.HostKeyAlgorithms] so that Go's crypto/ssh requests only key
// types already recorded in known_hosts during the server handshake.
//
// Without this, Go's crypto/ssh uses its own preference order (ECDSA before
// ED25519), which causes "knownhosts: key mismatch" when the server offers
// multiple key types but only one of them is stored locally.
//
// Returns nil when strict host key checking is disabled or no matching entries
// are found; in those cases the caller must not constrain HostKeyAlgorithms.
func (cw *sshConfig) knownHostAlgorithms(hostAlias string) []string {
	strictHostKeyChecking, err := cw.get(hostAlias, "StrictHostKeyChecking")
	if err != nil || strictHostKeyChecking == "no" {
		return nil
	}
	userKnownHostsFile, err := cw.get(hostAlias, "UserKnownHostsFile")
	if err != nil || userKnownHostsFile == "" {
		return nil
	}
	addr := cw.ConnectAddr(hostAlias)
	if addr == "" {
		return nil
	}
	norm := knownhosts.Normalize(addr)

	var algos []string
	seen := make(map[string]bool)
	for file := range strings.SplitSeq(userKnownHostsFile, " ") {
		file = expand(file)
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for line := range strings.Lines(string(data)) {
			line = strings.TrimSpace(line)
			// Skip comments, blank lines, and markers.
			if line == "" || line[0] == '#' || line[0] == '@' {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			// fields[0] is a comma-separated list of hostname patterns or a
			// single hashed entry in the |1|salt|hash format.
			// fields[1] is the key type (e.g. "ssh-ed25519").
			for h := range strings.SplitSeq(fields[0], ",") {
				var matches bool
				if strings.HasPrefix(h, "|1|") {
					matches = matchesHashedHost(h, norm)
				} else {
					matches = h == norm
				}
				if matches && !seen[fields[1]] {
					algos = append(algos, fields[1])
					seen[fields[1]] = true
					break
				}
			}
		}
	}
	return algos
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

// matchesHashedHost reports whether host matches the hashed known_hosts pattern.
// The pattern must be in the |1|<base64-salt>|<base64-hash> format used by OpenSSH.
// OpenSSH uses HMAC-SHA1 for this format (type "1"); SHA1 is intentional here for
// compatibility with existing known_hosts files and must not be upgraded.
func matchesHashedHost(pattern, host string) bool {
	// Split on "|": a valid hashed entry produces ["", "1", salt, hash].
	parts := strings.SplitN(pattern, "|", 4)
	if len(parts) != 4 || parts[0] != "" || parts[1] != "1" {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	hashBytes, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	mac := hmac.New(sha1.New, salt) //nolint:gosec // SHA1 required by OpenSSH known_hosts format
	mac.Write([]byte(host))
	return hmac.Equal(mac.Sum(nil), hashBytes)
}

func expand(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		return filepath.Join(homeDir, path[2:])
	}
	return path
}

// hostRangeRE matches a single PREFIX[lo-hi]SUFFIX numeric range within a host
// token. Prefix and suffix are captured so non-numeric parts of the alias are
// preserved (e.g. "rack2-node[1-4]").
var hostRangeRE = regexp.MustCompile(`^(.*)\[(\d+)-(\d+)\](.*)$`)

// ParseHosts resolves a comma-separated host specification to a slice of SSH
// host aliases. Each token in the spec is handled as follows:
//
//   - A PREFIX[lo-hi]SUFFIX token with numeric bounds is expanded to individual
//     aliases (e.g. "bb[1-30]" → bb1, bb2, …, bb30) without consulting the SSH
//     config.
//   - A token containing *, ?, or a non-numeric [...] bracket expression is
//     treated as a glob and matched against the non-wildcard Host entries read
//     from configFile. Wildcard SSH stanzas (e.g. "Host bb*") are skipped
//     because they do not enumerate specific host names.
//   - Any other token is returned verbatim as a literal alias.
//
// If configFile is empty, ~/.ssh/config is used. The config file is parsed at
// most once, only when a glob token is encountered.
func ParseHosts(spec, configFile string) ([]string, error) {
	var (
		hosts  []string
		config *sshConfig
	)
	for _, token := range splitHostSpec(spec) {
		if m := hostRangeRE.FindStringSubmatch(token); m != nil {
			expanded, err := expandHostToken(m)
			if err != nil {
				return nil, err
			}
			hosts = append(hosts, expanded...)
		} else if strings.ContainsAny(token, "*?[") {
			// Lazy-load the SSH config only if we encounter a glob token, to avoid
			// unnecessary parsing. We only need to parse once; hence the nil check.
			if config == nil {
				if configFile == "" {
					if err := initHomeDir(); err != nil {
						return nil, err
					}
					configFile = filepath.Join(homeDir, ".ssh", "config")
				}
				var err error
				config, err = ParseSSHConfig(configFile)
				if err != nil {
					return nil, err
				}
			}
			matched, err := config.hostAliases(token)
			if err != nil {
				return nil, err
			}
			hosts = append(hosts, matched...)
		} else {
			hosts = append(hosts, token)
		}
	}
	return hosts, nil
}

// splitHostSpec splits a comma-separated host specification into trimmed,
// non-empty tokens.
func splitHostSpec(spec string) []string {
	parts := strings.Split(strings.TrimSpace(spec), ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expandHostToken expands a matched PREFIX[lo-hi]SUFFIX host range into
// individual aliases. m is the result of hostRangeRE.FindStringSubmatch,
// where m[0] is the full token, m[1] the prefix, m[2] lo, m[3] hi, m[4]
// the suffix. Returns an error if the prefix or suffix contain brackets
// (indicating a second range in the same token) or if the bounds are reversed.
func expandHostToken(m []string) ([]string, error) {
	token, prefix, loStr, hiStr, suffix := m[0], m[1], m[2], m[3], m[4]
	if strings.ContainsAny(prefix, "[]") || strings.ContainsAny(suffix, "[]") {
		return nil, fmt.Errorf("iago: malformed host range %q: at most one [lo-hi] range per host is supported", token)
	}
	lo, err := strconv.Atoi(loStr)
	if err != nil {
		return nil, fmt.Errorf("iago: malformed host range %q: invalid bounds: %w", token, err)
	}
	hi, err := strconv.Atoi(hiStr)
	if err != nil {
		return nil, fmt.Errorf("iago: malformed host range %q: invalid bounds: %w", token, err)
	}
	if lo > hi {
		return nil, fmt.Errorf("iago: malformed host range %q: %d > %d", token, lo, hi)
	}
	// Preserve zero-padding only when at least one bound has a leading zero.
	hasLeadingZero := (len(loStr) > 1 && loStr[0] == '0') || (len(hiStr) > 1 && hiStr[0] == '0')
	width := max(len(loStr), len(hiStr))
	out := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		if hasLeadingZero {
			out = append(out, fmt.Sprintf("%s%0*d%s", prefix, width, i, suffix))
		} else {
			out = append(out, fmt.Sprintf("%s%d%s", prefix, i, suffix))
		}
	}
	return out, nil
}

// hostAliases returns the non-wildcard Host aliases in c that match the given
// glob pattern. Stanzas whose Host patterns contain SSH wildcard characters
// (*, ?, !, []) are skipped because they cannot enumerate specific host names.
// Aliases are returned in config-file order.
func (cw *sshConfig) hostAliases(pattern string) ([]string, error) {
	var hosts []string
	for _, h := range cw.config.Hosts {
		for _, p := range h.Patterns {
			alias := p.String()
			if strings.ContainsAny(alias, "*?![]") {
				continue
			}
			matched, err := filepath.Match(pattern, alias)
			if err != nil {
				return nil, fmt.Errorf("iago: invalid glob pattern %q: %w", pattern, err)
			}
			if matched {
				hosts = append(hosts, alias)
			}
		}
	}
	return hosts, nil
}
