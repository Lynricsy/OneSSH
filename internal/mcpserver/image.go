package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/files"
	"onessh/internal/imagex"
)

type ImageInput struct {
	Host   string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Path   string `json:"path" jsonschema:"远程图片路径，原文件不超过 20MiB"`
	MaxDim int    `json:"max_dim,omitempty" jsonschema:"长边像素上限，默认 1024，上限 2048；超出会等比缩小"`
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
	register[ImageInput, ImageOutput](s, &mcp.Tool{
		Name:  "image_view",
		Title: "查看远程图片",
		Description: "读取远程 PNG/JPEG/GIF/WebP 图片，按 max_dim 等比缩放后作为图片内容直接返回，另附原始与输出尺寸、字节数、MIME 类型。" +
			"用于看截图、监控图表、渲染产物；file_read 遇到二进制会拒绝，改用这个。原文件上限 20MiB，超大像素图会被拒绝。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ImageInput) (*mcp.CallToolResult, ImageOutput, error) {
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
