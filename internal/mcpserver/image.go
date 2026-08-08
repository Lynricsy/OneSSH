package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/files"
	"onessh/internal/imagex"
)

type ImageInput struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	MaxDim int    `json:"max_dim,omitempty"`
}
type ImageOutput struct {
	OriginalWidth  int    `json:"original_width"`
	OriginalHeight int    `json:"original_height"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	Bytes          int    `json:"bytes"`
	MIMEType       string `json:"mime_type"`
}

func (s *Server) registerImage(f *files.Manager) {
	register[ImageInput, ImageOutput](s, &mcp.Tool{Name: "image_view", Description: "读取并缩放远程 PNG/JPEG/GIF/WebP 图片"}, func(ctx context.Context, _ *mcp.CallToolRequest, in ImageInput) (*mcp.CallToolResult, ImageOutput, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), ImageOutput{}, nil
		}
		data, err := f.RawRead(ctx, in.Host, in.Path, 20<<20)
		if err != nil {
			return errorResult(err.Error()), ImageOutput{}, nil
		}
		result, err := imagex.Process(data, in.MaxDim)
		if err != nil {
			return errorResult(err.Error()), ImageOutput{}, nil
		}
		meta := ImageOutput{OriginalWidth: result.OriginalWidth, OriginalHeight: result.OriginalHeight, Width: result.Width, Height: result.Height, Bytes: len(result.Data), MIMEType: result.MIMEType}
		text := fmt.Sprintf("原始 %dx%d，输出 %dx%d，%d 字节", meta.OriginalWidth, meta.OriginalHeight, meta.Width, meta.Height, meta.Bytes)
		content := &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: result.Data, MIMEType: result.MIMEType}, &mcp.TextContent{Text: text}}}
		return content, meta, nil
	})
}
