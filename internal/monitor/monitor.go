package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

const command = `cat /proc/stat /proc/meminfo /proc/loadavg 2>/dev/null || uptime; echo __ONESSH_DF__; df -P -k`

type CPU struct{ Total, Idle uint64 }
type Disk struct {
	Mount   string `json:"mount"`
	UsedKB  int64  `json:"used_kb"`
	TotalKB int64  `json:"total_kb"`
}
type Snapshot struct {
	HostID     int64    `json:"host_id"`
	Ts         int64    `json:"ts"`
	CPUPct     *float64 `json:"cpu_pct"`
	MemUsedKB  *int64   `json:"mem_used_kb"`
	MemTotalKB *int64   `json:"mem_total_kb"`
	Load1      *float64 `json:"load1"`
	Disks      []Disk   `json:"disks"`
}
type Manager struct {
	Store    *store.Store
	Pool     *sshpool.Pool
	Exec     *execx.Runner
	interval time.Duration
	mu       sync.Mutex
	previous map[int64]CPU
	cancel   context.CancelFunc
}

func New(st *store.Store, p *sshpool.Pool, e *execx.Runner, interval time.Duration) *Manager {
	return &Manager{Store: st, Pool: p, Exec: e, interval: interval, previous: make(map[int64]CPU)}
}
func (m *Manager) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	m.cleanup(ctx)
	go func() {
		clean := time.NewTicker(time.Hour)
		defer clean.Stop()
		var poll *time.Ticker
		var pollC <-chan time.Time
		if m.interval > 0 {
			poll = time.NewTicker(m.interval)
			pollC = poll.C
			defer poll.Stop()
			m.Poll(ctx)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-pollC:
				m.Poll(ctx)
			case <-clean.C:
				m.cleanup(ctx)
			}
		}
	}()
}
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Manager) cleanup(ctx context.Context) {
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	if err := m.Store.CleanupMetrics(ctx, cutoff.UnixMilli()); err != nil {
		log.Printf("清理过期 metrics 失败: %v", err)
	}
	if _, err := m.Exec.CleanupArtifacts(cutoff); err != nil {
		log.Printf("清理过期 artifact 失败: %v", err)
	}
	if err := m.Store.ExpireCommandRunOutputs(ctx, cutoff.UnixMilli()); err != nil {
		log.Printf("标记过期命令输出失败: %v", err)
	}
	deletable, err := m.Store.DeletableCommandRunOutputIDs(ctx)
	if err != nil {
		log.Printf("读取待删除的命令记录失败: %v", err)
	} else if _, err = m.Exec.CleanupCommandOutputs(deletable); err != nil {
		log.Printf("清理过期命令输出失败: %v", err)
	} else if err = m.Store.MarkCommandRunOutputsCleaned(ctx, deletable); err != nil {
		log.Printf("收敛命令输出清理状态失败: %v", err)
	}
}
func (m *Manager) Poll(ctx context.Context) {
	hosts, err := m.Store.MonitoredHosts(ctx)
	if err != nil {
		log.Printf("监控主机列表失败: %v", err)
		return
	}
	for _, h := range hosts {
		h := h
		go func() {
			if _, err := m.Sample(ctx, h); err != nil {
				log.Printf("监控采样 %s 失败: %v", h.Name, err)
			}
		}()
	}
}
func (m *Manager) Sample(ctx context.Context, h store.Host) (Snapshot, error) {
	client, err := m.Pool.Get(ctx, h.Name)
	if err != nil {
		return Snapshot{}, err
	}
	res, err := m.Exec.Run(ctx, client, command, "~", nil, execx.Options{Timeout: 30 * time.Second, MaxLines: 2000})
	if err != nil {
		return Snapshot{}, err
	}
	if res.ExitCode != 0 {
		return Snapshot{}, fmt.Errorf("采样命令退出 %d", res.ExitCode)
	}
	m.mu.Lock()
	prev, hasPrev := m.previous[h.ID]
	snap, cpu, err := parse(h.ID, res.Output, func() *CPU {
		if hasPrev {
			return &prev
		}
		return nil
	}())
	if err == nil && cpu.Total > 0 {
		m.previous[h.ID] = cpu
	}
	m.mu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	if err = m.Store.AddMetric(ctx, toMetric(snap)); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}
