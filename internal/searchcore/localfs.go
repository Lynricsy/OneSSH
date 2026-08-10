package searchcore

import (
	"io"
	"os"
)

type LocalFS struct{}

func (LocalFS) Getwd() (string, error)                  { return os.Getwd() }
func (LocalFS) Lstat(name string) (os.FileInfo, error)  { return os.Lstat(name) }
func (LocalFS) Open(name string) (io.ReadCloser, error) { return os.Open(name) }
func (LocalFS) ReadDir(name string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	out := make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}
