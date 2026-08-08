package webapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
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
	if err = session.Shell(); err != nil {
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
		case "input":
			_, _ = io.WriteString(stdin, msg.Data)
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				_ = session.WindowChange(msg.Rows, msg.Cols)
			}
		}
	}
}