func (m *Manager) Fresh(ctx context.Context, h store.Host) (Snapshot, error) {
	client, err := m.Pool.Get(ctx, h.Name)
	if err != nil {
		return Snapshot{}, err
	}
	cmd := `cat /proc/stat /proc/meminfo /proc/loadavg 2>/dev/null; echo __ONESSH_FIRST__; sleep 1; cat /proc/stat /proc/meminfo /proc/loadavg 2>/dev/null; echo __ONESSH_DF__; df -P -k`
	res, err := m.Exec.Run(ctx, client, cmd, "~", nil, execx.Options{Timeout: 35 * time.Second, MaxLines: 3000})
	if err != nil {
		return Snapshot{}, err
	}
	parts := strings.SplitN(res.Output, "__ONESSH_FIRST__", 2)
	if len(parts) != 2 {
		return Snapshot{}, fmt.Errorf("主机不支持 /proc fresh 采样")
	}
	first, _, err := parse(h.ID, parts[0]+"\n__ONESSH_DF__\n", nil)
	_ = first
	cpu1 := parseCPU(parts[0])
	snap, _, err := parse(h.ID, parts[1], &cpu1)
	if err != nil {
		return Snapshot{}, err
	}
	if err = m.Store.AddMetric(ctx, toMetric(snap)); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}
func parse(hostID int64, text string, prev *CPU) (Snapshot, CPU, error) {
	snap := Snapshot{HostID: hostID, Ts: time.Now().UnixMilli()}
	cpu := parseCPU(text)
	if prev != nil && cpu.Total > prev.Total {
		dt := cpu.Total - prev.Total
		idle := cpu.Idle - prev.Idle
		v := 100 * (1 - float64(idle)/float64(dt))
		snap.CPUPct = &v
	}
	var total, avail int64
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(line, "MemTotal: %d kB", &total)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(line, "MemAvailable: %d kB", &avail)
		}
	}
	if total > 0 {
		used := total - avail
		snap.MemTotalKB = &total
		snap.MemUsedKB = &used
	}
	snap.Load1 = parseLoad(text)
	marker := strings.Index(text, "__ONESSH_DF__")
	if marker < 0 {
		return snap, cpu, fmt.Errorf("采样输出缺少 df 标记")
	}
	for _, line := range strings.Split(text[marker+len("__ONESSH_DF__"):], "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] == "Filesystem" {
			continue
		}
		totalKB, e1 := strconv.ParseInt(fields[1], 10, 64)
		usedKB, e2 := strconv.ParseInt(fields[2], 10, 64)
		if e1 == nil && e2 == nil {
			snap.Disks = append(snap.Disks, Disk{Mount: strings.Join(fields[5:], " "), UsedKB: usedKB, TotalKB: totalKB})
		}
	}
	return snap, cpu, nil
}
func parseCPU(text string) CPU {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 && f[0] == "cpu" {
			var c CPU
			for i := 1; i < len(f); i++ {
				v, _ := strconv.ParseUint(f[i], 10, 64)
				c.Total += v
				if i == 4 || i == 5 {
					c.Idle += v
				}
			}
			return c
		}
	}
	return CPU{}
}

var uptimeLoad = regexp.MustCompile(`load averages?:?\s*([0-9]+(?:\.[0-9]+)?)`)

func parseLoad(text string) *float64 {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && strings.Contains(line, "/") {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				return &v
			}
		}
		if m := uptimeLoad.FindStringSubmatch(line); len(m) == 2 {
			v, _ := strconv.ParseFloat(m[1], 64)
			return &v
		}
	}
	return nil
}
func toMetric(s Snapshot) store.Metric {
	m := store.Metric{HostID: s.HostID, Ts: s.Ts}
	if s.CPUPct != nil {
		m.CPUPct = sql.NullFloat64{Float64: *s.CPUPct, Valid: true}
	}
	if s.MemUsedKB != nil {
		m.MemUsedKB = sql.NullInt64{Int64: *s.MemUsedKB, Valid: true}
	}
	if s.MemTotalKB != nil {
		m.MemTotalKB = sql.NullInt64{Int64: *s.MemTotalKB, Valid: true}
	}
	if s.Load1 != nil {
		m.Load1 = sql.NullFloat64{Float64: *s.Load1, Valid: true}
	}
	raw, _ := json.Marshal(s.Disks)
	m.DisksJSON = string(raw)
	return m
}
func FromMetric(m store.Metric) Snapshot {
	s := Snapshot{HostID: m.HostID, Ts: m.Ts}
	if m.CPUPct.Valid {
		x := m.CPUPct.Float64
		s.CPUPct = &x
	}
	if m.MemUsedKB.Valid {
		x := m.MemUsedKB.Int64
		s.MemUsedKB = &x
	}
	if m.MemTotalKB.Valid {
		x := m.MemTotalKB.Int64
		s.MemTotalKB = &x
	}
	if m.Load1.Valid {
		x := m.Load1.Float64
		s.Load1 = &x
	}
	_ = json.Unmarshal([]byte(m.DisksJSON), &s.Disks)
	return s
}
