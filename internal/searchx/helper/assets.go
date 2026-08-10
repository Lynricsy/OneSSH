package helper

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"sync"
)

//go:embed assets
var assetsFS embed.FS

var payloadCache = struct {
	sync.Mutex
	items map[string][]byte
}{items: make(map[string][]byte)}

// Payload 返回解压后的 helper 静态二进制；平台无资产时 ok=false。
func Payload(goos, goarch string) (payload []byte, ok bool) {
	name := "assets/helper_" + goos + "_" + goarch + ".gz"
	payloadCache.Lock()
	defer payloadCache.Unlock()
	if payload, ok := payloadCache.items[name]; ok {
		return payload, true
	}
	compressed, err := fs.ReadFile(assetsFS, name)
	if err != nil {
		return nil, false
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, false
	}
	payload, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, false
	}
	payloadCache.items[name] = payload
	return payload, true
}
