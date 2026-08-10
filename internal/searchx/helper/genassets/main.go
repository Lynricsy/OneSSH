// Command genassets must be run from the repository root.
package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type target struct {
	goos   string
	goarch string
}

func main() {
	targets := []target{
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
	}
	if err := os.MkdirAll("internal/searchx/helper/assets", 0o755); err != nil {
		fatal(err)
	}
	tempDir, err := os.MkdirTemp("", "onessh-search-helper-assets-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(tempDir)

	for _, item := range targets {
		binary := filepath.Join(tempDir, "helper_"+item.goos+"_"+item.goarch)
		command := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", binary, "./cmd/onessh-search-helper")
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+item.goos, "GOARCH="+item.goarch)
		if output, err := command.CombinedOutput(); err != nil {
			fatal(fmt.Errorf("构建 %s/%s helper: %w\n%s", item.goos, item.goarch, err, output))
		}
		if err := compress(binary, filepath.Join("internal/searchx/helper/assets", "helper_"+item.goos+"_"+item.goarch+".gz")); err != nil {
			fatal(fmt.Errorf("压缩 %s/%s helper: %w", item.goos, item.goarch, err))
		}
	}
}

func compress(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	writer, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		output.Close()
		return err
	}
	_, copyErr := io.Copy(writer, input)
	closeWriterErr := writer.Close()
	closeOutputErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeWriterErr != nil {
		return closeWriterErr
	}
	return closeOutputErr
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
