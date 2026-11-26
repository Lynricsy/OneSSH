package webapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"net/http"
	"onessh/internal/execx"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 主机系统信息：指标页顶部的「服务器档案」。一次 SSH 执行收集，按段落标记切分，
// 缺少某个命令（精简镜像、BusyBox）时该段为空，不影响其余信息。

const sysinfoScript = `export LC_ALL=C 2>/dev/null
sec(){ printf '\n@@%s@@\n' "$1"; }
sec hostname; hostname 2>/dev/null || cat /etc/hostname 2>/dev/null
sec fqdn; hostname -f 2>/dev/null
sec os; cat /etc/os-release 2>/dev/null
sec kernel; uname -r 2>/dev/null
sec kernel_full; uname -v 2>/dev/null
sec arch; uname -m 2>/dev/null
sec uptime; cat /proc/uptime 2>/dev/null
sec cpuinfo; grep -m1 -E 'model name|Hardware|Processor' /proc/cpuinfo 2>/dev/null
sec cpus; nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo 2>/dev/null
sec sockets; grep 'physical id' /proc/cpuinfo 2>/dev/null | sort -u | wc -l
sec cpumhz; grep -m1 'cpu MHz' /proc/cpuinfo 2>/dev/null
sec meminfo; grep -E '^(MemTotal|MemAvailable|SwapTotal|SwapFree):' /proc/meminfo 2>/dev/null
sec loadavg; cat /proc/loadavg 2>/dev/null
sec virt; (systemd-detect-virt 2>/dev/null || true)
sec product; cat /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name 2>/dev/null
sec timezone; (cat /etc/timezone 2>/dev/null || readlink /etc/localtime 2>/dev/null || echo "$TZ")
sec date; date '+%Y-%m-%d %H:%M:%S %z' 2>/dev/null
sec boot; (uptime -s 2>/dev/null || who -b 2>/dev/null)
sec users; (who 2>/dev/null | wc -l)
sec procs; (ls -d /proc/[0-9]* 2>/dev/null | wc -l)
sec shell; getent passwd "$(id -un)" 2>/dev/null | cut -d: -f7
sec whoami; id 2>/dev/null
sec disks; df -PT -x tmpfs -x devtmpfs -x overlay -x squashfs -x efivarfs 2>/dev/null || df -P 2>/dev/null
sec ips; (ip -o -4 addr show 2>/dev/null; ip -o -6 addr show scope global 2>/dev/null)
sec gateway; ip route show default 2>/dev/null | head -1
sec dns; grep -E '^nameserver' /etc/resolv.conf 2>/dev/null
sec ports; (ss -Htlnp 2>/dev/null || ss -Htln 2>/dev/null || netstat -tln 2>/dev/null) | head -40
sec top; ps -eo pid,user,pcpu,pmem,rss,comm --sort=-pcpu 2>/dev/null | awk -v me=$$ 'NR==1 || ($6!="ps" && $6!="awk" && $6!="head" && $6!="systemd-timedat" && $1!=me)' | head -9
sec services; (systemctl list-units --type=service --state=running --no-legend --plain 2>/dev/null | wc -l)
sec failed; (systemctl --failed --no-legend --plain 2>/dev/null | awk '{print $1}' | head -10)
sec docker; (command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}|{{.Image}}|{{.Status}}' 2>/dev/null | head -20)
sec pkgs; (command -v dpkg-query >/dev/null && dpkg-query -f '.' -W 2>/dev/null | wc -c) || (command -v rpm >/dev/null && rpm -qa 2>/dev/null | wc -l) || (command -v apk >/dev/null && apk info 2>/dev/null | wc -l)
sec updates; (command -v apt-get >/dev/null && apt-get -s upgrade 2>/dev/null | grep -c '^Inst') || true
sec reboot; [ -f /var/run/reboot-required ] && echo yes
sec end
`

type sysDisk struct {
	Source  string `json:"source"`
	FSType  string `json:"fstype"`
	TotalKB int64  `json:"total_kb"`
	UsedKB  int64  `json:"used_kb"`
	Mount   string `json:"mount"`
}

