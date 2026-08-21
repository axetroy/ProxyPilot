package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/axetroy/ProxyPilot/proxy-core/model"
)

// httpServer is a local forward HTTP proxy (handles both plain and CONNECT).
type httpServer struct {
	addr string
	g    *Gateway

	srv *http.Server
	ln  net.Listener
}

func (h *httpServer) Start() error {
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return err
	}
	h.serveOn(ln)
	return nil
}

// serveOn 在外部提供的监听器上运行 HTTP 代理服务。
// 混合模式（HTTP+SOCKS5 共用端口）下由 mixedServer 传入 channelListener。
func (h *httpServer) serveOn(ln net.Listener) {
	h.ln = ln
	h.srv = &http.Server{Handler: h}
	go func() { _ = h.srv.Serve(ln) }()
}

func (h *httpServer) Stop() {
	if h.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.srv.Shutdown(ctx)
	}
	if h.ln != nil {
		_ = h.ln.Close()
	}
}

// ServeHTTP implements the proxy: CONNECT -> tunnel; else absolute-form forward.
func (h *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		h.tunnel(w, r)
		return
	}
	h.forward(w, r)
}

func (h *httpServer) tunnel(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if target == "" {
		http.Error(w, "missing Host", http.StatusBadRequest)
		return
	}
	// 显式指定 HTTP 协议，确保 CONNECT 隧道只从 HTTP/HTTPS 节点中挑选上游，
	// 避免 HTTPS 流量错误地使用 SOCKS5 节点。
	upstream, err := h.g.UpstreamWithProtocol(r.Context(), target, model.ProtocolHTTP)
	if err != nil {
		// 只向客户端返回通用错误，避免泄露内部节点信息。
		if h.g.bus != nil {
			h.g.bus.Debug(fmt.Sprintf("CONNECT %s upstream dial failed: %v", target, err))
		}
		http.Error(w, "upstream connection failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if !h.g.trackConn(client) {
		// 并发连接超限：连接已在 trackConn 中关闭，无需再处理。
		return
	}
	defer func() {
		h.g.untrackConn(client)
		_ = client.Close()
	}()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	relay(client, upstream)
}