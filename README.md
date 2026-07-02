# Infrastructure as Code (Iago)

Iago is a lightweight software deployment framework.
Iago scripts are written in Go and compiled into a single binary.
It supports executing tasks concurrently across multiple hosts, such as uploading and downloading files, running commands, and managing services.

## Basic API

Iago executes *tasks* on a *group* of *hosts*.
Tasks are functions that describe the *actions* to be performed on each individual host concurrently.
Use `iago.NewSSHGroup` to connect to remote hosts defined in an SSH config file,
or `iago.DialSSH` to connect to a single host directly.

```go
hosts := []string{"wrk1", "wrk2", "wrk3"}
configPath := "/path/to/ssh/config"

g, err := iago.NewSSHGroup(hosts, configPath)
if err != nil {
	// handle error
}
defer g.Close()

g.Run("Example Task", func(ctx context.Context, host iago.Host) error {
	// Executed concurrently on each host.
	log.Println(host.Name())
	return nil
})
```

Support for other connection methods can be added by implementing the `iago.Host` interface.

Error handling is configured at the group level using the `ErrorHandler` field.
By default, errors cause a panic, but you can set a custom handler:

```go
g.ErrorHandler = func(e error) {
	log.Printf("Task failed: %v", e)
}
```

## Agent forwarding and keepalives

Pass `iago.ForwardAgent()` to `NewSSHGroup` to forward the local SSH agent to every
host in the group, regardless of the `ForwardAgent` setting in the SSH config file —
equivalent to `ssh -A`. This lets a remote host authenticate onward to other hosts
using the caller's local agent, for example when a driver node SSHes into peer nodes.
Forwarding can also be enabled per host via `ForwardAgent yes` in the SSH config file.

Pass `iago.KeepAlive(interval)` to send a periodic SSH keepalive on every dialed
connection, so an idle connection (for example, a control channel streaming a long,
quiet remote run) is not silently dropped by a NAT or firewall idle timeout:

```go
g, err := iago.NewSSHGroup(hosts, configPath, iago.ForwardAgent(), iago.KeepAlive(30*time.Second))
```

## SSH config files

`iago.NewSSHGroup` reads an OpenSSH-style config file (defaulting to `~/.ssh/config`).
It honours the following per-host options: `Hostname`, `Port`, `User`, `IdentityFile`,
`ProxyJump`, `ConnectTimeout`, `StrictHostKeyChecking`, `UserKnownHostsFile`, and
`ForwardAgent`.
OpenSSH's **first-match-wins** rule applies: the first `Host` stanza that matches a given
alias wins for each option.

Connections must be passphrase-free at connect time.
Load keys into `ssh-agent` ahead of time (entering the passphrase once):

```sh
ssh-add
```

Alternatively, point `IdentityFile` at a passphrase-less private key.

### Example config

The following config connects 15 workers through a bastion host using a single wildcard stanza:

```ssh-config
Host *
  IdentityFile ~/.ssh/id_ed25519
  UserKnownHostsFile ~/.ssh/known_hosts

Host bastion
  User deploy
  HostName bastion.example.com

Host wrk*
  HostName %h.cluster.example.com
  User deploy
  ProxyJump bastion
  StrictHostKeyChecking no
```

The `%h` token expands to the alias, so `wrk7` resolves to `wrk7.cluster.example.com`.
No per-host stanzas are needed for the workers — the wildcard covers all of them.

### Resolving host aliases with ParseHosts

`iago.ParseHosts` resolves a comma-separated host spec to a slice of SSH aliases
suitable for passing to `NewSSHGroup`. Each token is handled as follows:

| Form | Example | Best for |
| --- | --- | --- |
| Literal list | `atlas,titan,helios` | A small, fixed set of irregularly named hosts |
| Numeric range | `wrk[1-15]` | Numerically named hosts; no config lookup needed |
| Glob | `gpu-*` | Irregularly named hosts that share a role prefix; config is the membership list |

```go
aliases, err := iago.ParseHosts("wrk[1-15]", configPath)
// aliases == []string{"wrk1", "wrk2", ..., "wrk15"}

g, err := iago.NewSSHGroup(aliases, configPath)
```

The numeric range form is usually the cleanest for regular names: it expands without
consulting the config file and works even when only a wildcard stanza is present.

The glob form is most useful when host names are irregular — for example, GPU nodes
named after Greek titans rather than by number. The config file becomes the source of
truth for cluster membership: adding or removing a `Host` stanza automatically changes
what the glob returns, with no code change required.

