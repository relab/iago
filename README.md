# Iago

Iago (Infrastructure as Code) is an software deployment framework.
Iago scripts are written in Go.
This means that Iago scripts can be compiled into a simple binary with no dependencies.

## Basic API

Iago executes *tasks* on a *group* of *hosts*.
Tasks are functions that describe the *actions* to be performed on each individual host concurrently.
The `iago.DialSSH` or `iago.NewSSHGroup` can be used to connect to existing SSH servers.

```go
hosts := []string{"host1", "host2", "host3"}
configPath := "/path/to/ssh/config"

// var g iago.Group is a set of iago.Host instances that the task will be applied to.
g, err := iago.NewSSHGroup(hosts, configPath)
if err != nil {
	// handle error
}
defer g.Close()

g.Run("Example Task", func(ctx context.Context, host iago.Host) error {
  // This function will be executed concurrently on each host in the group.
  // The context is used to control the execution of the task.
  log.Println(host.Name())
  return nil
})
```

We currently support connecting to remote hosts via SSH,
but support for other connection methods can be added by implementing the `iago.Host` interface.

Error handling is configured at the group level, using the `ErrorHandler` field of the `iago.Group` struct.
By default, errors cause a panic, but you can set a custom error handler:

```go
g.ErrorHandler = func(e error) {
  log.Printf("Task failed: %v", e)
}
```

### Example

The following example downloads a file from each remote host.
The file is downloaded to a temporary directory created by the test framework and named `os.<hostname>`.
See [iago_test.go](https://github.com/relab/iago/blob/master/iago_test.go#L81) for the complete example.

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
