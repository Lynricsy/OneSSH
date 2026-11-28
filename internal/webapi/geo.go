package webapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"onessh/internal/store"
)

// 主机地理位置：控制台「分布地图」用。
//
// 定位只用到主机的公网 IP，结果缓存在 host_geo 表里；表由这里按需建立而不走迁移，
// 回退到旧版本二进制时多出的这张表不会影响任何东西。ONESSH_GEOIP=off 可完全关闭对外查询，
// 此时只显示手动标注的位置。

const (
	geoAutoTTL    = 30 * 24 * time.Hour
	geoRetryTTL   = time.Hour
	geoOriginTTL  = 6 * time.Hour
	geoHTTPTimout = 8 * time.Second
)

type GeoInfo struct {
	HostID      int64   `json:"host_id"`
	Host        string  `json:"host"`
	Addr        string  `json:"addr"`
	Archived    bool    `json:"archived"`
	IP          string  `json:"ip"`
	CountryCode string  `json:"country_code"`
	Country     string  `json:"country"`
	Region      string  `json:"region"`
	City        string  `json:"city"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	ISP         string  `json:"isp"`
	Org         string  `json:"org"`
	ASN         string  `json:"asn"`
	Timezone    string  `json:"timezone"`
	// Source：auto 自动定位 / manual 手动标注 / private 内网地址 / unresolved 定位失败 / origin 网关自身
	Source    string `json:"source"`
	Error     string `json:"error,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

type geoResponse struct {
	Enabled bool      `json:"enabled"`
	Origin  *GeoInfo  `json:"origin"`
	Hosts   []GeoInfo `json:"hosts"`
}

type geoLatency struct {
	HostID    int64  `json:"host_id"`
	LatencyMS *int64 `json:"latency_ms"`
	// Method：ssh 表示计时到收到 SSH 版本横幅，tcp 表示只完成了 TCP 握手（端口不是 SSH 服务时）
	Method  string `json:"method,omitempty"`
	ViaJump bool   `json:"via_jump"`
	Error   string `json:"error,omitempty"`
}

// originHostID 是网关自身位置在 host_geo 表里的行号：主机 id 从 1 开始，0 不会冲突
const originHostID = 0

type geoService struct {
	db      *sql.DB
	client  *http.Client
	enabled bool
	once    sync.Once
	initErr error
	// 解析串行化：同一时刻多个页面打开地图时，不必对同一批 IP 发起重复查询
	resolveMu sync.Mutex
	originMu  sync.Mutex
	origin    *GeoInfo
}

var (
	geoInstances   = map[*sql.DB]*geoService{}
	geoInstancesMu sync.Mutex
)

func (a *API) geo() (*geoService, error) {
	geoInstancesMu.Lock()
	svc, ok := geoInstances[a.Store.DB]
	if !ok {
		svc = &geoService{
			db:      a.Store.DB,
			client:  &http.Client{Timeout: geoHTTPTimout},
			enabled: !strings.EqualFold(strings.TrimSpace(os.Getenv("ONESSH_GEOIP")), "off"),
		}
		geoInstances[a.Store.DB] = svc
	}
	geoInstancesMu.Unlock()
	svc.once.Do(func() {
		_, svc.initErr = svc.db.Exec(`CREATE TABLE IF NOT EXISTS host_geo (
			host_id INTEGER PRIMARY KEY,
			ip TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT '',
			country TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			city TEXT NOT NULL DEFAULT '',
			lat REAL NOT NULL DEFAULT 0,
			lon REAL NOT NULL DEFAULT 0,
			isp TEXT NOT NULL DEFAULT '',
			org TEXT NOT NULL DEFAULT '',
			asn TEXT NOT NULL DEFAULT '',
			timezone TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'auto',
			error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`)
	})
	return svc, svc.initErr
}

func (g *geoService) load(ctx context.Context) (map[int64]GeoInfo, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT host_id,ip,country_code,country,region,city,lat,lon,isp,org,asn,timezone,source,error,updated_at FROM host_geo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]GeoInfo{}
	for rows.Next() {
		var x GeoInfo
		if err := rows.Scan(&x.HostID, &x.IP, &x.CountryCode, &x.Country, &x.Region, &x.City, &x.Lat, &x.Lon, &x.ISP, &x.Org, &x.ASN, &x.Timezone, &x.Source, &x.Error, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out[x.HostID] = x
	}
	return out, rows.Err()
}

func (g *geoService) save(ctx context.Context, x GeoInfo) error {
	_, err := g.db.ExecContext(ctx, `INSERT INTO host_geo(host_id,ip,country_code,country,region,city,lat,lon,isp,org,asn,timezone,source,error,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(host_id) DO UPDATE SET ip=excluded.ip,country_code=excluded.country_code,country=excluded.country,
		region=excluded.region,city=excluded.city,lat=excluded.lat,lon=excluded.lon,isp=excluded.isp,org=excluded.org,
		asn=excluded.asn,timezone=excluded.timezone,source=excluded.source,error=excluded.error,updated_at=excluded.updated_at`,
		x.HostID, x.IP, strings.ToUpper(x.CountryCode), x.Country, x.Region, x.City, x.Lat, x.Lon, x.ISP, x.Org, x.ASN, x.Timezone, x.Source, x.Error, x.UpdatedAt)
	return err
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		// 100.64.0.0/10 运营商级 NAT、198.18.0.0/15 基准测试网段在公网上同样不可定位
		(ip.To4() != nil && ((ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64) || (ip.To4()[0] == 198 && ip.To4()[1]&0xFE == 18)))
}

// resolveIP 把主机地址解析成用于定位的 IP：已是 IP 直接用，域名优先取 IPv4。
func resolveIP(ctx context.Context, addr string) (net.IP, error) {
	if ip := net.ParseIP(strings.Trim(addr, "[]")); ip != nil {
		return ip, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, addr)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if a.IP.To4() != nil {
			return a.IP, nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0].IP, nil
	}
	return nil, errors.New("域名没有解析结果")
}

