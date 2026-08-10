package searchx

import (
	"testing"
	"time"
)

func TestNormalizePlatform(t *testing.T) {
	tests := []struct {
		name, osName, arch, goos, goarch string
		ok                               bool
	}{
		{name: "amd64", osName: "Linux", arch: "x86_64", goos: "linux", goarch: "amd64", ok: true},
		{name: "arm64", osName: "Linux", arch: "aarch64", goos: "linux", goarch: "arm64", ok: true},
		{name: "darwin", osName: "Darwin", arch: "arm64"},
		{name: "riscv", osName: "Linux", arch: "riscv64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			goos, goarch, ok := normalizePlatform(test.osName, test.arch)
			if goos != test.goos || goarch != test.goarch || ok != test.ok {
				t.Fatalf("normalizePlatform(%q, %q) = %q/%q, %v", test.osName, test.arch, goos, goarch, ok)
			}
		})
	}
}

func TestStaleHelper(t *testing.T) {
	now := time.Unix(10_000, 0)
	tests := []struct {
		name    string
		file    string
		modTime time.Time
		want    bool
	}{
		{name: "older", file: helperPrefix + "old", modTime: now.Add(-helperStaleAge - time.Second), want: true},
		{name: "boundary", file: helperPrefix + "boundary", modTime: now.Add(-helperStaleAge)},
		{name: "newer", file: helperPrefix + "new", modTime: now.Add(-time.Minute)},
		{name: "other prefix", file: "other-helper", modTime: now.Add(-2 * helperStaleAge)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := staleHelper(test.file, test.modTime, now); got != test.want {
				t.Fatalf("staleHelper() = %v, want %v", got, test.want)
			}
		})
	}
}
