package webapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/cryptox"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/files"
	"onessh/internal/hostmanager"
	"onessh/internal/jobs"
	"onessh/internal/memoryx"
	"onessh/internal/monitor"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

type API struct {
	Store   *store.Store
	Box     *cryptox.Box
	Pool    *sshpool.Pool
	Hosts   *hostmanager.Manager
	Exec    *execx.Runner
	Files   *files.Manager
	Jobs    *jobs.Manager
	Monitor *monitor.Manager
	Memory  *memoryx.Engine
	Events  *events.Bus
}

func NewAPI(st *store.Store, box *cryptox.Box, pool *sshpool.Pool, hosts *hostmanager.Manager, exec *execx.Runner, files *files.Manager, jobs *jobs.Manager, mon *monitor.Manager, memory *memoryx.Engine, bus *events.Bus) *API {
	return &API{Store: st, Box: box, Pool: pool, Hosts: hosts, Exec: exec, Files: files, Jobs: jobs, Monitor: mon, Memory: memory, Events: bus}
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hosts", a.hosts)
	mux.HandleFunc("POST /hosts", a.hosts)
	mux.HandleFunc("PUT /hosts/{id}", a.host)
	mux.HandleFunc("DELETE /hosts/{id}", a.host)
	mux.HandleFunc("POST /hosts/{id}/test", a.hostTest)
	mux.HandleFunc("POST /hosts/{id}/reset-fingerprint", a.resetFingerprint)
	mux.HandleFunc("GET /keys", a.keys)
	mux.HandleFunc("POST /keys", a.keys)
	mux.HandleFunc("DELETE /keys/{id}", a.key)
	mux.HandleFunc("GET /tokens", a.tokens)
	mux.HandleFunc("POST /tokens", a.tokens)
	mux.HandleFunc("DELETE /tokens/{id}", a.token)
	mux.HandleFunc("GET /jobs", a.jobsList)
	mux.HandleFunc("POST /jobs/{id}/kill", a.jobKill)
	mux.HandleFunc("GET /audit", a.audit)
	mux.HandleFunc("GET /memories", a.memories)
	mux.HandleFunc("GET /memories/stats", a.memoryStats)
	mux.HandleFunc("DELETE /memories/{id}", a.memory)
	mux.HandleFunc("GET /metrics/{hostID}", a.metrics)
	mux.HandleFunc("GET /sftp/{hostID}/list", a.sftpList)
	mux.HandleFunc("GET /sftp/{hostID}/download", a.sftpDownload)
	mux.HandleFunc("POST /sftp/{hostID}/upload", a.sftpUpload)
	mux.HandleFunc("GET /events", a.sse)
	mux.HandleFunc("GET /ws/terminal", a.terminal)
	return mux
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, err error) {
	jsonOut(w, status, map[string]string{"error": err.Error()})
}
func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}
func hostErrorStatus(err error) int {
	switch hostmanager.KindOf(err) {
	case hostmanager.ErrorInvalid:
		return http.StatusBadRequest
	case hostmanager.ErrorNotFound:
		return http.StatusNotFound
	case hostmanager.ErrorConflict:
		return http.StatusConflict
	case hostmanager.ErrorConnection:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

type hostInput = hostmanager.Input

func (a *API) hosts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		list, err := a.Store.ListHosts(r.Context())
		if err != nil {
			apiError(w, 500, err)
			return
		}
		out := make([]store.HostView, 0, len(list))
		for _, h := range list {
			out = append(out, h.View())
		}
		jsonOut(w, 200, out)
		return
	}
	var in hostInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiError(w, 400, err)
		return
	}
	h, err := a.Hosts.Create(r.Context(), in)
	if err != nil {
		apiError(w, hostErrorStatus(err), err)
		return
	}
	jsonOut(w, 201, h.View())
}
func (a *API) host(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if r.Method == http.MethodDelete {
		if err = a.Hosts.Delete(r.Context(), id); err != nil {
			apiError(w, hostErrorStatus(err), err)
			return
		}
		w.WriteHeader(204)
		return
	}
	var in hostInput
	if err = json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiError(w, 400, err)
		return
	}
	h, err := a.Hosts.Update(r.Context(), id, in)
	if err != nil {
		apiError(w, hostErrorStatus(err), err)
		return
	}
	jsonOut(w, 200, h.View())
}
func (a *API) hostTest(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	result, err := a.Hosts.Test(r.Context(), id, a.Exec)
	if err != nil {
		apiError(w, hostErrorStatus(err), err)
		return
	}
	jsonOut(w, 200, result)
}
func (a *API) resetFingerprint(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if err = a.Hosts.ResetFingerprint(r.Context(), id); err != nil {
		apiError(w, hostErrorStatus(err), err)
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}

type keyInput struct {
	Name       string `json:"name"`
	Mode       string `json:"mode"`
	PrivateKey string `json:"private_key"`
}

func (a *API) keys(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		list, err := a.Store.ListKeys(r.Context())
		if err != nil {
			apiError(w, 500, err)
			return
		}
		jsonOut(w, 200, list)
		return
	}
	var in keyInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiError(w, 400, err)
		return
	}
	var private []byte
	var public string
	switch in.Mode {
	case "generate":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			apiError(w, 500, err)
			return
		}
		block, err := ssh.MarshalPrivateKey(priv, in.Name)
		if err != nil {
			apiError(w, 500, err)
			return
		}
		private = pem.EncodeToMemory(block)
		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			apiError(w, 500, err)
			return
		}
		public = string(ssh.MarshalAuthorizedKey(sshPub))
	case "import":
		private = []byte(in.PrivateKey)
		signer, err := ssh.ParsePrivateKey(private)
		if err != nil {
			apiError(w, 400, fmt.Errorf("解析私钥: %w", err))
			return
		}
		public = string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	default:
		apiError(w, 400, fmt.Errorf("mode 必须是 generate 或 import"))
		return
	}
	enc, err := a.Box.Seal(private)
	for i := range private {
		private[i] = 0
	}
	if err != nil {
		apiError(w, 500, err)
		return
	}
	key, err := a.Store.CreateKey(r.Context(), in.Name, enc, public)
	if err != nil {
		apiError(w, 409, err)
		return
	}
	jsonOut(w, 201, key)
}
func (a *API) key(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if err = a.Store.DeleteKey(r.Context(), id); err != nil {
		apiError(w, 409, err)
		return
	}
	w.WriteHeader(204)
}

