package webapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"onessh/internal/execx"
)

// 文件管理器接口（WebUI 专用）：列目录、新建、重命名、删除、权限、读写、下载 / 打包、上传。
// 路径一律是远端绝对路径；"~" 与相对路径按登录用户家目录展开。

const (
	lsLimit        = 5000
	editLimit      = 5 << 20
	uploadLimit    = 8 << 30
	sniffLen       = 8192
	remoteOpTimout = 10 * time.Minute
)

type fileEntry struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	Mode          string `json:"mode"`
	Perm          string `json:"perm"`
	Mtime         int64  `json:"mtime"`
	Directory     bool   `json:"directory"`
	Symlink       bool   `json:"symlink"`
	SymlinkTarget string `json:"symlink_target,omitempty"`
	UID           uint32 `json:"uid"`
	GID           uint32 `json:"gid"`
}

type lsResponse struct {
	Path      string      `json:"path"`
	Parent    string      `json:"parent"`
	Home      string      `json:"home"`
	Entries   []fileEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

type httpError struct {
	status int
	err    error
}

func (e *httpError) Error() string { return e.err.Error() }

func failStatus(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		apiError(w, he.status, he.err)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		apiError(w, 404, errors.New("文件或目录不存在"))
		return
	}
	if errors.Is(err, os.ErrPermission) {
		apiError(w, 403, errors.New("没有权限："+err.Error()))
		return
	}
	apiError(w, 502, err)
}

// sftpFor 取请求里的主机并返回它的 SFTP 客户端（连接按主机复用）。
func (a *API) sftpFor(r *http.Request) (string, *sftp.Client, error) {
	name, err := a.hostNameByID(r.Context(), r.PathValue("hostID"))
	if err != nil {
		return "", nil, &httpError{404, errors.New("主机不存在")}
	}
	if a.Files == nil {
		return "", nil, &httpError{500, errors.New("文件服务未初始化")}
	}
	c, err := a.Files.Clients.Get(r.Context(), name)
	if err != nil {
		return "", nil, &httpError{502, fmt.Errorf("连接 %s 失败：%w", name, err)}
	}
	return name, c, nil
}

func remoteAbs(c *sftp.Client, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "~" {
		return c.Getwd()
	}
	if strings.HasPrefix(p, "~/") || !strings.HasPrefix(p, "/") {
		home, err := c.Getwd()
		if err != nil {
			return "", err
		}
		return path.Join(home, strings.TrimPrefix(p, "~/")), nil
	}
	return path.Clean(p), nil
}

func entryOf(c *sftp.Client, dir string, info os.FileInfo) fileEntry {
	full := path.Join(dir, info.Name())
	e := fileEntry{
		Name: info.Name(), Path: full, Size: info.Size(), Mode: info.Mode().String(), Perm: fmt.Sprintf("%04o", info.Mode().Perm()),
		Mtime: info.ModTime().Unix(), Directory: info.IsDir(),
	}
	if st, ok := info.Sys().(*sftp.FileStat); ok {
		e.UID, e.GID = st.UID, st.GID
	}
	if info.Mode()&os.ModeSymlink != 0 {
		e.Symlink = true
		e.SymlinkTarget, _ = c.ReadLink(full)
		// 指向目录的链接要能点进去：用目标的属性覆盖
		if target, err := c.Stat(full); err == nil {
			e.Directory = target.IsDir()
			if !target.IsDir() {
				e.Size = target.Size()
			}
		}
	}
	return e
}

func (a *API) sftpLs(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	dir, err := remoteAbs(c, r.URL.Query().Get("path"))
	if err != nil {
		failStatus(w, err)
		return
	}
	if real, err := c.RealPath(dir); err == nil && real != "" {
		dir = real
	}
	items, err := c.ReadDir(dir)
	if err != nil {
		failStatus(w, err)
		return
	}
	home, _ := c.Getwd()
	out := lsResponse{Path: dir, Parent: path.Dir(dir), Home: home, Entries: make([]fileEntry, 0, min(len(items), lsLimit))}
	if len(items) > lsLimit {
		out.Truncated = true
		items = items[:lsLimit]
	}
	for _, info := range items {
		out.Entries = append(out.Entries, entryOf(c, dir, info))
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].Directory != out.Entries[j].Directory {
			return out.Entries[i].Directory
		}
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	jsonOut(w, 200, out)
}

