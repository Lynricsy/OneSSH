package jobs

import (
	"context"
	"errors"
	"log"
	"time"

	"onessh/internal/store"
)

const (
	// UnconfirmedLostAfter 是运行中任务连续无法登录主机确认的上限，超过即标为 lost。
	UnconfirmedLostAfter = 7 * 24 * time.Hour
	// FinishedRetention 是已结束任务记录的保留期。
	FinishedRetention = 30 * 24 * time.Hour

	maintenanceInterval = time.Hour
	// 首轮延后执行：启动瞬间主机连接都还没建立，也避免与启动期的其他工作争抢。
	maintenanceFirstDelay = time.Minute
)

// maintenanceHostBudget 是维护时每台主机自己的探测预算：建连与握手各 15 秒，再留出探测余量。
// 按主机计时而不是整批计时，排队的主机不会因为前面的慢主机耗光预算而失去探测机会。
// 变量只为让测试缩短预算。
var maintenanceHostBudget = time.Minute

// MaintenanceReport 汇总一轮维护的结果。
type MaintenanceReport struct {
	Expired int   // 因长期无法确认被标为 lost 的运行中任务
	Pruned  int64 // 超过保留期被删除的已结束任务
}

// StartMaintenance 启动后台维护循环：首轮在 maintenanceFirstDelay 后执行，之后每小时一轮。
func (m *Manager) StartMaintenance(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	go func() {
		timer := time.NewTimer(maintenanceFirstDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if report, err := m.Maintain(ctx, time.Now()); err != nil {
					log.Printf("后台任务维护失败: %v", err)
				} else if report.Expired > 0 || report.Pruned > 0 {
					log.Printf("后台任务维护: %d 个长期无法确认的任务标为失联，清理 %d 条过期记录", report.Expired, report.Pruned)
				}
				timer.Reset(maintenanceInterval)
			}
		}
	}()
}

func (m *Manager) StopMaintenance() {
	if m.cancel != nil {
		m.cancel()
	}
}

// Maintain 执行一轮维护：
//  1. 逐台主机探测全部运行中任务。没人打开任务页时确认时间也要前进，否则健康的长任务会被误判失联；
//  2. 本轮确实探测过却没能确认、且连续 UnconfirmedLostAfter 未确认的任务标为 lost，并同步命令记录；
//  3. 删除结束超过 FinishedRetention 的任务记录。
//
// 只有「探测过并失败」的任务才可能被标 lost：没轮到探测（维护被取消）不构成失联证据。
// 网关停机超过 7 天恢复后，所有任务的确认时间都已过期，此时误把未探测的任务判失联代价最大。
func (m *Manager) Maintain(ctx context.Context, now time.Time) (MaintenanceReport, error) {
	var report MaintenanceReport
	var errs error
	running, err := m.Store.ListRunningJobsOnActiveHosts(ctx)
	if err != nil {
		errs = errors.Join(errs, err)
	} else if failed := m.probeRunning(ctx, running); len(failed) > 0 && ctx.Err() == nil {
		expired, expireErr := m.Store.ExpireUncheckedJobs(ctx, failed, now.Add(-UnconfirmedLostAfter).Unix(), now.Unix())
		errs = errors.Join(errs, expireErr)
		for _, j := range expired {
			m.Events.Publish("job_status", map[string]any{"job_id": j.ID, "status": j.Status})
			m.syncCommandRun(j, nil)
		}
		report.Expired = len(expired)
	}
	pruned, err := m.Store.PruneFinishedJobs(ctx, now.Add(-FinishedRetention).Unix())
	report.Pruned = pruned
	return report, errors.Join(errs, err)
}

// probeRunning 让每台主机都轮到一次探测，返回本轮探测失败的任务 ID。
// 并发槽只在父 ctx 取消时放弃等待，因此慢主机只会拖长这一轮，不会挤掉排在后面的主机。
func (m *Manager) probeRunning(ctx context.Context, running []store.Job) []string {
	byHost := make(map[int64][]store.Job)
	for _, j := range running {
		byHost[j.HostID] = append(byHost[j.HostID], j)
	}
	sem := make(chan struct{}, refreshHosts)
	results := make(chan []string, len(byHost))
	for hostID, jobs := range byHost {
		go func() { results <- m.probeHost(ctx, sem, hostID, jobs) }()
	}
	var failed []string
	for range byHost {
		failed = append(failed, <-results...)
	}
	return failed
}

// probeHost 在独立预算内探测一台主机，返回失败的任务 ID；没轮到或维护被取消时返回空。
// 预算到点而 worker 仍挂着（NewSession 等调用不响应 context）同样算探测失败，并登记为挂起，
// 列表刷新与下一轮维护都不会再为这台主机叠加新的 worker。
func (m *Manager) probeHost(ctx context.Context, sem chan struct{}, hostID int64, jobs []store.Job) []string {
	ids := func(list []store.Job) []string {
		out := make([]string, len(list))
		for i, j := range list {
			out[i] = j.ID
		}
		return out
	}
	if m.hostStuck(hostID) {
		// 之前登录这台主机的请求至今没有回应，本身就是无法确认的证据
		return ids(jobs)
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return nil
	}
	hostCtx, cancel := context.WithTimeout(ctx, maintenanceHostBudget)
	defer cancel()
	statuses := make(chan []Status, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		statuses <- m.refreshHost(hostCtx, hostID, jobs)
	}()
	select {
	case out := <-statuses:
		if ctx.Err() != nil {
			return nil
		}
		var failed []string
		for i, st := range out {
			if st.RefreshError != "" {
				failed = append(failed, jobs[i].ID)
			}
		}
		return failed
	case <-hostCtx.Done():
		if ctx.Err() != nil {
			return nil
		}
		m.stuck.Store(hostID, done)
		return ids(jobs)
	}
}
