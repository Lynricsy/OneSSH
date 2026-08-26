package monitor

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"onessh/internal/store"
)

func newPollTestManager(t *testing.T, hostCount int, sample sampleFunc) *Manager {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i := range hostCount {
		_, err = st.CreateHost(ctx, store.Host{
			Name:           fmt.Sprintf("test-%d", i),
			Addr:           "127.0.0.1",
			Port:           22,
			Username:       "root",
			AuthType:       "password",
			MonitorEnabled: true,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	manager := New(st, nil, nil, 0)
	manager.sample = sample
	return manager
}

func waitForSamples(t *testing.T, started <-chan struct{}, count int) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range count {
		select {
		case <-started:
		case <-timer.C:
			t.Fatalf("等待 %d 个采样启动超时", count)
		}
	}
}

func TestPollLimitsGlobalConcurrencyAndSkipsOverlap(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	var active, peak, sampled atomic.Int64
	manager := newPollTestManager(t, 10, func(ctx context.Context, _ store.Host) (Snapshot, error) {
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		sampled.Add(1)
		started <- struct{}{}
		defer active.Add(-1)
		select {
		case <-release:
			return Snapshot{}, nil
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	})

	pollDone := make(chan struct{})
	go func() {
		manager.Poll(ctx)
		close(pollDone)
	}()
	waitForSamples(t, started, maxConcurrentSamples)
	select {
	case <-pollDone:
		t.Fatal("Poll 在活动采样结束前返回")
	default:
	}

	overlapDone := make(chan struct{})
	go func() {
		manager.Poll(ctx)
		close(overlapDone)
	}()
	select {
	case <-overlapDone:
	case <-time.After(time.Second):
		t.Fatal("重叠 Poll 未被立即跳过")
	}
	select {
	case <-started:
		t.Fatal("重叠 Poll 启动了额外采样")
	default:
	}

	close(release)
	select {
	case <-pollDone:
	case <-time.After(time.Second):
		t.Fatal("释放采样后 Poll 未返回")
	}
	if got := peak.Load(); got != maxConcurrentSamples {
		t.Fatalf("峰值并发 = %d，期望 %d", got, maxConcurrentSamples)
	}
	if got := sampled.Load(); got != 10 {
		t.Fatalf("完成采样数 = %d，期望 10", got)
	}
}

func TestPollCancellationStopsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, maxConcurrentSamples)
	var active, sampled atomic.Int64
	manager := newPollTestManager(t, 10, func(ctx context.Context, _ store.Host) (Snapshot, error) {
		active.Add(1)
		sampled.Add(1)
		started <- struct{}{}
		defer active.Add(-1)
		<-ctx.Done()
		return Snapshot{}, ctx.Err()
	})

	pollDone := make(chan struct{})
	go func() {
		manager.Poll(ctx)
		close(pollDone)
	}()
	waitForSamples(t, started, maxConcurrentSamples)
	cancel()
	select {
	case <-pollDone:
	case <-time.After(time.Second):
		t.Fatal("取消后 Poll 未返回")
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("取消后仍有 %d 个活动采样", got)
	}
	if got := sampled.Load(); got != maxConcurrentSamples {
		t.Fatalf("取消后启动了 %d 个采样，期望 %d", got, maxConcurrentSamples)
	}
}
