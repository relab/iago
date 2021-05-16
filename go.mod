module github.com/Raytar/iago

go 1.16

require (
	github.com/Raytar/wrfs v0.0.0
	github.com/containerd/containerd v1.5.1 // indirect
	github.com/docker/docker v20.10.6+incompatible
	github.com/docker/go-connections v0.4.0
	github.com/kevinburke/ssh_config v1.1.0
	github.com/moby/moby v20.10.6+incompatible
	github.com/moby/sys/mount v0.2.0 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/pkg/sftp v1.13.0
	go.uber.org/multierr v1.6.0
	golang.org/x/crypto v0.0.0-20210421170649-83a5a9bb288b
)

replace github.com/Raytar/wrfs => ../wrfs
