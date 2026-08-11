package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ── 外部接入 HTTP 服务 ──
// 在本机启动一个 HTTP 服务，暴露"发送聊天消息"接口，供外部程序
// （脚本 / OBS / Twitch 聊天桥 / 自动化工具等）调用。
//
// 接口：
//   POST http://127.0.0.1:<port>/api/chat/send
//     body: {"text":"要发送的消息"}
//     resp: {"ok":true} 或 {"ok":false,"error":"..."}
//
// 只绑定 127.0.0.1（仅本机可访问）；发送复用 sendChatTextLocked
// （含 1 秒频率限制），不修改定时/手动配置内容。

var externalAPIMu sync.Mutex
var externalAPIServer *http.Server
var externalAPIListener net.Listener

// externalAPIRequest 是 /api/chat/send 的请求体。
type externalAPIRequest struct {
	Text string `json:"text"`
}

// externalAPIResponse 是响应体。
type externalAPIResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// externalAPISendHandler 处理发送请求。
func (a *App) externalAPISendHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodPost {
		writeExternalAPIError(w, "仅支持 POST")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		writeExternalAPIError(w, "读取请求体失败: "+err.Error())
		return
	}
	var req externalAPIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeExternalAPIError(w, "请求体不是合法 JSON（期望 {\"text\":\"...\"}）")
		return
	}
	if req.Text == "" {
		writeExternalAPIError(w, "text 不能为空")
		return
	}
	if len([]byte(req.Text)) > autoChatMaxTextLen {
		writeExternalAPIError(w, fmt.Sprintf("text 最多 %d 个 UTF-8 字节", autoChatMaxTextLen))
		return
	}

	autoChat.mu.Lock()
	err = a.sendChatTextLocked(req.Text)
	if err != nil {
		autoChat.lastError = err.Error()
	}
	autoChat.mu.Unlock()
	if err != nil {
		writeExternalAPIError(w, err.Error())
		return
	}
	resp, _ := json.Marshal(externalAPIResponse{OK: true})
	_, _ = w.Write(resp)
}

func writeExternalAPIError(w http.ResponseWriter, msg string) {
	resp, _ := json.Marshal(externalAPIResponse{OK: false, Error: msg})
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write(resp)
}

// startExternalAPI 启动 HTTP 服务（幂等）。port<=0 时使用默认端口。
func (a *App) startExternalAPI(port int) error {
	externalAPIMu.Lock()
	defer externalAPIMu.Unlock()
	if externalAPIServer != nil {
		return nil // 已在运行
	}
	if port <= 0 {
		port = 17395
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat/send", a.externalAPISendHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w,
			"GBFR 外部消息接入服务\n\n"+
				"POST /api/chat/send  body: {\"text\":\"消息\"}\n")
	})

	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("监听端口失败: %w", err)
	}
	externalAPIListener = ln
	externalAPIServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = externalAPIServer.Serve(ln)
	}()
	return nil
}

// stopExternalAPI 停止 HTTP 服务。
func stopExternalAPI() {
	externalAPIMu.Lock()
	defer externalAPIMu.Unlock()
	if externalAPIServer != nil {
		_ = externalAPIServer.Close()
		externalAPIServer = nil
	}
	if externalAPIListener != nil {
		_ = externalAPIListener.Close()
		externalAPIListener = nil
	}
}

// AutoChatExternalStatus 返回外部接入服务状态。
func (a *App) AutoChatExternalStatus() ExternalAPIStatus {
	externalAPIMu.Lock()
	running := externalAPIServer != nil
	externalAPIMu.Unlock()

	autoChat.mu.Lock()
	enabled := a.config.ExternalApiEnabled
	port := a.config.ExternalApiPort
	autoChat.mu.Unlock()

	return ExternalAPIStatus{
		Running: running,
		Enabled: enabled,
		Port:    port,
	}
}

// AutoChatExternalSetEnabled 启用/停用外部接入服务，并持久化端口配置。
func (a *App) AutoChatExternalSetEnabled(enabled bool, port int) (ExternalAPIStatus, error) {
	if port <= 0 {
		port = 17395
	}
	if port > 65535 {
		return a.AutoChatExternalStatus(), fmt.Errorf("端口无效")
	}

	autoChat.mu.Lock()
	a.config.ExternalApiEnabled = enabled
	a.config.ExternalApiPort = port
	err := a.saveConfig()
	autoChat.mu.Unlock()
	if err != nil {
		return a.AutoChatExternalStatus(), fmt.Errorf("保存配置失败: %w", err)
	}

	if enabled {
		if err := a.startExternalAPI(port); err != nil {
			return a.AutoChatExternalStatus(), err
		}
	} else {
		stopExternalAPI()
	}
	return a.AutoChatExternalStatus(), nil
}

// ExternalAPIStatus 是外部接入服务状态快照。
type ExternalAPIStatus struct {
	Running bool `json:"running"` // 服务实际在运行
	Enabled bool `json:"enabled"` // 配置为启用
	Port    int  `json:"port"`
}
