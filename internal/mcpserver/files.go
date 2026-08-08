package mcpserver

import (
	"context"
	"os"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/files"
)

type FileReadInput struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type FileWriteInput struct {
	Host    string `json:"host"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`
}
type FileEditInput struct {
	Host           string       `json:"host"`
	Path           string       `json:"path"`
	Edits          []files.Edit `json:"edits"`
	ExpectedSHA256 string       `json:"expected_sha256,omitempty"`
}
type FileListInput struct {
	Host string `json:"host"`
	Path string `json:"path,omitempty"`
}
type FileListOutput struct {
	Entries []files.Entry `json:"entries"`
}
type FileTransferInput struct {
	SrcHost string `json:"src_host"`
	SrcPath string `json:"src_path"`
	DstHost string `json:"dst_host"`
	DstPath string `json:"dst_path"`
}

func (s *Server) registerFiles(f *files.Manager) {
	register[FileReadInput, files.ReadResult](s, &mcp.Tool{Name: "file_read", Description: "按行读取不超过 4MiB 的远程文本文件"}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileReadInput) (*mcp.CallToolResult, files.ReadResult, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), files.ReadResult{}, nil
		}
		out, err := f.Read(ctx, in.Host, in.Path, in.Offset, in.Limit)
		if err != nil {
			return errorResult(err.Error()), out, nil
		}
		return nil, out, nil
	})
	register[FileWriteInput, files.WriteResult](s, &mcp.Tool{Name: "file_write", Description: "原子写入远程文件并返回 SHA-256"}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileWriteInput) (*mcp.CallToolResult, files.WriteResult, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), files.WriteResult{}, nil
		}
		mode := os.FileMode(0o644)
		if in.Mode != "" {
			v, err := strconv.ParseUint(in.Mode, 8, 32)
			if err != nil {
				return errorResult("mode 必须是八进制权限"), files.WriteResult{}, nil
			}
			mode = os.FileMode(v)
		}
		out, err := f.Write(ctx, in.Host, in.Path, []byte(in.Content), mode)
		if err != nil {
			return errorResult(err.Error()), out, nil
		}
		return nil, out, nil
	})
	register[FileEditInput, files.EditResult](s, &mcp.Tool{Name: "file_edit", Description: "唯一匹配结构化编辑远程文件，支持乐观锁与统一 diff"}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileEditInput) (*mcp.CallToolResult, files.EditResult, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), files.EditResult{}, nil
		}
		out, err := f.Edit(ctx, in.Host, in.Path, in.Edits, in.ExpectedSHA256)
		if err != nil {
			return errorResult(err.Error()), out, nil
		}
		return nil, out, nil
	})
	register[FileListInput, FileListOutput](s, &mcp.Tool{Name: "file_list", Description: "列出远程目录，目录优先排序"}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileListInput) (*mcp.CallToolResult, FileListOutput, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), FileListOutput{}, nil
		}
		if in.Path == "" {
			in.Path = "."
		}
		out, err := f.List(ctx, in.Host, in.Path)
		if err != nil {
			return errorResult(err.Error()), FileListOutput{}, nil
		}
		return nil, FileListOutput{Entries: out}, nil
	})
	register[FileTransferInput, files.TransferResult](s, &mcp.Tool{Name: "file_transfer", Description: "通过网关在两台授权主机间流式传输文件"}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileTransferInput) (*mcp.CallToolResult, files.TransferResult, error) {
		if _, err := AuthorizedHost(ctx, in.SrcHost); err != nil {
			return errorResult(err.Error()), files.TransferResult{}, nil
		}
		if _, err := AuthorizedHost(ctx, in.DstHost); err != nil {
			return errorResult(err.Error()), files.TransferResult{}, nil
		}
		out, err := f.Transfer(ctx, in.SrcHost, in.SrcPath, in.DstHost, in.DstPath)
		if err != nil {
			return errorResult(err.Error()), out, nil
		}
		return nil, out, nil
	})
}