func (a *API) sftpStat(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	p, err := remoteAbs(c, r.URL.Query().Get("path"))
	if err != nil {
		failStatus(w, err)
		return
	}
	info, err := c.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		jsonOut(w, 200, map[string]any{"exists": false, "path": p})
		return
	}
	if err != nil {
		failStatus(w, err)
		return
	}
	jsonOut(w, 200, map[string]any{"exists": true, "path": p, "entry": entryOf(c, path.Dir(p), info)})
}

func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(v); err != nil {
		return &httpError{400, fmt.Errorf("请求格式错误：%w", err)}
	}
	return nil
}

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return &httpError{400, fmt.Errorf("名称 %q 无效（不能为空，也不能包含 /）", name)}
	}
	if utf8.RuneCountInString(name) > 255 {
		return &httpError{400, errors.New("名称过长")}
	}
	return nil
}

func (a *API) sftpMkdir(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		Path    string `json:"path"`
		Parents bool   `json:"parents"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	p, err := remoteAbs(c, in.Path)
	if err == nil {
		err = validName(path.Base(p))
	}
	if err != nil {
		failStatus(w, err)
		return
	}
	if _, err := c.Lstat(p); err == nil {
		apiError(w, 409, fmt.Errorf("%s 已存在", path.Base(p)))
		return
	}
	if in.Parents {
		err = c.MkdirAll(p)
	} else {
		err = c.Mkdir(p)
	}
	if err != nil {
		failStatus(w, err)
		return
	}
	jsonOut(w, 201, map[string]string{"path": p})
}

func (a *API) sftpCreate(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	p, err := remoteAbs(c, in.Path)
	if err == nil {
		err = validName(path.Base(p))
	}
	if err != nil {
		failStatus(w, err)
		return
	}
	f, err := c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if errors.Is(err, os.ErrExist) {
		apiError(w, 409, fmt.Errorf("%s 已存在", path.Base(p)))
		return
	}
	if err != nil {
		// 部分 SFTP 服务端对 O_EXCL 返回通用错误，再查一次是否已存在
		if _, statErr := c.Lstat(p); statErr == nil {
			apiError(w, 409, fmt.Errorf("%s 已存在", path.Base(p)))
			return
		}
		failStatus(w, err)
		return
	}
	_ = f.Close()
	_ = c.Chmod(p, 0o644)
	jsonOut(w, 201, map[string]string{"path": p})
}

func posixRename(c *sftp.Client, from, to string) error {
	if err := c.PosixRename(from, to); err != nil {
		return c.Rename(from, to)
	}
	return nil
}

func (a *API) sftpRename(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	from, err := remoteAbs(c, in.From)
	if err != nil {
		failStatus(w, err)
		return
	}
	to, err := remoteAbs(c, in.To)
	if err == nil {
		err = validName(path.Base(to))
	}
	if err != nil {
		failStatus(w, err)
		return
	}
	if from == to {
		jsonOut(w, 200, map[string]string{"path": to})
		return
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		apiError(w, 400, errors.New("不能把目录移动到它自己里面"))
		return
	}
	if _, err := c.Lstat(to); err == nil {
		if !in.Overwrite {
			apiError(w, 409, fmt.Errorf("%s 已存在", path.Base(to)))
			return
		}
		err = posixRename(c, from, to)
	} else {
		err = c.Rename(from, to)
	}
	if err != nil {
		failStatus(w, err)
		return
	}
	jsonOut(w, 200, map[string]string{"path": to})
}

var protectedDirs = map[string]bool{
	"bin": true, "boot": true, "dev": true, "etc": true, "home": true, "lib": true, "lib32": true, "lib64": true, "media": true,
	"mnt": true, "opt": true, "proc": true, "root": true, "run": true, "sbin": true, "srv": true, "sys": true, "tmp": true,
	"usr": true, "var": true, "snap": true, "data": true, "www": true,
}

// guardDelete 拦下明显是误操作的删除：根目录、一级系统目录、登录用户的家目录本身。
func guardDelete(p, home string) error {
	clean := path.Clean(p)
	if clean == "/" || clean == "." || clean == "" {
		return &httpError{400, errors.New("拒绝删除根目录")}
	}
	if parts := strings.Split(strings.Trim(clean, "/"), "/"); len(parts) == 1 && protectedDirs[parts[0]] {
		return &httpError{400, fmt.Errorf("拒绝删除系统目录 %s", clean)}
	}
	if home != "" && clean == path.Clean(home) {
		return &httpError{400, errors.New("拒绝删除登录用户的家目录")}
	}
	return nil
}

// remoteRun 在主机上执行一条内部命令（打包、解压、rm -rf 之类），非零退出码视为失败。
func (a *API) remoteRun(ctx context.Context, host, command string) (execx.Result, error) {
	if a.Exec == nil || a.Pool == nil {
		return execx.Result{}, errors.New("命令执行服务未初始化")
	}
	client, err := a.Pool.Get(ctx, host)
	if err != nil {
		return execx.Result{}, err
	}
	res, err := a.Exec.Run(ctx, client, command, "~", nil, execx.Options{Timeout: remoteOpTimout, MaxLines: 60, Tail: true})
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Output)
		if msg == "" {
			msg = fmt.Sprintf("退出码 %d", res.ExitCode)
		}
		return res, errors.New(msg)
	}
	return res, nil
}

func (a *API) removePath(ctx context.Context, host string, c *sftp.Client, p string) error {
	info, err := c.Lstat(p)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return c.Remove(p)
	}
	// 大目录用 rm -rf 比逐个 SFTP 删除快得多；没有执行通道时退回 SFTP 递归
	if _, err := a.remoteRun(ctx, host, "rm -rf -- "+execx.SHQ(p)); err == nil {
		return nil
	}
	return c.RemoveAll(p)
}

type itemResult struct {
	Path  string `json:"path"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (a *API) sftpDelete(w http.ResponseWriter, r *http.Request) {
	host, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		Paths []string `json:"paths"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	if len(in.Paths) == 0 {
		apiError(w, 400, errors.New("没有要删除的项目"))
		return
	}
	home, _ := c.Getwd()
	results := make([]itemResult, 0, len(in.Paths))
	failed := 0
	for _, raw := range in.Paths {
		p, err := remoteAbs(c, raw)
		if err == nil {
			err = guardDelete(p, home)
		}
		if err == nil {
			err = a.removePath(r.Context(), host, c, p)
		}
		if err != nil {
			failed++
			results = append(results, itemResult{Path: raw, Error: err.Error()})
			continue
		}
		results = append(results, itemResult{Path: p, OK: true})
	}
	jsonOut(w, 200, map[string]any{"results": results, "failed": failed})
}

func (a *API) sftpChmod(w http.ResponseWriter, r *http.Request) {
	host, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		Paths     []string `json:"paths"`
		Mode      string   `json:"mode"`
		Recursive bool     `json:"recursive"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	mode, err := strconv.ParseUint(strings.TrimSpace(in.Mode), 8, 32)
	if err != nil || mode > 0o7777 {
		apiError(w, 400, errors.New("权限必须是八进制，例如 755 或 0644"))
		return
	}
	results := make([]itemResult, 0, len(in.Paths))
	failed := 0
	for _, raw := range in.Paths {
		p, err := remoteAbs(c, raw)
		if err == nil {
			if in.Recursive {
				_, err = a.remoteRun(r.Context(), host, fmt.Sprintf("chmod -R %o -- %s", mode, execx.SHQ(p)))
			} else {
				err = c.Chmod(p, os.FileMode(mode))
			}
		}
		if err != nil {
			failed++
			results = append(results, itemResult{Path: raw, Error: err.Error()})
			continue
		}
		results = append(results, itemResult{Path: p, OK: true})
	}
	jsonOut(w, 200, map[string]any{"results": results, "failed": failed})
}

func isBinary(data []byte) bool {
	sniff := data
	if len(sniff) > sniffLen {
		sniff = sniff[:sniffLen]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return true
	}
	// 截断处可能切在多字节字符中间：去掉末尾最多 3 个字节再判断
	for i := 0; i < 4 && len(sniff) > 0; i++ {
		if utf8.Valid(sniff) {
			return false
		}
		sniff = sniff[:len(sniff)-1]
	}
	return !utf8.Valid(sniff)
}

func (a *API) sftpRead(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	p, err := remoteAbs(c, r.URL.Query().Get("path"))
	if err != nil {
		failStatus(w, err)
		return
	}
	info, err := c.Stat(p)
	if err != nil {
		failStatus(w, err)
		return
	}
	if info.IsDir() {
		apiError(w, 400, errors.New("这是一个目录"))
		return
	}
	if info.Size() > editLimit {
		apiError(w, 413, fmt.Errorf("文件 %.1f MiB，超过在线编辑上限 %d MiB，请下载后编辑", float64(info.Size())/(1<<20), editLimit>>20))
		return
	}
	f, err := c.Open(p)
	if err != nil {
		failStatus(w, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, editLimit+1))
	if err != nil {
		failStatus(w, err)
		return
	}
	sum := sha256.Sum256(data)
	out := map[string]any{
		"path": p, "size": len(data), "mtime": info.ModTime().Unix(), "perm": fmt.Sprintf("%04o", info.Mode().Perm()),
		"sha256": hex.EncodeToString(sum[:]), "binary": isBinary(data),
	}
	if out["binary"] == false {
		out["content"] = string(data)
	}
	jsonOut(w, 200, out)
}

// atomicWrite 先写同目录临时文件再改名覆盖，避免读到写了一半的文件。
func atomicWrite(c *sftp.Client, p string, src io.Reader, mode os.FileMode) (int64, error) {
	tmp := path.Join(path.Dir(p), "."+path.Base(p)+".onessh-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	f, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, src)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = c.Remove(tmp)
		return n, errors.Join(copyErr, closeErr)
	}
	_ = c.Chmod(tmp, mode)
	if err := posixRename(c, tmp, p); err != nil {
		// 不支持覆盖式改名的服务端：先删旧文件再改名
		_ = c.Remove(p)
		if err = c.Rename(tmp, p); err != nil {
			_ = c.Remove(tmp)
			return n, err
		}
	}
	return n, nil
}

func (a *API) sftpWrite(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		Path           string `json:"path"`
		Content        string `json:"content"`
		ExpectedSHA256 string `json:"expected_sha256"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	p, err := remoteAbs(c, in.Path)
	if err != nil {
		failStatus(w, err)
		return
	}
	mode := os.FileMode(0o644)
	if info, err := c.Stat(p); err == nil {
		if info.IsDir() {
			apiError(w, 400, errors.New("这是一个目录"))
			return
		}
		mode = info.Mode().Perm()
		if in.ExpectedSHA256 != "" {
			f, err := c.Open(p)
			if err != nil {
				failStatus(w, err)
				return
			}
			h := sha256.New()
			_, err = io.Copy(h, io.LimitReader(f, editLimit*2))
			f.Close()
			if err != nil {
				failStatus(w, err)
				return
			}
			if current := hex.EncodeToString(h.Sum(nil)); current != in.ExpectedSHA256 {
				jsonOut(w, 409, map[string]string{"error": "文件在你打开之后已被修改（可能是 Agent 或其他人改的），请重新加载后再保存", "current_sha256": current})
				return
			}
		}
	}
	data := []byte(in.Content)
	if _, err := atomicWrite(c, p, bytes.NewReader(data), mode); err != nil {
		failStatus(w, err)
		return
	}
	sum := sha256.Sum256(data)
	var mtime int64
	if info, err := c.Stat(p); err == nil {
		mtime = info.ModTime().Unix()
	}
	jsonOut(w, 200, map[string]any{"path": p, "sha256": hex.EncodeToString(sum[:]), "size": len(data), "mtime": mtime})
}

func contentDisposition(kind, name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s", kind, ascii, url.PathEscape(name))
}

// inline 预览只放行不会执行脚本的类型；HTML / SVG 一律按纯文本或附件处理，
// 否则远端一个恶意文件就能在控制台的源上跑脚本。
func inlineType(name string) (string, bool) {
	ext := strings.ToLower(path.Ext(name))
	typ := mime.TypeByExtension(ext)
	switch {
	case ext == ".svg":
		return "image/svg+xml", true
	case strings.HasPrefix(typ, "image/"), strings.HasPrefix(typ, "video/"), strings.HasPrefix(typ, "audio/"), typ == "application/pdf":
		return typ, true
	case ext == ".mkv":
		return "video/x-matroska", true
	}
	return "text/plain; charset=utf-8", true
}

func (a *API) sftpDownload(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	p, err := remoteAbs(c, r.URL.Query().Get("path"))
	if err != nil {
		failStatus(w, err)
		return
	}
	f, err := c.Open(p)
	if err != nil {
		failStatus(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		failStatus(w, err)
		return
	}
	if info.IsDir() {
		apiError(w, 400, errors.New("目录请使用打包下载"))
		return
	}
	name := path.Base(p)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("inline") == "1" {
		typ, _ := inlineType(name)
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Content-Disposition", contentDisposition("inline", name))
		// 即使被当成页面直接打开，也不允许执行脚本。PDF 例外：Chrome 的 PDF 查看器在 sandbox 下拒绝渲染，
		// 而它本身就运行在独立的扩展进程里，不会在控制台的源上执行文件里的脚本
		if typ != "application/pdf" {
			w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
		}
	} else {
		typ := mime.TypeByExtension(path.Ext(name))
		if typ == "" {
			typ = "application/octet-stream"
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
	}
	// sftp.File 可 Seek：ServeContent 顺带支持 HEAD、Range（视频拖动进度条）与断点续传
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// sameParent 校验一组路径位于同一目录，返回父目录与各自的名称（打包与压缩都以父目录为工作目录）。
func sameParent(c *sftp.Client, raw []string) (string, []string, error) {
	if len(raw) == 0 {
		return "", nil, &httpError{400, errors.New("没有选择任何项目")}
	}
	var parent string
	names := make([]string, 0, len(raw))
	for i, item := range raw {
		p, err := remoteAbs(c, item)
		if err != nil {
			return "", nil, err
		}
		if p == "/" {
			return "", nil, &httpError{400, errors.New("不能打包根目录")}
		}
		if i == 0 {
			parent = path.Dir(p)
		} else if path.Dir(p) != parent {
			return "", nil, &httpError{400, errors.New("只能打包同一目录下的项目")}
		}
		names = append(names, path.Base(p))
	}
	return parent, names, nil
}

func shqAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = execx.SHQ(n)
	}
	return strings.Join(quoted, " ")
}

type firstWriteResponse struct {
	w       http.ResponseWriter
	onFirst func()
	wrote   bool
}

func (f *firstWriteResponse) Write(p []byte) (int, error) {
	if !f.wrote {
		f.wrote = true
		f.onFirst()
	}
	return f.w.Write(p)
}

// sftpArchive 把目录或多选项目实时打包成 tar.gz 下载，不在远端落临时文件。
func (a *API) sftpArchive(w http.ResponseWriter, r *http.Request) {
	host, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	parent, names, err := sameParent(c, r.URL.Query()["path"])
	if err != nil {
		failStatus(w, err)
		return
	}
	if a.Pool == nil {
		apiError(w, 500, errors.New("连接池未初始化"))
		return
	}
	client, err := a.Pool.Get(r.Context(), host)
	if err != nil {
		failStatus(w, err)
		return
	}
	session, err := client.NewSession()
	if err != nil {
		failStatus(w, err)
		return
	}
	defer session.Close()
	archive := names[0]
	if len(names) > 1 {
		archive = fmt.Sprintf("%s-%d项", path.Base(parent), len(names))
	}
	var stderr bytes.Buffer
	out := &firstWriteResponse{w: w, onFirst: func() {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", contentDisposition("attachment", archive+".tar.gz"))
		w.WriteHeader(200)
	}}
	session.Stdout, session.Stderr = out, &stderr
	done := make(chan error, 1)
	go func() { done <- session.Run("tar -czf - -C " + execx.SHQ(parent) + " -- " + shqAll(names)) }()
	select {
	case err = <-done:
	case <-r.Context().Done():
		_ = session.Close()
		return
	}
	if err != nil && !out.wrote {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		apiError(w, 502, fmt.Errorf("打包失败：%s", msg))
	}
}

func (a *API) sftpCompress(w http.ResponseWriter, r *http.Request) {
	host, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		Paths   []string `json:"paths"`
		Archive string   `json:"archive"`
		Format  string   `json:"format"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	parent, names, err := sameParent(c, in.Paths)
	if err == nil {
		err = validName(in.Archive)
	}
	if err != nil {
		failStatus(w, err)
		return
	}
	target := path.Join(parent, in.Archive)
	if _, err := c.Lstat(target); err == nil {
		apiError(w, 409, fmt.Errorf("%s 已存在", in.Archive))
		return
	}
	var cmd string
	switch in.Format {
	case "zip":
		cmd = "command -v zip >/dev/null || { echo '目标主机没有安装 zip，可改用 tar.gz' >&2; exit 127; }; cd -- " + execx.SHQ(parent) + " && zip -qr -- " + execx.SHQ(in.Archive) + " " + shqAll(names)
	case "", "tar.gz":
		cmd = "tar -czf " + execx.SHQ(target) + " -C " + execx.SHQ(parent) + " -- " + shqAll(names)
	default:
		apiError(w, 400, errors.New("format 只能是 tar.gz 或 zip"))
		return
	}
	if _, err := a.remoteRun(r.Context(), host, cmd); err != nil {
		apiError(w, 502, fmt.Errorf("压缩失败：%w", err))
		return
	}
	jsonOut(w, 200, map[string]string{"path": target})
}

func (a *API) sftpExtract(w http.ResponseWriter, r *http.Request) {
	host, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	var in struct {
		Path string `json:"path"`
		Dest string `json:"dest"`
	}
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	p, err := remoteAbs(c, in.Path)
	if err != nil {
		failStatus(w, err)
		return
	}
	dest := path.Dir(p)
	if strings.TrimSpace(in.Dest) != "" {
		if dest, err = remoteAbs(c, in.Dest); err != nil {
			failStatus(w, err)
			return
		}
	}
	lower := strings.ToLower(p)
	q, d := execx.SHQ(p), execx.SHQ(dest)
	var cmd string
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		cmd = "tar -xzf " + q + " -C " + d
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		cmd = "tar -xJf " + q + " -C " + d
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		cmd = "tar -xjf " + q + " -C " + d
	case strings.HasSuffix(lower, ".tar.zst"):
		cmd = "tar --zstd -xf " + q + " -C " + d
	case strings.HasSuffix(lower, ".tar"):
		cmd = "tar -xf " + q + " -C " + d
	case strings.HasSuffix(lower, ".zip"):
		cmd = "command -v unzip >/dev/null || { echo '目标主机没有安装 unzip' >&2; exit 127; }; unzip -oq " + q + " -d " + d
	case strings.HasSuffix(lower, ".gz"):
		cmd = "gunzip -kf " + q
	case strings.HasSuffix(lower, ".7z"):
		cmd = "command -v 7z >/dev/null || { echo '目标主机没有安装 7z' >&2; exit 127; }; 7z x -y -o" + d + " " + q
	default:
		apiError(w, 400, errors.New("不支持的压缩格式（支持 tar / tar.gz / tar.xz / tar.bz2 / tar.zst / zip / gz / 7z）"))
		return
	}
	if _, err := a.remoteRun(r.Context(), host, "mkdir -p -- "+d+" && "+cmd); err != nil {
		apiError(w, 502, fmt.Errorf("解压失败：%w", err))
		return
	}
	jsonOut(w, 200, map[string]string{"dest": dest})
}

