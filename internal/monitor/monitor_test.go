package monitor

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"onessh/internal/execx"
	"onessh/internal/store"
)

func TestParseLinuxMetrics(t *testing.T) {
	prev := CPU{Total: 100, Idle: 40}
	text := "cpu  50 0 50 50 0 0 0 0\nMemTotal: 1000 kB\nMemAvailable: 400 kB\n0.50 0.2 0.1 1/10 1\n__ONESSH_DF__\nFilesystem 1024-blocks Used Available Capacity Mounted on\n/dev/a 1000 250 750 25% /\n"
	s, cpu, err := parse(2, text, &prev)
	if err != nil {
		t.Fatal(err)
	}
	if cpu.Total != 150 || s.CPUPct == nil || *s.CPUPct != 80 {
		t.Fatalf("CPU %+v %v", cpu, s.CPUPct)
	}
	if s.MemUsedKB == nil || *s.MemUsedKB != 600 {
		t.Fatalf("内存 %+v", s)
	}
	if s.Load1 == nil || *s.Load1 != 0.5 || len(s.Disks) != 1 {
		t.Fatalf("负载磁盘 %+v", s)
	}
}

func TestStartCleansRetentionWhenPollingDisabled(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := st.AddMetric(ctx, store.Metric{HostID: 1, Ts: old.UnixMilli(), DisksJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddAudit(ctx, store.Audit{Ts: old.UnixMilli(), Tool: "exec", ParamsJSON: "{}", OK: true}); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dataDir, "artifacts", uuid.NewString()+".log")
	if err := os.WriteFile(artifact, []byte("expired"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(artifact, old, old); err != nil {
		t.Fatal(err)
	}
	manager := New(st, nil, execx.New(dataDir), 0)
	manager.Start(ctx)
	manager.Stop()
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Fatalf("过期 artifact 未在启动时删除: %v", err)
	}
	if _, err := st.LatestMetric(ctx, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("过期 metrics 未删除: %v", err)
	}
	audit, err := st.ListAudit(ctx, nil, nil, nil, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 {
		t.Fatalf("审计不应按 metrics 保留策略删除: %d", len(audit))
	}
}
