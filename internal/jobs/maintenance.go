package jobs

import (
	"context"
	"errors"
	"log"
	"time"
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
//  1. 刷新全部运行中任务。没人打开任务页时确认时间也要前进，否则健康的长任务会被误判失联；
//  2. 把连续 UnconfirmedLostAfter 无法确认的运行中任务标为 lost，并同步命令记录；
//  3. 删除结束超过 FinishedRetention 的任务记录。
//
// 刷新这一步没有跑完（读库失败或维护被取消）时跳过第 2 步：
// 没有拿到最新确认结果，就不能断定任务失联。
func (m *Manager) Maintain(ctx context.Context, now time.Time) (MaintenanceReport, error) {
	var report MaintenanceReport
	var errs error
	running, err := m.Store.ListRunningJobs(ctx)
	if err != nil {
		errs = errors.Join(errs, err)
	} else {
		m.RefreshAll(ctx, running)
		if ctx.Err() == nil {
			expired, expireErr := m.Store.ExpireUncheckedJobs(ctx, now.Add(-UnconfirmedLostAfter).Unix(), now.Unix())
			errs = errors.Join(errs, expireErr)
			for _, j := range expired {
				m.Events.Publish("job_status", map[string]any{"job_id": j.ID, "status": j.Status})
				m.syncCommandRun(j, nil)
			}
			report.Expired = len(expired)
		}
	}
	pruned, err := m.Store.PruneFinishedJobs(ctx, now.Add(-FinishedRetention).Unix())
	report.Pruned = pruned
	return report, errors.Join(errs, err)
}
