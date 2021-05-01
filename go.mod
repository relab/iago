module github.com/Raytar/iago

go 1.16

require (
	github.com/kevinburke/ssh_config v1.1.0
	github.com/pkg/sftp v1.13.0
	golang.org/x/crypto v0.0.0-20210421170649-83a5a9bb288b
	github.com/Raytar/wrfs v0.0.0
)

replace github.com/Raytar/wrfs => ../wrfs