// sftpUpload 流式写入：multipart 里先给 path（完整目标路径，或以 / 结尾的目录）与 overwrite，再给 file。
// 不把整个文件读进内存，单文件上限 8 GiB。
func (a *API) sftpUpload(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.sftpFor(r)
	if err != nil {
		failStatus(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)
	reader, err := r.MultipartReader()
	if err != nil {
		apiError(w, 400, err)
		return
	}
	dest, overwrite := "", true
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			apiError(w, 400, errors.New("请求里没有文件"))
			return
		}
		if err != nil {
			apiError(w, 400, err)
			return
		}
		switch part.FormName() {
		case "path":
			b, _ := io.ReadAll(io.LimitReader(part, 8<<10))
			dest = string(b)
			continue
		case "overwrite":
			b, _ := io.ReadAll(io.LimitReader(part, 8))
			overwrite = string(b) != "0"
			continue
		case "file":
		default:
			continue
		}
		if dest == "" || strings.HasSuffix(dest, "/") {
			dest += path.Base(part.FileName())
		}
		p, err := remoteAbs(c, dest)
		if err == nil {
			err = validName(path.Base(p))
		}
		if err != nil {
			failStatus(w, err)
			return
		}
		mode := os.FileMode(0o644)
		if info, err := c.Stat(p); err == nil {
			if info.IsDir() {
				apiError(w, 409, fmt.Errorf("%s 是一个已存在的目录", path.Base(p)))
				return
			}
			if !overwrite {
				apiError(w, 409, fmt.Errorf("%s 已存在", path.Base(p)))
				return
			}
			mode = info.Mode().Perm()
		}
		if err := c.MkdirAll(path.Dir(p)); err != nil {
			failStatus(w, err)
			return
		}
		n, err := atomicWrite(c, p, part, mode)
		if err != nil {
			failStatus(w, err)
			return
		}
		jsonOut(w, 201, map[string]any{"path": p, "bytes": n})
		return
	}
}
