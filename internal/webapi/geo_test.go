package webapi

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"onessh/internal/cryptox"
	"onessh/internal/hostmanager"
	"onessh/internal/memoryx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestIsPrivateIP(t *testing.T) {
	for addr, want := range map[string]bool{
		"10.0.0.1": true, "192.168.1.2": true, "127.0.0.1": true, "100.64.3.4": true, "198.18.0.9": true,
		"203.0.113.7": false, "198.51.100.20": false, "8.8.8.8": false,
	} {
		if got := isPrivateIP(net.ParseIP(addr)); got != want {
			t.Fatalf("isPrivateIP(%s) = %v，期望 %v", addr, got, want)
		}
	}
}

// 全程不访问外网：内网地址直接标为 private，公网地址在 ONESSH_GEOIP=off 下只能手动标注。
func TestGeoListManualAndLatency(t *testing.T) {
	t.Setenv("ONESSH_GEOIP", "off")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	api := NewAPI(st, box, pool, hostmanager.New(st, box, pool), nil, nil, nil, nil, memoryx.New(st, memoryx.EmbeddingConfig{}), nil).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	for _, body := range []string{
		`{"name":"lan","addr":"127.0.0.1","port":` + strconv.Itoa(port) + `,"username":"u","auth_type":"password","password":"p"}`,
		`{"name":"wan","addr":"8.8.8.8","port":22,"username":"u","auth_type":"password","password":"p"}`,
	} {
		if w := call(http.MethodPost, "/hosts", body); w.Code != http.StatusCreated {
			t.Fatalf("创建主机 %d %s", w.Code, w.Body.String())
		}
	}

	decode := func(w *httptest.ResponseRecorder) map[string]GeoInfo {
		t.Helper()
		var out geoResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析 /geo: %v %s", err, w.Body.String())
		}
		if out.Enabled {
			t.Fatal("ONESSH_GEOIP=off 时 enabled 应为 false")
		}
		m := map[string]GeoInfo{}
		for _, h := range out.Hosts {
			m[h.Host] = h
		}
		return m
	}
	got := decode(call(http.MethodGet, "/geo", ""))
	if got["lan"].Source != "private" || got["wan"].Source != "unresolved" || got["wan"].IP != "8.8.8.8" {
		t.Fatalf("初始定位 = %+v", got)
	}

	wanID := got["wan"].HostID
	if w := call(http.MethodPut, "/geo/"+strconv.FormatInt(wanID, 10), `{"country_code":"sg","city":"新加坡","lat":1.35,"lon":103.82}`); w.Code != 200 {
		t.Fatalf("手动标注 %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/geo/"+strconv.FormatInt(wanID, 10), `{"lat":120,"lon":0}`); w.Code != 400 {
		t.Fatalf("越界纬度应 400，得到 %d", w.Code)
	}
	got = decode(call(http.MethodGet, "/geo", ""))
	if w := got["wan"]; w.Source != "manual" || w.CountryCode != "SG" || w.City != "新加坡" {
		t.Fatalf("手动标注后 = %+v", w)
	}
	if w := call(http.MethodDelete, "/geo/"+strconv.FormatInt(wanID, 10), ""); w.Code != 204 {
		t.Fatalf("恢复自动 %d", w.Code)
	}
	if got = decode(call(http.MethodGet, "/geo", "")); got["wan"].Source != "unresolved" {
		t.Fatalf("恢复自动后 = %+v", got["wan"])
	}

	if w := call(http.MethodPut, "/geo/origin", `{"country_code":"CN","city":"上海","lat":31.23,"lon":121.47}`); w.Code != 200 {
		t.Fatalf("设置网关位置 %d %s", w.Code, w.Body.String())
	}
	var withOrigin geoResponse
	_ = json.Unmarshal(call(http.MethodGet, "/geo", "").Body.Bytes(), &withOrigin)
	if withOrigin.Origin == nil || withOrigin.Origin.City != "上海" || withOrigin.Origin.Source != "manual" || len(withOrigin.Hosts) != 2 {
		t.Fatalf("手动网关位置 = %+v", withOrigin.Origin)
	}

	w := call(http.MethodGet, "/geo/latency", "")
	var latency []geoLatency
	if err := json.Unmarshal(w.Body.Bytes(), &latency); err != nil {
		t.Fatal(err)
	}
	for _, item := range latency {
		if item.HostID == got["lan"].HostID && (item.LatencyMS == nil || item.Error != "") {
			t.Fatalf("本机监听端口应可测出延迟: %+v", item)
		}
	}
}