type ipAPIResult struct {
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	Query       string  `json:"query"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	RegionName  string  `json:"regionName"`
	City        string  `json:"city"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Timezone    string  `json:"timezone"`
	ISP         string  `json:"isp"`
	Org         string  `json:"org"`
	AS          string  `json:"as"`
}

const ipAPIFields = "status,message,query,country,countryCode,regionName,city,lat,lon,timezone,isp,org,as"

func (r ipAPIResult) apply(x *GeoInfo) {
	x.CountryCode, x.Country, x.Region, x.City = strings.ToUpper(r.CountryCode), r.Country, r.RegionName, r.City
	x.Lat, x.Lon, x.Timezone, x.ISP, x.Org, x.ASN = r.Lat, r.Lon, r.Timezone, r.ISP, r.Org, r.AS
}

// lookupBatch 用 ip-api 的批量接口一次定位最多 100 个 IP（中文地名）。
// 免费接口只有 http：传出去的只是服务器公网 IP，不含任何凭据。
func (g *geoService) lookupBatch(ctx context.Context, ips []string) (map[string]ipAPIResult, error) {
	out := map[string]ipAPIResult{}
	for start := 0; start < len(ips); start += 100 {
		end := min(start+100, len(ips))
		body, _ := json.Marshal(ips[start:end])
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://ip-api.com/batch?lang=zh-CN&fields="+ipAPIFields, bytes.NewReader(body))
		if err != nil {
			return out, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.client.Do(req)
		if err != nil {
			return out, err
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			return out, err
		}
		if resp.StatusCode != http.StatusOK {
			return out, fmt.Errorf("ip-api 返回 HTTP %d", resp.StatusCode)
		}
		var list []ipAPIResult
		if err := json.Unmarshal(raw, &list); err != nil {
			return out, err
		}
		for _, item := range list {
			out[item.Query] = item
		}
	}
	return out, nil
}

// lookupSelf 定位网关自身的出口 IP，用作地图上连线的起点。
func (g *geoService) lookupSelf(ctx context.Context) (*GeoInfo, error) {
	g.originMu.Lock()
	defer g.originMu.Unlock()
	if g.origin != nil && time.Since(time.Unix(g.origin.UpdatedAt, 0)) < geoOriginTTL {
		return g.origin, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ip-api.com/json/?lang=zh-CN&fields="+ipAPIFields, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return g.origin, err
	}
	defer resp.Body.Close()
	var r ipAPIResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&r); err != nil {
		return g.origin, err
	}
	if r.Status != "success" {
		return g.origin, errors.New(r.Message)
	}
	x := &GeoInfo{Host: "OneSSH 网关", IP: r.Query, Source: "origin", UpdatedAt: time.Now().Unix()}
	r.apply(x)
	g.origin = x
	return x, nil
}

