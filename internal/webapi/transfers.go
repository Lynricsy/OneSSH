package webapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"onessh/internal/execx"
)

// 文件复制 / 移动任务：同一台主机内用 cp -a / rename，跨主机由网关经两条 SFTP 连接流式中转，
// 两台主机之间不需要互相打通 SSH。任务在后台跑，前端轮询进度，可随时取消。

const transferKeep = 60

// TransferView 是任务对外的 JSON 视图；Transfer 额外带着进度计数器、锁与取消函数
type TransferView struct {
	ID         string   `json:"id"`
	Op         string   `json:"op"`
	SrcHostID  int64    `json:"src_host_id"`
	SrcHost    string   `json:"src_host"`
	DstHostID  int64    `json:"dst_host_id"`
	DstHost    string   `json:"dst_host"`
	SrcPaths   []string `json:"src_paths"`
	DstDir     string   `json:"dst_dir"`
	Conflict   string   `json:"conflict"`
	Status     string   `json:"status"`
	TotalBytes int64    `json:"total_bytes"`
	DoneBytes  int64    `json:"done_bytes"`
	TotalFiles int64    `json:"total_files"`
	DoneFiles  int64    `json:"done_files"`
	Skipped    int64    `json:"skipped"`
	Current    string   `json:"current"`
	Errors     []string `json:"errors"`
	StartedAt  int64    `json:"started_at"`
	FinishedAt int64    `json:"finished_at,omitempty"`
}

type Transfer struct {
	TransferView
	doneBytes atomic.Int64
	cancel    context.CancelFunc
	mu        sync.Mutex
}

type transferManager struct {
	mu    sync.Mutex
	jobs  map[string]*Transfer
	order []string
}

var (
	transfersOnce sync.Once
	transfers     *transferManager
)

func transferStore() *transferManager {
	transfersOnce.Do(func() { transfers = &transferManager{jobs: map[string]*Transfer{}} })
	return transfers
}

func (t *Transfer) snapshot() TransferView {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := TransferView{
		ID: t.ID, Op: t.Op, SrcHostID: t.SrcHostID, SrcHost: t.SrcHost, DstHostID: t.DstHostID, DstHost: t.DstHost,
		SrcPaths: append([]string(nil), t.SrcPaths...), DstDir: t.DstDir, Conflict: t.Conflict, Status: t.Status,
		TotalBytes: t.TotalBytes, TotalFiles: t.TotalFiles, DoneFiles: t.DoneFiles, Skipped: t.Skipped,
		Current: t.Current, Errors: append([]string{}, t.Errors...), StartedAt: t.StartedAt, FinishedAt: t.FinishedAt,
	}
	out.DoneBytes = t.doneBytes.Load()
	return out
}

func (t *Transfer) set(fn func(*Transfer)) {
	t.mu.Lock()
	fn(t)
	t.mu.Unlock()
}

func (t *Transfer) fail(msg string) {
	t.set(func(x *Transfer) {
		if len(x.Errors) < 200 {
			x.Errors = append(x.Errors, msg)
		}
	})
}

type transferInput struct {
	SrcHostID int64    `json:"src_host_id"`
	SrcPaths  []string `json:"src_paths"`
	DstHostID int64    `json:"dst_host_id"`
	DstDir    string   `json:"dst_dir"`
	Op        string   `json:"op"`
	Conflict  string   `json:"conflict"`
}

func (a *API) transferStart(w http.ResponseWriter, r *http.Request) {
	var in transferInput
	if err := decodeBody(r, &in); err != nil {
		failStatus(w, err)
		return
	}
	if in.Op == "" {
		in.Op = "copy"
	}
	if in.Op != "copy" && in.Op != "move" {
		apiError(w, 400, errors.New("op 只能是 copy 或 move"))
		return
	}
	if in.Conflict == "" {
		in.Conflict = "rename"
	}
	if !strings.Contains("|error|overwrite|rename|skip|", "|"+in.Conflict+"|") {
		apiError(w, 400, errors.New("conflict 只能是 error / overwrite / rename / skip"))
		return
	}
	if len(in.SrcPaths) == 0 || strings.TrimSpace(in.DstDir) == "" {
		apiError(w, 400, errors.New("缺少源路径或目标目录"))
		return
	}
	srcHost, err := a.Store.GetHost(r.Context(), in.SrcHostID)
	if err != nil {
		apiError(w, 404, errors.New("源主机不存在"))
		return
	}
	dstHost, err := a.Store.GetHost(r.Context(), in.DstHostID)
	if err != nil {
		apiError(w, 404, errors.New("目标主机不存在"))
		return
	}
	var id [8]byte
	_, _ = rand.Read(id[:])
	ctx, cancel := context.WithCancel(context.Background())
	job := &Transfer{TransferView: TransferView{
		ID: hex.EncodeToString(id[:]), Op: in.Op, SrcHostID: srcHost.ID, SrcHost: srcHost.Name, DstHostID: dstHost.ID, DstHost: dstHost.Name,
		SrcPaths: in.SrcPaths, DstDir: in.DstDir, Conflict: in.Conflict, Status: "running", Errors: []string{},
		StartedAt: time.Now().Unix(),
	}, cancel: cancel}
	m := transferStore()
	m.mu.Lock()
	m.jobs[job.ID] = job
	m.order = append(m.order, job.ID)
	for len(m.order) > transferKeep {
		old := m.jobs[m.order[0]]
		if old != nil && old.snapshot().Status == "running" {
			break
		}
		delete(m.jobs, m.order[0])
		m.order = m.order[1:]
	}
	m.mu.Unlock()
	go a.runTransfer(ctx, job)
	jsonOut(w, 202, job.snapshot())
}

