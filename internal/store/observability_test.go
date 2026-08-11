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
		OK:         true,
	}); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}

	audit, err := st.ListAudit(ctx, nil, "", "", 0, 10)
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
}
