package iago_test

import (
	"testing"

	"github.com/relab/iago"
)

func TestNewPath(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		path    string
		want    string
		wantErr bool
	}{
		{name: "empty path", prefix: "/home/user", path: "", want: "/home/user"},
		{name: "empty prefix", prefix: "", path: "file.txt", want: "", wantErr: true},
		{name: "empty prefix and path", prefix: "", path: "", want: "", wantErr: true},
		{name: "valid", prefix: "/home/user", path: "file.txt", want: "/home/user/file.txt"},
		{name: "valid with trailing slash", prefix: "/home/user/", path: "file.txt", want: "/home/user/file.txt"},
		{name: "invalid absolute path", prefix: "/home/user", path: "/file.txt", want: "", wantErr: true},
		{name: "invalid relative prefix", prefix: "home/user", path: "file.txt", want: "", wantErr: true},
		{name: "invalid prefix and path", prefix: "home/user", path: "/file.txt", want: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := iago.NewPath(tt.prefix, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if got.String() != tt.want {
				t.Errorf("NewPath() = %v, want %v", got.String(), tt.want)
			}
		})
	}
}