```ssh-config
Host gpu-atlas gpu-titan gpu-helios
  HostName %h.cluster.example.com
  User deploy
  ProxyJump bastion
  StrictHostKeyChecking no
```

```go
aliases, err := iago.ParseHosts("gpu-*", configPath)
// aliases == []string{"gpu-atlas", "gpu-titan", "gpu-helios"}
```

Multiple space-separated aliases can share one stanza; `%h` still expands per-alias.
When a fourth GPU node is added to the config, `gpu-*` picks it up without touching
any Go code.

### ProxyJump and connection sharing

When `Host wrk1` has `ProxyJump bastion`, iago dials `bastion` first and tunnels
the connection to `wrk1` through it. `NewSSHGroup` dials each unique `ProxyJump`
target **once** and reuses that connection for every alias that routes through it.
A group of 15 workers that all share `ProxyJump bastion` opens exactly **one**
TCP/SSH connection to `bastion` — equivalent to what OpenSSH's `ControlMaster` /
`ControlPersist` achieves for the system `ssh` client, without requiring a background
process or a Unix-domain socket.

The shared connection is owned by the `Group` and closed by `group.Close()`.
Closing an individual `iago.Host` closes only that host's tunnel.

## Collecting results and errors

`Group.Run` runs a task on every host but discards each host's return value beyond
passing it to `ErrorHandler`. `iago.Collect` is the value-returning counterpart: it
runs a function that returns `(T, error)` on every host and returns a
`map[string]T` keyed by host name, plus a joined error for any host that failed.

```go
versions, err := iago.Collect(g, "Get kernel version", func(ctx context.Context, host iago.Host) (string, error) {
	return iago.Output(ctx, host, "uname -r")
})
```

`iago.Output` is a convenience wrapper around `Shell` for the common case of
wanting a command's captured stdout as a string rather than streaming it to a
caller-provided writer.

By default, a group's `ErrorHandler` panics on the first task error. Pass
`iago.WithErrorHandler` to `NewSSHGroup` to collect errors instead, using the
`iago.Errors` accumulator:

```go
var errs iago.Errors
g, err := iago.NewSSHGroup(hosts, configPath, iago.WithErrorHandler(errs.Handle))
// ...
g.Run("task", task)
return errs.Err()
```

`Collect` uses this pattern internally, so its returned error is already joined
the same way.

## Shell command helpers

`iago.Quote` wraps a string in single quotes so it is safe to embed as one
argument in a `Shell` command run on a POSIX shell.

`iago.FileExists` and `iago.DirExists` check whether a path exists on a remote
host, backed by `test -f` / `test -d`:

```go
ok, err := iago.FileExists(ctx, host, "/etc/os-release")
```

Both distinguish "the path fails the test" (returns `false, nil`) from a
genuine failure such as a transport error (returns `false, err`), using the
`iago.ExitStatus` interface to inspect a remote command's exit code without
importing `golang.org/x/crypto/ssh` directly.

## UploadFile

`iago.UploadFile` is a convenience wrapper around `Upload` for a single file,
handling the `Path` conversion for an already-absolute local path and an
absolute remote path so callers do not repeat that boilerplate at every call
site:

```go
err := iago.UploadFile(ctx, host, "/local/path/binary", "/remote/path/binary", iago.NewPerm(0o755))
```

## Example

The following example downloads a file from each remote host.
The file is downloaded to a temporary directory created by the test framework and named `os.<hostname>`.
See [iago_test.go](https://github.com/relab/iago/blob/master/iago_test.go#L81) for the complete example with logging.

This example uses the `iagotest` package, which spawns docker containers and connects to them with SSH for testing.

```go
func TestIago(t *testing.T) {
	dir := t.TempDir()

	// The iagotest package provides a helper function that automatically
	// builds and starts docker containers with an exposed SSH port for testing.
	g := iagotest.CreateSSHGroup(t, 4, false)

	g.Run("Download files", func(ctx context.Context, host iago.Host) error {
		src, err := iago.NewPath("/etc", "os-release")
		if err != nil {
			return err
		}
		dest, err := iago.NewPath(dir, "os")
		if err != nil {
			return err
		}
		return iago.Download{
			Src:  src,
			Dest: dest,
			Perm: iago.NewPerm(0o644),
		}.Apply(ctx, host)
	})
}
```
