package webapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"onessh/internal/store"
)

type commandRunView struct {
	Seq             int64   `json:"seq"`
	ID              string  `json:"id"`
	TokenID         *int64  `json:"token_id"`
	TokenName       *string `json:"token_name"`
	Tool            string  `json:"tool"`
	HostID          *int64  `json:"host_id"`
	Host            string  `json:"host"`
	Command         string  `json:"command"`
	Cwd             string  `json:"cwd"`
	Session         *string `json:"session"`
	JobID           *string `json:"job_id"`
	Status          string  `json:"status"`
	ExitCode        *int64  `json:"exit_code"`
	StdoutPreview   string  `json:"stdout_preview,omitempty"`
	StderrPreview   string  `json:"stderr_preview,omitempty"`
	StdoutBytes     int64   `json:"stdout_bytes"`
	StderrBytes     int64   `json:"stderr_bytes"`
	OutputAvailable bool    `json:"output_available"`
	OutputExpired   bool    `json:"output_expired"`
	OutputError     *string `json:"output_error"`
	ErrorText       *string `json:"error"`
	StartedAt       int64   `json:"started_at"`
	FinishedAt      *int64  `json:"finished_at"`
	DurationMS      int64   `json:"duration_ms"`
}

func viewCommandRun(run store.CommandRun, detail bool) commandRunView {
	view := commandRunView{
		Seq: run.Seq, ID: run.ID, Tool: run.Tool, Host: run.Host, Command: run.Command,
		Cwd: run.Cwd, Status: run.Status, StdoutBytes: run.StdoutBytes, StderrBytes: run.StderrBytes,
		OutputAvailable: run.OutputAvailable, OutputExpired: run.OutputExpired, StartedAt: run.StartedAt,
	}
	if detail {
		view.StdoutPreview = run.StdoutPreview
		view.StderrPreview = run.StderrPreview
	}
	if run.TokenID.Valid {
		view.TokenID = &run.TokenID.Int64
	}
	if run.TokenName.Valid {
		view.TokenName = &run.TokenName.String
	}
	if run.HostID.Valid {
		view.HostID = &run.HostID.Int64
	}
	if run.Session.Valid {
		view.Session = &run.Session.String
	}
	if run.JobID.Valid {
		view.JobID = &run.JobID.String
	}
	if run.ExitCode.Valid {
		view.ExitCode = &run.ExitCode.Int64
	}
	if run.OutputError.Valid {
		view.OutputError = &run.OutputError.String
	}
	if run.ErrorText.Valid {
		view.ErrorText = &run.ErrorText.String
	}
	if run.FinishedAt.Valid {
		view.FinishedAt = &run.FinishedAt.Int64
		view.DurationMS = max(0, run.FinishedAt.Int64-run.StartedAt)
	} else {
		view.DurationMS = max(0, time.Now().UnixMilli()-run.StartedAt)
	}
	return view
}

func (a *API) commandRuns(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	tokens, err := auditFilterValues("令牌", query["token"])
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	tokenIDs := make([]int64, 0, len(tokens))
	for _, value := range tokens {
		id, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil {
			apiError(w, http.StatusBadRequest, parseErr)
			return
		}
		tokenIDs = append(tokenIDs, id)
	}
	hosts, err := auditFilterValues("主机", query["host"])
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	tools, err := auditFilterValues("工具", query["tool"])
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	statuses, err := auditFilterValues("状态", query["status"])
	if err != nil {
		apiError(w, http.StatusBadRequest, err)
		return
	}
	before, _ := strconv.ParseInt(query.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(query.Get("limit"))
	runs, err := a.Store.ListCommandRuns(r.Context(), store.CommandRunFilter{
		TokenIDs: tokenIDs, Hosts: hosts, Tools: tools, Statuses: statuses,
		Query: query.Get("q"), Before: before, Limit: limit,
	})
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]commandRunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, viewCommandRun(run, false))
	}
	jsonOut(w, http.StatusOK, out)
}

func (a *API) commandRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.Store.GetCommandRun(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, http.StatusNotFound, errors.New("命令执行记录不存在"))
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonOut(w, http.StatusOK, viewCommandRun(run, true))
}

func (a *API) commandRunOutput(w http.ResponseWriter, r *http.Request) {
	run, err := a.Store.GetCommandRun(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, http.StatusNotFound, errors.New("命令执行记录不存在"))
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	if run.OutputExpired {
		apiError(w, http.StatusGone, errors.New("命令输出已超过 7 天保留期"))
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset_bytes"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit_bytes"))
	stream := r.URL.Query().Get("stream")
	if run.JobID.Valid {
		if stream != "" && stream != "combined" {
			apiError(w, http.StatusBadRequest, errors.New("后台任务仅提供 combined 合并日志"))
			return
		}
		job, getErr := a.Store.GetJob(r.Context(), run.JobID.String)
		if getErr != nil {
			apiError(w, http.StatusNotFound, errors.New("后台任务不存在"))
			return
		}
		if job.Status == "running" {
			if _, refreshErr := a.Jobs.Refresh(r.Context(), job); refreshErr == nil {
				job, _ = a.Store.GetJob(r.Context(), job.ID)
			}
		}
		chunk, readErr := a.Jobs.LogChunk(r.Context(), job, offset, limit)
		if readErr != nil {
			apiError(w, http.StatusBadGateway, readErr)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		jsonOut(w, http.StatusOK, chunk)
		return
	}
	if !run.OutputAvailable && run.Status != "running" {
		message := "命令没有可读取的输出"
		if run.OutputError.Valid {
			message += ": " + run.OutputError.String
		}
		apiError(w, http.StatusNotFound, errors.New(message))
		return
	}
	if stream == "" {
		stream = "stdout"
	}
	if stream != "stdout" && stream != "stderr" {
		apiError(w, http.StatusBadRequest, errors.New("stream 仅支持 stdout 或 stderr"))
		return
	}
	chunk, err := a.Exec.ReadCommandOutput(run.ID, stream, offset, limit)
	if err != nil {
		apiError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonOut(w, http.StatusOK, chunk)
}

func (a *API) jobLogs(w http.ResponseWriter, r *http.Request) {
	job, err := a.Store.GetJob(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, http.StatusNotFound, errors.New("后台任务不存在"))
		return
	}
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset_bytes"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit_bytes"))
	chunk, err := a.Jobs.LogChunk(r.Context(), job, offset, limit)
	if err != nil {
		apiError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonOut(w, http.StatusOK, chunk)
}
