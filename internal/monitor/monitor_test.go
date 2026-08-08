package monitor

import "testing"

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
