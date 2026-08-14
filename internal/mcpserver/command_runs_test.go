package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/store"
)

func TestCommandRunRecorderPersistsOutcomeAndEvents(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.New()
	server := &Server{Store: st, Events: bus}
	stream, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	run, err := server.startCommandRun(context.Background(), "exec", store.Host{ID: 1, Name: "web"}, "printf ok", "/srv", "default")
	if err != nil {
		t.Fatal(err)
	}
	publisher := newCommandOutputPublisher(server, run)
	publisher.Publish("stdout", []byte("ok"))
	result := execx.Result{Stdout: "ok", ExitCode: 0, StdoutBytes: 2, TotalBytes: 2, OutputRecorded: true}
	if err = server.finishCommandRun(context.Background(), run, result, nil); err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetCommandRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeeded" || stored.ExitCode.Int64 != 0 || stored.StdoutPreview != "ok" || !stored.OutputAvailable {
		t.Fatalf("命令记录结果异常: %#v", stored)
	}
	types := make([]string, 0, 3)
	deadline := time.After(time.Second)
	for len(types) < 3 {
		select {
		case event := <-stream:
			types = append(types, event.Type)
		case <-deadline:
			t.Fatalf("事件不完整: %#v", types)
		}
	}
	if strings.Join(types, ",") != "command_started,command_output,command_finished" {
		t.Fatalf("事件顺序异常: %#v", types)
	}
}

func TestFinishCommandRunOmitsUnknownExitCode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := &Server{Store: st, Events: events.New()}
	run, err := server.startCommandRun(context.Background(), "exec", store.Host{ID: 1, Name: "web"}, "sleep 10", "~", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err = server.finishCommandRun(context.Background(), run, execx.Result{Timeout: true, ExitCode: -1}, nil); err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetCommandRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "timeout" || stored.ExitCode.Valid {
		t.Fatalf("超时记录不应伪造退出码: %#v", stored)
	}
}

func TestCommandPreviewPreservesUTF8(t *testing.T) {
	input := strings.Repeat("你", commandPreviewLimit)
	preview := commandPreview(input)
	if !utf8.ValidString(preview) || !strings.Contains(preview, "中间输出已省略") {
		t.Fatalf("预览截断破坏 UTF-8: valid=%v len=%d", utf8.ValidString(preview), len(preview))
	}
}

func TestCommandOutputPublisherPreservesSplitUTF8(t *testing.T) {
	bus := events.New()
	server := &Server{Events: bus}
	stream, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	publisher := newCommandOutputPublisher(server, store.CommandRun{ID: "run", Tool: "exec", Host: "web"})
	encoded := []byte("你")
	publisher.Publish("stdout", encoded[:1])
	select {
	case event := <-stream:
		t.Fatalf("不完整字符不应提前发布: %#v", event)
	default:
	}
	publisher.Publish("stdout", encoded[1:])
	publisher.Finish()
	var event events.Event
	select {
	case event = <-stream:
	case <-time.After(time.Second):
		t.Fatal("没有收到完整 UTF-8 输出事件")
	}
	payload, err := json.Marshal(event.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"data":"你"`) || !utf8.Valid(payload) {
		t.Fatalf("实时输出破坏 UTF-8: %s", payload)
	}
}

func TestCommandRunStatus(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name   string
		ctx    context.Context
		result execx.Result
		err    error
		want   string
	}{
		{name: "成功", ctx: context.Background(), result: execx.Result{ExitCode: 0}, want: "succeeded"},
		{name: "非零退出", ctx: context.Background(), result: execx.Result{ExitCode: 2}, want: "failed"},
		{name: "超时", ctx: context.Background(), result: execx.Result{Timeout: true}, want: "timeout"},
		{name: "取消", ctx: cancelled, result: execx.Result{Timeout: true}, want: "cancelled"},
		{name: "连接阶段取消", ctx: cancelled, err: context.Canceled, want: "cancelled"},
	}
	for _, test := range tests {
		if got := commandRunStatus(test.ctx, test.result, test.err); got != test.want {
			t.Fatalf("%s状态=%s，期望 %s", test.name, got, test.want)
		}
	}
}

func TestAuditCommandRunIDs(t *testing.T) {
	if ok, code := (ExecOutput{Result: execx.Result{Timeout: true, ExitCode: -1}}).auditOutcome(); ok || code != nil {
		t.Fatalf("超时审计不应携带退出码: ok=%v code=%v", ok, code)
	}
	if ids := (ExecOutput{RunID: "run-1"}).auditCommandRunIDs(); len(ids) != 1 || ids[0] != "run-1" {
		t.Fatalf("exec 审计关联 = %#v", ids)
	}
	if ids := (JobStartOutput{RunID: "run-2"}).auditCommandRunIDs(); len(ids) != 1 || ids[0] != "run-2" {
		t.Fatalf("job_start 审计关联 = %#v", ids)
	}
	ids := (ExecManyOutput{Results: []ExecManyItem{
		{Host: "web-1", RunID: "run-3"},
		{Host: "forbidden"},
		{Host: "web-2", RunID: "run-4"},
	}}).auditCommandRunIDs()
	if len(ids) != 2 || ids[0] != "run-3" || ids[1] != "run-4" {
		t.Fatalf("exec_many 审计关联 = %#v", ids)
	}
}
