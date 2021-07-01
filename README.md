# Iago

Iago (**I**nfrastructure **A**s **GO**) is an experimental software deployment framework.
Iago scripts are written in Go. This means that Iago scripts can be compiled into a simple binary with no dependencies.

## Basic API

Iago executes *tasks* on *groups* of *hosts*.
Tasks describe *actions* to be performed to each individual host, and how to handle errors.
We currently support connecting to remote hosts via SSH,
but support for other connection methods can be added by implementing the `Host` interface.

### Example

The following example downloads a file from each remote host.
This example uses the `iagotest` package, which spawns docker containers and connects to them with SSH for testing.
The `iago.DialSSH` or `iago.NewSSHGroup` can be used to connect to existing SSH servers.

```go
func TestIago(t *testing.T) {
  dir, _ := os.Getwd()

  // The iagotest package provides a helper function that automatically
  // builds and starts docker containers with an exposed SSH port for testing.
  g := iagotest.CreateSSHGroup(t, 4)

  g.Run(iago.Task{
    Name: "Download files",
    Action: iago.Download{
      Src:  iago.P("/etc/os-release"),
      Dest: iago.P("os").RelativeTo(dir),
      Mode: 0644,
    },
    OnError: iago.Panic,
  }
}
```

## TODO

* Improve the reading of `ssh_config` files
  * Should support reading the public key files (it doesn't look like the `ssher` package supports this fully)
* Prompt the user when verifying host keys?
* Improve logging
  * Logging for tasks as they are running
  * Progress -- how many hosts have finished a task