func (a *API) geoList(w http.ResponseWriter, r *http.Request) {
	g, err := a.geo()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	ctx := r.Context()
	hosts, err := a.Store.ListHosts(ctx)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	force := r.URL.Query().Get("refresh") == "1"

	g.resolveMu.Lock()
	defer g.resolveMu.Unlock()
	cached, err := g.load(ctx)
	if err != nil {
		apiError(w, 500, err)
		return
	}

	// 1. 并发解析所有主机地址
	type resolved struct {
		ip  net.IP
		err error
	}
	ips := make([]resolved, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, addr string) {
			defer wg.Done()
			ip, err := resolveIP(ctx, addr)
			ips[i] = resolved{ip, err}
		}(i, host.Addr)
	}
	wg.Wait()

	// 2. 判断哪些需要重新定位
	now := time.Now()
	result := make([]GeoInfo, len(hosts))
	var pending []int
	for i, host := range hosts {
		view := host.View()
		base := GeoInfo{HostID: host.ID, Host: host.Name, Addr: host.Addr, Archived: view.Archived}
		prev, has := cached[host.ID]
		if has && prev.Source == "manual" {
			prev.Host, prev.Addr, prev.Archived = host.Name, host.Addr, view.Archived
			if ips[i].ip != nil {
				prev.IP = ips[i].ip.String()
			}
			result[i] = prev
			continue
		}
		if ips[i].err != nil {
			// 解析失败时保留上一次的定位结果，只附上错误，免得 DNS 抖一下地图上就少一个点
			if has && prev.Source == "auto" {
				prev.Host, prev.Addr, prev.Archived, prev.Error = host.Name, host.Addr, view.Archived, "地址解析失败："+ips[i].err.Error()
				result[i] = prev
				continue
			}
			base.Source, base.Error, base.UpdatedAt = "unresolved", "地址解析失败："+ips[i].err.Error(), now.Unix()
			result[i] = base
			continue
		}
		ip := ips[i].ip
		base.IP = ip.String()
		if isPrivateIP(ip) {
			base.Source, base.UpdatedAt = "private", now.Unix()
			result[i] = base
			_ = g.save(ctx, base)
			continue
		}
		age := now.Sub(time.Unix(prev.UpdatedAt, 0))
		fresh := has && prev.IP == base.IP && ((prev.Source == "auto" && age < geoAutoTTL) || (prev.Source == "unresolved" && age < geoRetryTTL))
		if fresh && !force {
			prev.Host, prev.Addr, prev.Archived = host.Name, host.Addr, view.Archived
			result[i] = prev
			continue
		}
		if !g.enabled {
			base.Source, base.Error = "unresolved", "自动定位已关闭（ONESSH_GEOIP=off），可手动标注位置"
			result[i] = base
			continue
		}
		result[i] = base
		pending = append(pending, i)
	}

	// 3. 批量定位
	if len(pending) > 0 {
		queries := make([]string, 0, len(pending))
		seen := map[string]bool{}
		for _, i := range pending {
			if !seen[result[i].IP] {
				seen[result[i].IP] = true
				queries = append(queries, result[i].IP)
			}
		}
		found, lookupErr := g.lookupBatch(ctx, queries)
		for _, i := range pending {
			x := result[i]
			x.UpdatedAt = now.Unix()
			if hit, ok := found[x.IP]; ok && hit.Status == "success" {
				hit.apply(&x)
				x.Source, x.Error = "auto", ""
			} else if prev, has := cached[x.HostID]; has && prev.Source == "auto" && prev.IP == x.IP {
				// 定位服务暂时不可用：沿用旧结果
				prev.Host, prev.Addr, prev.Archived = x.Host, x.Addr, x.Archived
				if lookupErr != nil {
					prev.Error = "刷新定位失败：" + lookupErr.Error()
				}
				result[i] = prev
				continue
			} else {
				x.Source = "unresolved"
				switch {
				case ok:
					x.Error = "定位失败：" + hit.Message
				case lookupErr != nil:
					x.Error = "定位服务不可用：" + lookupErr.Error()
				default:
					x.Error = "定位服务没有返回结果"
				}
			}
			result[i] = x
			if err := g.save(ctx, x); err != nil {
				apiError(w, 500, err)
				return
			}
		}
	}

	// 4. 清掉已删除主机遗留的缓存行
	live := map[int64]bool{}
	for _, host := range hosts {
		live[host.ID] = true
	}
	for id := range cached {
		if id != originHostID && !live[id] {
			_, _ = g.db.ExecContext(ctx, `DELETE FROM host_geo WHERE host_id=?`, id)
		}
	}

	out := geoResponse{Enabled: g.enabled, Hosts: result}
	// 手动设置过网关位置时以它为准：走代理 / VPN 时自动检测到的只是代理出口
	if manual, ok := cached[originHostID]; ok {
		manual.Host = "OneSSH 网关"
		out.Origin = &manual
	} else if g.enabled {
		out.Origin, _ = g.lookupSelf(ctx)
	}
	jsonOut(w, 200, out)
}

