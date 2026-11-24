package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestAuditPreservesTokenIdentityAfterTokenDeletion(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	token, err := st.CreateToken(ctx, TokenCreate{Name: "deploy-agent", Hash: "deploy-agent-hash", AllHosts: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.AddAudit(ctx, Audit{
		Ts:         1,
		TokenID:    sql.NullInt64{Int64: token.ID, Valid: true},
		TokenName:  sql.NullString{String: token.Name, Valid: true},
		Tool:       "exec",
		ParamsJSON: "{}",
		RunIDs:     []string{"run-1", "run-2"},
		OK:         true,
	}); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}

	audit, err := st.ListAudit(ctx, nil, nil, nil, nil, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 {
		t.Fatalf("审计数量 = %d", len(audit))
	}
	if audit[0].TokenID != (sql.NullInt64{Int64: token.ID, Valid: true}) {
		t.Fatalf("审计令牌 ID = %#v", audit[0].TokenID)
	}
	if audit[0].TokenName != (sql.NullString{String: token.Name, Valid: true}) {
		t.Fatalf("审计令牌名称 = %#v", audit[0].TokenName)
	}
	if len(audit[0].RunIDs) != 2 || audit[0].RunIDs[0] != "run-1" || audit[0].RunIDs[1] != "run-2" {
		t.Fatalf("审计命令关联 = %#v", audit[0].RunIDs)
	}
}

func TestListAuditFiltersByOK(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err = st.AddAudit(ctx, Audit{Ts: 1, Tool: "exec", ParamsJSON: "{}", OK: true}); err != nil {
		t.Fatal(err)
	}
	if err = st.AddAudit(ctx, Audit{Ts: 2, Tool: "exec", ParamsJSON: "{}", OK: false}); err != nil {
		t.Fatal(err)
	}

	failed := false
	audit, err := st.ListAudit(ctx, nil, nil, nil, &failed, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 || audit[0].OK {
		t.Fatalf("ok=false 过滤结果 = %#v", audit)
	}

	succeeded := true
	audit, err = st.ListAudit(ctx, nil, nil, nil, &succeeded, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 || !audit[0].OK {
		t.Fatalf("ok=true 过滤结果 = %#v", audit)
	}
}

func TestListAuditFiltersByMultipleValues(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rows := []Audit{
		{Ts: 1, Tool: "exec", Host: sql.NullString{String: "web-01", Valid: true}, ParamsJSON: "{}", OK: true},
		{Ts: 2, Tool: "file_read", Host: sql.NullString{String: "web-02", Valid: true}, ParamsJSON: "{}", OK: true},
		{Ts: 3, Tool: "job_list", Host: sql.NullString{String: "db-01", Valid: true}, ParamsJSON: "{}", OK: true},
	}
	for _, row := range rows {
		if err = st.AddAudit(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	// 多工具 OR：exec + file_read 命中两条，job_list 被排除
	audit, err := st.ListAudit(ctx, nil, nil, []string{"exec", "file_read"}, nil, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 2 || audit[0].Tool != "file_read" || audit[1].Tool != "exec" {
		t.Fatalf("多工具过滤结果 = %#v", audit)
	}

	// 多主机 OR + 多工具 AND：两个维度都满足才保留
	audit, err = st.ListAudit(ctx, nil, []string{"web-01", "db-01"}, []string{"job_list"}, nil, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 || audit[0].Tool != "job_list" || audit[0].Host.String != "db-01" {
		t.Fatalf("主机+工具组合过滤结果 = %#v", audit)
	}

	tools, err := st.ListAuditTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 || tools[0] != "exec" || tools[1] != "file_read" || tools[2] != "job_list" {
		t.Fatalf("审计工具列表 = %#v", tools)
	}
}

func TestListAuditRejectsOversizedFilters(t *testing.T) {
	st := &Store{}
	hosts := make([]string, MaxAuditFilterValues+1)
	if _, err := st.ListAudit(context.Background(), nil, hosts, nil, nil, "", 0, 10); err == nil {
		t.Fatal("超出上限的审计筛选未被拒绝")
	}
}

// outcome 必须能稳定区分策略拒绝与执行失败，同时不丢掉迁移前的历史失败记录。
func TestAuditOutcomeRoundTripAndFilter(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	rows := []Audit{
		{Ts: 1, Tool: "exec", ParamsJSON: "{}", OK: true, Outcome: AuditOutcomeOK},
		{Ts: 2, Tool: "exec", ParamsJSON: "{}", OK: false, Outcome: AuditOutcomeFailed},
		{Ts: 3, Tool: "exec", ParamsJSON: "{}", OK: false, Outcome: AuditOutcomeDenied, DenyReason: "实例拒绝规则命中：rm -rf"},
		{Ts: 4, Tool: "exec", ParamsJSON: "{}", OK: false}, // 迁移前的历史失败：outcome 为空
	}
	for _, row := range rows {
		if err := st.AddAudit(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	all, err := st.ListAudit(ctx, nil, nil, nil, nil, "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 || all[0].Outcome != "" || all[1].DenyReason == "" || all[2].Outcome != AuditOutcomeFailed || all[3].Outcome != AuditOutcomeOK {
		t.Fatalf("审计回读 = %#v", all)
	}

	denied, err := st.ListAudit(ctx, nil, nil, nil, nil, AuditOutcomeDenied, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(denied) != 1 || denied[0].Outcome != AuditOutcomeDenied || denied[0].DenyReason != "实例拒绝规则命中：rm -rf" {
		t.Fatalf("denied 筛选 = %#v", denied)
	}

	failed, err := st.ListAudit(ctx, nil, nil, nil, nil, AuditOutcomeFailed, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 2 || failed[0].Outcome != "" || failed[1].Outcome != AuditOutcomeFailed {
		t.Fatalf("failed 筛选应包含历史失败 = %#v", failed)
	}

	ok, err := st.ListAudit(ctx, nil, nil, nil, nil, AuditOutcomeOK, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ok) != 1 || !ok[0].OK || ok[0].Outcome != AuditOutcomeOK {
		t.Fatalf("ok 筛选 = %#v", ok)
	}

	if _, err = st.ListAudit(ctx, nil, nil, nil, nil, "bogus", 0, 10); err == nil {
		t.Fatal("未知 outcome 应报错")
	}
}
