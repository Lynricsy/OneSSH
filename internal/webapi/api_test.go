package webapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"onessh/internal/cryptox"
	"onessh/internal/execx"
	"onessh/internal/hostmanager"
	"onessh/internal/memoryx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestAdminKeyHostTokenLifecycle(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	hosts := hostmanager.New(st, box, pool)
	api := NewAPI(st, box, pool, hosts, nil, nil, nil, nil, memoryx.New(st, memoryx.EmbeddingConfig{}), nil).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	key := call(http.MethodPost, "/keys", `{"name":"generated","mode":"generate"}`)
	if key.Code != http.StatusCreated {
		t.Fatalf("创建密钥 %d %s", key.Code, key.Body.String())
	}
	var keyOut map[string]any
	if json.Unmarshal(key.Body.Bytes(), &keyOut) != nil || keyOut["public_key"] == "" {
		t.Fatalf("公钥响应 %s", key.Body.String())
	}
	if strings.Contains(key.Body.String(), "PRIVATE KEY") {
		t.Fatal("响应泄漏私钥")
	}
	host := call(http.MethodPost, "/hosts", `{"name":"ssh","addr":"127.0.0.1","port":2222,"username":"test","auth_type":"password","password":"pass","tags":[" prod ","web","web"]}`)
	if host.Code != http.StatusCreated {
		t.Fatalf("创建主机 %d %s", host.Code, host.Body.String())
	}
	if strings.Contains(host.Body.String(), `"password":"pass"`) || strings.Contains(host.Body.String(), "password_enc") {
		t.Fatal("响应泄漏密码")
	}
	var hostOut map[string]any
	if err := json.Unmarshal(host.Body.Bytes(), &hostOut); err != nil {
		t.Fatal(err)
	}
	tags, ok := hostOut["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "prod" || tags[1] != "web" {
		t.Fatalf("创建主机响应标签异常: %s", host.Body.String())
	}
	hostList := call(http.MethodGet, "/hosts", "")
	if hostList.Code != http.StatusOK {
		t.Fatalf("列出主机 %d %s", hostList.Code, hostList.Body.String())
	}
	var hostsOut []map[string]any
	if err := json.Unmarshal(hostList.Body.Bytes(), &hostsOut); err != nil {
		t.Fatal(err)
	}
	if len(hostsOut) != 1 {
		t.Fatalf("主机列表数量 = %d", len(hostsOut))
	}
	if listedTags, ok := hostsOut[0]["tags"].([]any); !ok || len(listedTags) != 2 {
		t.Fatalf("主机列表标签异常: %s", hostList.Body.String())
	}
	token := call(http.MethodPost, "/tokens", `{"name":"manager","all_hosts":false,"manage_hosts":true,"host_ids":[]}`)
	if token.Code != http.StatusCreated {
		t.Fatalf("创建管理令牌 %d %s", token.Code, token.Body.String())
	}
	var tokenOut map[string]any
	_ = json.Unmarshal(token.Body.Bytes(), &tokenOut)
	if !strings.HasPrefix(tokenOut["token"].(string), "osh_") || tokenOut["manage_hosts"] != true {
		t.Fatalf("管理令牌响应 %s", token.Body.String())
	}
	ordinary := call(http.MethodPost, "/tokens", `{"name":"ordinary","all_hosts":true}`)
	if ordinary.Code != http.StatusCreated {
		t.Fatalf("创建默认令牌 %d %s", ordinary.Code, ordinary.Body.String())
	}
	var ordinaryOut map[string]any
	_ = json.Unmarshal(ordinary.Body.Bytes(), &ordinaryOut)
	if ordinaryOut["manage_hosts"] != false {
		t.Fatalf("默认令牌意外获得管理权限: %s", ordinary.Body.String())
	}
	list := call(http.MethodGet, "/tokens", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "osh_") {
		t.Fatalf("列表泄漏令牌 %s", list.Body.String())
	}
	var listed []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	permissions := make(map[string]bool, len(listed))
	for _, item := range listed {
		permissions[item["name"].(string)] = item["manage_hosts"].(bool)
	}
	if !permissions["manager"] || permissions["ordinary"] {
		t.Fatalf("令牌列表权限异常: %#v", permissions)
	}
}