type sysAddr struct {
	Iface string `json:"iface"`
	Addr  string `json:"addr"`
	V6    bool   `json:"v6"`
}

type sysPort struct {
	Proto   string `json:"proto"`
	Addr    string `json:"addr"`
	Port    string `json:"port"`
	Process string `json:"process"`
}

type sysProc struct {
	PID  string  `json:"pid"`
	User string  `json:"user"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	RSS  int64   `json:"rss_kb"`
	Comm string  `json:"comm"`
}

type sysContainer struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
}

type SysInfo struct {
	HostID          int64          `json:"host_id"`
	Hostname        string         `json:"hostname"`
	FQDN            string         `json:"fqdn"`
	OSName          string         `json:"os_name"`
	OSID            string         `json:"os_id"`
	OSVersion       string         `json:"os_version"`
	OSCodename      string         `json:"os_codename"`
	Kernel          string         `json:"kernel"`
	KernelBuild     string         `json:"kernel_build"`
	Arch            string         `json:"arch"`
	CPUModel        string         `json:"cpu_model"`
	CPUCores        int            `json:"cpu_cores"`
	CPUSockets      int            `json:"cpu_sockets"`
	CPUMHz          float64        `json:"cpu_mhz"`
	MemTotalKB      int64          `json:"mem_total_kb"`
	MemAvailableKB  int64          `json:"mem_available_kb"`
	SwapTotalKB     int64          `json:"swap_total_kb"`
	SwapFreeKB      int64          `json:"swap_free_kb"`
	UptimeSeconds   float64        `json:"uptime_seconds"`
	BootTime        string         `json:"boot_time"`
	Load            []float64      `json:"load"`
	Virtualization  string         `json:"virtualization"`
	Vendor          string         `json:"vendor"`
	Product         string         `json:"product"`
	Timezone        string         `json:"timezone"`
	RemoteTime      string         `json:"remote_time"`
	Users           int            `json:"users"`
	Processes       int            `json:"processes"`
	Shell           string         `json:"shell"`
	Identity        string         `json:"identity"`
	Disks           []sysDisk      `json:"disks"`
	Addrs           []sysAddr      `json:"addrs"`
	Gateway         string         `json:"gateway"`
	DNS             []string       `json:"dns"`
	Ports           []sysPort      `json:"ports"`
	Top             []sysProc      `json:"top"`
	Services        int            `json:"services"`
	FailedUnits     []string       `json:"failed_units"`
	Containers      []sysContainer `json:"containers"`
	Packages        int            `json:"packages"`
	Updates         int            `json:"updates"`
	RebootRequired  bool           `json:"reboot_required"`
	CollectedAt     int64          `json:"collected_at"`
	CollectDuration int64          `json:"collect_ms"`
}

var (
	sysinfoMu    sync.Mutex
	sysinfoCache = map[int64]SysInfo{}
)

const sysinfoTTL = 5 * time.Minute

func splitSections(out string) map[string]string {
	sections := map[string]string{}
	var name string
	var buf strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if strings.HasPrefix(line, "@@") && strings.HasSuffix(line, "@@") && len(line) > 4 {
			if name != "" {
				sections[name] = strings.TrimSpace(buf.String())
			}
			name = strings.Trim(line, "@")
			buf.Reset()
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return sections
}

func atoi(s string) int     { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }
func atoi64(s string) int64 { n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64); return n }
func atof(s string) float64 { n, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return n }
func afterColon(s string) string {
	if i := strings.Index(s, ":"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}

func parseSysinfo(out string) SysInfo {
	s := splitSections(out)
	info := SysInfo{
		Hostname: s["hostname"], FQDN: s["fqdn"], Kernel: s["kernel"], KernelBuild: s["kernel_full"], Arch: s["arch"],
		CPUCores: atoi(s["cpus"]), CPUSockets: atoi(s["sockets"]), Users: atoi(s["users"]), Processes: atoi(s["procs"]),
		Shell: s["shell"], Identity: s["whoami"], Gateway: s["gateway"], Services: atoi(s["services"]),
		Packages: atoi(s["pkgs"]), Updates: atoi(s["updates"]), RebootRequired: s["reboot"] == "yes", RemoteTime: s["date"],
		Disks: []sysDisk{}, Addrs: []sysAddr{}, DNS: []string{}, Ports: []sysPort{}, Top: []sysProc{}, FailedUnits: []string{}, Containers: []sysContainer{},
	}
	for _, line := range strings.Split(s["os"], "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "PRETTY_NAME":
			info.OSName = value
		case "ID":
			info.OSID = value
		case "VERSION_ID":
			info.OSVersion = value
		case "VERSION_CODENAME":
			info.OSCodename = value
		}
	}
	if up := strings.Fields(s["uptime"]); len(up) > 0 {
		info.UptimeSeconds = atof(up[0])
	}
	info.CPUModel = afterColon(s["cpuinfo"])
	info.CPUMHz = atof(afterColon(s["cpumhz"]))
	for _, line := range strings.Split(s["meminfo"], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		kb := atoi64(strings.TrimSuffix(strings.TrimSpace(value), " kB"))
		switch key {
		case "MemTotal":
			info.MemTotalKB = kb
		case "MemAvailable":
			info.MemAvailableKB = kb
		case "SwapTotal":
			info.SwapTotalKB = kb
		case "SwapFree":
			info.SwapFreeKB = kb
		}
	}
	if f := strings.Fields(s["loadavg"]); len(f) >= 3 {
		info.Load = []float64{atof(f[0]), atof(f[1]), atof(f[2])}
	}
	info.Virtualization = strings.TrimSpace(s["virt"])
	if info.Virtualization == "none" {
		info.Virtualization = "物理机"
	}
	if lines := strings.Split(s["product"], "\n"); len(lines) >= 1 {
		info.Vendor = strings.TrimSpace(lines[0])
		if len(lines) >= 2 {
			info.Product = strings.TrimSpace(lines[1])
		}
	}
	tz := strings.TrimSpace(s["timezone"])
	if i := strings.Index(tz, "zoneinfo/"); i >= 0 {
		tz = tz[i+len("zoneinfo/"):]
	}
	info.Timezone = tz
	boot := strings.TrimSpace(s["boot"])
	boot = strings.TrimSpace(strings.TrimPrefix(boot, "system boot"))
	info.BootTime = boot
	for i, line := range strings.Split(s["disks"], "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 6 {
			continue
		}
		if len(f) >= 7 {
			info.Disks = append(info.Disks, sysDisk{Source: f[0], FSType: f[1], TotalKB: atoi64(f[2]), UsedKB: atoi64(f[3]), Mount: f[6]})
		} else {
			info.Disks = append(info.Disks, sysDisk{Source: f[0], TotalKB: atoi64(f[1]), UsedKB: atoi64(f[2]), Mount: f[5]})
		}
	}
	for _, line := range strings.Split(s["ips"], "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] == "lo" {
			continue
		}
		info.Addrs = append(info.Addrs, sysAddr{Iface: f[1], Addr: f[3], V6: f[2] == "inet6"})
	}
	for _, line := range strings.Split(s["dns"], "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			info.DNS = append(info.DNS, f[1])
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(s["ports"], "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		var local, process string
		if f[0] == "LISTEN" { // ss -Htln：State Recv-Q Send-Q Local Peer [Process]
			local = f[3]
			if len(f) >= 6 {
				process = strings.Join(f[5:], " ")
				if i := strings.Index(process, `(("`); i >= 0 {
					process = process[i+3:]
					if j := strings.Index(process, `"`); j >= 0 {
						process = process[:j]
					}
				}
			}
		} else if strings.HasPrefix(f[0], "tcp") { // netstat
			local = f[3]
		} else {
			continue
		}
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		p := sysPort{Proto: "tcp", Addr: strings.Trim(local[:i], "[]"), Port: local[i+1:], Process: process}
		key := p.Port + "|" + p.Process
		if seen[key] {
			continue
		}
		seen[key] = true
		info.Ports = append(info.Ports, p)
	}
	for i, line := range strings.Split(s["top"], "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 6 {
			continue
		}
		info.Top = append(info.Top, sysProc{PID: f[0], User: f[1], CPU: atof(f[2]), Mem: atof(f[3]), RSS: atoi64(f[4]), Comm: strings.Join(f[5:], " ")})
	}
	for _, line := range strings.Split(s["failed"], "\n") {
		if line = strings.TrimSpace(line); line != "" {
			info.FailedUnits = append(info.FailedUnits, line)
		}
	}
	for _, line := range strings.Split(s["docker"], "\n") {
		if parts := strings.SplitN(line, "|", 3); len(parts) == 3 {
			info.Containers = append(info.Containers, sysContainer{Name: parts[0], Image: parts[1], Status: parts[2]})
		}
	}
	return info
}