func (a *API) transferList(w http.ResponseWriter, r *http.Request) {
	m := transferStore()
	m.mu.Lock()
	out := make([]TransferView, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		if job := m.jobs[m.order[i]]; job != nil {
			out = append(out, job.snapshot())
		}
	}
	m.mu.Unlock()
	jsonOut(w, 200, out)
}

func (a *API) transferCancel(w http.ResponseWriter, r *http.Request) {
	m := transferStore()
	m.mu.Lock()
	job := m.jobs[r.PathValue("id")]
	if job != nil && r.URL.Query().Get("remove") == "1" && job.snapshot().Status != "running" {
		delete(m.jobs, job.ID)
		for i, id := range m.order {
			if id == job.ID {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
	}
	m.mu.Unlock()
	if job == nil {
		apiError(w, 404, errors.New("任务不存在"))
		return
	}
	job.cancel()
	w.WriteHeader(204)
}

// freeName 在目录里为 name 找一个不冲突的名字：report.txt → report (1).txt
func freeName(c *sftp.Client, dir, name string) string {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if strings.HasSuffix(strings.ToLower(base), ".tar") {
		ext = base[len(base)-4:] + ext
		base = base[:len(base)-4]
	}
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := c.Lstat(path.Join(dir, candidate)); err != nil {
			return candidate
		}
	}
	return fmt.Sprintf("%s (%d)%s", base, time.Now().Unix(), ext)
}

type countingWriter struct {
	w   io.Writer
	job *Transfer
	ctx context.Context
}

func (c countingWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.w.Write(p)
	c.job.doneBytes.Add(int64(n))
	return n, err
}

func (a *API) runTransfer(ctx context.Context, job *Transfer) {
	defer job.cancel()
	finish := func(status string) {
		job.set(func(x *Transfer) {
			x.Status, x.FinishedAt, x.Current = status, time.Now().Unix(), ""
		})
	}
	src, err := a.Files.Clients.Get(ctx, job.SrcHost)
	if err != nil {
		job.fail("连接源主机失败：" + err.Error())
		finish("failed")
		return
	}
	dst, err := a.Files.Clients.Get(ctx, job.DstHost)
	if err != nil {
		job.fail("连接目标主机失败：" + err.Error())
		finish("failed")
		return
	}
	dstDir, err := remoteAbs(dst, job.DstDir)
	if err == nil {
		err = dst.MkdirAll(dstDir)
	}
	if err != nil {
		job.fail("目标目录不可用：" + err.Error())
		finish("failed")
		return
	}
	same := job.SrcHostID == job.DstHostID

	// 1. 统计总量，用于进度条（同主机移动是瞬时 rename，不需要）
	type item struct{ src, name string }
	items := make([]item, 0, len(job.SrcPaths))
	for _, raw := range job.SrcPaths {
		p, err := remoteAbs(src, raw)
		if err != nil {
			job.fail(raw + "：" + err.Error())
			continue
		}
		if same && (dstDir == p || strings.HasPrefix(dstDir, p+"/")) {
			job.fail(path.Base(p) + "：不能复制或移动到它自己里面")
			continue
		}
		items = append(items, item{src: p, name: path.Base(p)})
	}
	if !(same && job.Op == "move") {
		var total, files int64
		for _, it := range items {
			walker := src.Walk(it.src)
			for walker.Step() {
				if ctx.Err() != nil {
					finish("cancelled")
					return
				}
				if walker.Err() != nil {
					continue
				}
				if st := walker.Stat(); st.Mode().IsRegular() {
					total += st.Size()
					files++
				}
			}
		}
		job.set(func(x *Transfer) { x.TotalBytes, x.TotalFiles = total, files })
	} else {
		job.set(func(x *Transfer) { x.TotalFiles = int64(len(items)) })
	}

	// 2. 逐项处理
	for _, it := range items {
		if ctx.Err() != nil {
			finish("cancelled")
			return
		}
		name := it.name
		target := path.Join(dstDir, name)
		if same && target == it.src {
			if job.Op == "move" {
				job.set(func(x *Transfer) { x.Skipped++ })
				continue
			}
			// 原地复制：自动生成副本名
			name = freeName(dst, dstDir, name)
			target = path.Join(dstDir, name)
		} else if _, err := dst.Lstat(target); err == nil {
			switch job.Conflict {
			case "skip":
				job.set(func(x *Transfer) { x.Skipped++ })
				continue
			case "error":
				job.fail(name + "：目标已存在")
				continue
			case "rename":
				name = freeName(dst, dstDir, name)
				target = path.Join(dstDir, name)
			case "overwrite":
				if err := a.removePath(ctx, job.DstHost, dst, target); err != nil {
					job.fail(name + "：无法覆盖已有项目：" + err.Error())
					continue
				}
			}
		}
		job.set(func(x *Transfer) { x.Current = it.src })

		switch {
		case same && job.Op == "move":
			if err := src.Rename(it.src, target); err != nil {
				// 跨文件系统的 rename 会失败，退回 mv
				if _, err2 := a.remoteRun(ctx, job.SrcHost, "mv -- "+execx.SHQ(it.src)+" "+execx.SHQ(target)); err2 != nil {
					job.fail(name + "：" + err2.Error())
					continue
				}
			}
			job.set(func(x *Transfer) { x.DoneFiles++ })
		case same:
			// 同主机复制交给 cp -a：保留权限、时间与链接，比经网关中转快得多
			before := job.doneBytes.Load()
			if _, err := a.remoteRun(ctx, job.SrcHost, "cp -a -- "+execx.SHQ(it.src)+" "+execx.SHQ(target)); err != nil {
				if ctx.Err() == nil {
					// 没有执行通道时退回 SFTP 中转
					if err2 := a.copyTree(ctx, job, src, dst, it.src, target); err2 != nil {
						job.fail(name + "：" + err2.Error())
					}
				}
				continue
			}
			size := a.treeSize(src, it.src)
			job.doneBytes.Store(before + size.bytes)
			job.set(func(x *Transfer) { x.DoneFiles += size.files })
		default:
			if err := a.copyTree(ctx, job, src, dst, it.src, target); err != nil {
				if ctx.Err() != nil {
					finish("cancelled")
					return
				}
				job.fail(name + "：" + err.Error())
				continue
			}
			if job.Op == "move" {
				if err := a.removePath(ctx, job.SrcHost, src, it.src); err != nil {
					job.fail(name + "：已复制到目标，但删除源文件失败：" + err.Error())
				}
			}
		}
	}
	if ctx.Err() != nil {
		finish("cancelled")
		return
	}
	if len(job.snapshot().Errors) > 0 {
		if job.snapshot().DoneFiles > 0 {
			finish("partial")
		} else {
			finish("failed")
		}
		return
	}
	finish("done")
}

type sizeInfo struct{ bytes, files int64 }

func (a *API) treeSize(c *sftp.Client, root string) sizeInfo {
	var s sizeInfo
	walker := c.Walk(root)
	for walker.Step() {
		if walker.Err() == nil && walker.Stat().Mode().IsRegular() {
			s.bytes += walker.Stat().Size()
			s.files++
		}
	}
	return s
}

// copyTree 经网关把 src 上的文件或目录完整复制到 dst（可以是不同主机），保留权限与修改时间。
func (a *API) copyTree(ctx context.Context, job *Transfer, src, dst *sftp.Client, from, to string) error {
	info, err := src.Lstat(from)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := src.ReadLink(from)
		if err != nil {
			return err
		}
		return dst.Symlink(target, to)
	}
	if !info.IsDir() {
		return a.copyFile(ctx, job, src, dst, from, to, info)
	}
	if err := dst.MkdirAll(to); err != nil {
		return err
	}
	entries, err := src.ReadDir(from)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var firstErr error
	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := a.copyTree(ctx, job, src, dst, path.Join(from, e.Name()), path.Join(to, e.Name())); err != nil {
			if ctx.Err() != nil {
				return err
			}
			job.fail(path.Join(from, e.Name()) + "：" + err.Error())
			if firstErr == nil {
				firstErr = errors.New("部分子项目复制失败")
			}
		}
	}
	_ = dst.Chmod(to, info.Mode().Perm())
	_ = dst.Chtimes(to, info.ModTime(), info.ModTime())
	return firstErr
}

func (a *API) copyFile(ctx context.Context, job *Transfer, src, dst *sftp.Client, from, to string, info os.FileInfo) error {
	job.set(func(x *Transfer) { x.Current = from })
	in, err := src.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := path.Join(path.Dir(to), "."+path.Base(to)+".onessh-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	out, err := dst.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(countingWriter{w: out, job: job, ctx: ctx}, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = dst.Remove(tmp)
		return errors.Join(copyErr, closeErr)
	}
	_ = dst.Chmod(tmp, info.Mode().Perm())
	if err := posixRename(dst, tmp, to); err != nil {
		_ = dst.Remove(to)
		if err = dst.Rename(tmp, to); err != nil {
			_ = dst.Remove(tmp)
			return err
		}
	}
	_ = dst.Chtimes(to, info.ModTime(), info.ModTime())
	job.set(func(x *Transfer) { x.DoneFiles++ })
	return nil
}
