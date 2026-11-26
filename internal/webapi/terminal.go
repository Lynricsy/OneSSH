package webapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"onessh/internal/execx"
)

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (a *API) terminal(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	if _, err := a.Store.GetHostByName(r.Context(), host); err != nil {
		apiError(w, 404, err)
		return
	}
	client, err := a.Pool.Get(r.Context(), host)
	if err != nil {
		apiError(w, 502, err)
		return
	}
	session, err := client.NewSession()
	if err != nil {
		apiError(w, 502, err)
		return
	}
	defer session.Close()
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}
	if err = session.RequestPty("xterm-256color", rows, cols, sshTerminalModes); err != nil {
		apiError(w, 502, err)
		return
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		apiError(w, 502, err)
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		apiError(w, 502, err)
		return
	}
	session.Stderr = session.Stdout
	// ?cwd= 让「在此处打开终端」直接落在目标目录；目录不存在时 cd 失败也照样进入 shell
	if cwd := strings.TrimSpace(r.URL.Query().Get("cwd")); cwd != "" {
		err = session.Start("cd -- " + execx.SHQ(cwd) + " 2>/dev/null; exec \"${SHELL:-/bin/sh}\" -l")
	} else {
		err = session.Shell()
	}
	if err != nil {
		apiError(w, 502, err)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 心跳：防止反向代理与 NAT 在终端空闲时把连接悄悄掐掉
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, done := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pingCtx)
				done()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	if a.Events != nil {
		a.Events.Publish("terminal", map[string]any{"host": host, "action": "open"})
		defer a.Events.Publish("terminal", map[string]any{"host": host, "action": "close"})
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				if conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					_ = conn.Close(websocket.StatusInternalError, err.Error())
				}
				cancel()
				return
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg terminalMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "ping":
			continue
		case "input":
			_, _ = io.WriteString(stdin, msg.Data)
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = session.WindowChange(msg.Rows, msg.Cols)
			}
		}
	}
}