func TestAuditFiltersUseRepeatedQueryValues(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, row := range []store.Audit{
		{Ts: 1, Tool: "exec", Host: sql.NullString{String: "edge,west", Valid: true}, ParamsJSON: "{}", OK: true},
		{Ts: 2, Tool: "file_read", Host: sql.NullString{String: "edge,west", Valid: true}, ParamsJSON: "{}", OK: true},
		{Ts: 3, Tool: "job_list", Host: sql.NullString{String: "other", Valid: true}, ParamsJSON: "{}", OK: true},
	} {
		if err := st.AddAudit(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	handler := (&API{Store: st}).Handler()
	query := url.Values{
		"host": {"edge,west"},
		"tool": {"exec", "file_read"},
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/audit?"+query.Encode(), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("审计筛选状态码 = %d: %s", response.Code, response.Body.String())
	}
	var audit []store.Audit
	if err := json.Unmarshal(response.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	if len(audit) != 2 || audit[0].Tool != "file_read" || audit[1].Tool != "exec" {
		t.Fatalf("审计筛选结果 = %#v", audit)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/audit/tools", nil))
	if response.Code != http.StatusOK || response.Body.String() != "[\"exec\",\"file_read\",\"job_list\"]\n" {
		t.Fatalf("审计工具列表 = %d %s", response.Code, response.Body.String())
	}

	tooMany := make([]string, store.MaxAuditFilterValues+1)
	for i := range tooMany {
		tooMany[i] = "host"
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/audit?"+url.Values{"host": tooMany}.Encode(), nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("超大审计筛选状态码 = %d: %s", response.Code, response.Body.String())
	}
}

func TestHostErrorStatus(t *testing.T) {
	tests := []struct {
		kind hostmanager.ErrorKind
		want int
	}{
		{kind: hostmanager.ErrorInvalid, want: http.StatusBadRequest},
		{kind: hostmanager.ErrorNotFound, want: http.StatusNotFound},
		{kind: hostmanager.ErrorConflict, want: http.StatusConflict},
		{kind: hostmanager.ErrorConnection, want: http.StatusBadGateway},
		{kind: hostmanager.ErrorInternal, want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		err := &hostmanager.Error{Kind: test.kind, Err: errors.New("测试错误")}
		if got := hostErrorStatus(err); got != test.want {
			t.Fatalf("错误类型 %d 状态码 = %d，期望 %d", test.kind, got, test.want)
		}
	}
}

func TestCommandRunListDetailAndOutput(t *testing.T) {
	ctx := t.Context()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runner := execx.New(dataDir)
	id := uuid.NewString()
	run := store.CommandRun{ID: id, Tool: "exec", Host: "web-01", Command: "printf hello", Cwd: "/srv", StartedAt: 1000}
	if err = st.CreateCommandRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishCommandRun(ctx, id, store.CommandRunFinish{
		Status: "succeeded", ExitCode: sql.NullInt64{Int64: 0, Valid: true},
		StdoutPreview: "hello", StdoutBytes: 5, OutputAvailable: true, FinishedAt: 1100,
	}); err != nil {
		t.Fatal(err)
	}
	path, err := runner.CommandOutputPath(id, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderrPath, _ := runner.CommandOutputPath(id, "stderr")
	if err = os.WriteFile(stderrPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := (&API{Store: st, Exec: runner}).Handler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/command-runs?host=web-01&status=succeeded&q=HELLO", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"command":"printf hello"`) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("命令列表响应 = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "stdout_preview") {
		t.Fatalf("列表不应携带输出预览: %s", response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/command-runs/"+id, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"stdout_preview":"hello"`) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("命令详情响应 = %d %s headers=%v", response.Code, response.Body.String(), response.Header())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/command-runs/"+id+"/output?stream=stdout&offset_bytes=1&limit_bytes=3", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"ell"`) || !strings.Contains(response.Body.String(), `"complete":false`) {
		t.Fatalf("命令输出响应 = %d %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/command-runs/"+id+"/output?stream=combined", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("非法输出流状态码 = %d %s", response.Code, response.Body.String())
	}
}

func TestTokensRejectUnknownDisabledToolGroups(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	hosts := hostmanager.New(st, box, pool)
	api := NewAPI(st, box, pool, hosts, nil, nil, nil, nil, memoryx.New(st, memoryx.EmbeddingConfig{}), nil).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}

	created := call(http.MethodPost, "/tokens", `{"name":"bad","all_hosts":true,"disabled_tools":["not-a-group"]}`)
	if created.Code != http.StatusBadRequest {
		t.Fatalf("POST /tokens 未知分组应 400，实际 %d %s", created.Code, created.Body.String())
	}

	ok := call(http.MethodPost, "/tokens", `{"name":"ok","all_hosts":true,"disabled_tools":["memory"]}`)
	if ok.Code != http.StatusCreated {
		t.Fatalf("POST /tokens 合法分组应 201，实际 %d %s", ok.Code, ok.Body.String())
	}
	var tokenOut map[string]any
	if err := json.Unmarshal(ok.Body.Bytes(), &tokenOut); err != nil {
		t.Fatal(err)
	}
	id := int64(tokenOut["id"].(float64))

	updated := call(http.MethodPut, "/tokens/"+strconv.FormatInt(id, 10), `{"name":"ok","all_hosts":true,"disabled_tools":["still-bad"]}`)
	if updated.Code != http.StatusBadRequest {
		t.Fatalf("PUT /tokens 未知分组应 400，实际 %d %s", updated.Code, updated.Body.String())
	}
}

func TestUpdateTokenRoundTripDisabledToolsAndHostIDs(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	hosts := hostmanager.New(st, box, pool)
	api := NewAPI(st, box, pool, hosts, nil, nil, nil, nil, memoryx.New(st, memoryx.EmbeddingConfig{}), nil).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}

	host := call(http.MethodPost, "/hosts", `{"name":"edge","addr":"127.0.0.1","port":22,"username":"runner","auth_type":"password","password":"secret"}`)
	if host.Code != http.StatusCreated {
		t.Fatalf("创建主机 %d %s", host.Code, host.Body.String())
	}
	var hostOut map[string]any
	if err := json.Unmarshal(host.Body.Bytes(), &hostOut); err != nil {
		t.Fatal(err)
	}
	hostID := int64(hostOut["id"].(float64))

	created := call(http.MethodPost, "/tokens", `{"name":"agent","all_hosts":true,"disabled_tools":["exec"]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("创建令牌 %d %s", created.Code, created.Body.String())
	}
	var createdOut map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &createdOut); err != nil {
		t.Fatal(err)
	}
	id := int64(createdOut["id"].(float64))

	body := `{"name":"agent-edited","all_hosts":false,"manage_hosts":true,"host_ids":[` + strconv.FormatInt(hostID, 10) + `],"disabled_tools":["memory","files"]}`
	updated := call(http.MethodPut, "/tokens/"+strconv.FormatInt(id, 10), body)
	if updated.Code != http.StatusOK {
		t.Fatalf("PUT /tokens/{id} 应 200，实际 %d %s", updated.Code, updated.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(updated.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["name"] != "agent-edited" || out["all_hosts"] != false || out["manage_hosts"] != true {
		t.Fatalf("PUT 响应字段异常: %s", updated.Body.String())
	}
	disabled, ok := out["disabled_tools"].([]any)
	if !ok || len(disabled) != 2 || disabled[0] != "files" || disabled[1] != "memory" {
		t.Fatalf("PUT 响应 disabled_tools = %#v", out["disabled_tools"])
	}
	hostIDs, ok := out["host_ids"].([]any)
	if !ok || len(hostIDs) != 1 || int64(hostIDs[0].(float64)) != hostID {
		t.Fatalf("PUT 响应 host_ids = %#v", out["host_ids"])
	}

	listed := call(http.MethodGet, "/tokens", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("GET /tokens %d %s", listed.Code, listed.Body.String())
	}
	var tokens []map[string]any
	if err := json.Unmarshal(listed.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, token := range tokens {
		if int64(token["id"].(float64)) != id {
			continue
		}
		found = true
		gotDisabled, _ := token["disabled_tools"].([]any)
		gotHosts, _ := token["host_ids"].([]any)
		if len(gotDisabled) != 2 || gotDisabled[0] != "files" || gotDisabled[1] != "memory" {
			t.Fatalf("列表 disabled_tools = %#v", token["disabled_tools"])
		}
		if len(gotHosts) != 1 || int64(gotHosts[0].(float64)) != hostID {
			t.Fatalf("列表 host_ids = %#v", token["host_ids"])
		}
	}
	if !found {
		t.Fatal("更新后的令牌未出现在列表中")
	}
}
