package mcpserver

import (
	"context"
	"os"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/files"
)

type FileReadInput struct {
	Host   string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Path   string `json:"path" jsonschema:"远程文件路径，支持绝对路径、~ 展开与相对 SFTP 起始目录的路径"`
	Offset int    `json:"offset,omitempty" jsonschema:"起始行号，从 1 开始，默认 1"`
	Limit  int    `json:"limit,omitempty" jsonschema:"最大返回行数，默认 500，上限 5000"`
}
type FileWriteInput struct {
	Host    string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Path    string `json:"path" jsonschema:"远程文件路径，父目录不存在会自动创建"`
	Content string `json:"content" jsonschema:"写入的完整文件内容，会覆盖原文件"`
	Mode    string `json:"mode,omitempty" jsonschema:"八进制权限，例如 0644 或 0600，默认 0644"`
}
type FileEditInput struct {
	Host           string       `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Path           string       `json:"path" jsonschema:"远程文件路径"`
	Edits          []files.Edit `json:"edits" jsonschema:"按顺序应用的替换列表；每个 old_text 必须在文件中唯一匹配一次"`
	ExpectedSHA256 string       `json:"expected_sha256,omitempty" jsonschema:"乐观锁：上一次 file_read 返回的 sha256，文件已变化时报冲突并放弃写入"`
}
type FileListInput struct {
	Host string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Path string `json:"path,omitempty" jsonschema:"远程目录路径，默认登录用户的起始目录"`
}
type FileListOutput struct {
	Entries []files.Entry `json:"entries"`
}
type FileTransferInput struct {
	SrcHost string `json:"src_host" jsonschema:"源主机名，取自 hosts_list"`
	SrcPath string `json:"src_path" jsonschema:"源文件路径"`
	DstHost string `json:"dst_host" jsonschema:"目标主机名，取自 hosts_list；可与源主机相同"`
	DstPath string `json:"dst_path" jsonschema:"目标文件路径，已存在会被覆盖"`
}

func (s *Server) registerFiles(f *files.Manager) {
	register[FileReadInput, files.ReadResult](s, &mcp.Tool{
		Name:  "file_read",
		Title: "读取远程文件",
		Description: "按行读取远程文本文件，返回带行号的内容、全文 SHA-256、字节数和总行数。" +
			"文件上限 4MiB，检测到二进制内容会拒绝（图片改用 image_view）。默认从第 1 行返回 500 行，用 offset/limit 翻页。" +
			"准备用 file_edit 修改前先读一次，把返回的 sha256 作为 expected_sha256 传回去。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileReadInput) (*mcp.CallToolResult, files.ReadResult, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), files.ReadResult{}, nil
		}
		out, err := f.Read(ctx, in.Host, in.Path, in.Offset, in.Limit)
		if err != nil {
			return errorResult(err.Error()), out, nil
		}
		return nil, out, nil
	})
	register[FileWriteInput, files.WriteResult](s, &mcp.Tool{
		Name:  "file_write",
		Title: "写入远程文件",
		Description: "把 content 作为完整文件内容写到远程主机：先写同目录临时文件再 rename 覆盖，避免读到写了一半的文件；自动创建父目录，返回写入字节数与 SHA-256。" +
			"这是整文件覆盖，只改局部请用 file_edit，否则会丢掉这次没写进 content 的内容。" +
			"目标文件系统不支持覆盖 rename 时会退化为先删后写，结果里 non_atomic=true 并带 warning。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileWriteInput) (*mcp.CallToolResult, files.WriteResult, error) {
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
	register[FileEditInput, files.EditResult](s, &mcp.Tool{
		Name:  "file_edit",
		Title: "结构化编辑远程文件",
		Description: "对远程文本文件做精确替换，是修改配置和代码的首选方式。每个 edit 的 old_text 必须在文件中恰好出现一次，否则整批编辑失败且文件保持原样——匹配不唯一时请把上下文写长一些。" +
			"编辑按数组顺序依次应用，成功后返回统一 diff 和新的 SHA-256。传 expected_sha256（来自 file_read）可开启乐观锁，冲突时应重新 file_read 再重试。" +
			"注意写回后权限固定为 0644，需要其他权限请改用 file_write 并指定 mode。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileEditInput) (*mcp.CallToolResult, files.EditResult, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), files.EditResult{}, nil
		}
		out, err := f.Edit(ctx, in.Host, in.Path, in.Edits, in.ExpectedSHA256)
		if err != nil {
			return errorResult(err.Error()), out, nil
		}
		return nil, out, nil
	})
	register[FileListInput, FileListOutput](s, &mcp.Tool{
		Name:  "file_list",
		Title: "列出远程目录",
		Description: "列出远程目录下的条目：名称、字节数、权限、mtime、是否目录、软链接目标；目录在前，同类按名称排序。" +
			"只列一层，不递归；条目超过 500 会报错，这时请换更精确的目录或改用 find 按 glob 查找。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileListInput) (*mcp.CallToolResult, FileListOutput, error) {
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
	register[FileTransferInput, files.TransferResult](s, &mcp.Tool{
		Name:  "file_transfer",
		Title: "跨主机复制文件",
		Description: "经网关在两台授权主机之间流式复制文件，两端都用网关自己的凭据，无需主机之间互相打通 SSH。" +
			"返回字节数、源 SHA-256 以及目标校验结果（verified）。目标文件存在会被覆盖，整体超时 10 分钟。比在 exec 里拼 scp/rsync 更可靠。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileTransferInput) (*mcp.CallToolResult, files.TransferResult, error) {
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
