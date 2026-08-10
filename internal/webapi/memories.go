package webapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

type memoryAdminBankStat struct {
	HostID      *int64  `json:"host_id"`
	HostName    *string `json:"host_name"`
	Count       int64   `json:"count"`
	Embedded    int64   `json:"embedded"`
	LastWritten *int64  `json:"last_written"`
}

func (a *API) memories(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var hostID *int64
	if raw, present := query["host_id"]; present {
		if len(raw) == 0 || raw[0] == "" {
			apiError(w, http.StatusBadRequest, errors.New("host_id 不能为空"))
			return
		}
		value, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil || value < 0 {
			apiError(w, http.StatusBadRequest, errors.New("host_id 必须是非负整数"))
			return
		}
		hostID = &value
	}
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			apiError(w, http.StatusBadRequest, errors.New("limit 必须是正整数"))
			return
		}
		limit = min(value, 200)
	}
	offset := 0
	if raw := query.Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			apiError(w, http.StatusBadRequest, errors.New("offset 必须是非负整数"))
			return
		}
		offset = value
	}
	memories, err := a.Store.ListMemoriesAdmin(r.Context(), hostID, query.Get("q"), limit, offset)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, memories)
}

func (a *API) memory(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	if err = a.Store.DeleteMemory(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			apiError(w, http.StatusNotFound, err)
			return
		}
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) memoryStats(w http.ResponseWriter, r *http.Request) {
	stats, err := a.Store.MemoryStats(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	hosts, err := a.Store.ListHosts(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	hostNames := make(map[int64]string, len(hosts))
	for _, host := range hosts {
		hostNames[host.ID] = host.Name
	}
	out := make([]memoryAdminBankStat, 0, len(stats))
	for _, stat := range stats {
		var hostID *int64
		var hostName *string
		if stat.HostID.Valid {
			id := stat.HostID.Int64
			name := hostNames[id]
			hostID = &id
			hostName = &name
		}
		var lastWritten *int64
		if stat.LastWritten.Valid {
			value := stat.LastWritten.Int64
			lastWritten = &value
		}
		out = append(out, memoryAdminBankStat{
			HostID: hostID, HostName: hostName, Count: stat.Count,
			Embedded: stat.Embedded, LastWritten: lastWritten,
		})
	}
	jsonOut(w, http.StatusOK, out)
}