func (a *API) hostSysinfo(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		apiError(w, 400, err)
		return
	}
	host, err := a.Store.GetHost(r.Context(), id)
	if err != nil {
		apiError(w, 404, errors.New("主机不存在"))
		return
	}
	if r.URL.Query().Get("refresh") != "1" {
		sysinfoMu.Lock()
		cached, ok := sysinfoCache[id]
		sysinfoMu.Unlock()
		if ok && time.Since(time.Unix(cached.CollectedAt, 0)) < sysinfoTTL {
			jsonOut(w, 200, cached)
			return
		}
	}
	started := time.Now()
	out, err := a.collectSysinfo(r.Context(), host.Name)
	if err != nil && !strings.Contains(out, "@@end@@") {
		apiError(w, 502, err)
		return
	}
	info := parseSysinfo(out)
	info.HostID, info.CollectedAt, info.CollectDuration = id, time.Now().Unix(), time.Since(started).Milliseconds()
	sysinfoMu.Lock()
	sysinfoCache[id] = info
	sysinfoMu.Unlock()
	jsonOut(w, 200, info)
}

type capWriter struct {
	buf bytes.Buffer
	max int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

// collectSysinfo 用独立的 SSH 会话拿到脚本的完整输出。不能走通用命令执行器：它只保留
// 尾部 60 行，系统、内核、CPU、内存这些排在前面的段落会被整段截掉。
// 显式交给 sh 执行，登录 shell 是 fish 之类的非 POSIX shell 时也能跑。
func (a *API) collectSysinfo(ctx context.Context, host string) (string, error) {
	if a.Pool == nil {
		return "", errors.New("连接池未初始化")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	client, err := a.Pool.Get(ctx, host)
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	out := &capWriter{max: 1 << 20}
	session.Stdout, session.Stderr = out, io.Discard
	done := make(chan error, 1)
	go func() { done <- session.Run("sh -c " + execx.SHQ(sysinfoScript)) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = session.Close()
		err = fmt.Errorf("采集超时：%w", ctx.Err())
	}
	return out.buf.String(), err
}

// SystemInfo 描述网关自身，设置页「关于」使用。
type SystemInfo struct {
	Version         string `json:"version"`
	StartedAt       int64  `json:"started_at"`
	Listen          string `json:"listen"`
	DataDir         string `json:"data_dir"`
	MasterKeySource string `json:"master_key_source"`
	GoVersion       string `json:"go_version"`
	Platform        string `json:"platform"`
	PID             int    `json:"pid"`
	DBBytes         int64  `json:"db_bytes"`
	Hosts           int    `json:"hosts"`
	Keys            int    `json:"keys"`
	Tokens          int    `json:"tokens"`
	GeoIP           bool   `json:"geoip"`
}

func (a *API) systemInfo(w http.ResponseWriter, r *http.Request) {
	info := a.System
	info.GoVersion, info.Platform, info.PID = runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH, os.Getpid()
	info.GeoIP = !strings.EqualFold(strings.TrimSpace(os.Getenv("ONESSH_GEOIP")), "off")
	if info.DataDir != "" {
		for _, name := range []string{"onessh.db", "onessh.db-wal"} {
			if st, err := os.Stat(filepath.Join(info.DataDir, name)); err == nil {
				info.DBBytes += st.Size()
			}
		}
	}
	ctx := r.Context()
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM hosts`).Scan(&info.Hosts)
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM keys`).Scan(&info.Keys)
	_ = a.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM tokens`).Scan(&info.Tokens)
	jsonOut(w, 200, info)
}
