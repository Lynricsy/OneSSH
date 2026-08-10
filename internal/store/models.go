package store

import "database/sql"

type Key struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	PrivateKeyEnc []byte `json:"-"`
	PublicKey     string `json:"public_key"`
	CreatedAt     int64  `json:"created_at"`
}

type Host struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Addr           string         `json:"addr"`
	Port           int            `json:"port"`
	Username       string         `json:"username"`
	AuthType       string         `json:"auth_type"`
	KeyID          sql.NullInt64  `json:"-"`
	PasswordEnc    []byte         `json:"-"`
	HostKeyFP      sql.NullString `json:"-"`
	MonitorEnabled bool           `json:"monitor_enabled"`
	CreatedAt      int64          `json:"created_at"`
}

type HostView struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Addr           string  `json:"addr"`
	Port           int     `json:"port"`
	Username       string  `json:"username"`
	AuthType       string  `json:"auth_type"`
	KeyID          *int64  `json:"key_id"`
	HostKeyFP      *string `json:"hostkey_fp"`
	MonitorEnabled bool    `json:"monitor_enabled"`
	CreatedAt      int64   `json:"created_at"`
}

func (h Host) View() HostView {
	v := HostView{ID: h.ID, Name: h.Name, Addr: h.Addr, Port: h.Port, Username: h.Username, AuthType: h.AuthType, MonitorEnabled: h.MonitorEnabled, CreatedAt: h.CreatedAt}
	if h.KeyID.Valid {
		x := h.KeyID.Int64
		v.KeyID = &x
	}
	if h.HostKeyFP.Valid {
		x := h.HostKeyFP.String
		v.HostKeyFP = &x
	}
	return v
}

type Token struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	AllHosts    bool           `json:"all_hosts"`
	ManageHosts bool           `json:"manage_hosts"`
	Source      string         `json:"source"`
	CreatedAt   int64          `json:"created_at"`
	LastUsedAt  sql.NullInt64  `json:"-"`
	ExpiresAt   sql.NullInt64  `json:"-"`
	Resource    sql.NullString `json:"-"`
	ClientID    sql.NullString `json:"-"`
}

type Session struct {
	ID, TokenID, HostID int64
	Label, Cwd          string
	Env                 map[string]string
	UpdatedAt           int64
}

type Job struct {
	ID         string        `json:"id"`
	HostID     int64         `json:"host_id"`
	TokenID    sql.NullInt64 `json:"-"`
	Command    string        `json:"command"`
	Cwd        string        `json:"cwd"`
	PID        sql.NullInt64 `json:"pid"`
	UsedSetsid bool          `json:"used_setsid"`
	Status     string        `json:"status"`
	ExitCode   sql.NullInt64 `json:"-"`
	StartedAt  int64         `json:"started_at"`
	FinishedAt sql.NullInt64 `json:"-"`
}

type Audit struct {
	ID, Ts               int64
	TokenID              sql.NullInt64
	Tool                 string
	Host                 sql.NullString
	ParamsJSON           string
	OK                   bool
	ExitCode             sql.NullInt64
	DurationMS, BytesOut int64
}

type Metric struct {
	HostID, Ts            int64
	CPUPct                sql.NullFloat64
	MemUsedKB, MemTotalKB sql.NullInt64
	Load1                 sql.NullFloat64
	DisksJSON             string
}

type Memory struct {
	ID             int64          `json:"id"`
	HostID         sql.NullInt64  `json:"-"`
	Content        string         `json:"content"`
	Source         string         `json:"source"`
	Importance     float64        `json:"importance"`
	Veracity       string         `json:"veracity"`
	TokenID        sql.NullInt64  `json:"-"`
	CreatedAt      int64          `json:"created_at"`
	UpdatedAt      int64          `json:"updated_at"`
	RecallCount    int64          `json:"recall_count"`
	LastRecalled   sql.NullInt64  `json:"-"`
	Embedding      []byte         `json:"-"`
	EmbeddingModel sql.NullString `json:"-"`
}

type MemoryWithRank struct {
	Memory
	Rank float64
}

type MemoryVector struct {
	ID        int64
	Embedding []byte
}

type MemoryAdminRow struct {
	ID           int64         `json:"id"`
	HostID       *int64        `json:"host_id"`
	HostName     *string       `json:"host_name"`
	Content      string        `json:"content"`
	Source       string        `json:"source"`
	Importance   float64       `json:"importance"`
	Veracity     string        `json:"veracity"`
	CreatedAt    int64         `json:"created_at"`
	UpdatedAt    int64         `json:"updated_at"`
	RecallCount  int64         `json:"recall_count"`
	LastRecalled sql.NullInt64 `json:"-"`
}

type MemoryBankStat struct {
	HostID      sql.NullInt64 `json:"-"`
	Count       int64         `json:"count"`
	Embedded    int64         `json:"embedded"`
	LastWritten sql.NullInt64 `json:"-"`
}
