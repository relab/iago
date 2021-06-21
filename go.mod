module github.com/Raytar/iago

go 1.16

require (
	github.com/Raytar/wrfs v0.0.0
	github.com/alexhunt7/ssher v0.0.0-20190216204854-d36569cf7047
	github.com/containerd/containerd v1.5.2 // indirect
	github.com/docker/docker v20.10.7+incompatible // indirect
	github.com/docker/go-connections v0.4.0
	github.com/kevinburke/ssh_config v1.1.0 // indirect
	github.com/moby/moby v20.10.7+incompatible
	github.com/moby/sys/mount v0.2.0 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/pkg/sftp v1.13.1
	go.uber.org/multierr v1.7.0
	golang.org/x/crypto v0.0.0-20210616213533-5ff15b29337e
)

replace github.com/Raytar/wrfs => ../wrfs