type geoManualInput struct {
	CountryCode string  `json:"country_code"`
	Country     string  `json:"country"`
	Region      string  `json:"region"`
	City        string  `json:"city"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
}

// geoManual 手动标注位置（内网主机、定位不准时用）；DELETE 恢复自动定位。
func (a *API) geoManual(w http.ResponseWriter, r *http.Request) {
	g, err := a.geo()
	if err != nil {
		apiError(w, 500, err)
		return
	}
	var (
		id   int64
		host store.Host
	)
	if r.PathValue("hostID") == "origin" {
		id, host = originHostID, store.Host{Name: "OneSSH 网关"}
	} else {
		id, err = parseID(r, "hostID")
		if err != nil {
			apiError(w, 400, err)
			return
		}
		host, err = a.Store.GetHost(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			apiError(w, 404, errors.New("主机不存在"))
			return
		}
		if err != nil {
			apiError(w, 500, err)
			return
		}
	}
	if r.Method == http.MethodDelete {
		if _, err := g.db.ExecContext(r.Context(), `DELETE FROM host_geo WHERE host_id=?`, id); err != nil {
			apiError(w, 500, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	var in geoManualInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		apiError(w, 400, err)
		return
	}
	in.CountryCode = strings.ToUpper(strings.TrimSpace(in.CountryCode))
	if in.CountryCode != "" && len(in.CountryCode) != 2 {
		apiError(w, 400, errors.New("country_code 必须是两位国家代码，例如 CN、SG"))
		return
	}
	if in.Lat < -90 || in.Lat > 90 || in.Lon < -180 || in.Lon > 180 {
		apiError(w, 400, errors.New("经纬度超出范围：纬度 -90~90，经度 -180~180"))
		return
	}
	if in.Lat == 0 && in.Lon == 0 {
		apiError(w, 400, errors.New("请填写经纬度"))
		return
	}
	x := GeoInfo{
		HostID: id, Host: host.Name, Addr: host.Addr, CountryCode: in.CountryCode, Country: strings.TrimSpace(in.Country),
		Region: strings.TrimSpace(in.Region), City: strings.TrimSpace(in.City), Lat: in.Lat, Lon: in.Lon,
		Source: "manual", UpdatedAt: time.Now().Unix(),
	}
	if host.Addr != "" {
		if ip, err := resolveIP(r.Context(), host.Addr); err == nil {
			x.IP = ip.String()
		}
	}
	if err := g.save(r.Context(), x); err != nil {
		apiError(w, 500, err)
		return
	}
	jsonOut(w, 200, x)
}

// geoLatency 从网关直接连接每台主机的 SSH 端口，计时到收到 SSH 版本横幅为止。
// 只测 TCP 握手在本机开着代理（TUN / 透明代理）时会在本地就完成，测出来永远是几毫秒；
// 等到服务器自己发出的横幅，才是真实的网络往返。经跳板的主机网关本就直连不到，只标记 via_jump。
func (a *API) geoLatency(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.Store.ListHosts(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	out := make([]geoLatency, 0, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, host := range hosts {
		if host.View().Archived {
			continue
		}
		if host.JumpHostID.Valid {
			mu.Lock()
			out = append(out, geoLatency{HostID: host.ID, ViaJump: true})
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(host store.Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := geoLatency{HostID: host.ID}
			started := time.Now()
			conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(r.Context(), "tcp", net.JoinHostPort(host.Addr, strconv.Itoa(host.Port)))
			if err != nil {
				item.Error = err.Error()
			} else {
				connected := time.Since(started)
				_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				buf := make([]byte, 64)
				n, readErr := conn.Read(buf)
				ms := time.Since(started).Milliseconds()
				item.Method = "ssh"
				if readErr != nil || n == 0 {
					ms, item.Method = connected.Milliseconds(), "tcp"
					if n == 0 && readErr != nil && !errors.Is(readErr, os.ErrDeadlineExceeded) {
						// 连接被代理或防火墙立刻关闭：握手看似成功，实际上服务器并不可达
						item.Error = "连接建立后被立即关闭：" + readErr.Error()
					}
				}
				if item.Error == "" {
					item.LatencyMS = &ms
				}
				_ = conn.Close()
			}
			mu.Lock()
			out = append(out, item)
			mu.Unlock()
		}(host)
	}
	wg.Wait()
	jsonOut(w, 200, out)
}
