package memoryx

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type EmbeddingConfig struct {
	APIURL string
	APIKey string
	Model  string
}

func (c EmbeddingConfig) Enabled() bool {
	return c.APIURL != "" && c.Model != ""
}

type Embedder struct {
	cfg    EmbeddingConfig
	client *http.Client
}

func newEmbedder(cfg EmbeddingConfig) *Embedder {
	cfg.APIURL = strings.TrimRight(cfg.APIURL, "/")
	return &Embedder{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}

func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{Model: e.cfg.Model, Input: []string{text}})
	if err != nil {
		return nil, fmt.Errorf("编码 embedding 请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.APIURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建 embedding 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 embedding: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embedding 服务返回 %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("解析 embedding 响应: %w", err)
	}
	if len(decoded.Data) == 0 || len(decoded.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding 响应缺少向量")
	}
	return decoded.Data[0].Embedding, nil
}

func encodeVector(vector []float32) []byte {
	if len(vector) == 0 {
		return nil
	}
	encoded := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(encoded[i*4:], math.Float32bits(value))
	}
	return encoded
}

func decodeVector(encoded []byte) ([]float32, error) {
	if len(encoded)%4 != 0 {
		return nil, fmt.Errorf("向量字节长度 %d 不是 4 的倍数", len(encoded))
	}
	vector := make([]float32, len(encoded)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[i*4:]))
	}
	return vector, nil
}
