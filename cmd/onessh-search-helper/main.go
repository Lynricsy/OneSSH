package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"onessh/internal/searchcore"
)

func main() {
	_ = os.Remove(os.Args[0])

	var req searchcore.HelperRequest
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "解码 helper 请求: %v\n", err)
		os.Exit(2)
	}
	if err := searchcore.CheckProtocolVersion(req.Version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp := searchcore.HelperResponse{Version: searchcore.ProtocolVersion}
	switch req.Op {
	case "grep":
		if req.Grep == nil {
			resp.Error = "grep 请求缺少 grep 选项"
			break
		}
		out, err := searchcore.Grep(ctx, searchcore.LocalFS{}, *req.Grep)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Grep = &out
		}
	case "find":
		if req.Find == nil {
			resp.Error = "find 请求缺少 find 选项"
			break
		}
		out, err := searchcore.Find(ctx, searchcore.LocalFS{}, *req.Find)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Find = &out
		}
	default:
		resp.Error = "不支持的搜索操作: " + req.Op
	}

	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "编码 helper 响应: %v\n", err)
		os.Exit(2)
	}
}
