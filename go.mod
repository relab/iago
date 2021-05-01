module github.com/Raytar/iago

go 1.16

require (
	github.com/Raytar/wrfs v0.0.0
	github.com/kevinburke/ssh_config v1.1.0
	github.com/pkg/sftp v1.13.0
	go.uber.org/multierr v1.6.0 // indirect
	golang.org/x/crypto v0.0.0-20210421170649-83a5a9bb288b
)

replace github.com/Raytar/wrfs => ../wrfs