type tokenInput struct {
	Name        string  `json:"name"`
	AllHosts    bool    `json:"all_hosts"`
	ManageHosts bool    `json:"manage_hosts"`
	HostIDs     []int64 `json:"host_ids"`
}
type tokenView struct {
	store.Token
	HostIDs    []int64 `json:"host_ids,omitempty"`
	PlainToken string  `json:"token,omitempty"`
}

func (a *API) tokens(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		list, err := a.Store.ListTokens(r.Context())
		if err != nil {
			apiError(w, 500, err)
			return
		}
		out := make([]tokenView, 0, len(list))
		for _, t := range list {
			ids, _ := a.Store.TokenHostIDs(r.Context(), t.ID)
			out = append(out, tokenView{Token: t, HostIDs: ids})
		}
		jsonOut(w, 200, out)
		return
	}
	var in tokenInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apiError(w, 400, err)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		apiError(w, 500, err)
		return
	}
	plain := "osh_" + base64.RawURLEncoding.EncodeToString(raw)
	t, err := a.Store.CreateToken(r.Context(), store.TokenCreate{Name: in.Name, Hash: store.TokenHash(plain), AllHosts: in.AllHosts, ManageHosts: in.ManageHosts, HostIDs: in.HostIDs})
	if err != nil {
		apiError(w, 409, err)
		return
	}
	jsonOut(w, 201, tokenView{Token: t, HostIDs: in.HostIDs, PlainToken: plain})
}
func (a *API) token(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if err = a.Store.DeleteToken(r.Context(), id); err != nil {
		apiError(w, 500, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) jobsList(w http.ResponseWriter, r *http.Request) {
	var hostID *int64
	if name := r.URL.Query().Get("host"); name != "" {
		h, err := a.Store.GetHostByName(r.Context(), name)
		if err != nil {
			apiError(w, 404, err)
			return
		}
		hostID = &h.ID
	}
	list, err := a.Store.ListJobs(r.Context(), nil, hostID)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	out := make([]jobs.Status, 0, len(list))
	for _, j := range list {
		st, e := a.Jobs.Refresh(r.Context(), j)
		if e == nil {
			out = append(out, st)
		}
	}
	jsonOut(w, 200, out)
}
func (a *API) jobKill(w http.ResponseWriter, r *http.Request) {
	j, err := a.Store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	if err = a.Jobs.Kill(r.Context(), j, "TERM"); err != nil {
		apiError(w, 502, err)
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *API) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var tid *int64
	if q.Get("token") != "" {
		x, err := strconv.ParseInt(q.Get("token"), 10, 64)
		if err != nil {
			apiError(w, 400, err)
			return
		}
		tid = &x
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	var ok *bool
	if q.Get("ok") != "" {
		b, err := strconv.ParseBool(q.Get("ok"))
		if err != nil {
			apiError(w, 400, err)
			return
		}
		ok = &b
	}
	list, err := a.Store.ListAudit(r.Context(), tid, q.Get("host"), q.Get("tool"), ok, before, limit)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	jsonOut(w, 200, list)
}
func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "hostID")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	list, err := a.Store.MetricsSince(r.Context(), id, time.Now().Add(-time.Duration(hours)*time.Hour).UnixMilli())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	out := make([]monitor.Snapshot, 0, len(list))
	for _, m := range list {
		out = append(out, monitor.FromMetric(m))
	}
	jsonOut(w, 200, out)
}
func (a *API) hostNameByID(ctx context.Context, raw string) (string, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", err
	}
	h, err := a.Store.GetHost(ctx, id)
	return h.Name, err
}
func (a *API) sftpList(w http.ResponseWriter, r *http.Request) {
	name, err := a.hostNameByID(r.Context(), r.PathValue("hostID"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	items, err := a.Files.List(r.Context(), name, r.URL.Query().Get("path"))
	if err != nil {
		apiError(w, 502, err)
		return
	}
	jsonOut(w, 200, items)
}
func (a *API) sftpDownload(w http.ResponseWriter, r *http.Request) {
	name, err := a.hostNameByID(r.Context(), r.PathValue("hostID"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	p := r.URL.Query().Get("path")
	data, err := a.Files.RawRead(r.Context(), name, p, 100<<20)
	if err != nil {
		apiError(w, 502, err)
		return
	}
	if typ := mime.TypeByExtension(filepath.Ext(p)); typ != "" {
		w.Header().Set("Content-Type", typ)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(p)))
	_, _ = w.Write(data)
}
func (a *API) sftpUpload(w http.ResponseWriter, r *http.Request) {
	name, err := a.hostNameByID(r.Context(), r.PathValue("hostID"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	if err = r.ParseMultipartForm(100 << 20); err != nil {
		apiError(w, 400, err)
		return
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 100<<20+1))
	if err != nil || len(data) > 100<<20 {
		apiError(w, 400, fmt.Errorf("上传文件过大或读取失败"))
		return
	}
	p := r.FormValue("path")
	if strings.HasSuffix(p, "/") || p == "" {
		p += h.Filename
	}
	out, err := a.Files.Write(r.Context(), name, p, data, 0o644)
	if err != nil {
		apiError(w, 502, err)
		return
	}
	jsonOut(w, 201, out)
}
func (a *API) sse(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, fmt.Errorf("不支持 SSE"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, unsubscribe := a.Events.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", e.JSON())
			flusher.Flush()
		}
	}
}
